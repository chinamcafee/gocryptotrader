package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultSwitches() map[string]bool {
	out := make(map[string]bool, len(RiskSwitchCatalog))
	for _, spec := range RiskSwitchCatalog {
		out[spec.Name] = spec.DefaultOn
	}
	return out
}

func readinessGreenInput() *ReadinessInput {
	now := time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC)
	return &ReadinessInput{
		Network: "binance-testnet", Mode: LLMRunModeTestnet, Now: now,
		PairReadiness:  LLMPairReady,
		LLMProbePassed: true, LLMProbeAt: now.Add(-2 * time.Minute),
		JevProbePassed: true, JevProbeAt: now.Add(-3 * time.Minute),
		ProbeMaxAge: 24 * time.Hour,
		Switches:    defaultSwitches(),
		Reconciled:  true, ReconciledUntil: now.Add(4 * time.Minute),
		DayKeyUTC: UtcDayKey(now), DaySealed: true,
		CoverageEnabled:   588,
		LiveApprovals:     1,
		ConfiguredNetwork: "binance-testnet",
	}
}

func readinessGateStatus(t *testing.T, r *ReadinessReport, code string) string {
	t.Helper()
	for _, gate := range r.Gates {
		if gate.Code == code {
			return gate.Status
		}
	}
	t.Fatalf("gate %s not present in report", code)
	return ""
}

func readinessGateDetail(t *testing.T, r *ReadinessReport, code string) string {
	t.Helper()
	for _, gate := range r.Gates {
		if gate.Code == code {
			return gate.Detail
		}
	}
	t.Fatalf("gate %s not present in report", code)
	return ""
}

func TestEvaluateReadinessAllGreen(t *testing.T) {
	report := EvaluateReadiness(readinessGreenInput())
	assert.True(t, report.Ready)
	assert.Equal(t, "binance-testnet", report.Network)
	assert.Equal(t, LLMRunModeTestnet, report.Mode)
	require.Len(t, report.Gates, 10)
	wantOrder := []string{
		ReadinessGateModePermitted, ReadinessGatePairReady, ReadinessGateProbeFresh,
		ReadinessGateNetwork, ReadinessGateNoFullHalt, ReadinessGateSwitchesIntact,
		ReadinessGateReconciled, ReadinessGateDaySealed, ReadinessGateCoverage,
		ReadinessGateApprovalLive,
	}
	for i, code := range wantOrder {
		assert.Equal(t, code, report.Gates[i].Code, "gate order is part of the contract")
		assert.Equalf(t, ReadinessPass, report.Gates[i].Status, "gate %s should pass", code)
	}
}

func TestEvaluateReadinessInputIncomplete(t *testing.T) {
	report := EvaluateReadiness(&ReadinessInput{Mode: LLMRunModeTestnet, Now: time.Now()})
	assert.False(t, report.Ready)
	require.Len(t, report.Gates, 1)
	assert.Equal(t, ReadinessGateInput, report.Gates[0].Code)
	assert.Equal(t, ReadinessFail, report.Gates[0].Status)

	report = EvaluateReadiness(&ReadinessInput{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	assert.False(t, report.Ready)
	require.Len(t, report.Gates, 1)
	assert.Equal(t, ReadinessGateInput, report.Gates[0].Code)
}

func TestEvaluateReadinessModeGate(t *testing.T) {
	in := readinessGreenInput()
	in.Mode = LLMRunModeReadOnly
	report := EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateModePermitted))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateModePermitted), "SHADOW/PAPER/TESTNET/LIVE")
}

func TestEvaluateReadinessPairAndProbe(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ReadinessInput)
		gate   string
		detail string
	}{
		{"pair incomplete fails", func(in *ReadinessInput) { in.PairReadiness = LLMPairIncomplete }, ReadinessGatePairReady, "INCOMPLETE"},
		{"pair degraded fails", func(in *ReadinessInput) { in.PairReadiness = LLMPairDegraded }, ReadinessGatePairReady, "DEGRADED"},
		{"probe never passed", func(in *ReadinessInput) { in.JevProbePassed = false }, ReadinessGateProbeFresh, "jev probe did not pass"},
		{"probe stale", func(in *ReadinessInput) { in.LLMProbeAt = in.Now.Add(-25 * time.Hour) }, ReadinessGateProbeFresh, "llm probe passed 25h0m0s ago"},
		{"probe timestamp future", func(in *ReadinessInput) { in.JevProbeAt = in.Now.Add(time.Minute) }, ReadinessGateProbeFresh, "in the future"},
		{"no ttl configured", func(in *ReadinessInput) { in.ProbeMaxAge = 0 }, ReadinessGateProbeFresh, "no freshness TTL is configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := readinessGreenInput()
			tc.mutate(in)
			report := EvaluateReadiness(in)
			assert.False(t, report.Ready)
			assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, tc.gate))
			assert.Contains(t, readinessGateDetail(t, &report, tc.gate), tc.detail)
		})
	}
}

