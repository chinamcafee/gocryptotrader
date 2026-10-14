package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckLLMConfigDefaults(t *testing.T) {
	t.Parallel()

	var c Config
	c.CheckLLMConfig()

	assert.Equal(t, "READ_ONLY", c.LLM.RunMode, "an absent run mode should default to the safest analysing mode")
	assert.Equal(t, "binance-testnet", c.LLM.Network, "the network should default to the sandbox so mainnet is never inherited")
	assert.Equal(t, "Binance", c.LLM.Exchange, "the exchange should be scoped to the venue this platform trades")
	assert.Equal(t, "usdtmarginedfutures", c.LLM.Asset, "the asset should be scoped to USDT-margined perpetuals")

	assert.Equal(t, 60, c.LLM.Analysis.AutoCandidateWindowSeconds, "the candidate window should match OPEN-QUESTIONS Q9 = C (60s)")
	assert.Equal(t, 90, c.LLM.Analysis.PipelineDeadlineSeconds, "the pipeline deadline should default to 90s")
	assert.Equal(t, 30, c.LLM.Analysis.RequestTimeoutSeconds, "the per-request timeout should be capped at 30s")
	assert.Equal(t, 4, c.LLM.Analysis.MaxToolRounds, "tool rounds should default to 4")
	assert.Equal(t, 8, c.LLM.Analysis.MaxToolCalls, "tool calls should default to 8")
	assert.Equal(t, 24000, c.LLM.Analysis.MaxCumulativeTokens, "cumulative tokens should default to 24000")
	assert.Equal(t, 1024, c.LLM.Analysis.MaxDefaultOutputTokens, "output tokens should default to 1024 so candidates are not truncated")

	assert.Equal(t, "1h", c.LLM.Indicators.DefaultInterval, "the analysis interval should default to 1h")
	assert.Equal(t, int64(12), c.LLM.Indicators.MACDFast, "MACD fast should default to 12")
	assert.Equal(t, int64(26), c.LLM.Indicators.MACDSlow, "MACD slow should default to 26")
	assert.Equal(t, int64(9), c.LLM.Indicators.MACDSignal, "MACD signal should default to 9")
	assert.Equal(t, []int64{20}, c.LLM.Indicators.EMAPeriods, "EMA periods should default to 20")
	assert.Equal(t, []int64{200}, c.LLM.Indicators.SMAPeriods, "SMA periods should default to 200")
	require.NotNil(t, c.LLM.Indicators.EnableRSI, "RSI enablement must be materialised as a pointer")
	assert.True(t, *c.LLM.Indicators.EnableRSI, "RSI should be enabled by default per OPEN-QUESTIONS Q16")
	require.NotNil(t, c.LLM.Indicators.EnableBollinger, "Bollinger enablement must be materialised as a pointer")
	assert.True(t, *c.LLM.Indicators.EnableBollinger, "Bollinger bands should be enabled by default per OPEN-QUESTIONS Q16")
	require.NotNil(t, c.LLM.Indicators.ExcludePartialCandle, "partial-candle exclusion must be materialised as a pointer")
	assert.True(t, *c.LLM.Indicators.ExcludePartialCandle, "partial candles should be excluded so indicators stay replayable")
	require.NotNil(t, c.LLM.Indicators.MultiTimeframe, "multi-timeframe must be materialised as a pointer")
	assert.False(t, *c.LLM.Indicators.MultiTimeframe, "multi-timeframe analysis should stay off by default")

	assert.Equal(t, "ALL_USDTM_PERPETUAL", c.LLM.Coverage.Scope, "coverage should default to the full perpetual universe")
	assert.Equal(t, 10, c.LLM.Coverage.RotationCycleMinutes, "the rotation cycle should default to 10 minutes")
	assert.Equal(t, []string{"1h", "4h", "1d"}, c.LLM.Coverage.PersistIntervals, "only coarse intervals should be persisted by default")
	assert.InDelta(t, 0.6, c.LLM.Coverage.WeightBudgetFraction, 1e-9, "the rotation should leave 40 percent of the rate limit free")

	assert.Equal(t, 100, c.LLM.Budget.LLMReservationCap24h, "the LLM reservation cap should default to 100 per 24h")
	assert.Equal(t, 100, c.LLM.Budget.JevReservationCap24h, "the Jev reservation cap should default to 100 per 24h")
	assert.Nil(t, c.LLM.Budget.DailyBudgetUSD, "an unset daily budget should stay nil rather than becoming zero, which would forbid all model calls")

	assert.Equal(t, 1000, c.LLM.Halt.HaltOnClockDriftMs, "clock drift should halt before Binance rejects a signed request")
	require.NotNil(t, c.LLM.Halt.RequireConfirmationToClear, "halt clearance confirmation must be materialised as a pointer")
	assert.True(t, *c.LLM.Halt.RequireConfirmationToClear, "clearing a halt should require confirmation by default")
	assert.Equal(t, "gct-llm-", c.LLM.Halt.RobotClientOrderPrefix, "the robot order prefix should scope cancellations away from operator orders")
	assert.Equal(t, 12, c.LLM.Halt.ReviewExpireHours, "unhandled reviews should expire within 12 hours")
}

