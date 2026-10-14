package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
)

const (
	jevSnapID = "3a0c1f60-1a2b-4c3d-8e4f-5a6b7c8d9e0f"
	jevVerID  = "4b1d2e71-2b3c-4d4e-9f50-6b7c8d9e0f10"
)

type fakeJevJudge struct {
	assessment *jev.Assessment
	err        error
	calls      int
	last       *jev.SystemOneRequest
	onCall     func()
}

func (f *fakeJevJudge) SystemOne(_ context.Context, req *jev.SystemOneRequest) (*jev.Assessment, error) {
	f.calls++
	f.last = req
	if f.onCall != nil {
		f.onCall()
	}
	return f.assessment, f.err
}

func jevTestEvidence(t *testing.T) []candidate.ToolEvidence {
	t.Helper()
	market := policyTestMarket
	return []candidate.ToolEvidence{{
		Ref: "tool:getCandleWindow:call-1", AsOf: providerAdminNow.Add(-time.Minute),
		Quality: candidate.QualityExchangeREST, Exchange: "Binance", MarketID: &market,
		Data: json.RawMessage(`{"candles":[]}`),
	}}
}

func jevTestPipeline(t *testing.T, judge *fakeJevJudge, epoch *int64) (*JevPipeline, *chatTestBudget) {
	t.Helper()
	budget := &chatTestBudget{}
	return &JevPipeline{
		Judge:  judge,
		Budget: budget,
		Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
			ID: "rev-jev", Kind: ProviderKindJev, ModelID: "jev-1.0.0", TimeoutSeconds: 60,
		}},
		PairEpoch: func(context.Context) (int64, error) { return *epoch, nil },
		Now:       func() time.Time { return providerAdminNow },
	}, budget
}

func jevTestRequest(t *testing.T) *JevReviewRequest {
	t.Helper()
	return &JevReviewRequest{
		Model: "jev-1.0.0", Network: "binance-testnet", StrategyVersionID: jevVerID,
		StrategyInstruction: "低频趋势跟随",
		Snapshot: jev.SnapshotRef{
			ID: jevSnapID, MarketID: policyTestMarket, MarketType: "PERP",
			SourceTime: providerAdminNow.UTC().Format(time.RFC3339), Quality: string(candidate.QualityExchangeREST),
		},
		Candidate: &candidate.Candidate{
			StrategyVersionID: jevVerID, SnapshotID: jevSnapID, MarketID: policyTestMarket,
			Action: candidate.ActionOpenLong, Thesis: "结构偏多",
			EvidenceRefs: []string{jevSnapID, "tool:getCandleWindow:call-1"}, Invalidation: "跌破前低",
			SuggestedEntry: "1", SuggestedExit: "2", ProposedMaxNotionalUSD: "20",
			Uncertainty: candidate.UncertaintyMedium, TTLSeconds: 60,
		},
		Evidence: jevTestEvidence(t),
		EvidencePolicy: jev.EvidencePolicy{
			Source: jev.EvidenceSourceRetained, Treatment: jev.EvidenceTreatment,
			DataSourceCertified: true,
		},
		Safety:            jev.SafetyStatus{Status: "OK"},
		ProfileRevisionID: "rev-jev",
		ExpectedPairEpoch: 3,
	}
}

func TestJevReviewHappyPath(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{assessment: &jev.Assessment{
		ResponseModel: "jev-1.0.0", Usage: jev.Usage{InputTokens: 300, OutputTokens: 60},
	}}
	pipeline, budget := jevTestPipeline(t, judge, &epoch)

	outcome, err := pipeline.Review(t.Context(), jevTestRequest(t))
	require.NoError(t, err)
	require.NotNil(t, outcome.Assessment)
	assert.Equal(t, "jev-1.0.0", outcome.Assessment.ResponseModel)
	assert.Empty(t, outcome.ReasonCode)
	assert.Equal(t, int64(300), outcome.InputTokens)
	assert.Equal(t, int64(60), outcome.OutputTokens)
	assert.Equal(t, 1, judge.calls)

	// The recorded hash is of the exact bytes the judge was asked, recomputed
	// independently from the captured request.
	body, err := judge.last.Marshal()
	require.NoError(t, err)
	assert.Equal(t, jev.HashBytes(body), outcome.RequestSha256)

	assert.Equal(t, 1, budget.reserved)
	assert.Equal(t, "DECISION", budget.lastPurpose)
	assert.Equal(t, 1, budget.marked)
	assert.Zero(t, budget.released)

	// The byte-for-byte invariant: the evidence the judge saw is the analyst's
	// data, unmarshalled straight back out of the request state.
	state, ok := judge.last.State.(jev.ReviewState)
	require.True(t, ok)
	require.Len(t, state.Evidence, 1)
	assert.True(t, json.Valid(state.Evidence[0].Data))
	original := jevTestEvidence(t)
	assert.Equal(t, string(original[0].Data), string(state.Evidence[0].Data),
		"the judge's evidence data must be the analyst's bytes verbatim")
}

