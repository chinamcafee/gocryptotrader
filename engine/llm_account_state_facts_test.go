package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// TestBuildRiskAccountStateFacts covers the repo-backed slice of the risk
// account facts: today's sealed baseline (absent until sealed, never invented)
// and the unresolved UNKNOWN-intent count.
func TestBuildRiskAccountStateFacts(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	now := providerAdminNow

	state, err := buildRiskAccountStateFacts(ctx, "acct-1", now)
	require.NoError(t, err)
	assert.Nil(t, state.DayStartEquityUSD, "an unsealed day has no baseline, not a zero")
	assert.Equal(t, 0, state.UnresolvedUnknownOrders)

	require.NoError(t, llmstore.InsertDayStartInventory(ctx, &llmstore.DayStartInventory{
		ID: "ds-1", AccountID: "acct-1", UtcDay: UtcDayKey(now), EquityUsdt: "1000.50",
		BalanceUsdt: "1000.50", Source: "test", ObservedAt: now, SealedAt: now,
	}))
	seedIntent := func(id, status string) {
		t.Helper()
		require.NoError(t, llmstore.InsertOrderIntent(ctx, &llmstore.OrderIntent{
			ID: id, DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
			StrategyVersionID: "ver-1", ApprovalID: "appr-1",
			Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
			Action: "OPEN_LONG", Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
			ClientOrderID: "gct-" + id, NotionalUSD: "500.00", EnvelopeHash: "env",
			Status: status, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
		}))
	}
	seedIntent("i-unknown-1", IntentStatusUnknown)
	seedIntent("i-unknown-2", IntentStatusUnknown)
	seedIntent("i-prepared", "PREPARED")
	seedIntent("i-partial", IntentStatusPartiallyFilled)

	state, err = buildRiskAccountStateFacts(ctx, "acct-1", now)
	require.NoError(t, err)
	require.NotNil(t, state.DayStartEquityUSD)
	assert.Equal(t, "1000.50", *state.DayStartEquityUSD)
	assert.Equal(t, 2, state.UnresolvedUnknownOrders, "only UNKNOWN intents are unresolved")
	assert.Equal(t, 1, state.UnreconciledPartials, "a partially filled intent has not settled")

	_, err = buildRiskAccountStateFacts(ctx, "  ", now)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = buildRiskAccountStateFacts(ctx, "acct-1", time.Time{})
	require.ErrorIs(t, err, ErrRiskFactsInput)
}
