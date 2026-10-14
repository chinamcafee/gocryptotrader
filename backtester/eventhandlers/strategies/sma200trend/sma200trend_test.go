package sma200trend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/backtester/common"
	"github.com/thrasher-corp/gocryptotrader/backtester/data"
	"github.com/thrasher-corp/gocryptotrader/backtester/data/kline"
	"github.com/thrasher-corp/gocryptotrader/backtester/eventhandlers/strategies/base"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	gctkline "github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// trendData builds a one-hour candle stream from the closes so OnSignal can
// run over a whole series. Offsets start at 1 like the backtester's loader.
func trendData(t *testing.T, closes []float64) *kline.DataFromKline {
	t.Helper()
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	candles := make([]gctkline.Candle, len(closes))
	for i, c := range closes {
		candles[i] = gctkline.Candle{
			Time:   start.Add(time.Duration(i) * time.Hour),
			Open:   c,
			High:   c,
			Low:    c,
			Close:  c,
			Volume: 1,
		}
	}
	item := &gctkline.Item{Exchange: "binance", Pair: currency.NewBTCUSDT(), Asset: asset.Spot, Interval: gctkline.OneHour, Candles: candles}
	d := &kline.DataFromKline{Item: item, Base: &data.Base{}}
	require.NoError(t, d.Load())
	ranger, err := gctkline.CalculateCandleDateRanges(start, start.Add(time.Duration(len(closes))*time.Hour), gctkline.OneHour, 100000)
	require.NoError(t, err)
	d.RangeHolder = ranger
	require.NoError(t, d.RangeHolder.SetHasDataFromCandles(item.Candles))
	return d
}

func drainToEnd(t *testing.T, d *kline.DataFromKline) {
	t.Helper()
	for {
		_, err := d.Next()
		if err != nil {
			require.ErrorIs(t, err, data.ErrEndOfData)
			return
		}
	}
}

func runSeries(t *testing.T, s *Strategy, d *kline.DataFromKline) []order.Side {
	t.Helper()
	var directions []order.Side
	for {
		_, err := d.Next()
		if err != nil {
			require.ErrorIs(t, err, data.ErrEndOfData)
			break
		}
		resp, err := s.OnSignal(d, nil, nil)
		require.NoError(t, err)
		directions = append(directions, resp.GetDirection())
	}
	return directions
}

func count(directions []order.Side, side order.Side) int {
	n := 0
	for _, d := range directions {
		if d == side {
			n++
		}
	}
	return n
}

func TestName(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	assert.Equal(t, Name, s.Name())
}

func TestSupportsSimultaneousProcessing(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	assert.True(t, s.SupportsSimultaneousProcessing())
}

func TestSetCustomSettings(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	assert.NoError(t, s.SetCustomSettings(nil))

	assert.NoError(t, s.SetCustomSettings(map[string]any{smaPeriodKey: float64(50)}))
	assert.Equal(t, int64(50), s.smaPeriod.IntPart())

	assert.ErrorIs(t, s.SetCustomSettings(map[string]any{smaPeriodKey: "200"}), base.ErrInvalidCustomSettings)
	assert.ErrorIs(t, s.SetCustomSettings(map[string]any{smaPeriodKey: float64(0)}), base.ErrInvalidCustomSettings)
	assert.ErrorIs(t, s.SetCustomSettings(map[string]any{"lol": float64(1)}), base.ErrInvalidCustomSettings)
}

func TestSetDefaults(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	assert.Equal(t, int64(200), s.smaPeriod.IntPart())
}

func TestOnSignalNilData(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	_, err := s.OnSignal(nil, nil, nil)
	assert.ErrorIs(t, err, common.ErrNilEvent)
}

func TestOnSignalRefusesTooMuchBadData(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	s.smaPeriod = decimal.NewFromInt(10)
	d := trendData(t, make([]float64, 40))
	drainToEnd(t, d)
	_, err := s.OnSignal(d, nil, nil)
	assert.ErrorIs(t, err, base.ErrTooMuchBadData)
}

// TestCrossAboveAndBelowTheSMA pins the fixed rule: a flat series then a jump
// buys once as the close crosses above the SMA; the following collapse sells
// once as it crosses below. It is the baseline contract from Q24=A.
func TestCrossAboveAndBelowTheSMA(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	s.smaPeriod = decimal.NewFromInt(10)
	closes := make([]float64, 0, 45)
	for range 25 {
		closes = append(closes, 100)
	}
	for range 10 {
		closes = append(closes, 130)
	}
	for range 10 {
		closes = append(closes, 80)
	}
	directions := runSeries(t, &s, trendData(t, closes))
	assert.Equal(t, 1, count(directions, order.Buy), "the cross above should buy exactly once")
	assert.Equal(t, 1, count(directions, order.Sell), "the cross below should sell exactly once")
	buyIndex := -1
	sellIndex := -1
	for i, d := range directions {
		if d == order.Buy && buyIndex == -1 {
			buyIndex = i
		}
		if d == order.Sell {
			sellIndex = i
		}
	}
	assert.Less(t, buyIndex, sellIndex, "the buy should precede the sell")
}

// TestWarmupDoesNothing makes sure the strategy stays quiet before the SMA
// window is full.
func TestWarmupDoesNothing(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	s.smaPeriod = decimal.NewFromInt(20)
	closes := make([]float64, 15)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	directions := runSeries(t, &s, trendData(t, closes))
	for i, d := range directions {
		assert.Equalf(t, order.DoNothing, d, "bar %d is inside the warmup window", i+1)
	}
}
