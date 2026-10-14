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
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

const analysisMarket = policyTestMarket

func analysisSnapshotFixture(t *testing.T, degraded, stale bool) *SnapshotResult {
	t.Helper()
	built := &snapshot.MarketSnapshot{ID: "0f8fad5b-d9cb-469f-a165-70867728950e", SourceTime: providerAdminNow}
	for _, category := range []string{snapshot.CategoryCandles, snapshot.CategoryIndicators, snapshot.CategoryAccount} {
		quality := candidate.QualityExchangeREST
		if degraded && category == snapshot.CategoryCandles {
			quality = candidate.QualityGap
		}
		require.NoError(t, built.SetQuality(category, quality))
	}
	report := &snapshot.FreshnessReport{EvaluatedAt: providerAdminNow}
	if stale {
		report.BlockingStale = []string{snapshot.CategoryCandles}
	}
	return &SnapshotResult{Snapshot: built, Freshness: report}
}

func analysisPipeline(t *testing.T, transport ChatCompleter, runner ToolRunner) (*AnalysisPipeline, *chatTestBudget) {
	t.Helper()
	budget := &chatTestBudget{}
	maxTokens := int64(2048)
	return &AnalysisPipeline{
		Transport: transport,
		Tools:     runner,
		Budget:    budget,
		Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
			ID: "rev-1", Kind: ProviderKindLLM, ModelID: "gpt-test",
			TimeoutSeconds: 60, MaxOutputTokens: &maxTokens,
		}},
		Now: func() time.Time { return providerAdminNow },
	}, budget
}

func analysisRequest(built *SnapshotResult) *AnalysisRequest {
	pre := passingPreGateInput()
	pre.Now = providerAdminNow
	pre.StrategyValidFrom = providerAdminNow.Add(-time.Hour)
	pre.StrategyValidUntil = providerAdminNow.Add(3 * time.Hour)
	pre.ProbeCheckedAt = providerAdminNow.Add(-5 * time.Minute)
	return &AnalysisRequest{
		PreGates:            *pre,
		Assemble:            func(context.Context) (*SnapshotResult, error) { return built, nil },
		ProfileRevisionID:   "rev-1",
		StrategyVersionID:   "16fd2706-8baf-433b-82eb-8c7a7fdaa7f7",
		AccountID:           "acct-1",
		MarketID:            analysisMarket,
		StrategyInstruction: "低频趋势跟随",
	}
}

// analysisEnvelope builds one tool response with a chosen quality label.
func analysisEnvelope(t *testing.T, quality candidate.DataQuality) string {
	t.Helper()
	marketID := analysisMarket
	evidence := candidate.ToolEvidence{
		Ref: "tool:getCandleWindow:call-1", AsOf: providerAdminNow.Add(-time.Minute),
		Quality: quality, Exchange: "Binance", MarketID: &marketID,
		Data: json.RawMessage(`{"candles":[]}`),
	}
	raw, err := json.Marshal(evidence)
	require.NoError(t, err)
	return string(raw)
}

// analysisCandidate builds a valid non-HOLD candidate citing call-1.
func analysisCandidate(entry, exit string) string {
	return `{"strategyVersionId":"16fd2706-8baf-433b-82eb-8c7a7fdaa7f7","snapshotId":"0f8fad5b-d9cb-469f-a165-70867728950e","marketId":"` + analysisMarket + `",` +
		`"action":"OPEN_LONG","thesis":"结构偏多","evidenceRefs":["0f8fad5b-d9cb-469f-a165-70867728950e","tool:getCandleWindow:call-1"],` +
		`"invalidation":"跌破前低","suggestedEntry":"` + entry + `","suggestedExit":"` + exit + `",` +
		`"proposedMaxNotionalUsd":"20","uncertainty":"MEDIUM","ttlSeconds":60}`
}

// analysisToolThenFinal scripts one tool round followed by a final answer.
func analysisToolThenFinal(t *testing.T, envelope, finalContent string) (*chatTestTransport, *fakeRunner) {
	t.Helper()
	withTool := chatMessage("", 120, 30)
	withTool.Choices[0].FinishReason = "tool_calls"
	withTool.Choices[0].Message.ToolCalls = []openai.ToolCall{toolCall("call-1", "getCandleWindow", `{}`)}
	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		withTool,
		chatMessage(finalContent, 200, 90),
	}}
	runner := &fakeRunner{
		supported: map[string]bool{"getCandleWindow": true},
		results:   map[string]string{"getCandleWindow": envelope},
	}
	return transport, runner
}

