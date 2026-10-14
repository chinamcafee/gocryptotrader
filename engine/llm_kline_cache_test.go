package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

type countingCandleSource struct {
	calls  int
	mu     sync.Mutex
	window *kline.Item
}

func (s *countingCandleSource) GetHistoricCandles(_ context.Context, _ currency.Pair, _ asset.Item, _ kline.Interval, _, _ time.Time) (*kline.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.window, nil
}

func (s *countingCandleSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func cacheWindow() *kline.Item {
	candles := make([]kline.Candle, 0, 61)
	start := providerAdminNow.Add(-60 * time.Hour)
	for i := range 61 {
		candles = append(candles, kline.Candle{
			Time: start.Add(time.Duration(i) * time.Hour),
			Open: float64(100 + i), High: float64(101 + i), Low: float64(99 + i),
			Close: float64(100 + i), Volume: 1,
		})
	}
	// The newest candle is stamped at providerAdminNow, so it is still forming
	// there; the 60 before it are closed.
	return &kline.Item{
		Pair:  currency.NewPairWithDelimiter("BTC", "USDT", "_"),
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Candles: candles,
	}
}

func TestKlineCacheStoresClosedOnly(t *testing.T) {
	cache, err := NewKlineCache(4)
	require.NoError(t, err)

	added, err := cache.merge(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour, cacheWindow(), providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, 60, added, "61 candles end at now; the forming one is never cached")

	// Idempotent: merging the same window adds nothing.
	added, err = cache.merge(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour, cacheWindow(), providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, 0, added)

	closed := cache.closedCandles(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour,
		providerAdminNow.Add(-61*time.Hour), providerAdminNow)
	require.Len(t, closed, 60)
	for _, candle := range closed {
		assert.True(t, candle.Time.Add(time.Hour).Before(providerAdminNow) || candle.Time.Add(time.Hour).Equal(providerAdminNow),
			"no cached candle may still be forming: %v", candle.Time)
	}
}

func TestKlineCacheCoverageExemptsFormingSlot(t *testing.T) {
	cache, err := NewKlineCache(4)
	require.NoError(t, err)
	_, err = cache.merge(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour, cacheWindow(), providerAdminNow)
	require.NoError(t, err)

	start := providerAdminNow.Add(-50 * time.Hour)
	assert.True(t, cache.covers(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour, start, providerAdminNow),
		"a request through now is covered: only the forming slot may be missing")
	assert.False(t, cache.covers(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour,
		providerAdminNow.Add(-70*time.Hour), providerAdminNow),
		"a window reaching before the cached history is not covered")
	assert.False(t, cache.covers(cacheWindow().Pair, asset.USDTMarginedFutures, kline.OneHour,
		start, providerAdminNow.Add(3*time.Hour)),
		"slots after the cached history are not covered")
}

func TestKlineCacheLRUEvictsWholeKeys(t *testing.T) {
	cache, err := NewKlineCache(2)
	require.NoError(t, err)
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	eth := currency.NewPairWithDelimiter("ETH", "USDT", "_")
	sol := currency.NewPairWithDelimiter("SOL", "USDT", "_")

	window := cacheWindow()
	_, err = cache.merge(btc, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
	require.NoError(t, err)
	_, err = cache.merge(eth, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
	require.NoError(t, err)
	// Touch BTC so ETH is the least recently used, then add SOL.
	_ = cache.closedCandles(btc, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow)
	_, err = cache.merge(sol, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
	require.NoError(t, err)

	assert.Equal(t, 2, cache.Len())
	assert.NotEmpty(t, cache.closedCandles(btc, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow))
	assert.Empty(t, cache.closedCandles(eth, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow),
		"the least recently used key is evicted as a whole")
	assert.NotEmpty(t, cache.closedCandles(sol, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow))
}

func TestKlineCacheInvalidatePair(t *testing.T) {
	cache, err := NewKlineCache(8)
	require.NoError(t, err)
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	eth := currency.NewPairWithDelimiter("ETH", "USDT", "_")

	window := cacheWindow()
	_, err = cache.merge(btc, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
	require.NoError(t, err)
	_, err = cache.merge(btc, asset.USDTMarginedFutures, kline.FourHour, window, providerAdminNow)
	require.NoError(t, err)
	_, err = cache.merge(eth, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
	require.NoError(t, err)

	assert.Equal(t, 2, cache.InvalidatePair(btc, asset.USDTMarginedFutures), "every interval of the pair goes")
	assert.Equal(t, 1, cache.Len())
	assert.NotEmpty(t, cache.closedCandles(eth, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow))
}

func TestFetchCandlesThroughCache(t *testing.T) {
	cache, err := NewKlineCache(4)
	require.NoError(t, err)
	source := &countingCandleSource{window: cacheWindow()}
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	start := providerAdminNow.Add(-50 * time.Hour)

	first, err := fetchCandlesThroughCache(t.Context(), source, cache, btc, asset.USDTMarginedFutures, kline.OneHour, start, providerAdminNow, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, 1, source.callCount())
	require.Len(t, first.Candles, 61, "the fetch path hands the source's window through (forming bar included) for the assembler")

	second, err := fetchCandlesThroughCache(t.Context(), source, cache, btc, asset.USDTMarginedFutures, kline.OneHour, start, providerAdminNow, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, 1, source.callCount(), "an identical request is served from the cache")
	require.Len(t, second.Candles, 50, "the cache carries closed history only")

	// A later clock reaches one slot past the cached history: fetch again, and
	// the next identical call is cached once more.
	later := providerAdminNow.Add(time.Hour)
	third, err := fetchCandlesThroughCache(t.Context(), source, cache, btc, asset.USDTMarginedFutures, kline.OneHour, later.Add(-50*time.Hour), later, later)
	require.NoError(t, err)
	assert.Equal(t, 2, source.callCount())
	_ = third

	fourth, err := fetchCandlesThroughCache(t.Context(), source, cache, btc, asset.USDTMarginedFutures, kline.OneHour, later.Add(-50*time.Hour), later, later)
	require.NoError(t, err)
	assert.Equal(t, 2, source.callCount(), "the merged window now covers the later request")
	require.Len(t, fourth.Candles, 50, "50 closed slots in [later-50h, later]; the forming slot has no bar")
}

func TestKlineCacheConcurrentAccess(t *testing.T) {
	cache, err := NewKlineCache(4)
	require.NoError(t, err)
	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	window := cacheWindow()

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, _ = cache.merge(btc, asset.USDTMarginedFutures, kline.OneHour, window, providerAdminNow)
			_ = cache.closedCandles(btc, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-time.Hour), providerAdminNow)
			_ = cache.covers(btc, asset.USDTMarginedFutures, kline.OneHour, providerAdminNow.Add(-2*time.Hour), providerAdminNow)
		})
	}
	wg.Wait()
	assert.Equal(t, 1, cache.Len())
}
