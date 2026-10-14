package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

func TestPlanFromCoverageMapsEveryField(t *testing.T) {
	t.Parallel()

	// The config carries numbers a human typed; the plan carries the arithmetic
	// they imply. A field that fails to map would leave the plan on its default,
	// which is a weight budget the operator did not choose.
	plan := planFromCoverage(&config.LLMCoverageConfig{
		Enabled:                 true,
		Scope:                   "ALL_USDTM_PERPETUAL",
		RotationCycleMinutes:    7,
		KlineIncrementLimit:     9,
		KlineFullLimit:          300,
		FullReloadSpreadMinutes: 3,
		WeightBudgetFraction:    0.45,
		PersistIntervals:        []string{"1h", "4h"},
	})
	assert.True(t, plan.Enabled, "enabled should map")
	assert.Equal(t, "ALL_USDTM_PERPETUAL", plan.Scope, "the scope should map")
	assert.Equal(t, 7, plan.RotationCycleMinutes, "the cycle should map")
	assert.Equal(t, 9, plan.KlineIncrementLimit, "the increment limit should map")
	assert.Equal(t, 300, plan.KlineFullLimit, "the reload limit should map")
	assert.Equal(t, 3, plan.FullReloadSpreadMinutes, "the spread should map")
	assert.Equal(t, 0.45, plan.WeightBudgetFraction, "the budget fraction should map")
	assert.Equal(t, []string{"1h", "4h"}, plan.PersistIntervals, "the intervals should map")
	require.NoError(t, plan.Validate(), "a mapped plan must be valid")

	// The mapped fraction must survive into the budget arithmetic, because that
	// figure is the hard gate the rotation respects.
	assert.Equal(t, 1080, coverage.BudgetPerMinute(&plan),
		"a mapped fraction must produce the corresponding per-minute budget")
}

func TestPlanFromCoverageFallsBackOnUnsetFields(t *testing.T) {
	t.Parallel()

	// Zero values mean "not set", so the documented default applies rather than
	// a zero cycle or a zero budget.
	plan := planFromCoverage(&config.LLMCoverageConfig{Enabled: true})
	assert.Equal(t, coverage.DefaultPlan(), plan, "unset fields should take the documented defaults")

	assert.Equal(t, coverage.DefaultPlan(), planFromCoverage(nil),
		"a nil section must take the defaults rather than producing an unusable plan")
}

func TestPlanFromCoverageDoesNotAliasTheConfigSlice(t *testing.T) {
	t.Parallel()

	intervals := []string{"1h", "4h"}
	plan := planFromCoverage(&config.LLMCoverageConfig{Enabled: true, PersistIntervals: intervals})
	intervals[0] = "1d"
	assert.Equal(t, []string{"1h", "4h"}, plan.PersistIntervals,
		"the plan must not alias the caller's slice: a later config edit must not mutate a live plan")
}

func TestIntervalFromConfig(t *testing.T) {
	t.Parallel()

	for spelling, want := range map[string]kline.Interval{
		"":     kline.OneHour, // the documented default
		"1h":   kline.OneHour,
		"1H":   kline.OneHour,
		" 1h ": kline.OneHour, //nolint:gocritic // the padded key is the fixture: it proves the mapper trims
		"1m":   kline.OneMin,
		"5m":   kline.FiveMin,
		"15m":  kline.FifteenMin,
		"4h":   kline.FourHour,
		"1d":   kline.OneDay,
	} {
		got, err := intervalFromConfig(spelling)
		require.NoErrorf(t, err, "%q must map", spelling)
		assert.Equalf(t, want, got, "%q should map to %s", spelling, want)
	}

	// An unknown spelling is refused rather than defaulted: the freshness limit
	// and the fetch window both derive from the interval.
	_, err := intervalFromConfig("30m")
	require.ErrorIs(t, err, ErrUnsupportedSnapshotInterval,
		"an unsupported spelling must be refused rather than quietly becoming 1h")
	assert.Contains(t, err.Error(), "30m", "the message should name the spelling that was not understood")
}

func TestIndicatorParamsFromConfig(t *testing.T) {
	t.Parallel()

	params := indicatorParamsFromConfig(&config.LLMIndicatorConfig{
		MACDFast: 8, MACDSlow: 21, MACDSignal: 5,
		EMAPeriods: []int64{9, 21}, SMAPeriods: []int64{34},
		RSIPeriod: 21, ATRPeriod: 7, BollingerPeriod: 10,
	})
	assert.Equal(t, int64(8), params.MACDFast, "the fast period should map")
	assert.Equal(t, int64(21), params.MACDSlow, "the slow period should map")
	assert.Equal(t, int64(5), params.MACDSignal, "the signal period should map")
	assert.Equal(t, []int64{34}, params.SMAPeriods, "the SMA periods should map")
	assert.Equal(t, []int64{9, 21}, params.EMAPeriods, "the EMA periods should map")
	assert.Equal(t, int64(21), params.RSIPeriod, "the RSI period should map")
	assert.Equal(t, int64(7), params.ATRPeriod, "the ATR period should map")
	assert.Equal(t, int64(10), params.BollingerPeriod, "the Bollinger period should map")
	require.NoError(t, params.Validate(), "mapped parameters must be valid")

	// Unset fields take the documented defaults, and a nil section is the whole
	// default set.
	assert.Equal(t, snapshot.DefaultIndicatorParams(), indicatorParamsFromConfig(nil),
		"a nil section must take the defaults")

	partial := indicatorParamsFromConfig(&config.LLMIndicatorConfig{MACDFast: 8})
	assert.Equal(t, int64(8), partial.MACDFast, "the supplied field should map")
	assert.Equal(t, snapshot.DefaultIndicatorParams().MACDSlow, partial.MACDSlow,
		"an unset field must keep its default rather than becoming zero")
	assert.Equal(t, snapshot.DefaultIndicatorParams().SMAPeriods, partial.SMAPeriods,
		"an unset list must keep its default")

	// A mismatched pair is refused by the contract's own validation, which the
	// assembler calls; mapping must not silently repair it.
	bad := indicatorParamsFromConfig(&config.LLMIndicatorConfig{MACDFast: 30, MACDSlow: 26, MACDSignal: 9})
	require.ErrorIs(t, bad.Validate(), snapshot.ErrIndicatorParamsInvalid,
		"an impossible pair must be refused rather than corrected")
}

