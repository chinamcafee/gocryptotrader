package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
)

// M4-16 测试：组装序逐段验证 + 门禁先于付费 + 只记录不执行。

func shadowTestRunner(t *testing.T, transport ChatCompleter, tools ToolRunner, judge *fakeJevJudge, epoch *int64) (*ShadowRunner, *recordHarness) {
	t.Helper()
	analysis, _ := analysisPipeline(t, transport, tools)
	jevPipeline, _ := jevTestPipeline(t, judge, epoch)
	rec := recordTestRecorder()
	clock := func() time.Time { return providerAdminNow }
	return &ShadowRunner{
		Scheduler: NewEvaluationScheduler(clock),
		Analysis:  analysis,
		Jev:       jevPipeline,
		Recorder:  rec.recorder,
		Now:       clock,
	}, rec
}

func shadowTestRequest(t *testing.T, built *SnapshotResult) ShadowRunRequest {
	t.Helper()
	base := analysisRequest(built)
	return ShadowRunRequest{
		StrategyVersionID:   base.StrategyVersionID,
		AccountID:           base.AccountID,
		MarketID:            base.MarketID,
		StrategyInstruction: base.StrategyInstruction,
		ProfileRevisionID:   base.ProfileRevisionID,
		JevRevisionID:       "rev-jev",
		LLMModel:            "gpt-test",
		JevModel:            "jev-1.0.0",
		RunMode:             LLMRunModeShadow,
		PairEpoch:           3,
		EnvelopeHash:        "env-hash",
		PreGates:            base.PreGates,
		Assemble:            base.Assemble,
		BuildJev: func(context.Context, *AnalysisOutcome) (JevReviewRequest, error) {
			return *jevTestRequest(t), nil
		},
	}
}

func TestShadowRunnerGateRefusalAndDebounce(t *testing.T) {
	t.Parallel()

	epoch := int64(3)
	judge := &fakeJevJudge{}
	runner, rec := shadowTestRunner(t, &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}}, judge, &epoch)
	in := shadowTestRequest(t, analysisSnapshotFixture(t, false, false))
	in.PreGates.RunMode = LLMRunModeReadOnly

	result, err := runner.RunOnce(context.Background(), &in)
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, result.Decision)
	assert.Equal(t, ReasonModeNotPermitted, result.Reason, "a gate refusal is recorded with the gate's own reason")
	require.Len(t, rec.runs.inserted, 1, "the refusal is a decision and is audited")
	assert.Zero(t, rec.runs.inserted[0].UsageInputTokens, "the gate stops before any model is consulted")
	assert.Zero(t, rec.runs.inserted[0].LLMRoundCount)
	assert.Zero(t, judge.calls)

	// The debounce window is now open for this version: a second immediate run
	// is skipped and records nothing.
	result, err = runner.RunOnce(context.Background(), &in)
	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Equal(t, ReasonSkippedRateLimited, result.SkipReason)
	assert.Positive(t, result.RetryAfter)
	assert.Len(t, rec.runs.inserted, 1, "a skip is not a decision")
}

func TestShadowRunnerAnalysisHoldRecorded(t *testing.T) {
	t.Parallel()

	epoch := int64(3)
	judge := &fakeJevJudge{}
	runner, rec := shadowTestRunner(t, &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}}, judge, &epoch)
	in := shadowTestRequest(t, analysisSnapshotFixture(t, false, true)) // stale snapshot

	result, err := runner.RunOnce(context.Background(), &in)
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, result.Decision)
	assert.Equal(t, "MARKET_SNAPSHOT_STALE", result.Reason)
	assert.Zero(t, judge.calls, "a stale snapshot never reaches the judge")
	require.Len(t, rec.runs.inserted, 1)
	assert.Nil(t, rec.runs.inserted[0].JevJSON, "no judge ran, so no judge row content")
}

func TestShadowRunnerFullChainRecordsAnAllow(t *testing.T) {
	t.Parallel()

	epoch := int64(3)
	judge := &fakeJevJudge{assessment: synthAssessment(jev.CriteriaOpenLong, 2, 0.9)}
	transport, tools := analysisToolThenFinal(t, analysisEnvelope(t, candidate.QualityExchangeREST), analysisCandidate("100", "95"))
	runner, rec := shadowTestRunner(t, transport, tools, judge, &epoch)
	in := shadowTestRequest(t, analysisSnapshotFixture(t, false, false))
	in.RiskFacts = func(context.Context, *AnalysisOutcome, *JevReviewOutcome) (*RiskInput, *SizingInput, error) {
		return riskOKInput(), sizingTestInput(), nil
	}

	result, err := runner.RunOnce(context.Background(), &in)
	require.NoError(t, err)
	assert.Equal(t, RiskVerdictAllow, result.Decision, "SHADOW records what would have been placed")
	assert.Equal(t, ReasonAwaitingHardRiskAndApproval, result.Reason, "the reason records the gate that advanced the run")
	assert.Equal(t, 1, judge.calls)

	require.Len(t, rec.runs.inserted, 1)
	run := rec.runs.inserted[0]
	assert.Equal(t, RiskVerdictAllow, run.Decision)
	assert.Equal(t, DecisionTriggerAutoShadow, run.Trigger)
	assert.Equal(t, LLMRunModeShadow, run.RunMode)
	assert.Equal(t, "gpt-test", run.LLMModel)
	require.NotNil(t, run.JevModel)
	assert.Equal(t, "jev-1.0.0", *run.JevModel)
	assert.Equal(t, int64(2), run.LLMRoundCount)
	assert.Equal(t, int64(1), run.ToolCallCount)
	require.NotNil(t, run.JevJSON)

	require.Len(t, rec.risk.inserted, 1, "the hard-risk verdict is recorded alongside")
	riskRow := rec.risk.inserted[0]
	require.NotNil(t, riskRow.ComputedQty)
	assert.Equal(t, "0.0199", *riskRow.ComputedQty, "the recomputed quantity is the one the audit shows")
	require.Len(t, rec.audit.inserted, 1)
}

func TestShadowRunnerJudgeBuildErrorHolds(t *testing.T) {
	t.Parallel()

	epoch := int64(3)
	judge := &fakeJevJudge{}
	transport, tools := analysisToolThenFinal(t, analysisEnvelope(t, candidate.QualityExchangeREST), analysisCandidate("100", "95"))
	runner, rec := shadowTestRunner(t, transport, tools, judge, &epoch)
	in := shadowTestRequest(t, analysisSnapshotFixture(t, false, false))
	in.BuildJev = func(context.Context, *AnalysisOutcome) (JevReviewRequest, error) {
		return JevReviewRequest{}, errors.New("evidence assembly failed")
	}

	result, err := runner.RunOnce(context.Background(), &in)
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, result.Decision, "a judge that cannot be assembled stops automation")
	assert.Equal(t, ReasonJevUnavailable, result.Reason)
	assert.Zero(t, judge.calls)
	require.Len(t, rec.runs.inserted, 1)
}

func TestShadowRunnerMisuseNeverRuns(t *testing.T) {
	t.Parallel()

	var nilRunner *ShadowRunner
	_, err := nilRunner.RunOnce(context.Background(), &ShadowRunRequest{})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
