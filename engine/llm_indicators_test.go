package engine

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// This file is the double-verification of the model layer's indicator assembly
// against the platform's own indicator implementation.
//
// It lives in engine, not in llm/snapshot, for a reason that is a design rule
// rather than a convenience: llm/ may not import exchanges/kline (see
// llm/boundary_test.go), because kline reaches exchanges/order and the model
// layer has to stay extractable. So the code that turns a candle window into an
// indicator set belongs here, and the test that proves the assembly agrees with
// the numbers the webUI displays belongs here with it.
//
// The rule being checked: there must be exactly one calculation of each
// indicator. If the decision reads a different MACD from the one an operator
// sees on the chart, both are wrong and neither can be trusted.

// syntheticCandles builds a window whose price falls then rises, so the MACD
// histogram crosses sign inside the carried window and the derived cross state
// has a definite answer.
func syntheticCandles(n int) []kline.Candle {
	candles := make([]kline.Candle, n)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range candles {
		var price float64
		if i < n/2 {
			// Fall from 200 towards 100.
			price = 200 - float64(i)*(100/float64(n/2))
		} else {
			// Rise from 100 towards 260.
			price = 100 + float64(i-n/2)*(160/float64(n/2))
		}
		candles[i] = kline.Candle{
			Time:             start.Add(time.Duration(i) * time.Hour),
			Open:             price - 0.5,
			High:             price + 1,
			Low:              price - 1,
			Close:            price,
			Volume:           10,
			QuoteVolume:      price * 10,
			ValidationIssues: "",
		}
	}
	return candles
}

func syntheticItem(n int) *kline.Item {
	return &kline.Item{
		Exchange: "Binance",
		Pair:     currency.NewBTCUSDT(),
		Asset:    asset.USDTMarginedFutures,
		Interval: kline.OneHour,
		Candles:  syntheticCandles(n),
	}
}

