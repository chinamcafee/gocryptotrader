package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/currency"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

// llmStubExchangeManager satisfies iExchangeManager without touching a real
// exchange, so the LLM manager can be exercised in isolation.
type llmStubExchangeManager struct{}

func (llmStubExchangeManager) GetExchanges() ([]exchange.IBotExchange, error) { return nil, nil }

func (llmStubExchangeManager) GetExchangeByName(string) (exchange.IBotExchange, error) {
	return nil, nil
}

func TestSetupLLMManagerRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	_, err := SetupLLMManager(nil, &config.LLMConfig{})
	require.ErrorIs(t, err, errNilExchangeManager, "a nil exchange manager must be rejected")

	_, err = SetupLLMManager(llmStubExchangeManager{}, nil)
	require.ErrorIs(t, err, errNilLLMConfig, "a nil LLM config must be rejected")
}

func TestSetupLLMManagerRunModeFailsClosed(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mode    string
		ack     string
		want    string
		unlocks bool
	}{
		{name: "empty", mode: "", want: LLMRunModeReadOnly},
		{name: "unknown", mode: "TURBO", want: LLMRunModeReadOnly},
		{name: "read only passthrough", mode: LLMRunModeReadOnly, want: LLMRunModeReadOnly},
		{name: "lowercase normalised", mode: "shadow", want: LLMRunModeShadow},
		{name: "paper", mode: LLMRunModePaper, want: LLMRunModePaper},
		{name: "testnet", mode: LLMRunModeTestnet, want: LLMRunModeTestnet},
		{
			name: "live without acknowledgement must downgrade",
			mode: LLMRunModeLive, ack: "", want: LLMRunModeReadOnly,
		},
		{
			name: "live with a wrong acknowledgement must downgrade",
			mode: LLMRunModeLive, ack: "yes", want: LLMRunModeReadOnly,
		},
		{
			name: "live with the exact acknowledgement is honoured",
			mode: LLMRunModeLive, ack: liveTradingAck, want: LLMRunModeLive, unlocks: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := SetupLLMManager(llmStubExchangeManager{}, &config.LLMConfig{
				RunMode:                tc.mode,
				AcknowledgeLiveTrading: tc.ack,
			})
			require.NoError(t, err, "setup must succeed for a well-formed config")
			require.NotNil(t, m, "a manager must be returned")

			status := m.Status()
			assert.Equal(t, tc.want, status.RunMode, "the effective run mode should be the fail-closed value")
			assert.Equal(t, tc.unlocks, status.LiveUnlocked, "live should only be unlocked by the exact acknowledgement")
		})
	}
}

func TestLLMManagerLifecycle(t *testing.T) {
	t.Parallel()

	m, err := SetupLLMManager(llmStubExchangeManager{}, &config.LLMConfig{RunMode: LLMRunModeReadOnly})
	require.NoError(t, err, "setup must succeed")

	assert.False(t, m.IsRunning(), "a freshly set up manager should not report as running")
	require.ErrorIs(t, m.Stop(), ErrSubSystemNotStarted, "stopping before starting must be reported")

	require.NoError(t, m.Start(context.Background()), "the first start must succeed")
	assert.True(t, m.IsRunning(), "the manager should report as running after start")
	require.ErrorIs(t, m.Start(context.Background()), ErrSubSystemAlreadyStarted, "a second start must be reported")

	require.NoError(t, m.Stop(), "the first stop must succeed")
	assert.False(t, m.IsRunning(), "the manager should not report as running after stop")
	require.ErrorIs(t, m.Stop(), ErrSubSystemNotStarted, "a second stop must be reported")
}

func TestLLMManagerStatusIsFailClosed(t *testing.T) {
	t.Parallel()

	m, err := SetupLLMManager(llmStubExchangeManager{}, &config.LLMConfig{RunMode: LLMRunModeTestnet})
	require.NoError(t, err, "setup must succeed")

	status := m.Status()
	assert.Equal(t, LLMRunModeTestnet, status.RunMode, "the status should echo the effective mode")
	assert.Equal(t, LLMPairIncomplete, status.PairReadiness, "readiness should stay INCOMPLETE until profiles and probes exist")
	assert.False(t, status.TradeAuthorized, "trade authorisation should never default to true")
	assert.False(t, status.ExecutionReady, "execution readiness should never default to true")
	assert.False(t, status.AccountReconciled, "reconciliation should never default to true")
	assert.False(t, status.Halted, "a fresh manager should not report as halted")
	assert.Equal(t, "NONE", status.HaltLevel, "a fresh manager should report no halt level")

	var nilManager *LLMManager
	nilStatus := nilManager.Status()
	assert.Equal(t, LLMRunModeReadOnly, nilStatus.RunMode, "a nil manager should report the safest mode")
	assert.False(t, nilStatus.ExecutionReady, "a nil manager should never report as ready to execute")

	// Nil receivers follow the same convention as the other subsystems, so the
	// engine shutdown path guards with IsRunning() rather than relying on Stop.
	assert.False(t, nilManager.IsRunning(), "a nil manager should not report as running")
	require.ErrorIs(t, nilManager.Stop(), ErrNilSubsystem, "stopping a nil manager must report an unset subsystem")

	require.Nil(t, nilManager.Config(), "a nil manager must return a nil config instead of panicking")
}

func TestLLMManagerConfigIsExposed(t *testing.T) {
	t.Parallel()

	cfg := &config.LLMConfig{RunMode: LLMRunModePaper, Exchange: "Binance"}
	m, err := SetupLLMManager(llmStubExchangeManager{}, cfg)
	require.NoError(t, err, "setup must succeed")

	got := m.Config()
	require.NotNil(t, got, "the manager must expose the configuration it was set up with")
	assert.Equal(t, "Binance", got.Exchange, "the exposed config should be the one supplied")
}

func TestEngineInvalidatesTheLLMCachedPair(t *testing.T) {
	t.Parallel()

	m, err := SetupLLMManager(llmStubExchangeManager{}, &config.LLMConfig{})
	require.NoError(t, err, "the manager must build")
	cache := m.Cache()
	require.NotNil(t, cache, "the manager must own the candle cache")

	pair := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	added, err := cache.merge(pair, asset.USDTMarginedFutures, kline.OneHour, &kline.Item{
		Candles: []kline.Candle{{Time: time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)}},
	}, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err, "the seed merge must succeed")
	require.Equal(t, 1, added, "one closed candle must enter the cache")
	require.Equal(t, 1, cache.Len())

	bot := &Engine{llmManager: m}
	assert.Equal(t, 1, bot.InvalidateLLMCachedPair(pair, asset.USDTMarginedFutures),
		"the disabled pair's key must drop")
	assert.Zero(t, cache.Len(), "the cache should be empty after the invalidation")

	// The hook must be safe on a bot that never enabled the subsystem.
	var nilBot *Engine
	assert.Zero(t, nilBot.InvalidateLLMCachedPair(pair, asset.USDTMarginedFutures))
	assert.Zero(t, (&Engine{}).InvalidateLLMCachedPair(pair, asset.USDTMarginedFutures))
}
