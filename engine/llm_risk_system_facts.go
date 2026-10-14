package engine

import (
	"fmt"
	"strings"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M4-11 装配件 · 引擎态 → 硬风控 G 组（系统）映射。
//
// 能从既有状态派生的派生，其余**显式输入、禁发明**：
//
//   - `GlobalHalt`/`MarketHalt`：从**活跃 halt 行**派生（`ClearedAt == nil`）：
//     SOFT/FULL 是全局面（SOFT 停运行中策略、FULL 全停），MARKET 命中被裁决
//     市场时才是市场面——两个布尔一起进 `RiskHalted`，与检查的语义一一对应；
//   - `Mode`/`LiveAcknowledged`/`PairReadiness`：调用方各自读一次后传入
//     （模式在 `EffectiveRunMode`、确认在配置、就绪在 readiness 事实）；
//   - `ModelBudgetExhausted`/`DatabaseHealthy`/`ClockDriftMs`/`RateLimitDegraded`
//     /`OrderManagerUp`/`ExchangeManagerUp`：**库中没有统一的现成读面**——
//     显式输入（预算可由预约窗口推导、DB 健康可由连接探测、漂移可由交易所
//     校时——但这些计算各有其归属，装配层只负责申报，不负责发明）。
//
// 纪律：**报不出自己的限额即拒**（`MaxClockDriftMs` 必须为正——时钟闸的
// 判据是"偏差超限"，没有限就没有闸）；负漂移 = 读不可信，报错不猜。
func riskSystemFactsFromState(in *RiskSystemFactsInput) (*RiskSystemFacts, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: the system material is required", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.Mode) == "" {
		return nil, fmt.Errorf("%w: the run mode is required", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.PairReadiness) == "" {
		return nil, fmt.Errorf("%w: the pair readiness is required", ErrRiskFactsInput)
	}
	if in.MaxClockDriftMs <= 0 {
		return nil, fmt.Errorf("%w: the clock-drift limit must be positive", ErrRiskFactsInput)
	}
	if in.ClockDriftMs < 0 {
		return nil, fmt.Errorf("%w: a negative clock drift is unreadable", ErrRiskFactsInput)
	}
	global, market := false, false
	for i := range in.Halts {
		halt := in.Halts[i]
		if halt.ClearedAt != nil {
			continue
		}
		switch halt.Level {
		case HaltLevelSoft, HaltLevelFull:
			global = true
		case HaltLevelMarket:
			if halt.MarketID != nil && *halt.MarketID == in.MarketID {
				market = true
			}
		}
	}
	return &RiskSystemFacts{
		GlobalHalt:           global,
		MarketHalt:           market,
		Mode:                 in.Mode,
		LiveAcknowledged:     in.LiveAcknowledged,
		ModelBudgetExhausted: in.ModelBudgetExhausted,
		DatabaseHealthy:      in.DatabaseHealthy,
		ClockDriftMs:         in.ClockDriftMs,
		MaxClockDriftMs:      in.MaxClockDriftMs,
		RateLimitDegraded:    in.RateLimitDegraded,
		PairReadiness:        in.PairReadiness,
		OrderManagerUp:       in.OrderManagerUp,
		ExchangeManagerUp:    in.ExchangeManagerUp,
	}, nil
}

// RiskSystemFactsInput is the engine-state material the system facts carry.
type RiskSystemFactsInput struct {
	// Halts are the persisted halt rows; only uncleared ones count.
	Halts []llmstore.Halt
	// MarketID is the market under evaluation (market-scope halts match on it).
	MarketID string
	// Mode is the effective run mode (after fail-closed downgrades).
	Mode string
	// LiveAcknowledged is the operator's LIVE acknowledgement.
	LiveAcknowledged bool
	// ModelBudgetExhausted reports no model headroom remains.
	ModelBudgetExhausted bool
	// DatabaseHealthy is the persistence probe's verdict.
	DatabaseHealthy bool
	// ClockDriftMs is the measured skew magnitude; negative is unreadable.
	ClockDriftMs int64
	// MaxClockDriftMs is the policy limit; it must be positive.
	MaxClockDriftMs int64
	// RateLimitDegraded is the venue rate-limit state.
	RateLimitDegraded bool
	// PairReadiness is the active pairing's readiness label.
	PairReadiness string
	// OrderManagerUp / ExchangeManagerUp report the engines' managers exist.
	OrderManagerUp    bool
	ExchangeManagerUp bool
}
