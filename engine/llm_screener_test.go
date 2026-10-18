package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/screener"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
	"github.com/thrasher-corp/gocryptotrader/types"
)

// M2-17 测试：screenMarkets 的端到端执行（T0 ticker + T1 缓存 K 线 + T2 假场地流），
// 证据信封与缺席计数。

// screenerTestSource adds the T2 taker-flow seam and the N10 on-demand seams
// to the shared fake source.
type screenerTestSource struct {
	fakeToolSource
	flow              map[string]binance.UTakerVolumeData
	batchStats        []binance.U24HrPriceChangeStats
	unrestrictedCalls int
	unrestrictedErr   error
}

func (s *screenerTestSource) GetName() string { return "Binance" }

func (s *screenerTestSource) UTakerBuySellVol(_ context.Context, symbol currency.Pair, _ string, _ int64, _, _ time.Time) ([]binance.UTakerVolumeData, error) {
	key := symbol.Base.Item.Symbol + symbol.Quote.Item.Symbol
	row, ok := s.flow[key]
	if !ok {
		return nil, errors.New("no flow for " + key)
	}
	return []binance.UTakerVolumeData{row}, nil
}

func (s *screenerTestSource) U24HTickerPriceChangeStats(_ context.Context, _ currency.Pair) ([]binance.U24HrPriceChangeStats, error) {
	return s.batchStats, nil
}

func (s *screenerTestSource) GetHistoricCandlesUnrestricted(_ context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, _ time.Time) (*kline.Item, error) {
	s.unrestrictedCalls++
	if s.unrestrictedErr != nil {
		return nil, s.unrestrictedErr
	}
	candles := make([]kline.Candle, 250)
	last := 100.0
	for i := range candles {
		last *= 1.001
		candles[i] = kline.Candle{
			Time: start.Add(time.Duration(i) * time.Hour), Open: last, High: last * 1.001, Low: last * 0.999,
			Close: last, Volume: 10,
		}
	}
	return &kline.Item{Pair: pair, Asset: a, Interval: interval, Candles: candles}, nil
}

// screenerWindows seeds the kline cache with one trend per market: a steady
// drift then a sharp move at the tail. A pure monotone series is degenerate
// (the histogram converges to zero), so the fixture mirrors the screener
// package's own convention: the pullback carries a bearish histogram, the
// bounce a bullish one.
func screenerWindows(t *testing.T, cache *KlineCache, now time.Time) {
	t.Helper()
	seed := func(pair currency.Pair, bullishAtEnd bool) {
		type segment struct {
			bars int
			rate float64
		}
		segments := []segment{{230, 0.001}, {20, -0.01}}
		if bullishAtEnd {
			segments = []segment{{230, -0.001}, {20, 0.01}}
		}
		candles := make([]kline.Candle, 0, 250)
		last := 100.0
		start := now.Add(-251 * time.Hour)
		i := 0
		for _, segment := range segments {
			for range segment.bars {
				last *= 1 + segment.rate
				at := start.Add(time.Duration(i) * time.Hour)
				i++
				candles = append(candles, kline.Candle{
					Time: at, Open: last / (1 + segment.rate), High: last * 1.0005, Low: last * 0.9995, Close: last, Volume: 10,
				})
			}
		}
		_, err := cache.merge(pair, asset.USDTMarginedFutures, kline.OneHour, &kline.Item{
			Pair: pair, Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Candles: candles,
		}, now)
		require.NoError(t, err)
	}
	seed(currency.NewPairWithDelimiter("BTC", "USDT", "_"), false) // uptrend then pullback: bearish momentum
	seed(currency.NewPairWithDelimiter("ETH", "USDT", "_"), true)  // downtrend then bounce: bullish momentum
}

