package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// riskSystemFixture is a fully readable G-group input.
func riskSystemFixture() *RiskSystemFactsInput {
	return &RiskSystemFactsInput{
		MarketID: "BTC_USDT", Mode: LLMRunModePaper, PairReadiness: LLMPairReady,
		LiveAcknowledged: true, MaxClockDriftMs: 1000,
		OrderManagerUp: true, ExchangeManagerUp: true, DatabaseHealthy: true,
	}
}

// TestRiskSystemFactsFromState covers the G-group mapping: halt scoping from
// the persisted rows, and the explicit engine-state material passing through.
func TestRiskSystemFactsFromState(t *testing.T) {
	t.Parallel()

	halted := riskSystemFixture()
	halted.Halts = []llmstore.Halt{{ID: "h1", Level: HaltLevelMarket, MarketID: new("BTC_USDT")}}
	facts, err := riskSystemFactsFromState(halted)
	require.NoError(t, err)
	assert.False(t, facts.GlobalHalt)
	assert.True(t, facts.MarketHalt, "a market halt on this market halts it")

	otherMarket := riskSystemFixture()
	otherMarket.Halts = []llmstore.Halt{{ID: "h2", Level: HaltLevelMarket, MarketID: new("ETH_USDT")}}
	facts, err = riskSystemFactsFromState(otherMarket)
	require.NoError(t, err)
	assert.False(t, facts.GlobalHalt)
	assert.False(t, facts.MarketHalt, "another market's halt does not halt this one")

	for _, level := range []string{HaltLevelSoft, HaltLevelFull} {
		global := riskSystemFixture()
		global.Halts = []llmstore.Halt{{ID: "h3", Level: level}}
		facts, err = riskSystemFactsFromState(global)
		require.NoError(t, err)
		assert.Truef(t, facts.GlobalHalt, "%s is a global halt", level)
	}

	cleared := riskSystemFixture()
	cleared.Halts = []llmstore.Halt{{
		ID: "h4", Level: HaltLevelFull,
		ClearedAt: new(providerAdminNow), ClearedBy: new("op"),
	}}
	facts, err = riskSystemFactsFromState(cleared)
	require.NoError(t, err)
	assert.False(t, facts.GlobalHalt, "a cleared halt is not in force")

	plain := riskSystemFixture()
	plain.ModelBudgetExhausted = true
	plain.ClockDriftMs = 1200
	plain.RateLimitDegraded = true
	plain.OrderManagerUp = false
	facts, err = riskSystemFactsFromState(plain)
	require.NoError(t, err)
	assert.Equal(t, LLMRunModePaper, facts.Mode)
	assert.True(t, facts.LiveAcknowledged)
	assert.True(t, facts.ModelBudgetExhausted)
	assert.Equal(t, int64(1200), facts.ClockDriftMs)
	assert.Equal(t, int64(1000), facts.MaxClockDriftMs)
	assert.True(t, facts.RateLimitDegraded)
	assert.Equal(t, LLMPairReady, facts.PairReadiness)
	assert.False(t, facts.OrderManagerUp)
	assert.True(t, facts.ExchangeManagerUp)
	assert.True(t, facts.DatabaseHealthy)
}

// TestRiskSystemFactsFromStateRefusals pins the fail-closed material checks.
func TestRiskSystemFactsFromStateRefusals(t *testing.T) {
	t.Parallel()

	_, err := riskSystemFactsFromState(nil)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noMode := riskSystemFixture()
	noMode.Mode = " "
	_, err = riskSystemFactsFromState(noMode)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noReady := riskSystemFixture()
	noReady.PairReadiness = ""
	_, err = riskSystemFactsFromState(noReady)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noLimit := riskSystemFixture()
	noLimit.MaxClockDriftMs = 0
	_, err = riskSystemFactsFromState(noLimit)
	assert.ErrorIs(t, err, ErrRiskFactsInput, "a clock gate without a limit is no gate")

	negativeDrift := riskSystemFixture()
	negativeDrift.ClockDriftMs = -5
	_, err = riskSystemFactsFromState(negativeDrift)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
}
