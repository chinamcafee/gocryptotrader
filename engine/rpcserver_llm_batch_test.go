package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// 批量工作台 RPC 面：未装配 fail-closed、起批→轮询→结果往返、未知作业拒绝。

func TestLLMBatchDecisionHandlers(t *testing.T) {
	srv := &RPCServer{Engine: &Engine{}}
	_, err := srv.LLMStartBatchDecision(t.Context(), &gctrpc.LLMStartBatchDecisionRequest{})
	require.ErrorIs(t, err, errLLMDecisionRunner, "an unassembled batch runner refuses")
	_, err = srv.LLMGetBatchDecision(t.Context(), &gctrpc.LLMGetBatchDecisionRequest{JobId: "j"})
	require.ErrorIs(t, err, errLLMDecisionRunner)

	srv = &RPCServer{Engine: &Engine{llmBatchRunner: batchTestRunner()}}
	started, err := srv.LLMStartBatchDecision(t.Context(), &gctrpc.LLMStartBatchDecisionRequest{
		StrategyVersionId: "ver-1",
		MarketIds:         []string{"Binance:usdtmarginedfutures:BTC_USDT"},
		Kind:              BatchKindOpenDecision,
		Actor:             "op",
	})
	require.NoError(t, err)
	require.NotEmpty(t, started.GetJobId())

	var got *gctrpc.LLMGetBatchDecisionResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err = srv.LLMGetBatchDecision(t.Context(), &gctrpc.LLMGetBatchDecisionRequest{JobId: started.GetJobId()})
		require.NoError(t, err)
		if got.GetDone() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.True(t, got.GetDone(), "the job must finish within the poll window")
	assert.Equal(t, int32(1), got.GetTotal())
	assert.Equal(t, int32(1), got.GetFinished())
	require.Len(t, got.GetRows(), 1)
	assert.Equal(t, BatchRowDone, got.GetRows()[0].GetStatus())
	assert.Equal(t, "run-btc", got.GetRows()[0].GetDecisionRunId())
	assert.Equal(t, BatchKindOpenDecision, got.GetKind())

	_, err = srv.LLMGetBatchDecision(t.Context(), &gctrpc.LLMGetBatchDecisionRequest{JobId: "nope"})
	require.Error(t, err, "an unknown job refuses")
}

func TestLLMCancelBatchDecisionHandler(t *testing.T) {
	started := make(chan struct{}, 1)
	runner := batchTestRunner()
	runner.Runner = runnerFunc(func(ctx context.Context, _ *ShadowRunRequest) (*ShadowRunResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	server := &RPCServer{Engine: &Engine{llmBatchRunner: runner}}
	job, err := runner.Start(t.Context(), BatchKindOpenDecision, "ver-1", []string{
		"Binance:usdtmarginedfutures:BTC_USDT",
		"Binance:usdtmarginedfutures:ETH_USDT",
	}, "op")
	require.NoError(t, err)
	<-started

	out, err := server.LLMCancelBatchDecision(t.Context(), &gctrpc.LLMCancelBatchDecisionRequest{JobId: job.ID, Actor: "op"})
	require.NoError(t, err)
	assert.Equal(t, job.ID, out.GetJobId())
	assert.GreaterOrEqual(t, out.GetCancelled(), int32(1))
	done := waitBatchDone(t, runner, job.ID)
	assert.True(t, done.Done)
}
