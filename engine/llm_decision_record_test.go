package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-13 测试：三张不可变表的落账顺序、决策映射、输入哈希的确定性与敏感性。

type recordFakeRuns struct {
	inserted []llmstore.DecisionRun
	err      error
}

func (f *recordFakeRuns) InsertDecisionRun(_ context.Context, r *llmstore.DecisionRun) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *r)
	return nil
}

type recordFakeRisk struct {
	inserted []llmstore.RiskEvaluation
	err      error
}

func (f *recordFakeRisk) InsertRiskEvaluation(_ context.Context, e *llmstore.RiskEvaluation) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *e)
	return nil
}

type recordFakeAudit struct {
	inserted []llmstore.AuditEvent
	err      error
}

func (f *recordFakeAudit) InsertAuditEvent(_ context.Context, e *llmstore.AuditEvent) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *e)
	return nil
}

type recordHarness struct {
	recorder *DecisionRecorder
	runs     *recordFakeRuns
	risk     *recordFakeRisk
	audit    *recordFakeAudit
	now      time.Time
}

func recordTestRecorder() *recordHarness {
	runs := &recordFakeRuns{}
	risk := &recordFakeRisk{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 12, 30, 0, 0, time.UTC)
	counter := 0
	return &recordHarness{
		recorder: &DecisionRecorder{
			Runs: runs, Risk: risk, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		runs: runs, risk: risk, audit: audit, now: now,
	}
}

func recordTestInput() *DecisionRecordInput {
	return &DecisionRecordInput{
		Trigger:              DecisionTriggerAutoCandidate,
		RunMode:              LLMRunModeTestnet,
		StrategyVersionID:    "4b1d2e71-2b3c-4d4e-9f50-6b7c8d9e0f10",
		SnapshotID:           "3a0c1f60-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
		LLMProfileRevisionID: "rev-llm",
		JevProfileRevisionID: "rev-jev",
		LLMModel:             "gpt-test",
		JevModel:             "jev-1.13.0",
		PromptVersion:        "analysis-readonly-v1",
		PairEpoch:            7,
		EnvelopeHash:         "env-hash",
		LatencyMS:            1234,
		Analysis: &AnalysisOutcome{
			Verdict:           VerdictProceed,
			SnapshotID:        "3a0c1f60-1a2b-4c3d-8e4f-5a6b7c8d9e0f",
			Candidate:         &candidate.Candidate{MarketID: "Binance:usdtmarginedfutures:BTC_USDT", Action: candidate.ActionOpenLong, Thesis: "momentum"},
			TranscriptJSON:    `{"entries":[]}`,
			Rounds:            2,
			ToolCalls:         3,
			UsageInputTokens:  120,
			UsageOutputTokens: 45,
			Tokens:            165,
		},
		Synthesis: &SynthesisOutcome{
			Verdict:    VerdictReview,
			ReasonCode: ReasonAwaitingHardRiskAndApproval,
			Candidate:  &candidate.Candidate{MarketID: "Binance:usdtmarginedfutures:BTC_USDT", Action: candidate.ActionOpenLong, Thesis: "momentum"},
		},
	}
}

func TestDecisionRecorderWritesTheHoldChain(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	in.Synthesis = &SynthesisOutcome{Verdict: VerdictHold, ReasonCode: "MARKET_SNAPSHOT_STALE"}
	in.Analysis.Candidate = nil
	result, err := h.recorder.Record(context.Background(), in)
	require.NoError(t, err)

	require.Len(t, h.runs.inserted, 1)
	run := h.runs.inserted[0]
	assert.Equal(t, "id-1", run.ID)
	assert.Equal(t, VerdictHold, run.Decision)
	assert.Equal(t, "MARKET_SNAPSHOT_STALE", run.Reason)
	assert.Equal(t, DecisionTriggerAutoCandidate, run.Trigger)
	assert.Equal(t, h.now, run.CreatedAt)
	assert.Len(t, run.InputHash, 64, "the input hash is a SHA-256 hex digest")
	assert.Nil(t, run.CandidateJSON, "no candidate means a NULL column, not an empty string")
	assert.Nil(t, run.JevJSON)

	assert.Empty(t, h.risk.inserted, "no hard-risk verdict means no risk evaluation row")
	require.Len(t, h.audit.inserted, 1)
	event := h.audit.inserted[0]
	assert.Equal(t, AuditKindDecisionRecorded, event.Kind)
	require.NotNil(t, event.Subject)
	assert.Equal(t, run.ID, *event.Subject)
	assert.Contains(t, event.DetailJSON, "MARKET_SNAPSHOT_STALE")

	assert.Equal(t, run.ID, result.DecisionRunID)
	assert.Equal(t, VerdictHold, result.Decision)
}

func TestDecisionRecorderMapsTheRiskVerdict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		risk       *RiskResult
		wantDecide string
		wantReason string
	}{
		{"allow", &RiskResult{Verdict: RiskVerdictAllow}, RiskVerdictAllow, ReasonAwaitingHardRiskAndApproval},
		{"reject uses the first refusal", &RiskResult{
			Verdict:    RiskVerdictReject,
			Rejections: []RiskRejection{{Code: RiskHalted}, {Code: RiskMarketNotGranted}},
		}, RiskVerdictReject, RiskHalted},
		{"review", &RiskResult{
			Verdict:    RiskVerdictReview,
			Rejections: []RiskRejection{{Code: RiskOpposingPosition}},
		}, RiskVerdictReview, RiskOpposingPosition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := recordTestRecorder()
			in := recordTestInput()
			in.Risk = tc.risk
			result, err := h.recorder.Record(context.Background(), in)
			require.NoError(t, err)
			assert.Equal(t, tc.wantDecide, h.runs.inserted[0].Decision)
			assert.Equal(t, tc.wantReason, h.runs.inserted[0].Reason)
			assert.Equal(t, tc.wantDecide, result.Decision)
		})
	}
}