func screenerRunner(t *testing.T) *LLMToolRunner {
	t.Helper()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	markets := []string{
		"Binance:usdtmarginedfutures:BTC_USDT",
		"Binance:usdtmarginedfutures:ETH_USDT",
	}
	source := &screenerTestSource{flow: map[string]binance.UTakerVolumeData{
		"BTCUSDT": {BuyVol: 700, SellVol: 300},
		"ETHUSDT": {BuyVol: 200, SellVol: 800},
	}}
	runner, err := NewLLMToolRunner("Binance", asset.USDTMarginedFutures, "acct-1",
		tools.NewScope(markets, []string{"acct-1"}, []string{}), source)
	require.NoError(t, err)
	cache, err := NewKlineCache(8)
	require.NoError(t, err)
	screenerWindows(t, cache, now)
	runner.Markets = markets
	runner.Interval = kline.OneHour
	runner.Cache = cache
	runner.FlowScanLimit = 10
	runner.Now = func() time.Time { return now }

	require.NoError(t, ticker.ProcessTicker(&ticker.Price{
		ExchangeName: "Binance", AssetType: asset.USDTMarginedFutures,
		Pair: currency.NewPairWithDelimiter("BTC", "USDT", "_"),
		Last: 50000, PercentChange24Hour: 3.2, QuoteVolume: 900,
	}))
	require.NoError(t, ticker.ProcessTicker(&ticker.Price{
		ExchangeName: "Binance", AssetType: asset.USDTMarginedFutures,
		Pair: currency.NewPairWithDelimiter("ETH", "USDT", "_"),
		Last: 3000, PercentChange24Hour: -1.5, QuoteVolume: 500,
	}))
	return runner
}

// screenerOut is the decoded screen payload.
type screenerOut struct {
	Rows []struct {
		MarketID string              `json:"marketId"`
		Numbers  map[string]*float64 `json:"numbers"`
		States   map[string]string   `json:"states"`
	} `json:"rows"`
	Meta  map[string]any `json:"meta"`
	Notes []string       `json:"notes"`
}