func TestSetupRotationRespectsTheEnabledFlag(t *testing.T) {
	t.Parallel()

	cfg := &config.LLMConfig{Enabled: true, Exchange: "Binance", Asset: "usdtmarginedfutures"}
	cfg.Coverage.Enabled = false

	m, err := SetupLLMManager(llmStubExchangeManager{}, cfg)
	require.NoError(t, err, "the manager must build")
	rotation, err := m.SetupRotation()
	require.NoError(t, err, "a disabled coverage section is a configuration, not a failure")
	assert.Nil(t, rotation, "a disabled section should produce no rotator rather than a disabled one")

	cfg.Coverage.Enabled = true
	_, err = m.SetupRotation()
	require.Error(t, err,
		"an enabled section must surface a problem with the exchange rather than starting half-configured")

	var nilManager *LLMManager
	_, err = nilManager.SetupRotation()
	require.ErrorIs(t, err, ErrNilSubsystem, "a nil manager must refuse rather than panic")
}

func TestRotationManagerReflectsTheStubExchange(t *testing.T) {
	t.Parallel()

	// The stub manager cannot resolve an exchange, so an enabled section reports
	// an error and leaves the rotation nil. What matters is that a failed setup
	// does not leave a half-built rotator behind.
	cfg := &config.LLMConfig{Enabled: true, Exchange: "Binance", Asset: "usdtmarginedfutures"}
	cfg.Coverage.Enabled = true
	m, err := SetupLLMManager(llmStubExchangeManager{}, cfg)
	require.NoError(t, err, "the manager must build")
	require.NoError(t, m.Start(context.Background()), "startup must succeed even when coverage cannot start")
	assert.Nil(t, m.Rotation(), "a failed coverage setup should leave the rotation nil rather than half-built")
	require.NoError(t, m.Stop(), "the manager must still stop cleanly")
}

func TestIntervalSpellingAgreesWithTheContract(t *testing.T) {
	t.Parallel()

	// The config's spellings and the contract's must cover the same intervals,
	// or an operator could configure one the snapshot has no spelling for.
	for _, contractInterval := range snapshot.SupportedIntervals() {
		got, err := intervalFromConfig(string(contractInterval))
		require.NoErrorf(t, err, "the contract interval %s must be configurable", contractInterval)
		mapped, mapErr := snapshotInterval(got)
		require.NoErrorf(t, mapErr, "%s must map back to the contract", contractInterval)
		assert.Equal(t, contractInterval, mapped,
			"a config spelling must round-trip to the same contract interval")
	}
	assert.Equal(t, asset.USDTMarginedFutures, func() asset.Item {
		item, _ := asset.New("usdtmarginedfutures")
		return item
	}(), "the configured asset name must resolve to the perpetual futures class")
}

// llmExchangeManager resolves one concrete exchange so the cache pass-through
// in SetupRotation can be exercised without a network call.
type llmExchangeManager struct{ exch exchange.IBotExchange }

func (m llmExchangeManager) GetExchanges() ([]exchange.IBotExchange, error) {
	return []exchange.IBotExchange{m.exch}, nil
}

func (m llmExchangeManager) GetExchangeByName(string) (exchange.IBotExchange, error) {
	return m.exch, nil
}

func TestSetupRotationHandsTheManagerCacheToTheRotator(t *testing.T) {
	t.Parallel()

	cfg := &config.LLMConfig{Enabled: true, Exchange: "Binance", Asset: "usdtmarginedfutures"}
	cfg.Coverage.Enabled = true
	m, err := SetupLLMManager(llmExchangeManager{exch: &binance.Exchange{}}, cfg)
	require.NoError(t, err, "the manager must build")
	require.NotNil(t, m.Cache(), "the manager must own a candle cache")

	rotation, err := m.SetupRotation()
	require.NoError(t, err, "the binance wrapper satisfies the rotation interface")
	require.NotNil(t, rotation, "the rotator must build")
	assert.Same(t, m.Cache(), rotation.cfg.Cache,
		"the rotator must warm the manager's cache rather than a private one")
}