func TestDecisionRecorderWritesTheRiskRowWithSizing(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	in.Risk = &RiskResult{
		Verdict: RiskVerdictAllow,
		Checks:  []RiskCheckResult{{Code: RiskHalted, Passed: true}},
	}
	in.Sizing = &SizingResult{Verdict: RiskVerdictAllow, Quantity: "0.0199", NotionalUSD: "19.9", ReferencePrice: "1000"}
	_, err := h.recorder.Record(context.Background(), in)
	require.NoError(t, err)

	require.Len(t, h.risk.inserted, 1)
	row := h.risk.inserted[0]
	assert.Equal(t, h.runs.inserted[0].ID, row.DecisionRunID)
	assert.Equal(t, RiskCheckVersion, row.CheckVersion, "the default check version is the current catalog")
	assert.Equal(t, RiskVerdictAllow, row.Verdict)
	assert.Equal(t, "env-hash", row.EnvelopeHash)
	assert.Equal(t, h.now, row.EvaluatedAt)
	assert.Nil(t, row.RejectionsJSON, "an allow has no rejections column content")
	var checks []RiskCheckResult
	require.NoError(t, json.Unmarshal([]byte(row.ChecksJSON), &checks))
	assert.Len(t, checks, 1)
	require.NotNil(t, row.ComputedQty)
	assert.Equal(t, "0.0199", *row.ComputedQty)
	require.NotNil(t, row.ComputedNotional)
	assert.Equal(t, "19.9", *row.ComputedNotional)
	require.NotNil(t, row.ReferencePrice)
	assert.Equal(t, "1000", *row.ReferencePrice)
}

func TestDecisionRecorderWritesRejectionsJSON(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	in.Risk = &RiskResult{Verdict: RiskVerdictReject, Rejections: []RiskRejection{{Code: RiskHalted, Actual: "halted", Allowed: "no halt"}}}
	_, err := h.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, h.risk.inserted, 1)
	require.NotNil(t, h.risk.inserted[0].RejectionsJSON)
	var rejections []RiskRejection
	require.NoError(t, json.Unmarshal([]byte(*h.risk.inserted[0].RejectionsJSON), &rejections))
	require.Len(t, rejections, 1)
	assert.Equal(t, RiskHalted, rejections[0].Code)
}