// screenerEvidence decodes the serialised ToolEvidence a Run call returned.
func screenerEvidence(t *testing.T, raw string) (out screenerOut, quality string) {
	t.Helper()
	var envelope struct {
		Data    json.RawMessage `json:"data"`
		Quality string          `json:"quality"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &envelope))
	require.NoError(t, json.Unmarshal(envelope.Data, &out))
	return out, envelope.Quality
}

func TestRunScreenMarketsEndToEnd(t *testing.T) {
	runner := screenerRunner(t)

	raw, err := runner.Run(t.Context(), "call-1", tools.ToolScreenMarkets,
		`{"criteria":[{"metric":"change24h","op":"gte","value":0}],"sort":[{"metric":"quoteVolume24h","desc":true}],"limit":10}`)
	require.NoError(t, err)
	out, quality := screenerEvidence(t, raw)
	require.Len(t, out.Rows, 1, "only BTC passes the 24h-change criterion")
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", out.Rows[0].MarketID)
	assert.Equal(t, "DATABASE_CACHED", quality, "the screen rides the caches; T2 readings are marked inside the row data")
	assert.Equal(t, float64(2), out.Meta["universeSize"])
	assert.Equal(t, float64(1), out.Meta["filteredOut"], "ETH failed the criterion")

	// The flow tier reads the venue seam and folds into the ratio metric.
	raw, err = runner.Run(t.Context(), "call-2", tools.ToolScreenMarkets,
		`{"criteria":[{"metric":"takernetflowratio24h","op":"gte","value":0}]}`)
	require.NoError(t, err)
	out, _ = screenerEvidence(t, raw)
	require.Len(t, out.Rows, 1, "only the net-buying market passes")
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", out.Rows[0].MarketID)
	require.NotNil(t, out.Rows[0].Numbers["takernetflowratio24h"])
	assert.InDelta(t, 0.4, *out.Rows[0].Numbers["takernetflowratio24h"], 1e-9)
	assert.Equal(t, float64(1), out.Meta["filteredOut"], "ETH's net selling fails the criterion")

	// MACD folds the cached window into the categorical state.
	raw, err = runner.Run(t.Context(), "call-3", tools.ToolScreenMarkets,
		`{"criteria":[{"metric":"macdstate","op":"eq","state":"BULL"}]}`)
	require.NoError(t, err)
	out, _ = screenerEvidence(t, raw)
	require.Len(t, out.Rows, 1)
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", out.Rows[0].MarketID, "the bounce carries the bullish histogram")
}

func TestRunScreenMarketsRefusesWithoutAssembly(t *testing.T) {
	runner := screenerRunner(t)
	runner.Markets = nil
	_, err := runner.Run(t.Context(), "call-x", tools.ToolScreenMarkets, `{}`)
	require.ErrorContains(t, err, "screener has no markets",
		"an unassembled screener refuses closed with the environmental cause, not a retryable argument error")
	require.NotErrorIs(t, err, ErrToolArgumentsInvalid, "retrying the same call cannot help")
}

func TestRunScreenMarketsScansTheFullUniverseOnDemand(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	source := &screenerTestSource{
		batchStats: []binance.U24HrPriceChangeStats{
			{Symbol: "BTCUSDT", LastPrice: types.Number(50000), PriceChangePercent: types.Number(1.5), QuoteVolume: types.Number(900)},
			{Symbol: "ETHUSDT", LastPrice: types.Number(3000), PriceChangePercent: types.Number(3.2), QuoteVolume: types.Number(700)},
			{Symbol: "SOLUSDT", LastPrice: types.Number(150), PriceChangePercent: types.Number(-2.0), QuoteVolume: types.Number(500)},
		},
	}
	source.available = currency.Pairs{
		currency.NewPairWithDelimiter("BTC", "USDT", "_"),
		currency.NewPairWithDelimiter("ETH", "USDT", "_"),
		currency.NewPairWithDelimiter("SOL", "USDT", "_"),
		currency.NewPairWithDelimiter("BTC", "USDC", "_"), // out of scope: not USDT-quoted
	}
	source.enabled = currency.Pairs{currency.NewPairWithDelimiter("ETH", "USDT", "_")}
	runner, err := NewLLMToolRunner("Binance", asset.USDTMarginedFutures, "acct-1",
		tools.NewScope(nil, []string{"acct-1"}, nil), source)
	require.NoError(t, err)
	cache, cacheErr := NewKlineCache(16)
	require.NoError(t, cacheErr)
	runner.Markets = []string{"Binance:usdtmarginedfutures:ETH_USDT"} // the rotator set; the full universe overrides it
	runner.Interval = kline.OneHour
	runner.Cache = cache
	runner.FullUniverse = true
	runner.Now = func() time.Time { return now }

	var updates []ToolProgress
	ctx := withToolProgress(t.Context(), func(progress ToolProgress) { updates = append(updates, progress) })
	raw, err := runner.Run(ctx, "call-1", tools.ToolScreenMarkets, `{"sort":[{"metric":"change24h","desc":true}],"limit":2}`)
	require.NoError(t, err)
	out, _ := screenerEvidence(t, raw)
	assert.Equal(t, float64(3), out.Meta["universeSize"], "available ∪ enabled in scope: BTC/ETH/SOL, BTC_USDC excluded")
	require.Len(t, out.Rows, 2)
	assert.Equal(t, "Binance:usdtmarginedfutures:ETH_USDT", out.Rows[0].MarketID, "the batched ticker ranks ETH first")
	assert.Equal(t, 3, source.unrestrictedCalls, "each uncached market is fetched on demand once")
	require.NotEmpty(t, updates)
	assert.Equal(t, "CANDLES", updates[len(updates)-1].Phase)
	assert.Equal(t, 3, updates[len(updates)-1].Done)
	solPair := currency.NewPairWithDelimiter("SOL", "USDT", "_")
	assert.NotEmpty(t, cache.closedCandles(solPair, asset.USDTMarginedFutures, kline.OneHour, now.Add(-300*time.Hour), now),
		"an on-demand read backfills the cache, so the next screen is fast")
}

func TestFitScreenResultToEvidenceBudget(t *testing.T) {
	build := func(n int) map[string]any {
		rows := make([]screener.Row, n)
		for i := range rows {
			numbers := map[string]*float64{}
			for j := range 12 {
				v := float64(i) + float64(j)/7
				numbers[fmt.Sprintf("metric%02d", j)] = &v
			}
			rows[i] = screener.Row{MarketID: fmt.Sprintf("Binance:usdtmarginedfutures:M%03d_USDT", i), Numbers: numbers}
		}
		return map[string]any{"rows": rows, "meta": screener.Meta{UniverseSize: n}}
	}

	small := fitScreenResultToEvidenceBudget(build(3))
	smallRows, isRows := small["rows"].([]screener.Row)
	require.True(t, isRows)
	assert.Len(t, smallRows, 3, "a result inside the budget passes untouched")
	assert.NotContains(t, small, "notes")

	big := fitScreenResultToEvidenceBudget(build(50))
	rows, isRows := big["rows"].([]screener.Row)
	require.True(t, isRows)
	require.NotEmpty(t, rows, "the trimmed result still carries rows")
	assert.Less(t, len(rows), 50, "the tail is dropped to fit the budget")
	raw, err := json.Marshal(big)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(raw), toolEvidenceResultBudget, "the serialised result fits the budget")
	notes, isNotes := big["notes"].([]string)
	require.True(t, isNotes, "the trim is stated in the notes")
	require.NotEmpty(t, notes)
	assert.Contains(t, notes[0], "trimmed", "the trim is never silent")
}