func TestIndicatorAssemblyAgreesWithThePlatformsOwnIndicators(t *testing.T) {
	t.Parallel()

	const window = 250
	item := syntheticItem(window)

	// The platform's own implementation. These are the same calls the RPC
	// endpoint makes, so the numbers below are what an operator sees.
	macd, err := item.GetMovingAverageConvergenceDivergenceOnClose(12, 26, 9)
	require.NoError(t, err, "the platform must compute MACD for this window")
	sma20, err := item.GetSimpleMovingAverageOnClose(20)
	require.NoError(t, err, "the platform must compute a 20-period SMA")
	ema26, err := item.GetExponentialMovingAverageOnClose(26)
	require.NoError(t, err, "the platform must compute a 26-period EMA")

	closes := make([]float64, len(item.Candles))
	for i, candle := range item.Candles {
		closes[i] = candle.Close
	}

	set, err := snapshot.BuildIndicatorSet(&snapshot.IndicatorInput{
		Params: snapshot.IndicatorParams{
			MACDFast: 12, MACDSlow: 26, MACDSignal: 9,
			SMAPeriods: []int64{20}, EMAPeriods: []int64{26},
		},
		Closes: closes,
		MACD: &snapshot.MACDSeries{
			DIF: macd.Results, DEA: macd.SignalVals, Histogram: macd.Histogram,
		},
		MovingAverages: []snapshot.MASeries{
			{Kind: snapshot.MASimple, Period: 20, Values: sma20},
			{Kind: snapshot.MAExponential, Period: 26, Values: ema26},
		},
		ComputedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err, "the assembler must accept the platform's own output")

	// The carried series must be the tail of the platform's series, rendered
	// with the same convention — not a recalculated or rounded copy.
	require.NotNil(t, set.MACD, "MACD must be assembled")
	require.Len(t, set.MACD.Histogram, snapshot.MaxIndicatorPoints, "the histogram must be capped")

	tail := macd.Histogram[len(macd.Histogram)-snapshot.MaxIndicatorPoints:]
	for i, want := range tail {
		text, convErr := snapshot.DecimalFromFloat(want)
		require.NoError(t, convErr, "the platform's value must render")
		assert.Equal(t, text, set.MACD.Histogram[i],
			"carried histogram point %d must be the platform's own value", i)
	}

	// The derived cross state must agree with a sign change read straight off
	// the platform's histogram — over the *carried window*, not the whole
	// series. That scoping is the point: the model can only see the window, so
	// a cross deeper in history must not be reported as the current state.
	//
	// crossOf duplicates the definition rather than calling the package helper,
	// so this asserts the behaviour instead of restating the implementation.
	crossOf := func(histogram []float64) string {
		for i := len(histogram) - 1; i > 0; i-- {
			previous, current := histogram[i-1], histogram[i]
			if previous <= 0 && current > 0 {
				return snapshot.CrossStateGolden
			}
			if previous >= 0 && current < 0 {
				return snapshot.CrossStateDeath
			}
		}
		return snapshot.CrossStateNone
	}

	carried := macd.Histogram[len(macd.Histogram)-snapshot.MaxIndicatorPoints:]
	assert.Equal(t, crossOf(carried), set.MACD.CrossState,
		"the derived cross must match the sign change inside the carried window")

	// The fixture is chosen so the two answers differ, which is what makes the
	// scoping decision observable rather than incidental.
	assert.NotEqual(t, crossOf(macd.Histogram), set.MACD.CrossState,
		"the fixture must cross outside the carried window, or this test cannot see the difference")
	assert.Equal(t, snapshot.CrossStateNone, set.MACD.CrossState,
		"a cross the model cannot see must not be reported as the current state")

	// The newest moving average value must be the platform's newest value.
	var sawSMA, sawEMA bool
	for _, average := range set.MovingAverages {
		switch average.Type {
		case snapshot.MASimple:
			sawSMA = true
			assert.Equal(t, int64(20), average.Period, "the SMA period should be recorded")
			if len(sma20) > 0 {
				text, convErr := snapshot.DecimalFromFloat(sma20[len(sma20)-1])
				require.NoError(t, convErr, "the platform's SMA must render")
				assert.Equal(t, text, average.Latest, "the SMA latest should be the platform's newest value")
			}
		case snapshot.MAExponential:
			sawEMA = true
			assert.Equal(t, int64(26), average.Period, "the EMA period should be recorded")
			if len(ema26) > 0 {
				text, convErr := snapshot.DecimalFromFloat(ema26[len(ema26)-1])
				require.NoError(t, convErr, "the platform's EMA must render")
				assert.Equal(t, text, average.Latest, "the EMA latest should be the platform's newest value")
			}
		}
	}
	assert.True(t, sawSMA, "the SMA should be assembled")
	assert.True(t, sawEMA, "the EMA should be assembled")

	// The position fact must agree with a plain comparison of the newest close
	// against the newest average, which is the definition the contract states.
	newestClose := closes[len(closes)-1]
	wantPosition := snapshot.PriceBelow
	if newestClose > sma20[len(sma20)-1] {
		wantPosition = snapshot.PriceAbove
	}
	for _, average := range set.MovingAverages {
		if average.Type != snapshot.MASimple {
			continue
		}
		assert.Equal(t, wantPosition, average.PriceVsMA,
			"the position fact must agree with a direct comparison of close against average")
	}
}

func TestAssemblerRejectsAWindowThePlatformWouldAlsoReject(t *testing.T) {
	t.Parallel()

	// The assembler's minimum must not be looser than the platform's, or a
	// window would pass assembly and then fail inside the engine's call.
	const tooShort = 30 // below slow + signal - 2 = 33
	item := syntheticItem(tooShort)
	_, err := item.GetMovingAverageConvergenceDivergenceOnClose(12, 26, 9)
	require.Error(t, err, "the platform must refuse a window this short")

	closes := make([]float64, tooShort)
	for i := range closes {
		closes[i] = item.Candles[i].Close
	}
	_, err = snapshot.BuildIndicatorSet(&snapshot.IndicatorInput{
		Params:     snapshot.IndicatorParams{MACDFast: 12, MACDSlow: 26, MACDSignal: 9},
		Closes:     closes,
		MACD:       &snapshot.MACDSeries{DIF: make([]float64, 5), DEA: make([]float64, 5), Histogram: make([]float64, 5)},
		ComputedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	})
	require.ErrorIs(t, err, snapshot.ErrNotEnoughCandles,
		"the assembler must refuse the same window, and say so before the engine is called")

	assert.Equal(t, int64(33), snapshot.MinCandlesForMACD(12, 26, 9),
		"the documented minimum is slow + signal - 2, which the platform enforces")
}

func TestAssemblerRefusesNonFiniteInputFromThePlatform(t *testing.T) {
	t.Parallel()

	// A short window can make a dividing indicator produce a NaN in the middle
	// of a series. The assembler must refuse it rather than let it reach a
	// decimal constructor that panics.
	closes := make([]float64, 250)
	histogram := make([]float64, 250)
	for i := range closes {
		closes[i] = float64(i + 1)
		histogram[i] = math.NaN()
	}
	histogram[len(histogram)-1] = 1

	_, err := snapshot.BuildIndicatorSet(&snapshot.IndicatorInput{
		Params:     snapshot.IndicatorParams{MACDFast: 12, MACDSlow: 26, MACDSignal: 9},
		Closes:     closes,
		MACD:       &snapshot.MACDSeries{DIF: histogram, DEA: histogram, Histogram: histogram},
		ComputedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	})
	require.ErrorIs(t, err, snapshot.ErrInvalidDecimal, "a NaN must be refused, not converted")
}
