package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

type stubCandlesSource struct {
	err           error
	baseCalls     int
	extendedCalls int
}

func (s *stubCandlesSource) GetHistoricCandles(_ context.Context, p currency.Pair, a asset.Item, i kline.Interval, start, _ time.Time) (*kline.Item, error) {
	s.baseCalls++
	if s.err != nil {
		return nil, s.err
	}
	return &kline.Item{
		Exchange: "stub",
		Pair:     p,
		Asset:    a,
		Interval: i,
		Candles:  []kline.Candle{{Time: start, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}},
	}, nil
}

func (s *stubCandlesSource) GetHistoricCandlesExtended(_ context.Context, p currency.Pair, a asset.Item, i kline.Interval, start, _ time.Time) (*kline.Item, error) {
	s.extendedCalls++
	if s.err != nil {
		return nil, s.err
	}
	return &kline.Item{
		Exchange: "stub",
		Pair:     p,
		Asset:    a,
		Interval: i,
		Candles:  []kline.Candle{{Time: start, Open: 2, High: 2, Low: 2, Close: 2, Volume: 2}},
	}, nil
}

func TestGetHistoricCandlesSharedModeSelection(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	pair := currency.NewBTCUSDT()

	t.Run("default uses the plain fetch", func(t *testing.T) {
		t.Parallel()
		src := &stubCandlesSource{}
		item, err := getHistoricCandlesShared(t.Context(), src, pair, asset.Spot, kline.OneHour, start, end, historicCandlesOptions{})
		require.NoError(t, err)
		assert.Equal(t, 1, src.baseCalls)
		assert.Equal(t, 0, src.extendedCalls)
		assert.Equal(t, 1.0, item.Candles[0].Close)
	})

	t.Run("exRequest uses the extended fetch", func(t *testing.T) {
		t.Parallel()
		src := &stubCandlesSource{}
		item, err := getHistoricCandlesShared(t.Context(), src, pair, asset.Spot, kline.OneHour, start, end, historicCandlesOptions{ExRequest: true})
		require.NoError(t, err)
		assert.Equal(t, 0, src.baseCalls)
		assert.Equal(t, 1, src.extendedCalls)
		assert.Equal(t, 2.0, item.Candles[0].Close)
	})
}

func TestGetHistoricCandlesSharedPropagatesError(t *testing.T) {
	t.Parallel()
	expectedErr := errors.New("test candles error")
	src := &stubCandlesSource{err: expectedErr}
	_, err := getHistoricCandlesShared(t.Context(), src, currency.NewBTCUSDT(), asset.Spot, kline.OneHour, time.Now(), time.Now().Add(time.Hour), historicCandlesOptions{})
	assert.ErrorIs(t, err, expectedErr)
}

// TestGetHistoricCandlesSharedDoesNotGateOnEnabledPairs pins the property the
// LLM layer relies on: the shared fetch knows nothing about enabled pairs, so
// any in-scope market can be read on demand. The RPC layer keeps its own
// enabled-pair gate (checkParams) on top of this function.
func TestGetHistoricCandlesSharedDoesNotGateOnEnabledPairs(t *testing.T) {
	t.Parallel()
	src := &stubCandlesSource{}
	pair := currency.NewPairWithDelimiter("ADA", "USDT", "-")
	item, err := getHistoricCandlesShared(t.Context(), src, pair, asset.USDTMarginedFutures, kline.OneHour, time.Now(), time.Now().Add(time.Hour), historicCandlesOptions{})
	require.NoError(t, err)
	assert.Equal(t, pair, item.Pair)
	assert.Equal(t, 1, src.baseCalls)
}
