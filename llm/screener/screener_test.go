package screener

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trendCandles builds candles from (bars, per-bar rate) segments. Long,
// realistic windows matter: the EMA seed transient dominates short series
// (a linear ramp is fully degenerate -- its histogram converges to zero).
func trendCandles(base float64, segments ...[2]float64) []Candle {
	out := []Candle{}
	last := base
	for _, segment := range segments {
		for range int(segment[0]) {
			last *= 1 + segment[1]
			out = append(out, Candle{Close: last, High: last * 1.0005, Low: last * 0.9995, Volume: 10})
		}
	}
	return out
}

func testMarkets() []*Market {
	return []*Market{
		{
			MarketID: "Binance:usdtmarginedfutures:BTC_USDT", Last: 50000, HasLast: true,
			Change24h: 3.2, HasChange24h: true, QuoteVolume24h: 900, HasVolume24h: true,
			Candles: trendCandles(100, [2]float64{230, 0.001}, [2]float64{20, -0.01}), // uptrend then pullback: bearish momentum
			Flow:    &FlowFacts{BuyVol: 700, SellVol: 300},
		},
		{
			MarketID: "Binance:usdtmarginedfutures:ETH_USDT", Last: 3000, HasLast: true,
			Change24h: -1.5, HasChange24h: true, QuoteVolume24h: 500, HasVolume24h: true,
			Candles: trendCandles(100, [2]float64{230, -0.001}, [2]float64{20, 0.01}), // downtrend then bounce: bullish momentum
			Flow:    &FlowFacts{BuyVol: 200, SellVol: 800},
		},
		{
			// Ticker-only market: no candles, no flow.
			MarketID: "Binance:usdtmarginedfutures:SOL_USDT", Last: 150, HasLast: true,
			Change24h: 8.0, HasChange24h: true, QuoteVolume24h: 700, HasVolume24h: true,
		},
	}
}

