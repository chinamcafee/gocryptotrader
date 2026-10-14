package macdcross

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
)

// crossData builds a one-hour candle stream from the closes so OnSignal can
// run over a whole series. Offsets start at 1 like the backtester's loader.
func crossData(t *testing.T, closes []float64) *kline.DataFromKline {
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

// drainToEnd advances the stream so Latest() sits on the final candle.
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

	settings := map[string]any{fastPeriodKey: float64(8), slowPeriodKey: float64(21), signalPeriodKey: float64(5)}
	assert.NoError(t, s.SetCustomSettings(settings))
	assert.Equal(t, int64(8), s.fastPeriod.IntPart())
	assert.Equal(t, int64(21), s.slowPeriod.IntPart())
	assert.Equal(t, int64(5), s.signalPeriod.IntPart())

	bad := map[string]any{fastPeriodKey: "12"}
	assert.ErrorIs(t, s.SetCustomSettings(bad), base.ErrInvalidCustomSettings)

	bad = map[string]any{slowPeriodKey: float64(0)}
	assert.ErrorIs(t, s.SetCustomSettings(bad), base.ErrInvalidCustomSettings)

	bad = map[string]any{signalPeriodKey: float64(-1)}
	assert.ErrorIs(t, s.SetCustomSettings(bad), base.ErrInvalidCustomSettings)

	bad = map[string]any{"lol": float64(1)}
	assert.ErrorIs(t, s.SetCustomSettings(bad), base.ErrInvalidCustomSettings)

	// A fast period at or above the slow period can never produce a cross.
	inverted := map[string]any{fastPeriodKey: float64(30), slowPeriodKey: float64(20)}
	assert.ErrorIs(t, s.SetCustomSettings(inverted), base.ErrInvalidCustomSettings)
}

func TestSetDefaults(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	assert.Equal(t, int64(12), s.fastPeriod.IntPart())
	assert.Equal(t, int64(26), s.slowPeriod.IntPart())
	assert.Equal(t, int64(9), s.signalPeriod.IntPart())
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
	d := crossData(t, make([]float64, 100))
	drainToEnd(t, d)
	_, err := s.OnSignal(d, nil, nil)
	assert.ErrorIs(t, err, base.ErrTooMuchBadData)
}

// TestGoldenAndDeathCross pins the fixed rule: a rising series after a flat
// stretch buys once on the golden cross; the following decline sells once on
// the death cross. It is the baseline contract from Q24=A.
func TestGoldenAndDeathCross(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	closes := make([]float64, 0, 130)
	for range 50 {
		closes = append(closes, 100)
	}
	for i := range 40 {
		closes = append(closes, 101+float64(i))
	}
	for i := range 40 {
		closes = append(closes, 140-float64(i))
	}
	directions := runSeries(t, &s, crossData(t, closes))
	assert.Equal(t, 1, count(directions, order.Buy), "the golden cross should buy exactly once")
	assert.Equal(t, 1, count(directions, order.Sell), "the death cross should sell exactly once")
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

// TestWarmupDoesNothing makes sure the strategy stays quiet before the
// slow+signal warmup is complete.
func TestWarmupDoesNothing(t *testing.T) {
	t.Parallel()
	s := Strategy{}
	s.SetDefaults()
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	directions := runSeries(t, &s, crossData(t, closes))
	for i, d := range directions {
		assert.Equalf(t, order.DoNothing, d, "bar %d is inside the warmup window", i+1)
	}
}
