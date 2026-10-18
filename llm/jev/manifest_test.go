package jev

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

const testStrategyInstruction = "Only trade when the hourly trend is clear and funding is neutral."

func manifestInputs(t *testing.T) (request *SystemOneRequest, snapshotJSON, candidateJSON []byte, modeled *candidate.Candidate, responseRefs []ToolResponseRef) {
	t.Helper()
	req, err := BuildReviewRequest(testInput())
	require.NoError(t, err, "building the request must succeed")
	requestBody, err := req.Marshal()
	require.NoError(t, err, "marshalling the request must succeed")
	responseBody := []byte(`{"model":"jev-1.13.0","answers":` + string(validAnswers()) + `}`)
	evidence := testInput().Evidence
	require.Len(t, evidence, 1, "the fixture must carry one evidence record")
	refs := []ToolResponseRef{NewToolResponseRef("call_01", "getIndicators", &evidence[0])}
	return req, requestBody, responseBody, testCandidate(), refs
}

// sha256Hex recomputes a digest independently of the package helper, so the
// test proves the value rather than restating the implementation.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestBuildInputManifestHashesTheRightThings(t *testing.T) {
	t.Parallel()

	req, requestBody, responseBody, cand, refs := manifestInputs(t)
	m, err := BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.NoError(t, err, "a complete input must build a manifest")

	assert.Equal(t, SchemaVersionFactualInput, m.SchemaVersion, "the schema version should be pinned")
	assert.Equal(t, QuestionVersionReviewV2, m.QuestionVersion, "the question version should be pinned")
	assert.Equal(t, goodModel, m.RequestModel, "the requested model should be recorded")
	assert.Equal(t, len(requestBody), m.RequestBytes, "the request size should be recorded")

	assert.Equal(t, sha256Hex(requestBody), m.RequestSha256, "the request hash should be the digest of the exact bytes sent")
	assert.Equal(t, sha256Hex(responseBody), m.ParsedResponseSha256, "the response hash should be the digest of the bytes received")
	assert.Equal(t, sha256Hex([]byte(testStrategyInstruction)), m.StrategyInstructionSha256, "the strategy text hash should be recorded")

	candidateJSON, err := json.Marshal(cand)
	require.NoError(t, err, "marshalling the candidate must succeed")
	assert.Equal(t, sha256Hex(candidateJSON), m.CandidateSha256, "the candidate hash should cover the canonical candidate JSON")

	require.Len(t, m.ToolResponses, 1, "every retained tool response must be referenced")
	assert.Equal(t, "call_01", m.ToolResponses[0].CallID, "the call id should be recorded")
	assert.Equal(t, "getIndicators", m.ToolResponses[0].Name, "the tool name should be recorded")
	assert.Equal(t, sha256Hex(testInput().Evidence[0].Data), m.ToolResponses[0].ResultSha256,
		"each tool response hash must cover the exact payload the analyst saw")

	assert.Equal(t, FactsScopeRetained, m.FactsScope, "the facts scope should state the retained responses")
	assert.False(t, m.TradeAuthorized, "a manifest should never claim authority to trade")
}

func TestBuildInputManifestIsDeterministic(t *testing.T) {
	t.Parallel()

	req, requestBody, responseBody, cand, refs := manifestInputs(t)
	first, err := BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.NoError(t, err, "the first build must succeed")
	second, err := BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.NoError(t, err, "the second build must succeed")

	a, err := json.Marshal(first)
	require.NoError(t, err, "the first manifest must marshal")
	b, err := json.Marshal(second)
	require.NoError(t, err, "the second manifest must marshal")
	assert.Equal(t, string(a), string(b), "the same inputs should produce an identical manifest so it can be compared and replayed")
}