func TestEvaluateFiltersRanksAndTruncates(t *testing.T) {
	t.Parallel()
	// "24h change >= 0, top 2 by quote volume" -- BTC and SOL pass, ETH fails.
	result, err := Evaluate(&Request{
		Criteria: []Criteria{{Metric: "change24h", Op: "gte", Value: new(0.0)}},
		Sort:     []SortKey{{Metric: "quoteVolume24h", Desc: true}},
		Limit:    2,
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", result.Rows[0].MarketID, "highest volume first")
	assert.Equal(t, "Binance:usdtmarginedfutures:SOL_USDT", result.Rows[1].MarketID)
	assert.Equal(t, 1, result.Meta.FilteredOut, "ETH failed the criterion")
	assert.Zero(t, result.Meta.ExcludedForMissing)
	require.NotNil(t, result.Rows[0].Numbers["change24h"])
	assert.InDelta(t, 3.2, *result.Rows[0].Numbers["change24h"], 1e-9)
	require.NotNil(t, result.Rows[0].Numbers["quoteVolume24h"], "the identity metrics ride every row")
}

func TestEvaluateMissingValuesNeverPass(t *testing.T) {
	t.Parallel()
	// MACD needs ~35 candles: SOL (no candles) is excluded as missing, never
	// zero-filled; BTC computes a bearish histogram and is filtered.
	result, err := Evaluate(&Request{
		Criteria: []Criteria{{Metric: "macdstate", Op: "eq", State: MacdBull}},
		Limit:    20,
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", result.Rows[0].MarketID, "the bounce carries a bullish histogram")
	assert.Equal(t, 1, result.Meta.FilteredOut, "BTC's bearish histogram fails the criterion")
	assert.Equal(t, 1, result.Meta.ExcludedForMissing, "SOL has no candles: counted apart from the criterion failure")

	// Flow criteria: ETH's flow exists but is net selling (filtered); SOL has
	// no T2 read at all (missing).
	result, err = Evaluate(&Request{
		Criteria: []Criteria{{Metric: "takernetflowratio24h", Op: "gte", Value: new(0.0)}},
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", result.Rows[0].MarketID)
	require.NotNil(t, result.Rows[0].Numbers["takernetflowratio24h"])
	assert.InDelta(t, 0.4, *result.Rows[0].Numbers["takernetflowratio24h"], 1e-9, "(700-300)/(700+300)")
	assert.Equal(t, 1, result.Meta.FilteredOut)
	assert.Equal(t, 1, result.Meta.ExcludedForMissing)

	// A sort metric that is missing excludes the row and is counted as such.
	result, err = Evaluate(&Request{
		Sort: []SortKey{{Metric: "change48h", Desc: true}},
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 2, "both candle-carrying markets have a 48h window")
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", result.Rows[0].MarketID, "the bounce outranks the pullback over 48h")
	assert.Equal(t, 1, result.Meta.ExcludedForMissing, "SOL sorts out as missing")
}

func TestEvaluateCategoricalCrossAndValidation(t *testing.T) {
	t.Parallel()
	// A sharp three-bar drop after flat drift flips the histogram recently.
	market := &Market{
		MarketID: "Binance:usdtmarginedfutures:XRP_USDT",
		Candles:  trendCandles(100, [2]float64{247, 0.0002}, [2]float64{3, -0.05}),
	}
	result, err := Evaluate(&Request{
		Criteria: []Criteria{{Metric: "macdcross", Op: "eq", State: MacdBearCross}},
	}, []*Market{market})
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, MacdBearCross, result.Rows[0].States["macdcross"])

	// Unknown metrics, bad operators and bad categories refuse.
	_, err = Evaluate(&Request{Criteria: []Criteria{{Metric: "nope", Op: "gt", Value: new(1.0)}}}, testMarkets())
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Evaluate(&Request{Criteria: []Criteria{{Metric: "change24h", Op: "around", Value: new(1.0)}}}, testMarkets())
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Evaluate(&Request{Criteria: []Criteria{{Metric: "change24h", Op: "between", Value: new(1.0)}}}, testMarkets())
	require.ErrorIs(t, err, ErrInvalid, "between without value2 refuses")
	_, err = Evaluate(&Request{Criteria: []Criteria{{Metric: "macdstate", Op: "eq", State: "SIDEWAYS"}}}, testMarkets())
	require.ErrorIs(t, err, ErrInvalid)
	_, err = Evaluate(&Request{Sort: []SortKey{{Metric: "macdstate"}}}, testMarkets())
	require.ErrorIs(t, err, ErrInvalid, "categorical sort refuses")
}

func TestEvaluateConsistencyAndLimit(t *testing.T) {
	t.Parallel()
	result, err := Evaluate(&Request{
		Sort:  []SortKey{{Metric: "quoteVolume24h", Desc: false}},
		Limit: 99,
	}, testMarkets())
	require.NoError(t, err)
	assert.Len(t, result.Rows, 3)
	assert.Equal(t, 50, MaxLimit, "the hard limit keeps one screen small")
	assert.LessOrEqual(t, result.Meta.Matched, MaxLimit)

	// The catalogue reports every metric with its tier.
	names := map[string]bool{}
	for _, info := range Catalogue() {
		names[info.Name] = true
	}
	for _, want := range []string{"change24h", "change4h", "change48h", "macdstate", "macdcross", "takernetflowratio24h"} {
		assert.Truef(t, names[want], "catalogue should list %s", want)
	}
}

func TestEvaluateBetweenAndInclusiveBounds(t *testing.T) {
	t.Parallel()
	result, err := Evaluate(&Request{
		Criteria: []Criteria{{Metric: "change24h", Op: "between", Value: new(-2.0), Value2: new(4.0)}},
	}, testMarkets())
	require.NoError(t, err)
	assert.Len(t, result.Rows, 2, "BTC 3.2 and ETH -1.5 fall inside [-2, 4]; SOL's 8 is outside")

	result, err = Evaluate(&Request{
		Criteria: []Criteria{{Metric: "change24h", Op: "lte", Value: new(-1.5)}},
	}, testMarkets())
	require.NoError(t, err)
	assert.Len(t, result.Rows, 1, "lte is inclusive")
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", result.Rows[0].MarketID)
}

func TestMacdHistMatchesState(t *testing.T) {
	t.Parallel()
	// macdhist is macdstate as a sortable number: the sign of the histogram
	// value must agree with the categorical reading on the same candles.
	result, err := Evaluate(&Request{
		Sort: []SortKey{{Metric: "macdhist", Desc: true}},
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 2, "both candle-carrying markets compute a histogram")
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", result.Rows[0].MarketID, "the bullish bounce ranks above the bearish pullback")
	require.NotNil(t, result.Rows[0].Numbers["macdhist"])
	assert.Positive(t, *result.Rows[0].Numbers["macdhist"], "a BULL state reads a positive histogram")
	require.NotNil(t, result.Rows[1].Numbers["macdhist"])
	assert.Negative(t, *result.Rows[1].Numbers["macdhist"], "a BEAR state reads a negative histogram")
	assert.Equal(t, 1, result.Meta.ExcludedForMissing, "SOL without candles is missing, never zero")

	// A numeric criterion over the histogram passes exactly the bullish market.
	result, err = Evaluate(&Request{
		Criteria: []Criteria{{Metric: "macdhist", Op: "gt", Value: new(0.0)}},
		Sort:     []SortKey{{Metric: "macdhist", Desc: true}},
	}, testMarkets())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", result.Rows[0].MarketID)
}
