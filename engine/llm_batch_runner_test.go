package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M5-19 测试：批量作业的串行执行、失败隔离、审批绑定与拒绝径。

func batchTestApprovals() iTriggerApprovals {
	return approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
		return []llmstore.StrategyApproval{{
			ID: "appr-1", StrategyVersionID: "ver-1", AccountID: "acct-1", Network: "net-1",
		}}, nil
	})
}

func batchTestRunner() *BatchRunner {
	counter := 0
	runner := &BatchRunner{
		Approvals:  batchTestApprovals(),
		AccountID:  "acct-1",
		Network:    "net-1",
		MaxMarkets: 10,
		NewID:      func() string { counter++; return fmt.Sprintf("job-%d", counter) },
		Now:        func() time.Time { return providerAdminNow },
	}
	runner.Build = func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
		if marketID == "Binance:usdtmarginedfutures:BAD_USDT" {
			return nil, errors.New("no such market")
		}
		return &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID}, nil
	}
	runner.Runner = runnerFunc(func(_ context.Context, req *ShadowRunRequest) (*ShadowRunResult, error) {
		switch req.MarketID {
		case "Binance:usdtmarginedfutures:BTC_USDT":
			return &ShadowRunResult{Decision: "ALLOW", Reason: "AWAITING", DecisionRunID: "run-btc"}, nil
		case "Binance:usdtmarginedfutures:ETH_USDT":
			return &ShadowRunResult{Skipped: true, SkipReason: "DEBOUNCED"}, nil
		default:
			return nil, errors.New("venue exploded")
		}
	})
	return runner
}

func waitBatchDone(t *testing.T, runner *BatchRunner, id string) *BatchJobView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, ok := runner.Get(id)
		require.True(t, ok, "the job must be readable while it runs")
		if view.Done {
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("batch job did not finish in time")
	return nil
}

func TestBatchRunnerRunsSeriallyAndIsolatesFailures(t *testing.T) {
	runner := batchTestRunner()
	job, err := runner.Start(t.Context(), BatchKindOpenDecision, "ver-1", []string{
		"Binance:usdtmarginedfutures:BTC_USDT",
		"Binance:usdtmarginedfutures:BAD_USDT",
		"Binance:usdtmarginedfutures:ETH_USDT",
		"Binance:usdtmarginedfutures:SOL_USDT",
	}, "op")
	require.NoError(t, err)
	require.Equal(t, "job-1", job.ID)
	require.Len(t, job.Rows, 4)

	view := waitBatchDone(t, runner, job.ID)
	require.Len(t, view.Rows, 4)
	assert.Equal(t, BatchRowDone, view.Rows[0].Status)
	assert.Equal(t, "ALLOW", view.Rows[0].Decision)
	assert.Equal(t, "run-btc", view.Rows[0].DecisionRunID)
	assert.Equal(t, BatchRowFailed, view.Rows[1].Status, "a build failure is this row's outcome")
	assert.Contains(t, view.Rows[1].Error, "no such market")
	assert.Equal(t, BatchRowSkipped, view.Rows[2].Status, "a debounced run reports its skip reason")
	assert.Equal(t, "DEBOUNCED", view.Rows[2].Reason)
	assert.Equal(t, BatchRowFailed, view.Rows[3].Status, "a runner failure is isolated to its row")
}