func TestJevReviewRefusesBadEvidenceBeforeBudget(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{}
	pipeline, budget := jevTestPipeline(t, judge, &epoch)

	request := jevTestRequest(t)
	request.Evidence[0].Data = json.RawMessage(`{"api_key":"smuggled"}`)
	outcome, err := pipeline.Review(t.Context(), request)
	require.NoError(t, err, "a policy refusal is an outcome, not a crash")
	assert.Equal(t, ReasonJevEvidenceInvalid, outcome.ReasonCode)
	assert.Contains(t, outcome.Detail, "sensitive", "the detail should say which check refused it")
	assert.Zero(t, judge.calls, "re-validation happens before the judge is called")
	assert.Zero(t, budget.reserved, "and before the budget is touched")
}

func TestJevReviewRefusesOversizedRequestBeforeBudget(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{}
	pipeline, budget := jevTestPipeline(t, judge, &epoch)

	request := jevTestRequest(t)
	request.StrategyInstruction = strings.Repeat("x", jev.MaxRequestBytes)
	outcome, err := pipeline.Review(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, ReasonJevRequestTooLarge, outcome.ReasonCode)
	assert.Zero(t, judge.calls)
	assert.Zero(t, budget.reserved)
}

func TestJevReviewDetectsAPairChangeWhileJudging(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{assessment: &jev.Assessment{ResponseModel: "jev-1.0.0", Usage: jev.Usage{InputTokens: 100, OutputTokens: 20}}}
	judge.onCall = func() { epoch = 4 } // the pair moves while the judge runs
	pipeline, budget := jevTestPipeline(t, judge, &epoch)

	outcome, err := pipeline.Review(t.Context(), jevTestRequest(t))
	require.NoError(t, err)
	assert.Equal(t, ReasonJevStalePair, outcome.ReasonCode)
	assert.Nil(t, outcome.Assessment, "a verdict about a pairing that no longer exists is not a verdict about this world")
	assert.Contains(t, outcome.Detail, "3 to 4")
	assert.Equal(t, 1, budget.marked, "the call happened, so it settles")
}

func TestJevReviewReleasesWhenTheJudgeFails(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{err: errors.New("judge unreachable")}
	pipeline, budget := jevTestPipeline(t, judge, &epoch)

	_, err := pipeline.Review(t.Context(), jevTestRequest(t))
	require.Error(t, err)
	assert.Equal(t, 1, budget.released)
	assert.Zero(t, budget.marked)

	judge = &fakeJevJudge{assessment: nil}
	pipeline, budget = jevTestPipeline(t, judge, &epoch)
	_, err = pipeline.Review(t.Context(), jevTestRequest(t))
	require.Error(t, err)
	assert.Equal(t, 1, budget.released, "a nil assessment is not a success to settle")
}

func TestJevReviewMisuse(t *testing.T) {
	epoch := int64(3)
	judge := &fakeJevJudge{}
	pipeline, _ := jevTestPipeline(t, judge, &epoch)

	var nilPipeline *JevPipeline
	_, err := nilPipeline.Review(t.Context(), jevTestRequest(t))
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	// An LLM revision cannot serve as the judge.
	pipeline.Revisions = chatTestRevisions{revision: &llmstore.ProfileRevision{ID: "rev-jev", Kind: ProviderKindLLM}}
	_, err = pipeline.Review(t.Context(), jevTestRequest(t))
	assert.ErrorIs(t, err, ErrChatProfileKind)
	assert.Zero(t, judge.calls)
}
