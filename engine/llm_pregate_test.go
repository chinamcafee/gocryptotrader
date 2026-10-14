package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func passingPreGateInput() *PreGateInput {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	return &PreGateInput{
		RunMode:            LLMRunModeShadow,
		StrategyState:      strategyStateFrozen,
		StrategyValidFrom:  now.Add(-time.Hour),
		StrategyValidUntil: now.Add(3 * time.Hour),
		PairReadiness:      LLMPairReady,
		ProbePassed:        true,
		ProbeCheckedAt:     now.Add(-5 * time.Minute),
		ProbeMaxAge:        30 * time.Minute,
		BudgetAvailable:    true,
		AccountReconciled:  true,
		Now:                now,
	}
}

func TestPreGatesPassWhenEverythingHolds(t *testing.T) {
	verdict := EvaluatePreGates(passingPreGateInput())
	assert.True(t, verdict.Allowed)
	assert.Empty(t, verdict.ReasonCode)
}

func TestPreGatesFailOneAtATime(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PreGateInput)
		want   string
	}{
		{"read-only mode", func(in *PreGateInput) { in.RunMode = LLMRunModeReadOnly }, ReasonModeNotPermitted},
		{"unknown mode", func(in *PreGateInput) { in.RunMode = "TURBO" }, ReasonModeNotPermitted},
		{"empty mode", func(in *PreGateInput) { in.RunMode = "" }, ReasonModeNotPermitted},
		{"draft strategy", func(in *PreGateInput) { in.StrategyState = StrategyStateDraft }, ReasonStrategyNotApproved},
		{"paused strategy", func(in *PreGateInput) { in.StrategyState = StrategyStatePaused }, ReasonStrategyNotApproved},
		{"revoked strategy", func(in *PreGateInput) { in.StrategyState = "REVOKED" }, ReasonStrategyRevoked},
		{"expired state", func(in *PreGateInput) { in.StrategyState = "EXPIRED" }, ReasonStrategyExpired},
		{"window not started", func(in *PreGateInput) { in.StrategyValidFrom = in.Now.Add(time.Hour) }, ReasonStrategyNotApproved},
		{"window lapsed", func(in *PreGateInput) { in.StrategyValidUntil = in.Now.Add(-time.Second) }, ReasonStrategyExpired},
		{"pair incomplete", func(in *PreGateInput) { in.PairReadiness = LLMPairIncomplete }, ReasonPairIncomplete},
		{"pair degraded", func(in *PreGateInput) { in.PairReadiness = LLMPairDegraded }, ReasonPairDegraded},
		{"pair readiness unknown", func(in *PreGateInput) { in.PairReadiness = "WHATEVER" }, ReasonPairIncomplete},
		{"probe failed", func(in *PreGateInput) { in.ProbePassed = false }, ReasonProbeFailed},
		{"probe stale", func(in *PreGateInput) { in.ProbeCheckedAt = in.Now.Add(-time.Hour) }, ReasonProbeStale},
		{"probe ttl unset", func(in *PreGateInput) { in.ProbeMaxAge = 0 }, ReasonProbeStale},
		{"probe from the future", func(in *PreGateInput) { in.ProbeCheckedAt = in.Now.Add(time.Minute) }, ReasonProbeStale},
		{"halted", func(in *PreGateInput) { in.Halted = true }, ReasonHalted},
		{"budget exhausted", func(in *PreGateInput) { in.BudgetAvailable = false }, ReasonBudgetExhausted},
		{"account unreconciled", func(in *PreGateInput) { in.AccountReconciled = false }, ReasonAccountNotReconciled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := passingPreGateInput()
			tc.mutate(in)
			verdict := EvaluatePreGates(in)
			assert.False(t, verdict.Allowed)
			assert.Equal(t, tc.want, verdict.ReasonCode)
		})
	}
}

func TestPreGatesRecordTheFirstRefusalInDocumentOrder(t *testing.T) {
	// Several failures at once: the verdict must be the first one a reader of
	// docs 03 §6 would have encountered, so a decision run's reason is stable.
	in := passingPreGateInput()
	in.RunMode = LLMRunModeReadOnly
	in.Halted = true
	in.BudgetAvailable = false
	assert.Equal(t, ReasonModeNotPermitted, EvaluatePreGates(in).ReasonCode)

	in = passingPreGateInput()
	in.PairReadiness = LLMPairIncomplete
	in.Halted = true
	assert.Equal(t, ReasonPairIncomplete, EvaluatePreGates(in).ReasonCode)

	in = passingPreGateInput()
	in.Halted = true
	in.AccountReconciled = false
	assert.Equal(t, ReasonHalted, EvaluatePreGates(in).ReasonCode)

	// A missing clock means the caller passed an incomplete fact set: refuse
	// rather than evaluate windows against a zero time.
	in = passingPreGateInput()
	in.Now = time.Time{}
	verdict := EvaluatePreGates(in)
	assert.False(t, verdict.Allowed)
	assert.Equal(t, ReasonPreGateInputIncomplete, verdict.ReasonCode)
}

func TestPreGatesAcceptEveryDecisionMode(t *testing.T) {
	for _, mode := range []string{LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive, "shadow"} {
		in := passingPreGateInput()
		in.RunMode = mode
		assert.Truef(t, EvaluatePreGates(in).Allowed, "mode %q should permit automatic evaluation", mode)
	}
}