func TestEvaluateReadinessNetworkGate(t *testing.T) {
	in := readinessGreenInput()
	in.ConfiguredNetwork = "binance-mainnet"
	report := EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateNetwork))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateNetwork), "binance-mainnet != requested binance-testnet")

	// PAPER is exempt: it never touches a venue.
	in = readinessGreenInput()
	in.Mode = LLMRunModePaper
	in.ConfiguredNetwork = "binance-mainnet"
	in.LiveApprovals = 0
	report = EvaluateReadiness(in)
	assert.True(t, report.Ready)
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateNetwork))
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateApprovalLive))

	// A missing configured network fails closed for trading modes.
	in = readinessGreenInput()
	in.ConfiguredNetwork = ""
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateNetwork))
}

func TestEvaluateReadinessHaltsGate(t *testing.T) {
	in := readinessGreenInput()
	in.Halts = []ReadinessHalt{
		{ID: "h1", Level: HaltLevelSoft},
		{ID: "h2", Level: HaltLevelMarket, MarketID: "BTCUSDT"},
	}
	report := EvaluateReadiness(in)
	assert.True(t, report.Ready)
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateNoFullHalt))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateNoFullHalt), "SOFT/MARKET halts active: 2")

	in.Halts = append(in.Halts, ReadinessHalt{ID: "h3", Level: HaltLevelFull})
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateNoFullHalt))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateNoFullHalt), "h3")
}

func TestEvaluateReadinessSwitchesGate(t *testing.T) {
	in := readinessGreenInput()
	report := EvaluateReadiness(in)
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateSwitchesIntact))

	in.Switches["blockOnUnknownOrders"] = false
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateSwitchesIntact))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateSwitchesIntact), "blockOnUnknownOrders")

	in = readinessGreenInput()
	in.Switches = nil
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateSwitchesIntact))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateSwitchesIntact), "not supplied")
}

func TestEvaluateReadinessReconcileAndDayGate(t *testing.T) {
	in := readinessGreenInput()
	in.Reconciled = false
	report := EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateReconciled))

	in = readinessGreenInput()
	in.ReconciledUntil = in.Now.Add(-time.Second)
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateReconciled))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateReconciled), "validity ended")

	in = readinessGreenInput()
	in.DayKeyUTC = UtcDayKey(in.Now.Add(-24 * time.Hour))
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateDaySealed))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateDaySealed), "not sealed")

	in = readinessGreenInput()
	in.DaySealed = false
	in.DayKeyUTC = ""
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateDaySealed), "sealed: none")
}

func TestEvaluateReadinessCoverageAndApprovalGate(t *testing.T) {
	in := readinessGreenInput()
	in.CoverageEnabled = 0
	report := EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateCoverage))

	in = readinessGreenInput()
	in.LiveApprovals = 0
	report = EvaluateReadiness(in)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateApprovalLive))
	assert.Contains(t, readinessGateDetail(t, &report, ReadinessGateApprovalLive), "no live approval")

	// SHADOW does not submit: no approval required.
	in = readinessGreenInput()
	in.LiveApprovals = 0
	in.Mode = LLMRunModeShadow
	report = EvaluateReadiness(in)
	assert.True(t, report.Ready)
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateApprovalLive))
}

type fakeReadinessFacts struct {
	in      ReadinessInput
	err     error
	gotNet  string
	gotMode string
	calls   int
}

func (f *fakeReadinessFacts) GatherReadinessFacts(_ context.Context, network, mode string) (ReadinessInput, error) {
	f.calls++
	f.gotNet, f.gotMode = network, mode
	return f.in, f.err
}

func TestReadinessServiceEvaluate(t *testing.T) {
	facts := &fakeReadinessFacts{in: *readinessGreenInput()}
	service := &LLMReadinessService{Facts: facts, Now: func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }}
	report, err := service.Evaluate(context.Background(), ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.NoError(t, err)
	assert.True(t, report.Ready)
	assert.Equal(t, 1, facts.calls)
	assert.Equal(t, "binance-testnet", facts.gotNet)
	assert.Equal(t, LLMRunModeTestnet, facts.gotMode)
	// The adapter filled everything; the service clock must not override Now.
	assert.Equal(t, readinessGreenInput().Now, report.CheckedAt)
}

func TestReadinessServiceDefaultsAndFailures(t *testing.T) {
	// Adapter returns a mostly-empty input: request values and the service
	// clock fill the gaps, and the gates still evaluate (and fail) on facts.
	facts := &fakeReadinessFacts{}
	service := &LLMReadinessService{Facts: facts, Now: func() time.Time { return time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC) }}
	report, err := service.Evaluate(context.Background(), ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.Equal(t, "binance-testnet", report.Network)
	assert.Equal(t, LLMRunModeTestnet, report.Mode)
	assert.Equal(t, service.Now(), report.CheckedAt)

	// Gather errors surface as errors: an admission that cannot read its
	// facts is not a verdict.
	facts = &fakeReadinessFacts{err: errors.New("db down")}
	service = &LLMReadinessService{Facts: facts}
	_, err = service.Evaluate(context.Background(), ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")

	// No fact source at all: fail closed before evaluating anything.
	service = &LLMReadinessService{}
	_, err = service.Evaluate(context.Background(), ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.ErrorIs(t, err, ErrReadinessUnavailable)
}
