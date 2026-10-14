package engine

import (
	"context"
	"errors"
	"time"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W7-07 覆盖轮询运行面（docs 04 §7.4）：把 M2-14 旋转器的实测状态搬上 RPC。
//
// 「计划值」与「运行值」是两个不同的东西：`scope` 与 `rotationCycleMinutes`
// 来自配置映射（说明系统配置成什么样），`visitedThisCycle` /
// `cycleProgressPercent` / `worstContractAgeSeconds` / `weightUsedPerMinute`
// / `lastCycleCompletedAt` 来自旋转器实测（说明它此刻在做什么）。前端据此
// 渲染运行面，不必自己再算一遍。
//
// coverage 未启用是合法配置（enabled=false 是状态不是错误）；LLM 子系统
// 未装配才 fail-closed 拒——「读不到」与「读到未启用」不许长得一样。

// errLLMCoverageUnavailable marks the status call before the LLM subsystem is
// assembled.
var errLLMCoverageUnavailable = errors.New("llm coverage status is not configured on this bot")

// LLMGetCoverageStatus reports the coverage rotation's plan and measured state.
func (s *RPCServer) LLMGetCoverageStatus(_ context.Context, _ *gctrpc.LLMGetCoverageStatusRequest) (*gctrpc.LLMGetCoverageStatusResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil {
		return nil, errLLMCoverageUnavailable
	}
	out := &gctrpc.LLMGetCoverageStatusResponse{
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if cfg := s.Engine.llmManager.Config(); cfg != nil {
		out.Scope = cfg.Coverage.Scope
	}
	rotator := s.Engine.llmManager.Rotation()
	if rotator == nil {
		// 未启用轮询：enabled=false 是状态不是错误。
		return out, nil
	}
	out.Enabled = true
	stats := rotator.Stats()
	out.ContractCount = int64(stats.Markets)
	out.RotationCycleMinutes = int64(stats.CycleDuration.Minutes())
	out.WorstContractAgeSeconds = int64(stats.OldestMarketAge.Seconds())
	out.WeightUsedPerMinute = int64(stats.WeightUsedThisMinute)
	out.WeightBudgetPerMinute = int64(stats.WeightBudgetPerMinute)
	out.CycleProgressPercent = stats.CycleProgressPercent
	out.VisitedThisCycle = int64(stats.VisitedThisCycle)
	out.CyclesCompleted = stats.CyclesCompleted
	out.Failures = stats.Failures
	out.ReloadsThisHour = int64(stats.ReloadsThisHour)
	out.ReloadMarkets = int64(stats.ReloadMarkets)
	out.IncrementSpacingMs = stats.IncrementSpacing.Milliseconds()
	if !stats.LastTickerRefresh.IsZero() {
		out.LastTickerRefresh = stats.LastTickerRefresh.UTC().Format(time.RFC3339)
	}
	if !stats.LastReload.IsZero() {
		out.LastReload = stats.LastReload.UTC().Format(time.RFC3339)
	}
	if !stats.LastCycleCompletedAt.IsZero() {
		out.LastCycleCompletedAt = stats.LastCycleCompletedAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}