func TestCheckLLMConfigPreservesOperatorChoices(t *testing.T) {
	t.Parallel()

	zero := 0.0
	c := Config{}
	c.LLM.RunMode = "testnet"
	c.LLM.Network = "binance-mainnet"
	c.LLM.Analysis.RequestTimeoutSeconds = 12
	c.LLM.Analysis.MaxToolCalls = 3
	c.LLM.Coverage.RotationCycleMinutes = 7
	c.LLM.Budget.DailyBudgetUSD = &zero
	disableRSI := false
	c.LLM.Indicators.EnableRSI = &disableRSI
	c.LLM.Indicators.EMAPeriods = []int64{9, 21}

	c.CheckLLMConfig()

	assert.Equal(t, "TESTNET", c.LLM.RunMode, "the run mode should be upper-cased rather than replaced")
	assert.Equal(t, "binance-mainnet", c.LLM.Network, "an explicit network should never be overwritten")
	assert.Equal(t, 12, c.LLM.Analysis.RequestTimeoutSeconds, "an explicit timeout below the cap should be preserved")
	assert.Equal(t, 3, c.LLM.Analysis.MaxToolCalls, "an explicit tool-call limit should be preserved")
	assert.Equal(t, 7, c.LLM.Coverage.RotationCycleMinutes, "an explicit in-range rotation cycle should be preserved")
	require.NotNil(t, c.LLM.Budget.DailyBudgetUSD, "the daily budget pointer must be preserved")
	assert.InDelta(t, 0, *c.LLM.Budget.DailyBudgetUSD, 1e-9, "exactly zero should survive as the forbidding value")
	require.NotNil(t, c.LLM.Indicators.EnableRSI, "the RSI pointer must be preserved")
	assert.False(t, *c.LLM.Indicators.EnableRSI, "an explicit false should not be re-enabled by defaults")
	assert.Equal(t, []int64{9, 21}, c.LLM.Indicators.EMAPeriods, "explicit EMA periods should not be replaced by defaults")
}

func TestCheckLLMConfigClampsOutOfRangeValues(t *testing.T) {
	t.Parallel()

	c := Config{}
	c.LLM.RunMode = "not-a-mode"
	c.LLM.Analysis.RequestTimeoutSeconds = 500
	c.LLM.Coverage.RotationCycleMinutes = 99
	c.LLM.Coverage.WeightBudgetFraction = 3

	c.CheckLLMConfig()

	assert.Equal(t, "READ_ONLY", c.LLM.RunMode, "an unknown run mode should fall back to READ_ONLY")
	assert.Equal(t, 30, c.LLM.Analysis.RequestTimeoutSeconds, "a timeout above the ceiling should be clamped to 30s")
	assert.Equal(t, 10, c.LLM.Coverage.RotationCycleMinutes, "a rotation cycle above 10 minutes should be clamped")
	assert.InDelta(t, 0.6, c.LLM.Coverage.WeightBudgetFraction, 1e-9, "a fraction above 1 should be replaced by the default")
}

func TestCheckLLMConfigIsIdempotent(t *testing.T) {
	t.Parallel()

	var c Config
	c.CheckLLMConfig()
	first := c.LLM
	c.CheckLLMConfig()

	assert.Equal(t, first, c.LLM, "running the checks twice should produce identical configuration")
}
