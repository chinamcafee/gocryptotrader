package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-12 测试：九步逐级取 min + Q13 的"向下取整再减一个容差 step" + 滑点重跑。

// sizingTestInput prices the book at 1000 so quantities stay readable.
func sizingTestInput() *SizingInput {
	walk := func(_, _ string) (string, error) {
		return "1000", nil
	}
	return &SizingInput{
		Candidate: &candidate.Candidate{
			Action:                 candidate.ActionOpenLong,
			ProposedMaxNotionalUSD: "20",
		},
		Envelope: candidate.RiskEnvelope{
			MaxOrderNotionalUSD: new("20"),
			MaxTotalExposureUSD: new("100"),
			MaxDailyLossUSD:     new("20"),
			MaxSlippageBPS:      new(20),
			MaxLeverage:         new(1),
		},
		ExistingExposureUSD:    "0",
		DailyLossUSD:           "0",
		StopDistancePerUnitUSD: "100",
		Contract:               RiskContractSpec{TickSize: "0.1", StepSize: "0.0001", MinQuantity: "0.001", MinNotionalUSD: "5"},
		Walk:                   walk,
	}
}

func TestSizingHappyPathAppliesTheToleranceStep(t *testing.T) {
	t.Parallel()

	out := ComputeQuantity(sizingTestInput())
	require.Equalf(t, RiskVerdictAllow, out.Verdict, "rejection: %+v", out.Rejection)
	assert.Equal(t, "ask", out.Side, "a long opening consumes the ask side")
	assert.Equal(t, "1000", out.ReferencePrice)
	// cap 20 / 1000 = 0.02 raw; aligned exactly; minus one tolerance step
	// (0.0001) -> 0.0199. This is Q13's guarantee: the float64 conversion
	// downstream can only shrink, never grow, the approved size.
	assert.Equal(t, "0.0199", out.Quantity)
	assert.Equal(t, "19.9", out.NotionalUSD)
	assert.Equal(t, "20", out.Caps.ByCandidateBound)
	assert.Equal(t, "20", out.Caps.ByApprovedOrderCap)
	assert.Equal(t, "100", out.Caps.ByRemainingExposure)
	assert.NotEmpty(t, out.Caps.ByRemainingDailyLoss)
}

func TestSizingShortUsesTheBidSide(t *testing.T) {
	t.Parallel()

	in := sizingTestInput()
	in.Candidate.Action = candidate.ActionOpenShort
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictAllow, out.Verdict)
	assert.Equal(t, "bid", out.Side)
}

func TestSizingFloorsToTheStepSize(t *testing.T) {
	t.Parallel()

	in := sizingTestInput()
	in.Candidate.ProposedMaxNotionalUSD = "25" // 25/1000 = 0.025 raw
	in.Envelope.MaxOrderNotionalUSD = new("25")
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictAllow, out.Verdict)
	// floor(0.025 / 0.0001) = 250 -> 0.025, minus one step -> 0.0249.
	assert.Equal(t, "0.0249", out.Quantity, "the raw quantity should floor, never round up")
}

func TestSizingTakesTheStrictestCap(t *testing.T) {
	t.Parallel()

	// Candidate asks for less than the envelope allows.
	in := sizingTestInput()
	in.Candidate.ProposedMaxNotionalUSD = "10"
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictAllow, out.Verdict)
	assert.Equal(t, "0.0099", out.Quantity)

	// The remaining exposure binds: 100 - 90 = 10.
	in = sizingTestInput()
	in.ExistingExposureUSD = "90"
	out = ComputeQuantity(in)
	require.Equal(t, RiskVerdictAllow, out.Verdict)
	assert.Equal(t, "10", out.Caps.ByRemainingExposure)
	assert.Equal(t, "0.0099", out.Quantity)

	// The remaining daily-loss budget binds through the stop distance:
	// 20 / 2000 = 0.01 units = 10 USD at 1000.
	in = sizingTestInput()
	in.StopDistancePerUnitUSD = "2000"
	out = ComputeQuantity(in)
	require.Equal(t, RiskVerdictAllow, out.Verdict)
	assert.Equal(t, "10", out.Caps.ByRemainingDailyLoss)
	assert.Equal(t, "0.0099", out.Quantity)
}

func TestSizingRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*SizingInput)
	}{
		{"missing candidate bound", RiskOrderNotionalMissing, func(in *SizingInput) { in.Candidate.ProposedMaxNotionalUSD = "" }},
		{"missing approved cap", RiskOrderNotionalMissing, func(in *SizingInput) { in.Envelope.MaxOrderNotionalUSD = nil }},
		{"missing exposure cap", RiskEnvelopeIncomplete, func(in *SizingInput) { in.Envelope.MaxTotalExposureUSD = nil }},
		{"no exposure headroom", RiskTotalExposureExceeded, func(in *SizingInput) { in.ExistingExposureUSD = "100" }},
		{"no daily-loss headroom", RiskDailyLossExceeded, func(in *SizingInput) { in.DailyLossUSD = "20" }},
		{"missing stop distance", RiskDailyLossUnknown, func(in *SizingInput) { in.StopDistancePerUnitUSD = "" }},
		{"unreadable step size", RiskPrecisionInvalid, func(in *SizingInput) { in.Contract.StepSize = "0" }},
		{"quantity below minimum", RiskAmountBelowMin, func(in *SizingInput) { in.Contract.MinQuantity = "0.05" }},
		{"notional below minimum", RiskBelowMinNotional, func(in *SizingInput) { in.Contract.MinNotionalUSD = "19.95" }},
		{"zero quantity remains", RiskBelowMinNotional, func(in *SizingInput) { in.Candidate.ProposedMaxNotionalUSD = "0.1" }},
		{"reduce is not sized here", RiskInputInvalid, func(in *SizingInput) { in.Candidate.Action = candidate.ActionReduce }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := sizingTestInput()
			tc.mutate(in)
			out := ComputeQuantity(in)
			require.Equal(t, RiskVerdictReject, out.Verdict, "must refuse")
			require.NotNil(t, out.Rejection)
			assert.Equal(t, tc.code, out.Rejection.Code)
			assert.NotEmpty(t, out.Rejection.Message)
		})
	}
}

func TestSizingUnreadableExistingExposureRefuses(t *testing.T) {
	t.Parallel()

	in := sizingTestInput()
	in.ExistingExposureUSD = "not-a-number"
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskTotalExposureExceeded, out.Rejection.Code)
}

func TestSizingRefusesWhenTheBookCannotFill(t *testing.T) {
	t.Parallel()

	in := sizingTestInput()
	in.Walk = func(_, _ string) (string, error) {
		return "", assert.AnError
	}
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskInsufficientDepth, out.Rejection.Code)
}

func TestSizingRerunsTheSlippageCheckWithTheActualNotional(t *testing.T) {
	t.Parallel()

	// The re-walk of the actual notional (19.9) comes back 0.6% worse: over the
	// 20 bps cap. This is step 9: rounding changed the depth consumption.
	in := sizingTestInput()
	in.Walk = func(_, notional string) (string, error) {
		if notional == "19.9" {
			return "1006", nil
		}
		return "1000", nil
	}
	out := ComputeQuantity(in)
	require.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskSlippageExceeded, out.Rejection.Code)

	// A re-walk within tolerance passes.
	in.Walk = func(_, notional string) (string, error) {
		if notional == "19.9" {
			return "1001", nil
		}
		return "1000", nil
	}
	out = ComputeQuantity(in)
	require.Equalf(t, RiskVerdictAllow, out.Verdict, "rejection: %+v", out.Rejection)
	assert.Equal(t, "19.9", out.NotionalUSD)

	// Without an approved slippage cap the re-walk is skipped entirely.
	in = sizingTestInput()
	in.Envelope.MaxSlippageBPS = nil
	in.Walk = func(_, _ string) (string, error) {
		return "2000", nil // wildly different, but no cap to breach
	}
	out = ComputeQuantity(in)
	require.Equalf(t, RiskVerdictAllow, out.Verdict, "rejection: %+v", out.Rejection)
}

func TestSizingMisuseNeverAllows(t *testing.T) {
	t.Parallel()

	in := sizingTestInput()
	in.Candidate = nil
	out := ComputeQuantity(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskInputInvalid, out.Rejection.Code)

	in = sizingTestInput()
	in.Walk = nil
	out = ComputeQuantity(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskInputInvalid, out.Rejection.Code)
}