func TestAnalysisProceedsWithToolEvidence(t *testing.T) {
	transport, runner := analysisToolThenFinal(t, analysisEnvelope(t, candidate.QualityExchangeREST), analysisCandidate("60000.5", "58000"))
	pipeline, budget := analysisPipeline(t, transport, runner)
	built := analysisSnapshotFixture(t, false, false)

	outcome, err := pipeline.Run(t.Context(), analysisRequest(built))
	require.NoError(t, err)
	assert.Equal(t, VerdictProceed, outcome.Verdict)
	require.NotNil(t, outcome.Candidate)
	assert.Equal(t, candidate.ActionOpenLong, outcome.Candidate.Action)
	assert.Equal(t, "0f8fad5b-d9cb-469f-a165-70867728950e", outcome.SnapshotID)
	assert.Equal(t, 2, outcome.Rounds)
	assert.Equal(t, 1, outcome.ToolCalls)
	assert.NotEmpty(t, outcome.TranscriptJSON)

	assert.Equal(t, 1, budget.reserved)
	assert.Equal(t, "DECISION", budget.lastPurpose)
	assert.Equal(t, 1, budget.marked, "a loop that ran settles its reservation even if gates later refuse")
	assert.Zero(t, budget.released)

	require.Len(t, transport.requests, 2)
	require.GreaterOrEqual(t, len(transport.requests[0].Messages), 2)
	assert.Equal(t, "system", transport.requests[0].Messages[0].Role)
	analysisPrompt, err := openai.PromptFor(openai.PromptVersionAnalysisReadOnly)
	require.NoError(t, err)
	assert.Equal(t, analysisPrompt, transport.requests[0].Messages[0].Content)
	assert.Contains(t, transport.requests[0].Messages[1].Content, "0f8fad5b-d9cb-469f-a165-70867728950e", "the snapshot facts should be in context")
	assert.Contains(t, transport.requests[0].Messages[1].Content, analysisMarket,
		"the run market id should be in context: the prompt requires the candidate to copy it verbatim")
	assert.Contains(t, transport.requests[0].Messages[1].Content, "低频趋势跟随", "the approved instruction should be in context")
}

func TestAnalysisPreGatesCloseThePipeWithoutSpending(t *testing.T) {
	transport := &chatTestTransport{}
	pipeline, budget := analysisPipeline(t, transport, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	built := analysisSnapshotFixture(t, false, false)

	request := analysisRequest(built)
	request.PreGates.Halted = true
	outcome, err := pipeline.Run(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, outcome.Verdict)
	assert.Equal(t, ReasonHalted, outcome.ReasonCode)
	assert.Empty(t, transport.requests, "a refused pre-gate should not reach the provider")
	assert.Zero(t, budget.reserved, "and should not touch the budget")

	request = analysisRequest(built)
	request.PreGates.PairReadiness = LLMPairDegraded
	outcome, err = pipeline.Run(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, ReasonPairDegraded, outcome.ReasonCode)
	assert.Zero(t, budget.reserved)
}

func TestAnalysisSnapshotMustBeFitToDecide(t *testing.T) {
	transport := &chatTestTransport{}
	pipeline, budget := analysisPipeline(t, transport, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})

	outcome, err := pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, true)))
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, outcome.Verdict)
	assert.Equal(t, "MARKET_SNAPSHOT_STALE", outcome.ReasonCode, "a snapshot that may not be decided on stops before the model")
	assert.Zero(t, budget.reserved)

	outcome, err = pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, true, false)))
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, outcome.Verdict)
	assert.Equal(t, "MARKET_SNAPSHOT_STALE", outcome.ReasonCode, "a degraded fact set is unfit for the same reason")
	assert.Empty(t, transport.requests)
}

func TestAnalysisBudgetExhaustedIsAHold(t *testing.T) {
	transport := &chatTestTransport{}
	pipeline, budget := analysisPipeline(t, transport, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	budget.reserveErr = ErrBudgetExhausted

	outcome, err := pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, false)))
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, outcome.Verdict)
	assert.Equal(t, ReasonBudgetExhausted, outcome.ReasonCode)
	assert.Empty(t, transport.requests, "no budget, no provider call")
}

func TestAnalysisLoopHoldSettlesAndReports(t *testing.T) {
	truncated := chatMessage("半截", 150, 40)
	truncated.Choices[0].FinishReason = "length"
	transport := &chatTestTransport{responses: []*openai.ChatResponse{truncated}}
	pipeline, budget := analysisPipeline(t, transport, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})

	outcome, err := pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, false)))
	require.NoError(t, err)
	assert.Equal(t, VerdictHold, outcome.Verdict)
	assert.Equal(t, ReasonTruncated, outcome.ReasonCode)
	assert.Nil(t, outcome.Candidate)
	assert.Equal(t, 1, budget.marked, "the model's tokens are spent whatever the loop decided")
}

