package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestLLMGetCoverageStatus covers the W7-07 export: an unassembled subsystem
// refuses, a disabled coverage section reports enabled=false, and a live
// rotator carries both the plan's arithmetic and its measured state.
func TestLLMGetCoverageStatus(t *testing.T) {
	var empty *RPCServer
	_, err := empty.LLMGetCoverageStatus(context.Background(), &gctrpc.LLMGetCoverageStatusRequest{})
	require.ErrorIs(t, err, errLLMCoverageUnavailable, "an unassembled subsystem must fail closed")

	// No rotator: a legal configuration, reported as a state.
	disabled := &RPCServer{Engine: &Engine{llmManager: &LLMManager{cfg: &config.LLMConfig{}}}}
	status, err := disabled.LLMGetCoverageStatus(context.Background(), &gctrpc.LLMGetCoverageStatusRequest{})
	require.NoError(t, err)
	assert.False(t, status.GetEnabled())
	assert.Zero(t, status.GetContractCount())
	assert.NotEmpty(t, status.GetCheckedAt())

	// A live rotator mid-sweep: the plan figure and the measured figures both
	// report, and the coldest market is as old as the sweep. The clock sits
	// clear of the hourly reload window so the steps are visits, not reloads.
	clock := &fixedClock{at: time.Date(2026, 10, 11, 12, 30, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	rotator := newRotator(t, src, clock, 2)
	require.NoError(t, rotator.RefreshMarkets(context.Background()))

	// The first step opens the sweep with the batch ticker call; the next
	// visits one market, leaving one unvisited.
	first, err := rotator.Step(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StepTickerBatch, first.Kind)
	clock.advance(time.Minute)
	visit, err := rotator.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, StepKlineIncrement, visit.Kind)

	manager := &LLMManager{cfg: &config.LLMConfig{}}
	manager.cfg.Coverage.Scope = "ALL_USDTM_PERPETUAL"
	manager.rotation = rotator
	server := &RPCServer{Engine: &Engine{llmManager: manager}}

	status, err = server.LLMGetCoverageStatus(context.Background(), &gctrpc.LLMGetCoverageStatusRequest{})
	require.NoError(t, err)
	assert.True(t, status.GetEnabled())
	assert.Equal(t, "ALL_USDTM_PERPETUAL", status.GetScope(), "the plan's scope comes from the configuration")
	assert.Equal(t, int64(2), status.GetContractCount())
	assert.Equal(t, int64(10), status.GetRotationCycleMinutes(), "the default cycle is ten minutes")
	assert.Equal(t, int64(1), status.GetVisitedThisCycle(), "one visit should be visible as one")
	assert.InDelta(t, 50.0, status.GetCycleProgressPercent(), 0.01, "one of two markets is half a sweep")
	assert.Equal(t, int64(0), status.GetCyclesCompleted())
	assert.Positive(t, status.GetWorstContractAgeSeconds(), "an unvisited market is as cold as the sweep is old")
	assert.NotEmpty(t, status.GetLastTickerRefresh(), "the sweep opened with a batch ticker call")

	// Finishing the sweep must expose the completion moment, not just a count:
	// visit the second market, then the step that closes the sweep.
	clock.advance(time.Minute)
	_, err = rotator.Step(context.Background())
	require.NoError(t, err)
	clock.advance(time.Minute)
	closing, err := rotator.Step(context.Background())
	require.NoError(t, err)
	require.True(t, closing.CycleCompleted)
	status, err = server.LLMGetCoverageStatus(context.Background(), &gctrpc.LLMGetCoverageStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), status.GetCyclesCompleted())
	assert.Equal(t, int64(1), status.GetVisitedThisCycle(),
		"the closing step immediately opens the next sweep with its first visit")
	assert.NotEmpty(t, status.GetLastCycleCompletedAt(), "上次周期完成时刻应可报告")
}
