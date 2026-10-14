package engine

import (
	"strings"
	"time"
)

// M4-07 前置门禁（docs 03 §6 第 1 步）。
//
// 七个零成本检查在任何取数与模型调用之前回答"这一下该不该花钱"。失败不是
// 异常：调用方把 reason 记进 decision_run 后退出，**不消耗模型预算**——免费
// 的先拒，付费的别花。
//
// 这是纯函数：所有事实由调用方供给（运行模式、版本状态、配对 readiness、
// 探针结果、halt、预算、对账），便于离线钉死顺序与判定。

// Pre-gate reason codes (docs 05 §9 词表)。
const (
	ReasonModeNotPermitted       = "MODE_NOT_PERMITTED"
	ReasonStrategyNotApproved    = "STRATEGY_NOT_APPROVED"
	ReasonStrategyExpired        = "STRATEGY_EXPIRED"
	ReasonStrategyRevoked        = "STRATEGY_REVOKED"
	ReasonPairIncomplete         = "PAIR_INCOMPLETE"
	ReasonPairDegraded           = "PAIR_DEGRADED"
	ReasonProbeStale             = "PROBE_STALE"
	ReasonProbeFailed            = "PROBE_FAILED"
	ReasonHalted                 = "HALTED"
	ReasonBudgetExhausted        = "BUDGET_EXHAUSTED"
	ReasonAccountNotReconciled   = "ACCOUNT_NOT_RECONCILED"
	ReasonPreGateInputIncomplete = "PREGATE_INPUT_INCOMPLETE"
)

// PreGateInput is the fact set the gates evaluate. Zero values fail closed.
type PreGateInput struct {
	// RunMode is the effective LLM run mode.
	RunMode string
	// StrategyState is the version's lifecycle state; FROZEN is the runnable
	// value a live approval binds (M4-05: the approval row itself is the
	// APPROVED signal -- versions never leave FROZEN while approved).
	StrategyState string
	// StrategyValidFrom/Until bound the approval window.
	StrategyValidFrom  time.Time
	StrategyValidUntil time.Time
	// PairReadiness is the active pairing's readiness (READY/DEGRADED/INCOMPLETE).
	PairReadiness string
	// ProbePassed and ProbeCheckedAt describe the latest provider probe.
	ProbePassed    bool
	ProbeCheckedAt time.Time
	// ProbeMaxAge is the freshness TTL, supplied by the caller's config. Zero or
	// negative means "no TTL configured", which fails closed as stale: without a
	// TTL nothing can prove the probe is fresh.
	ProbeMaxAge time.Duration
	// Halted is true when any halt level is active.
	Halted bool
	// BudgetAvailable is false when the 24h reservation window is full (M1-11).
	BudgetAvailable bool
	// AccountReconciled is true once the local view matched the exchange.
	AccountReconciled bool
	// Now is the evaluation instant.
	Now time.Time
}

// PreGateVerdict is the gates' answer.
type PreGateVerdict struct {
	// Allowed is true only when every gate passed.
	Allowed bool `json:"allowed"`
	// ReasonCode is empty when allowed, otherwise one of the §9 vocabulary.
	ReasonCode string `json:"reasonCode,omitempty"`
}

// EvaluatePreGates runs the checks in the order docs 03 §6 lists.
//
// The order is deterministic so that a decision run records the FIRST refusal
// a reader would have hit, not whichever gate a map walk happened to visit.
func EvaluatePreGates(in *PreGateInput) PreGateVerdict {
	if in.Now.IsZero() {
		return PreGateVerdict{ReasonCode: ReasonPreGateInputIncomplete}
	}
	if !modePermitsDecisions(in.RunMode) {
		return PreGateVerdict{ReasonCode: ReasonModeNotPermitted}
	}
	switch in.StrategyState {
	case strategyStateFrozen:
		// FROZEN is the runnable state a live approval binds: M4-05 keeps
		// approved versions frozen and makes the approval row itself the
		// APPROVED signal, so a FROZEN version here is an approved one (the
		// caller fetched that approval before building this fact set).
	case "REVOKED":
		return PreGateVerdict{ReasonCode: ReasonStrategyRevoked}
	case "EXPIRED":
		return PreGateVerdict{ReasonCode: ReasonStrategyExpired}
	default:
		return PreGateVerdict{ReasonCode: ReasonStrategyNotApproved}
	}
	if in.StrategyValidFrom.IsZero() || in.StrategyValidUntil.IsZero() || in.Now.Before(in.StrategyValidFrom) {
		return PreGateVerdict{ReasonCode: ReasonStrategyNotApproved}
	}
	if !in.Now.Before(in.StrategyValidUntil) {
		return PreGateVerdict{ReasonCode: ReasonStrategyExpired}
	}
	switch in.PairReadiness {
	case LLMPairReady:
	case LLMPairDegraded:
		return PreGateVerdict{ReasonCode: ReasonPairDegraded}
	default:
		// INCOMPLETE and anything unknown fail closed to the same code: an
		// unrecognised readiness is not evidence of readiness.
		return PreGateVerdict{ReasonCode: ReasonPairIncomplete}
	}
	if !in.ProbePassed {
		return PreGateVerdict{ReasonCode: ReasonProbeFailed}
	}
	if in.ProbeMaxAge <= 0 || in.ProbeCheckedAt.IsZero() ||
		in.Now.Sub(in.ProbeCheckedAt) > in.ProbeMaxAge || in.ProbeCheckedAt.After(in.Now) {
		return PreGateVerdict{ReasonCode: ReasonProbeStale}
	}
	if in.Halted {
		return PreGateVerdict{ReasonCode: ReasonHalted}
	}
	if !in.BudgetAvailable {
		return PreGateVerdict{ReasonCode: ReasonBudgetExhausted}
	}
	if !in.AccountReconciled {
		return PreGateVerdict{ReasonCode: ReasonAccountNotReconciled}
	}
	return PreGateVerdict{Allowed: true}
}

// modePermitsDecisions reports whether the run mode may run automatic
// evaluation. READ_ONLY is the fail-closed default and never does.
func modePermitsDecisions(mode string) bool {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive:
		return true
	default:
		return false
	}
}