func TestAnalysisCandidateGates(t *testing.T) {
	cases := []struct {
		name     string
		envelope func(*testing.T) string
		final    string
		want     string
		verdict  string
	}{
		{
			name: "malformed json", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: "{not json", want: "CANDIDATE_JSON_INVALID", verdict: VerdictHold,
		},
		{
			name: "unknown field", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: `{"strategyVersionId":"16fd2706-8baf-433b-82eb-8c7a7fdaa7f7","snapshotId":"0f8fad5b-d9cb-469f-a165-70867728950e","marketId":"` + analysisMarket + `","action":"OPEN_LONG","thesis":"t","evidenceRefs":["0f8fad5b-d9cb-469f-a165-70867728950e","tool:getCandleWindow:call-1"],"invalidation":"i","suggestedEntry":"1","suggestedExit":"2","proposedMaxNotionalUsd":"20","uncertainty":"MEDIUM","ttlSeconds":60,"sneaky":1}`,
			want:  "CANDIDATE_JSON_INVALID", verdict: VerdictHold,
		},
		{
			name: "schema invalid action", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: `{"strategyVersionId":"16fd2706-8baf-433b-82eb-8c7a7fdaa7f7","snapshotId":"0f8fad5b-d9cb-469f-a165-70867728950e","marketId":"` + analysisMarket + `","action":"MOON","thesis":"t","evidenceRefs":["0f8fad5b-d9cb-469f-a165-70867728950e","tool:getCandleWindow:call-1"],"invalidation":"i","suggestedEntry":"1","suggestedExit":"2","proposedMaxNotionalUsd":"20","uncertainty":"MEDIUM","ttlSeconds":60}`,
			want:  "CANDIDATE_SCHEMA_INVALID", verdict: VerdictHold,
		},
		{
			name: "wrong snapshot id", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: `{"strategyVersionId":"16fd2706-8baf-433b-82eb-8c7a7fdaa7f7","snapshotId":"27ae3f9c-42d1-4e0e-9f6b-8a5b6c7d8e9f","marketId":"` + analysisMarket + `","action":"OPEN_LONG","thesis":"t","evidenceRefs":["27ae3f9c-42d1-4e0e-9f6b-8a5b6c7d8e9f","tool:getCandleWindow:call-1"],"invalidation":"i","suggestedEntry":"1","suggestedExit":"2","proposedMaxNotionalUsd":"20","uncertainty":"MEDIUM","ttlSeconds":60}`,
			want:  "CANDIDATE_SCOPE_DENIED", verdict: VerdictHold,
		},
		{
			name: "unknown evidence ref", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: `{"strategyVersionId":"16fd2706-8baf-433b-82eb-8c7a7fdaa7f7","snapshotId":"0f8fad5b-d9cb-469f-a165-70867728950e","marketId":"` + analysisMarket + `","action":"OPEN_LONG","thesis":"t","evidenceRefs":["0f8fad5b-d9cb-469f-a165-70867728950e","tool:getCandleWindow:call-999"],"invalidation":"i","suggestedEntry":"1","suggestedExit":"2","proposedMaxNotionalUsd":"20","uncertainty":"MEDIUM","ttlSeconds":60}`,
			want:  "CANDIDATE_EVIDENCE_UNKNOWN", verdict: VerdictHold,
		},
		{
			name: "non-hold without levels", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityExchangeREST) },
			final: analysisCandidate("", "58000"), want: "CANDIDATE_INCOMPLETE", verdict: VerdictHold,
		},
		{
			name: "risk on local evidence needs review", envelope: func(t *testing.T) string { t.Helper(); return analysisEnvelope(t, candidate.QualityLocalTracked) },
			final: analysisCandidate("60000.5", "58000"), want: ReasonReviewEvidenceQuality, verdict: VerdictReview,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, runner := analysisToolThenFinal(t, tc.envelope(t), tc.final)
			pipeline, budget := analysisPipeline(t, transport, runner)
			outcome, err := pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, false)))
			require.NoError(t, err)
			assert.Equal(t, tc.verdict, outcome.Verdict)
			assert.Equal(t, tc.want, outcome.ReasonCode)
			assert.Equal(t, 1, budget.marked, "the loop ran, so its tokens are spent either way")
		})
	}
}

func TestAnalysisMisuseAndProviderFailure(t *testing.T) {
	pipeline, _ := analysisPipeline(t, &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	var nilPipeline *AnalysisPipeline
	_, err := nilPipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, false)))
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	request := analysisRequest(analysisSnapshotFixture(t, false, false))
	request.Assemble = nil
	_, err = pipeline.Run(t.Context(), request)
	assert.ErrorIs(t, err, ErrChatNotConfigured, "an assembler-less pipeline should refuse rather than decide blind")

	transport := &chatTestTransport{err: errors.New("upstream exploded")}
	pipeline, budget := analysisPipeline(t, transport, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	_, err = pipeline.Run(t.Context(), analysisRequest(analysisSnapshotFixture(t, false, false)))
	require.Error(t, err)
	assert.Equal(t, 1, budget.released, "a provider that was never reached releases its slot")
	assert.Zero(t, budget.marked)
}