func TestBatchRunnerRefusals(t *testing.T) {
	runner := batchTestRunner()

	// No active approval for the version: the batch refuses.
	_, err := runner.Start(t.Context(), BatchKindOpenDecision, "ver-missing",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "op")
	require.Error(t, err)

	// Empty and oversized market lists refuse.
	_, err = runner.Start(t.Context(), BatchKindOpenDecision, "ver-1", nil, "op")
	require.Error(t, err)
	markets := make([]string, 0, 11)
	for i := range 11 {
		markets = append(markets, fmt.Sprintf("Binance:usdtmarginedfutures:M%d_USDT", i))
	}
	_, err = runner.Start(t.Context(), BatchKindOpenDecision, "ver-1", markets, "op")
	require.Error(t, err, "11 markets exceed the default cap of 10")

	// The position-review kind refuses until its runner is assembled.
	_, err = runner.Start(t.Context(), BatchKindPositionReview, "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "op")
	require.Error(t, err)

	// Unknown kinds refuse.
	_, err = runner.Start(t.Context(), "TURBO", "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "op")
	require.Error(t, err)

	// An empty actor refuses.
	_, err = runner.Start(t.Context(), BatchKindOpenDecision, "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "")
	require.Error(t, err)
}

func TestBatchRunnerPositionReviewKindUsesItsBuilder(t *testing.T) {
	runner := batchTestRunner()
	runner.ReviewBuild = func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
		return &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID, EnvelopeHash: "review"}, nil
	}
	job, err := runner.Start(t.Context(), BatchKindPositionReview, "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "op")
	require.NoError(t, err)
	view := waitBatchDone(t, runner, job.ID)
	require.Len(t, view.Rows, 1)
	assert.Equal(t, BatchRowDone, view.Rows[0].Status)
	assert.Equal(t, BatchKindPositionReview, view.Kind)
}

func TestBatchRunnerAuditsLaunch(t *testing.T) {
	runner := batchTestRunner()
	audit := &recordFakeAudit{}
	runner.Audit = audit
	job, err := runner.Start(t.Context(), BatchKindOpenDecision, "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT", "Binance:usdtmarginedfutures:ETH_USDT"}, "op")
	require.NoError(t, err)
	require.Len(t, audit.inserted, 1, "every launch is a governance act and is audited")
	event := audit.inserted[0]
	assert.Equal(t, "BATCH_DECISION_STARTED", event.Kind)
	assert.Equal(t, "op", event.Actor)
	require.NotNil(t, event.Subject)
	assert.Equal(t, job.ID, *event.Subject, "the job id is the audit subject")
	assert.Contains(t, event.DetailJSON, `"markets":2`)
	assert.Contains(t, event.DetailJSON, `"strategyVersionId":"ver-1"`)

	failing := batchTestRunner()
	failing.Audit = &recordFakeAudit{err: errors.New("audit store down")}
	_, err = failing.Start(t.Context(), BatchKindOpenDecision, "ver-1",
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"}, "op")
	require.ErrorContains(t, err, "audit event", "a launch that cannot be audited is refused")
}

func TestBatchRunnerCancelStopsRemainingMarkets(t *testing.T) {
	started := make(chan struct{}, 1)
	runner := batchTestRunner()
	runner.Runner = runnerFunc(func(ctx context.Context, _ *ShadowRunRequest) (*ShadowRunResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	job, err := runner.Start(t.Context(), BatchKindOpenDecision, "ver-1", []string{
		"Binance:usdtmarginedfutures:BTC_USDT",
		"Binance:usdtmarginedfutures:ETH_USDT",
		"Binance:usdtmarginedfutures:SOL_USDT",
	}, "op")
	require.NoError(t, err)
	<-started

	view, err := runner.Cancel(job.ID, "op")
	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Equal(t, BatchRowCancelled, view.Rows[1].Status, "pending rows are cancelled immediately")
	assert.Equal(t, BatchRowCancelled, view.Rows[2].Status)

	done := waitBatchDone(t, runner, job.ID)
	assert.Equal(t, BatchRowCancelled, done.Rows[0].Status, "the in-flight market is cancelled through its context")
	assert.True(t, done.Done)

	_, err = runner.Cancel("missing-job", "op")
	require.ErrorIs(t, err, errLLMStrategyInput)
	_, err = runner.Cancel(job.ID, "op")
	require.ErrorIs(t, err, errLLMStrategyInput, "a finished job has nothing left to cancel")
}
