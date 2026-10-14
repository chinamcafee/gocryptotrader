package jev

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

const (
	testSnapshotID = "6f2c0f7a-1c3b-4c8e-9a1d-2b3c4d5e6f70"
	testStrategyID = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	goodModel      = "jev-1.13.0"
)

func testCandidate() *candidate.Candidate {
	return &candidate.Candidate{
		StrategyVersionID:      testStrategyID,
		SnapshotID:             testSnapshotID,
		MarketID:               "Binance:usdtmarginedfutures:BTC_USDT",
		Action:                 candidate.ActionOpenLong,
		Thesis:                 "Trend is up on the hourly and funding is neutral.",
		EvidenceRefs:           []string{testSnapshotID, "ev_indicators_01"},
		Invalidation:           "A close back below the twenty period EMA.",
		SuggestedEntry:         "82760.00",
		SuggestedExit:          "80100.00",
		ProposedMaxNotionalUSD: "20",
		Uncertainty:            candidate.UncertaintyMedium,
		TTLSeconds:             300,
	}
}

func testInput() *ReviewInput {
	return &ReviewInput{
		Model:               goodModel,
		Network:             "binance-testnet",
		StrategyVersionID:   testStrategyID,
		StrategyInstruction: "Only trade when the hourly trend is clear and funding is neutral.",
		Snapshot: SnapshotRef{
			ID:         testSnapshotID,
			MarketID:   "Binance:usdtmarginedfutures:BTC_USDT",
			MarketType: "PERP",
			SourceTime: "2026-10-10T12:00:00Z",
			Quality:    string(candidate.QualityDerived),
		},
		Candidate: testCandidate(),
		Evidence: []candidate.ToolEvidence{{
			Ref:      "ev_indicators_01",
			AsOf:     time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
			Quality:  candidate.QualityDerived,
			Exchange: "Binance",
			Data:     json.RawMessage(`{"macd":{"histogram":2.4},"interval":"1h"}`),
		}},
		EvidencePolicy: EvidencePolicy{
			Source:              EvidenceSourceRetained,
			Treatment:           EvidenceTreatment,
			DataSourceCertified: true,
			TradeAuthorized:     false,
		},
		Safety: SafetyStatus{Status: "REVIEW", Reason: "AWAITING_JEV_AND_HARD_RISK"},
		Now:    time.Date(2026, 10, 10, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuildReviewRequestIsPureAndDeterministic(t *testing.T) {
	t.Parallel()

	in := testInput()
	first, err := BuildReviewRequest(in)
	require.NoError(t, err, "a well-formed input must build")
	second, err := BuildReviewRequest(in)
	require.NoError(t, err, "building twice must succeed")

	a, err := first.Marshal()
	require.NoError(t, err, "the first request must marshal")
	b, err := second.Marshal()
	require.NoError(t, err, "the second request must marshal")
	assert.True(t, bytes.Equal(a, b), "the same input should produce byte-identical requests so it can be hashed and replayed")
}

func TestBuildReviewRequestCarriesFramingAndVersion(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")
	assert.Equal(t, goodModel, req.Model, "the pinned model should be carried verbatim")

	state, ok := req.State.(ReviewState)
	require.True(t, ok, "the state must be the typed review state")
	assert.Equal(t, EvidenceSourceRetained, state.EvidencePolicy.Source, "the evidence provenance should be stated in the payload")
	assert.False(t, state.EvidencePolicy.TradeAuthorized, "the payload should state that no trade is authorised")
	require.Len(t, state.Evidence, 1, "the retained evidence must be carried into the request")

	body, err := req.Marshal()
	require.NoError(t, err, "marshalling must succeed")
	assert.Contains(t, string(body), EvidenceTreatment, "the untrusted-data framing should appear in the wire payload")

	choice, err := DecodeChoice(req.Questions[KeyCandidateAction])
	require.NoError(t, err, "the choice question must decode from an object shaped criteria")
	assert.Contains(t, choice.Criteria, criteriaOpenLong, "a perpetual long action should be offered")
	assert.Contains(t, choice.Criteria, criteriaReview, "a human-review action should be offered")
	assert.NotContains(t, choice.Criteria, "buy", "spot-only actions should not be offered for perpetuals")

	score, err := DecodeScore(req.Questions[KeyEvidenceQuality])
	require.NoError(t, err, "the score question must decode from an array shaped criteria")
	assert.Len(t, score.Criteria, 3, "the evidence scale should hold three points")

	noul, err := DecodeNoul(req.Questions[KeyStrategyAlignment])
	require.NoError(t, err, "the noul question must decode")
	assert.NotEmpty(t, noul.Instructions, "the noul question should carry instructions")
}

func TestBuildReviewRequestRejectsBadInput(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mutate  func(*ReviewInput)
		wantErr error
	}{
		{name: "alias model", mutate: func(in *ReviewInput) { in.Model = "jev-latest" }, wantErr: ErrModelInvalid},
		{name: "empty model", mutate: func(in *ReviewInput) { in.Model = "" }, wantErr: ErrModelInvalid},
		{name: "nil candidate", mutate: func(in *ReviewInput) { in.Candidate = nil }, wantErr: ErrStateInvalid},
		{name: "invalid candidate", mutate: func(in *ReviewInput) { in.Candidate.Action = "MOON" }, wantErr: ErrStateInvalid},
		{name: "no evidence", mutate: func(in *ReviewInput) { in.Evidence = nil }, wantErr: ErrEvidenceMissing},
		{name: "no snapshot id", mutate: func(in *ReviewInput) { in.Snapshot.ID = " " }, wantErr: ErrStateInvalid},
		{name: "no evidence policy source", mutate: func(in *ReviewInput) { in.EvidencePolicy.Source = "" }, wantErr: ErrStateInvalid},
		{
			name:    "policy claims authority",
			mutate:  func(in *ReviewInput) { in.EvidencePolicy.TradeAuthorized = true },
			wantErr: ErrStateInvalid,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := testInput()
			tc.mutate(in)
			_, err := BuildReviewRequest(in)
			require.Error(t, err, "the input must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestMarshalEnforcesByteCap(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")
	body, err := req.Marshal()
	require.NoError(t, err, "a normal request must fit")
	assert.Less(t, len(body), MaxRequestBytes, "a normal request should sit well under the cap")

	oversized, err := BuildReviewRequest(&ReviewInput{
		Model:               goodModel,
		Network:             "binance-testnet",
		StrategyInstruction: strings.Repeat("x", MaxRequestBytes+1024),
		Snapshot:            SnapshotRef{ID: testSnapshotID},
		Candidate:           testCandidate(),
		Evidence:            testInput().Evidence,
		EvidencePolicy:      EvidencePolicy{Source: EvidenceSourceRetained},
		Now:                 testInput().Now,
	})
	require.NoError(t, err, "the builder does not police size; the marshaller does")
	_, err = oversized.Marshal()
	require.ErrorIs(t, err, ErrRequestTooLarge, "an oversized request must be refused rather than truncated")
}

func validAnswers() json.RawMessage {
	return json.RawMessage(`{
	  "candidate_action": {
	    "type": "choice",
	    "choice": "open_long",
	    "probabilities": {"open_long": 0.52, "open_short": 0.06, "reduce": 0.04, "hold": 0.28, "review": 0.10},
	    "confidence": 0.71
	  },
	  "evidence_quality": {
	    "type": "score",
	    "score": 1.24,
	    "probabilities": {"0": 0.18, "1": 0.40, "2": 0.42},
	    "legend": ["Insufficient", "Mixed", "Adequate"],
	    "confidence": 0.65
	  },
	  "strategy_alignment": { "type": "noul", "noul": 0.83 }
	}`)
}

func responseWith(answers json.RawMessage) *SystemOneResponse {
	return &SystemOneResponse{
		Model:   goodModel,
		Answers: answers,
		Usage:   Usage{InputTokens: 1081, OutputTokens: 89},
	}
}

func TestValidateResponseAcceptsWellFormedReply(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	got, err := ValidateResponse(req, responseWith(validAnswers()))
	require.NoError(t, err, "a well-formed reply must validate")
	require.NotNil(t, got, "an assessment must be returned")

	assert.Equal(t, criteriaOpenLong, got.CandidateAction.Choice, "the chosen action should be surfaced")
	assert.InDelta(t, 0.71, got.CandidateAction.Confidence, 1e-9, "the confidence should be surfaced")
	assert.InDelta(t, 1.24, got.EvidenceQuality.Score, 1e-9, "the score should be surfaced")
	require.Len(t, got.EvidenceQuality.Legend, 3, "the legend must be surfaced")
	assert.InDelta(t, 0.83, got.StrategyAlignment.Noul, 1e-9, "the alignment probability should be surfaced")
	assert.Equal(t, 1081, got.Usage.InputTokens, "token usage should be surfaced")
	assert.Equal(t, goodModel, got.ResponseModel, "the served model should be recorded")
}

func TestValidateResponseRejectsBadReply(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	testCases := []struct {
		name    string
		answers string
		model   string
		wantErr error
	}{
		{name: "model drift", model: "jev-1.14.0", answers: string(validAnswers()), wantErr: ErrModelDrift},
		{name: "empty answers", model: goodModel, answers: ``, wantErr: ErrSchemaInvalid},
		{name: "not json", model: goodModel, answers: `nope`, wantErr: ErrSchemaInvalid},
		{
			name: "missing choice answer", model: goodModel, wantErr: ErrAnswerMissing,
			answers: `{"evidence_quality":{"type":"score","score":1,"probabilities":{"0":0.2,"1":0.4,"2":0.4},"legend":["a","b","c"]},"strategy_alignment":{"type":"noul","noul":0.5}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := responseWith(json.RawMessage(tc.answers))
			resp.Model = tc.model
			_, err := ValidateResponse(req, resp)
			require.Error(t, err, "the reply must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

// answersWith substitutes one clause of the otherwise valid reply.
func answersWith(choice, score, noul string) json.RawMessage {
	return json.RawMessage(`{
	  "candidate_action": ` + choice + `,
	  "evidence_quality": ` + score + `,
	  "strategy_alignment": ` + noul + `
	}`)
}

const (
	goodChoice = `{"type":"choice","choice":"open_long","probabilities":{"open_long":0.52,"open_short":0.06,"reduce":0.04,"hold":0.28,"review":0.10},"confidence":0.71}`
	goodScore  = `{"type":"score","score":1.24,"probabilities":{"0":0.18,"1":0.40,"2":0.42},"legend":["Insufficient","Mixed","Adequate"],"confidence":0.65}`
	goodNoul   = `{"type":"noul","noul":0.83}`
)

func TestValidateResponseChoiceRules(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	testCases := []struct {
		name    string
		choice  string
		wantErr error
	}{
		{
			name: "wrong answer type", wantErr: ErrSchemaInvalid,
			choice: `{"type":"score","choice":"open_long","probabilities":{"open_long":1},"confidence":1}`,
		},
		{
			name: "choice outside criteria", wantErr: ErrChoiceInvalid,
			choice: `{"type":"choice","choice":"buy","probabilities":{"open_long":0.52,"open_short":0.06,"reduce":0.04,"hold":0.28,"review":0.10},"confidence":0.7}`,
		},
		{
			name: "probability count mismatch", wantErr: ErrDistributionInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":1.0},"confidence":0.7}`,
		},
		{
			name: "unexpected probability key", wantErr: ErrDistributionInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":0.5,"open_short":0.1,"reduce":0.1,"hold":0.1,"review":0.1,"moon":0.1},"confidence":0.7}`,
		},
		{
			name: "distribution does not sum to one", wantErr: ErrProbabilityInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":0.2,"open_short":0.1,"reduce":0.1,"hold":0.1,"review":0.1},"confidence":0.7}`,
		},
		{
			name: "chosen below the maximum", wantErr: ErrChoiceInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":0.30,"open_short":0.06,"reduce":0.04,"hold":0.50,"review":0.10},"confidence":0.7}`,
		},
		{
			name: "negative probability", wantErr: ErrProbabilityInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":1.1,"open_short":-0.1,"reduce":0.0,"hold":0.0,"review":0.0},"confidence":0.7}`,
		},
		{
			name: "confidence out of range", wantErr: ErrChoiceInvalid,
			choice: `{"type":"choice","choice":"open_long","probabilities":{"open_long":0.52,"open_short":0.06,"reduce":0.04,"hold":0.28,"review":0.10},"confidence":1.4}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateResponse(req, responseWith(answersWith(tc.choice, goodScore, goodNoul)))
			require.Error(t, err, "the reply must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestValidateResponseScoreRules(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	testCases := []struct {
		name    string
		score   string
		wantErr error
	}{
		{
			name: "wrong answer type", wantErr: ErrSchemaInvalid,
			score: `{"type":"choice","score":1,"probabilities":{"0":0.2,"1":0.4,"2":0.4},"legend":["a","b","c"]}`,
		},
		{
			name: "legend length mismatch", wantErr: ErrScoreInvalid,
			score: `{"type":"score","score":1,"probabilities":{"0":0.2,"1":0.4,"2":0.4},"legend":["a","b"]}`,
		},
		{
			name: "missing bucket", wantErr: ErrDistributionInvalid,
			score: `{"type":"score","score":1,"probabilities":{"0":0.2,"1":0.8},"legend":["a","b","c"]}`,
		},
		{
			name: "score above the scale", wantErr: ErrScoreInvalid,
			score: `{"type":"score","score":7,"probabilities":{"0":0.0,"1":0.0,"2":1.0},"legend":["a","b","c"]}`,
		},
		{
			name: "score disagrees with its distribution", wantErr: ErrScoreInvalid,
			score: `{"type":"score","score":0.1,"probabilities":{"0":0.1,"1":0.2,"2":0.7},"legend":["a","b","c"]}`,
		},
		{
			name: "distribution does not sum to one", wantErr: ErrProbabilityInvalid,
			score: `{"type":"score","score":1,"probabilities":{"0":0.05,"1":0.05,"2":0.05},"legend":["a","b","c"]}`,
		},
		{
			name: "confidence out of range", wantErr: ErrScoreInvalid,
			score: `{"type":"score","score":1.24,"probabilities":{"0":0.18,"1":0.40,"2":0.42},"legend":["a","b","c"],"confidence":-1}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateResponse(req, responseWith(answersWith(goodChoice, tc.score, goodNoul)))
			require.Error(t, err, "the reply must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestValidateResponseNoulRules(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	for _, tc := range []struct {
		name    string
		noul    string
		wantErr error
	}{
		{name: "above one", noul: `{"type":"noul","noul":1.4}`, wantErr: ErrNoulInvalid},
		{name: "below zero", noul: `{"type":"noul","noul":-0.2}`, wantErr: ErrNoulInvalid},
		{name: "wrong type", noul: `{"type":"choice","noul":0.5}`, wantErr: ErrSchemaInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateResponse(req, responseWith(answersWith(goodChoice, goodScore, tc.noul)))
			require.Error(t, err, "the reply must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestValidateResponseRejectsNilInputs(t *testing.T) {
	t.Parallel()

	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building must succeed")

	_, err = ValidateResponse(nil, responseWith(validAnswers()))
	require.ErrorIs(t, err, ErrSchemaInvalid, "a nil request must be refused")

	_, err = ValidateResponse(req, nil)
	require.ErrorIs(t, err, ErrSchemaInvalid, "a nil response must be refused")
}

func TestIsPinnedModel(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"jev-1.13.0", "jev-2.0.1", "jev-10.20.30"} {
		assert.Truef(t, IsPinnedModel(ok), "%q should be accepted as pinned", ok)
	}
	for _, bad := range []string{"", "jev-latest", "jev", "jev-1", "jev-1.13", "gpt-4o", "jev-1.13.0-beta"} {
		assert.Falsef(t, IsPinnedModel(bad), "%q should be refused as an alias or foreign model", bad)
	}
}

func TestSyntheticProbeStateCarriesNoMarketData(t *testing.T) {
	t.Parallel()

	state := NewSyntheticProbeState()
	body, err := json.Marshal(state)
	require.NoError(t, err, "the synthetic state must marshal")
	assert.Contains(t, string(body), "there is no real market", "the probe should declare itself synthetic")
	assert.Len(t, state, 1, "the probe state should carry only the disclaimer so a misconfigured endpoint learns nothing")
}

func TestExportedCriteriaCoverTheOfferedQuestion(t *testing.T) {
	t.Parallel()

	questions, err := buildQuestions()
	require.NoError(t, err, "the question set must build")
	choice, err := DecodeChoice(questions[KeyCandidateAction])
	require.NoError(t, err, "the choice question must decode")
	offered := []string{CriteriaOpenLong, CriteriaOpenShort, CriteriaReduce, CriteriaHold, CriteriaReview}
	assert.Len(t, choice.Criteria, len(offered),
		"the exported aliases must cover exactly the offered criteria: a caller comparing against them sees every possible answer")
	for _, c := range offered {
		assert.Contains(t, choice.Criteria, c, "an exported criterion should be one the question offers")
	}
}