func TestDecisionInputHashIsDeterministicAndSensitive(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	result1, err := h.recorder.Record(context.Background(), in)
	require.NoError(t, err)

	h2 := recordTestRecorder()
	result2, err := h2.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, result1.InputHash, result2.InputHash, "the same inputs should hash identically")

	in.PairEpoch = 8
	h3 := recordTestRecorder()
	result3, err := h3.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	assert.NotEqual(t, result1.InputHash, result3.InputHash, "a different epoch is a different input")

	in = recordTestInput()
	in.Synthesis.Candidate.Thesis = "different words"
	h4 := recordTestRecorder()
	result4, err := h4.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	assert.NotEqual(t, result1.InputHash, result4.InputHash, "a different candidate is a different input")

	in = recordTestInput()
	in.SnapshotID = ""
	h5 := recordTestRecorder()
	result5, err := h5.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	assert.NotEqual(t, result1.InputHash, result5.InputHash)
}

func TestDecisionRecorderCarriesAnalysisFacts(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	in.Jev = &JevReviewOutcome{InputTokens: 400, OutputTokens: 30, RequestSha256: "req-sha"}
	_, err := h.recorder.Record(context.Background(), in)
	require.NoError(t, err)
	run := h.runs.inserted[0]
	assert.Equal(t, int64(2), run.LLMRoundCount)
	assert.Equal(t, int64(3), run.ToolCallCount)
	assert.Equal(t, int64(120), run.UsageInputTokens)
	assert.Equal(t, int64(45), run.UsageOutputTokens)
	assert.Equal(t, int64(1234), run.LatencyMS)
	require.NotNil(t, run.ToolTranscriptJSON)
	assert.JSONEq(t, `{"entries":[]}`, *run.ToolTranscriptJSON)
	require.NotNil(t, run.StrategyVersionID)
	assert.Equal(t, "4b1d2e71-2b3c-4d4e-9f50-6b7c8d9e0f10", *run.StrategyVersionID)
	require.NotNil(t, run.JevInputTokens)
	assert.Equal(t, int64(400), *run.JevInputTokens)
	require.NotNil(t, run.JevJSON)
	assert.Contains(t, *run.JevJSON, "req-sha", "the judge row keeps the request certificate")
}

func TestDecisionRecorderRequiresASynthesisOutcome(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	in := recordTestInput()
	in.Synthesis = nil
	_, err := h.recorder.Record(context.Background(), in)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrChatNotConfigured)
	assert.Empty(t, h.runs.inserted, "nothing may be written without a decision")

	var nilRecorder *DecisionRecorder
	_, err = nilRecorder.Record(context.Background(), in)
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

func TestDecisionRecorderStopsWhenAWriteFails(t *testing.T) {
	t.Parallel()

	h := recordTestRecorder()
	h.runs.err = errors.New("db down")
	in := recordTestInput()
	in.Risk = &RiskResult{Verdict: RiskVerdictAllow}
	_, err := h.recorder.Record(context.Background(), in)
	require.Error(t, err)
	assert.Empty(t, h.risk.inserted, "a failed decision run should not leave orphan rows")
	assert.Empty(t, h.audit.inserted)

	h = recordTestRecorder()
	h.risk.err = errors.New("db down")
	in = recordTestInput()
	in.Risk = &RiskResult{Verdict: RiskVerdictAllow}
	_, err = h.recorder.Record(context.Background(), in)
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted, "the audit event is written only when the whole chain landed")

	h = recordTestRecorder()
	h.audit.err = errors.New("db down")
	in = recordTestInput()
	_, err = h.recorder.Record(context.Background(), in)
	require.Error(t, err, "a decision that cannot be audited must surface as an error")
}
