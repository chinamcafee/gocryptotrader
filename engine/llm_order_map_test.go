package engine

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-03 测试：映射的三条铁律（Buy/Sell、Leverage=0、转换回读）与失败路径。

func mapTestIntent() *llmstore.OrderIntent {
	price := "1000"
	return &llmstore.OrderIntent{
		ID:                "intent-1",
		DecisionRunID:     "run-1",
		RiskEvaluationID:  "risk-1",
		StrategyVersionID: "4b1d2e71-2b3c-4d4e-9f50-6b7c8d9e0f10",
		ApprovalID:        "approval-1",
		Exchange:          "Binance",
		Pair:              "BTC_USDT",
		Asset:             "usdtmarginedfutures",
		Action:            "OPEN_LONG",
		Side:              "BUY",
		OrderType:         "LIMIT",
		Quantity:          "0.0199",
		Price:             &price,
		ReduceOnly:        false,
		ClientOrderID:     "gct-llm-abc",
		NotionalUSD:       "19.9",
		EnvelopeHash:      "env",
		PairEpoch:         7,
		Status:            IntentStatusPrepared,
	}
}

func TestBuildOrderSubmitLimitLong(t *testing.T) {
	t.Parallel()

	submit, err := BuildOrderSubmit(mapTestIntent())
	require.NoError(t, err)
	assert.Equal(t, "Binance", submit.Exchange)
	assert.Equal(t, order.Limit, submit.Type)
	assert.Equal(t, order.Buy, submit.Side, "the venue side is Buy, never Long")
	expectedAsset, err := asset.New("usdtmarginedfutures")
	require.NoError(t, err)
	assert.Equal(t, expectedAsset, submit.AssetType)
	assert.Equal(t, "BTC", submit.Pair.Base.String())
	assert.Equal(t, "USDT", submit.Pair.Quote.String())
	assert.Equal(t, 0.0199, submit.Amount)
	assert.Equal(t, 1000.0, submit.Price)
	assert.Equal(t, 0.0, submit.Leverage, "a non-zero leverage trips ErrSubmitLeverageNotSupported")
	assert.Equal(t, "gct-llm-abc", submit.ClientOrderID)
	assert.True(t, submit.RetrieveFees)
	assert.Equal(t, OrderSubmitFeeDelay, submit.RetrieveFeeDelay)
	assert.False(t, submit.ReduceOnly)
}

func TestBuildOrderSubmitReduceAndMarket(t *testing.T) {
	t.Parallel()

	intent := mapTestIntent()
	intent.Side = "SELL"
	intent.ReduceOnly = true
	intent.OrderType = "MARKET"
	intent.Price = nil
	submit, err := BuildOrderSubmit(intent)
	require.NoError(t, err)
	assert.Equal(t, order.Sell, submit.Side)
	assert.True(t, submit.ReduceOnly)
	assert.Equal(t, order.Market, submit.Type)
	assert.Zero(t, submit.Price, "a market order carries no price")
}

func TestBuildOrderSubmitStopMarketUsesTriggerPrice(t *testing.T) {
	t.Parallel()

	intent := mapTestIntent()
	intent.Side = "SELL"
	intent.ReduceOnly = true
	intent.OrderType = "STOP_MARKET"
	submit, err := BuildOrderSubmit(intent)
	require.NoError(t, err)
	assert.Equal(t, order.StopMarket, submit.Type)
	assert.Equal(t, 1000.0, submit.TriggerPrice, "a stop level is a trigger, not a limit price")
	assert.Zero(t, submit.Price)
}

func TestBuildOrderSubmitValidates(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*llmstore.OrderIntent){
		"missing client id": func(i *llmstore.OrderIntent) { i.ClientOrderID = "" },
		"bad pair":          func(i *llmstore.OrderIntent) { i.Pair = "BTCUSDT" },
		"bad asset":         func(i *llmstore.OrderIntent) { i.Asset = "not-an-asset" },
		"long side":         func(i *llmstore.OrderIntent) { i.Side = "LONG" },
		"bad type":          func(i *llmstore.OrderIntent) { i.OrderType = "ICEBERG" },
		"bad quantity":      func(i *llmstore.OrderIntent) { i.Quantity = "abc" },
		"negative quantity": func(i *llmstore.OrderIntent) { i.Quantity = "-1" },
		"limit without price": func(i *llmstore.OrderIntent) {
			i.Price = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			intent := mapTestIntent()
			mutate(intent)
			_, err := BuildOrderSubmit(intent)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrIntentInputInvalid)
		})
	}
}

func TestFloatWithinNeverGrows(t *testing.T) {
	t.Parallel()

	// Exact values convert cleanly.
	value, err := floatWithin("0.0199")
	require.NoError(t, err)
	assert.Equal(t, 0.0199, value)

	// The round trip check: whatever float64 does, the read-back may not
	// exceed the reviewed decimal (Q13's post-conversion re-check).
	for _, raw := range []string{"0.1", "0.001", "12345.6789", "0.00000001"} {
		value, err := floatWithin(raw)
		require.NoError(t, err, raw)
		roundTrip, err := decParse(shortestFloat(value))
		require.NoError(t, err)
		original, err := decParse(raw)
		require.NoError(t, err)
		assert.Falsef(t, roundTrip.GreaterThan(original), "%s should not grow through float64", raw)
	}
}

// shortestFloat mirrors the production round-trip formatting.
func shortestFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