func TestBuildInputManifestRejectsIncompleteInput(t *testing.T) {
	t.Parallel()

	req, requestBody, responseBody, cand, refs := manifestInputs(t)

	_, err := BuildInputManifest(nil, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.ErrorIs(t, err, ErrManifestIncomplete, "a nil request must be refused")

	_, err = BuildInputManifest(req, nil, responseBody, cand, testStrategyInstruction, refs)
	require.ErrorIs(t, err, ErrManifestIncomplete, "an empty request body must be refused")

	_, err = BuildInputManifest(req, requestBody, nil, cand, testStrategyInstruction, refs)
	require.ErrorIs(t, err, ErrManifestIncomplete, "an empty response body must be refused")

	_, err = BuildInputManifest(req, requestBody, responseBody, nil, testStrategyInstruction, refs)
	require.ErrorIs(t, err, ErrManifestIncomplete, "a nil candidate must be refused")

	_, err = BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, nil)
	require.ErrorIs(t, err, ErrManifestIncomplete, "a manifest without tool responses proves nothing")

	foreign := *req
	foreign.Model = "gpt-4o"
	_, err = BuildInputManifest(&foreign, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.ErrorIs(t, err, ErrModelInvalid, "a foreign model must be refused")
}

func TestInputManifestValidateRejectsTampering(t *testing.T) {
	t.Parallel()

	req, requestBody, responseBody, cand, refs := manifestInputs(t)
	base := func(t *testing.T) *InputManifest {
		t.Helper()
		m, err := BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, refs)
		require.NoError(t, err, "the fixture manifest must build")
		return m
	}

	testCases := []struct {
		name    string
		mutate  func(*InputManifest)
		wantErr error
	}{
		{
			name:    "claims authority to trade",
			mutate:  func(m *InputManifest) { m.TradeAuthorized = true },
			wantErr: ErrManifestAuthorized,
		},
		{
			name:    "unknown schema version",
			mutate:  func(m *InputManifest) { m.SchemaVersion = "jev-factual-input-v9" },
			wantErr: ErrManifestSchema,
		},
		{
			name:    "missing question version",
			mutate:  func(m *InputManifest) { m.QuestionVersion = " " },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "missing request model",
			mutate:  func(m *InputManifest) { m.RequestModel = "" },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "zero request bytes",
			mutate:  func(m *InputManifest) { m.RequestBytes = 0 },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "truncated request hash",
			mutate:  func(m *InputManifest) { m.RequestSha256 = "abcdef" },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "non hex response hash",
			mutate:  func(m *InputManifest) { m.ParsedResponseSha256 = strings.Repeat("z", 64) },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "empty candidate hash",
			mutate:  func(m *InputManifest) { m.CandidateSha256 = "" },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "empty strategy hash",
			mutate:  func(m *InputManifest) { m.StrategyInstructionSha256 = "" },
			wantErr: ErrManifestIncomplete,
		},
		{
			name:    "facts scope swapped for a weaker source",
			mutate:  func(m *InputManifest) { m.FactsScope = "EVIDENCE_SUMMARY" },
			wantErr: ErrManifestScope,
		},
		{
			name:    "no tool responses",
			mutate:  func(m *InputManifest) { m.ToolResponses = nil },
			wantErr: ErrManifestIncomplete,
		},
		{
			name: "tool response missing its call id",
			mutate: func(m *InputManifest) {
				m.ToolResponses = []ToolResponseRef{{Name: "getIndicators", Ref: "ev_1", ResultSha256: strings.Repeat("a", 64)}}
			},
			wantErr: ErrManifestIncomplete,
		},
		{
			name: "tool response hash malformed",
			mutate: func(m *InputManifest) {
				m.ToolResponses = []ToolResponseRef{{CallID: "c1", Name: "getIndicators", Ref: "ev_1", ResultSha256: "short"}}
			},
			wantErr: ErrManifestIncomplete,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := base(t)
			tc.mutate(m)
			err := m.Validate()
			require.Error(t, err, "the manifest must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}

	var none *InputManifest
	require.ErrorIs(t, none.Validate(), ErrManifestIncomplete, "a nil manifest must be refused")
}

func TestInspectManifestClassification(t *testing.T) {
	t.Parallel()

	req, requestBody, responseBody, cand, refs := manifestInputs(t)
	good, err := BuildInputManifest(req, requestBody, responseBody, cand, testStrategyInstruction, refs)
	require.NoError(t, err, "the fixture manifest must build")
	goodJSON, err := json.Marshal(good)
	require.NoError(t, err, "the manifest must marshal")

	t.Run("bound", func(t *testing.T) {
		t.Parallel()
		state, err := InspectManifest(goodJSON)
		require.NoError(t, err, "a complete manifest must inspect cleanly")
		assert.Equal(t, ManifestBound, state, "a complete manifest should be bound")
		assert.True(t, state.MayProceed(), "only a bound manifest may proceed to order preparation or send claim")
	})

	t.Run("legacy missing", func(t *testing.T) {
		t.Parallel()
		for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("  ")} {
			state, err := InspectManifest(raw)
			require.NoError(t, err, "an absent manifest is a classification, not an error")
			assert.Equal(t, ManifestLegacyMissing, state, "an absent manifest should be classified as legacy")
			assert.False(t, state.MayProceed(), "a legacy record should not authorise an order")
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		cases := map[string]json.RawMessage{
			"not json":         json.RawMessage("nope"),
			"empty object":     json.RawMessage(`{}`),
			"wrong scope":      json.RawMessage(`{"schemaVersion":"jev-factual-input-v1","questionVersion":"v2","requestModel":"jev-1.13.0","requestSha256":"` + strings.Repeat("a", 64) + `","requestBytes":10,"parsedResponseSha256":"` + strings.Repeat("b", 64) + `","candidateSha256":"` + strings.Repeat("c", 64) + `","strategyInstructionSha256":"` + strings.Repeat("d", 64) + `","toolResponses":[{"callId":"c","name":"n","ref":"r","resultSha256":"` + strings.Repeat("e", 64) + `"}],"factsScope":"EVIDENCE_SUMMARY","tradeAuthorized":false}`),
			"claims authority": json.RawMessage(`{"schemaVersion":"jev-factual-input-v1","questionVersion":"v2","requestModel":"jev-1.13.0","requestSha256":"` + strings.Repeat("a", 64) + `","requestBytes":10,"parsedResponseSha256":"` + strings.Repeat("b", 64) + `","candidateSha256":"` + strings.Repeat("c", 64) + `","strategyInstructionSha256":"` + strings.Repeat("d", 64) + `","toolResponses":[{"callId":"c","name":"n","ref":"r","resultSha256":"` + strings.Repeat("e", 64) + `"}],"factsScope":"RETAINED_LLM_TOOL_RESPONSES","tradeAuthorized":true}`),
		}
		for name, raw := range cases {
			state, err := InspectManifest(raw)
			assert.Equalf(t, ManifestInvalid, state, "%s should be classified as invalid", name)
			require.Errorf(t, err, "%s must explain why it is invalid", name)
			assert.Falsef(t, state.MayProceed(), "%s should not authorise an order", name)
		}
	})

	t.Run("no assessment is a caller decision", func(t *testing.T) {
		t.Parallel()
		assert.False(t, ManifestNoAssessment.MayProceed(), "a decision that never called the judge cannot proceed")
	})
}

func TestNewToolResponseRefCoversThePayload(t *testing.T) {
	t.Parallel()

	evidence := candidate.ToolEvidence{
		Ref:      "ev_indicators_01",
		AsOf:     time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		Quality:  candidate.QualityDerived,
		Exchange: "Binance",
		Data:     json.RawMessage(`{"macd":{"histogram":2.4}}`),
	}
	ref := NewToolResponseRef("call_01", "getIndicators", &evidence)
	assert.Equal(t, "call_01", ref.CallID, "the call id should be carried")
	assert.Equal(t, "getIndicators", ref.Name, "the tool name should be carried")
	assert.Equal(t, "ev_indicators_01", ref.Ref, "the evidence ref should be carried")
	assert.Equal(t, sha256Hex(evidence.Data), ref.ResultSha256, "the hash should cover the payload")

	mutated := evidence
	mutated.Data = json.RawMessage(`{"macd":{"histogram":2.5}}`)
	other := NewToolResponseRef("call_01", "getIndicators", &mutated)
	assert.NotEqual(t, ref.ResultSha256, other.ResultSha256, "a changed payload should change the hash")
}
