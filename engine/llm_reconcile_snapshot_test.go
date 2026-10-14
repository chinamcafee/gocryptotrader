package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

type fakeSummarySource struct {
	name      string
	pairs     currency.Pairs
	pairsErr  error
	summaries map[string]*futures.PositionSummary
	errs      map[string]error
}

func (f *fakeSummarySource) GetName() string { return f.name }

func (f *fakeSummarySource) GetEnabledPairs(asset.Item) (currency.Pairs, error) {
	return f.pairs, f.pairsErr
}

func (f *fakeSummarySource) GetFuturesPositionSummary(_ context.Context, req *futures.PositionSummaryRequest) (*futures.PositionSummary, error) {
	id := req.Pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String()
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	return f.summaries[id], nil
}

type fakeLocalPositions struct {
	rows []futures.Position
	err  error
}

func (f *fakeLocalPositions) GetAllOpenFuturesPositions() ([]futures.Position, error) {
	return f.rows, f.err
}

// TestFetchExchangePositions covers the exchange-truth side of W7-02: signed
// sizes become side+quantity, zero positions are not disagreements, and one
// unreadable pair refuses the whole pass.
func TestFetchExchangePositions(t *testing.T) {
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	eth := currency.NewPairWithDelimiter("ETH", "USDT", "_")
	sol := currency.NewPairWithDelimiter("SOL", "USDT", "_")
	src := &fakeSummarySource{
		name:  "Binance",
		pairs: currency.Pairs{btc, eth, sol},
		summaries: map[string]*futures.PositionSummary{
			"BTC_USDT": {CurrentSize: decimal.MustFromFloat(0.5)},
			"ETH_USDT": {CurrentSize: decimal.Zero},
			"SOL_USDT": {CurrentSize: decimal.MustFromFloat(-2)},
		},
		errs: map[string]error{},
	}
	rows, err := fetchExchangePositions(context.Background(), src, asset.USDTMarginedFutures)
	require.NoError(t, err)
	require.Len(t, rows, 2, "zero positions are not disagreements")
	assert.Equal(t, ReconcilePosition{MarketID: "binance:usdtmarginedfutures:BTC_USDT", Side: "LONG", Quantity: "0.5"}, rows[0])
	assert.Equal(t, ReconcilePosition{MarketID: "binance:usdtmarginedfutures:SOL_USDT", Side: "SHORT", Quantity: "2"}, rows[1])

	// One unreadable pair refuses the whole pass: half a reading is not a
	// reconciliation.
	src.errs["ETH_USDT"] = errors.New("boom")
	_, err = fetchExchangePositions(context.Background(), src, asset.USDTMarginedFutures)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ETH_USDT")

	src.errs = map[string]error{}
	src.pairsErr = errors.New("no pairs")
	_, err = fetchExchangePositions(context.Background(), src, asset.USDTMarginedFutures)
	require.Error(t, err)
}

// TestFetchLocalPositions covers the local side: the scope filter, the
// Latest→Opening fallback, and the direction refusal.
func TestFetchLocalPositions(t *testing.T) {
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	eth := currency.NewPairWithDelimiter("ETH", "USDT", "_")
	src := &fakeLocalPositions{rows: []futures.Position{
		{
			Exchange: "Binance", Asset: asset.USDTMarginedFutures, Pair: btc,
			LatestSize: decimal.MustFromFloat(0.5), LatestDirection: order.Long,
		},
		{
			// Another venue: out of the LLM trading scope, not a disagreement.
			Exchange: "Kraken", Asset: asset.USDTMarginedFutures, Pair: btc,
			LatestSize: decimal.MustFromFloat(9), LatestDirection: order.Long,
		},
		{
			// Older than its latest update: the opening facts still describe a
			// real position.
			Exchange: "Binance", Asset: asset.USDTMarginedFutures, Pair: eth,
			OpeningSize: decimal.MustFromFloat(-3), OpeningDirection: order.Short,
		},
	}}
	rows, err := fetchLocalPositions(src, "binance", asset.USDTMarginedFutures)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, ReconcilePosition{MarketID: "binance:usdtmarginedfutures:BTC_USDT", Side: "LONG", Quantity: "0.5"}, rows[0])
	assert.Equal(t, ReconcilePosition{MarketID: "binance:usdtmarginedfutures:ETH_USDT", Side: "SHORT", Quantity: "3"}, rows[1])

	src.rows = []futures.Position{{
		Exchange: "Binance", Asset: asset.USDTMarginedFutures, Pair: btc,
		LatestSize: decimal.MustFromFloat(1), LatestDirection: order.Side(99),
	}}
	_, err = fetchLocalPositions(src, "binance", asset.USDTMarginedFutures)
	require.ErrorIs(t, err, ErrIntentInputInvalid, "an uncategorisable direction is refused, not guessed")
}

// TestLLMReconcileFailsClosed covers the unassembled surface.
func TestLLMReconcileFailsClosed(t *testing.T) {
	var empty *RPCServer
	_, err := empty.LLMReconcile(context.Background(), &gctrpc.LLMReconcileRequest{})
	require.ErrorIs(t, err, errLLMReconcileUnavailable)

	// Assembled manager but no trading subsystems: still fail-closed.
	server := &RPCServer{Engine: &Engine{llmManager: &LLMManager{}}}
	_, err = server.LLMReconcile(context.Background(), &gctrpc.LLMReconcileRequest{})
	require.ErrorIs(t, err, errLLMReconcileUnavailable)
}
