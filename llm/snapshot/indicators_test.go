package snapshot

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rampCloses returns a rising close series long enough for the default
// parameters: 1, 2, 3, … The exact numbers do not matter to the assembly
// tests, only their length and their monotonicity.
func rampCloses(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}

// maOf builds a moving average series that tracks the closes with a constant
// offset, so the position fact has a known answer.
func maOf(period int64, closes []float64, offset float64) MASeries {
	values := make([]float64, len(closes))
	for i := range closes {
		start := max(i-int(period)+1, 0)
		var sum float64
		for _, v := range closes[start : i+1] {
			sum += v
		}
		values[i] = sum/float64(i-start+1) + offset
	}
	return MASeries{Kind: MASimple, Period: period, Values: values}
}

// flatMACD returns a MACD series whose histogram is constant, so its derived
// state is known: no cross, flat trend.
func flatMACD(n int, level float64) *MACDSeries {
	series := make([]float64, n)
	for i := range series {
		series[i] = level
	}
	return &MACDSeries{DIF: series, DEA: series, Histogram: series}
}

func validInput(t *testing.T) IndicatorInput {
	t.Helper()
	closes := rampCloses(210)
	return IndicatorInput{
		Params:         DefaultIndicatorParams(),
		Closes:         closes,
		MACD:           flatMACD(210, 0.5),
		MovingAverages: []MASeries{maOf(20, closes, 0), maOf(50, closes, 0), maOf(200, closes, 0)},
		RSI:            &SeriesInput{Period: 14, Values: make([]float64, 200)},
		ATR:            &SeriesInput{Period: 14, Values: make([]float64, 200)},
		Bollinger: &BollingerInput{
			Period: 20, StandardDeviationUp: 2, StandardDeviationDown: 2,
			Upper: make([]float64, 210), Middle: make([]float64, 210), Lower: make([]float64, 210),
		},
		ComputedAt: testTime(),
	}
}

func TestDefaultIndicatorParamsAreTheDocumentedSet(t *testing.T) {
	t.Parallel()

	p := DefaultIndicatorParams()
	assert.Equal(t, int64(12), p.MACDFast, "the textbook fast period is the one models misread least")
	assert.Equal(t, int64(26), p.MACDSlow, "the textbook slow period")
	assert.Equal(t, int64(9), p.MACDSignal, "the textbook signal period")
	assert.Equal(t, []int64{20, 50, 200}, p.SMAPeriods, "short, medium and long trend")
	assert.Equal(t, []int64{12, 26, 50, 200}, p.EMAPeriods, "aligned with the MACD fast and slow lines plus a long trend")
	assert.Equal(t, int64(14), p.RSIPeriod, "the familiar RSI period")
	assert.Equal(t, int64(14), p.ATRPeriod, "the reference for stop distance")
	assert.Equal(t, int64(20), p.BollingerPeriod, "the familiar band period")
	require.NoError(t, p.Validate(), "the default set must be valid")

	// The longest period decides how many candles the window must supply, and
	// the coverage plan fetches 250, so the defaults must fit inside that.
	assert.Equal(t, int64(200), p.LongestPeriod(), "the 200-period average sets the window requirement")
	assert.LessOrEqual(t, p.LongestPeriod(), int64(250), "the defaults should fit the fetched window")
}

func TestBuildIndicatorSetAssemblesEverything(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "a complete input must assemble")

	require.NotNil(t, set.MACD, "MACD must be present")
	assert.Equal(t, int64(12), set.MACD.Fast, "the parameters should be copied onto the fact")
	assert.Equal(t, int64(26), set.MACD.Slow, "the parameters should be copied onto the fact")
	assert.Equal(t, int64(9), set.MACD.Signal, "the parameters should be copied onto the fact")
	assert.Equal(t, CrossStateNone, set.MACD.CrossState, "a flat histogram has no cross")
	assert.Equal(t, TrendFlat, set.MACD.HistogramTrend, "a flat histogram is not expanding")

	assert.Len(t, set.MovingAverages, 3, "every supplied average should be reported")
	assert.NotNil(t, set.RSI, "RSI should be carried when supplied")
	assert.NotNil(t, set.ATR, "ATR should be carried when supplied")
	assert.NotNil(t, set.Bollinger, "the band should be carried when supplied")
	assert.Equal(t, testTime(), set.ComputedAt, "the computation time should be recorded")
	assert.Equal(t, in.Params, set.Params, "the parameters should be recorded so a decision can be replayed")

	require.NoError(t, set.validate(), "the assembled set must pass the same validation a stored one would")
}

func TestOptionalIndicatorsMayBeAbsent(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.RSI, in.ATR, in.Bollinger = nil, nil, nil
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "MACD and the averages are enough to assemble")
	assert.Nil(t, set.RSI, "an unsupplied RSI should stay absent, not be invented")
	assert.Nil(t, set.ATR, "an unsupplied ATR should stay absent")
	assert.Nil(t, set.Bollinger, "an unsupplied band should stay absent")
}

func TestSeriesAreTrimmedToTheCap(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")

	require.Len(t, set.MACD.Histogram, MaxIndicatorPoints, "the histogram must be trimmed to the cap")
	require.Len(t, set.MACD.DIF, MaxIndicatorPoints, "DIF must be trimmed to the cap")
	require.Len(t, set.MACD.DEA, MaxIndicatorPoints, "DEA must be trimmed to the cap")
	for _, average := range set.MovingAverages {
		assert.Lenf(t, average.Values, MaxIndicatorPoints, "%s(%d) should be trimmed to the cap", average.Type, average.Period)
	}
	assert.Len(t, set.RSI.Values, MaxIndicatorPoints, "RSI should be trimmed")
	assert.Len(t, set.Bollinger.Middle, MaxIndicatorPoints, "the band should be trimmed")

	// The newest point must survive the trim, or the reading would describe a
	// bar the model cannot see.
	assert.Equal(t, "0.5", set.MACD.Histogram[len(set.MACD.Histogram)-1], "the newest histogram point should be kept")
	// Latest is the average's own newest point, which on a rising series trails
	// the newest close — it is the 20-period mean, not the price.
	for _, average := range set.MovingAverages {
		assert.Equal(t, average.Values[len(average.Values)-1], average.Latest,
			"Latest must be the newest carried average value")
	}
}

func TestAShortSeriesIsKeptWhole(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.MACD = flatMACD(5, 0.25)
	in.Closes = rampCloses(210)
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "a short but non-empty series must assemble")
	assert.Len(t, set.MACD.Histogram, 5, "a series shorter than the cap should not be padded")
}

func TestDerivationUsesTheTrimmedSeries(t *testing.T) {
	t.Parallel()

	// The cross happened 30 points back, outside the cap. Because the reading
	// and the visible data must describe the same points, the derived state has
	// to be computed from the trimmed series — which cannot see it.
	histogram := make([]float64, 210)
	for i := range histogram {
		histogram[i] = -1
	}
	for i := 180; i < len(histogram); i++ {
		histogram[i] = 1
	}

	in := validInput(t)
	in.MACD = &MACDSeries{
		DIF:       make([]float64, len(histogram)),
		DEA:       make([]float64, len(histogram)),
		Histogram: histogram,
	}
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")

	assert.Equal(t, CrossStateNone, set.MACD.CrossState,
		"a cross outside the carried window must not be reported: the model cannot see its evidence")
	assert.Less(t, set.MACD.BarsSinceCross, MaxIndicatorPoints,
		"any reported bar count must be inside the carried window")
}

func TestDerivationSeesACrossInsideTheWindow(t *testing.T) {
	t.Parallel()

	histogram := make([]float64, 210)
	for i := range histogram {
		histogram[i] = -1
	}
	histogram[len(histogram)-3] = 1
	histogram[len(histogram)-2] = 2
	histogram[len(histogram)-1] = 3

	in := validInput(t)
	in.MACD = &MACDSeries{
		DIF:       make([]float64, len(histogram)),
		DEA:       make([]float64, len(histogram)),
		Histogram: histogram,
	}
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")
	assert.Equal(t, CrossStateGolden, set.MACD.CrossState, "the newest sign change should be reported")
	assert.Equal(t, 2, set.MACD.BarsSinceCross, "two bars have passed since the cross")
	assert.Equal(t, TrendExpanding, set.MACD.HistogramTrend, "the magnitude grew")
}

func TestMovingAverageOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	// Two runs over the same data must produce the same JSON, or a snapshot's
	// hash cannot be compared with a recorded one.
	in := validInput(t)
	in.MovingAverages = []MASeries{
		maOf(200, in.Closes, 0), maOf(20, in.Closes, 0), maOf(50, in.Closes, 0),
	}
	first, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the first run must assemble")

	reversed := validInput(t)
	reversed.MovingAverages = []MASeries{
		maOf(50, reversed.Closes, 0), maOf(200, reversed.Closes, 0), maOf(20, reversed.Closes, 0),
	}
	second, err := BuildIndicatorSet(&reversed)
	require.NoError(t, err, "the second run must assemble")

	require.Len(t, first.MovingAverages, 3, "all three must be present")
	require.Len(t, second.MovingAverages, 3, "all three must be present")
	for i := range first.MovingAverages {
		assert.Equal(t, first.MovingAverages[i], second.MovingAverages[i],
			"the order must not depend on the order the caller supplied")
	}
	assert.Equal(t, int64(20), first.MovingAverages[0].Period, "periods should be sorted within a kind")
	assert.Equal(t, int64(50), first.MovingAverages[1].Period, "periods should be sorted within a kind")
	assert.Equal(t, int64(200), first.MovingAverages[2].Period, "periods should be sorted within a kind")
}

func TestMovingAverageKindsAreSortedStable(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.MovingAverages = []MASeries{
		{Kind: MASimple, Period: 50, Values: in.Closes},
		{Kind: MAExponential, Period: 50, Values: in.Closes},
	}
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "both kinds must assemble")
	require.Len(t, set.MovingAverages, 2, "both must be present")
	// EMA sorts before SMA, so a reader comparing two snapshots sees the same
	// arrangement either way.
	assert.Equal(t, MAExponential, set.MovingAverages[0].Type, "kinds should be sorted, not left in input order")
	assert.Equal(t, MASimple, set.MovingAverages[1].Type, "kinds should be sorted")
}

func TestPositionAndSlopeFacts(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	// A running mean on a rising series trails the newest close by about half
	// the period, so the offset has to clear that lag before it changes the
	// verdict. Fifty is unambiguous for a 20-period mean on this ramp.
	in.MovingAverages = []MASeries{maOf(20, in.Closes, -50)}
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")
	require.Len(t, set.MovingAverages, 1, "one average was supplied")
	assert.Equal(t, PriceAbove, set.MovingAverages[0].PriceVsMA, "a close above its average should read as above")
	assert.Equal(t, SlopeUp, set.MovingAverages[0].Slope, "a rising average should read as up")

	in.MovingAverages = []MASeries{maOf(20, in.Closes, 50)}
	set, err = BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")
	assert.Equal(t, PriceBelow, set.MovingAverages[0].PriceVsMA, "a close below its average should read as below")
}

func TestBuildRefusesAnUnsatisfiableWindow(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.Closes = rampCloses(100)
	_, err := BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrNotEnoughCandles,
		"a window shorter than the longest period must be refused before the engine is called")
	assert.Contains(t, err.Error(), "200", "the message should name the shortfall, not just say there is one")

	in = validInput(t)
	in.Closes = nil
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrNotEnoughCandles, "no closes cannot produce anything")
}

func TestBuildRefusesInconsistentInput(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.MACD = nil
	_, err := BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrIndicatorSeriesMissing, "a declared MACD with no series must be refused")

	in = validInput(t)
	in.MACD = &MACDSeries{DIF: make([]float64, 210), DEA: make([]float64, 209), Histogram: make([]float64, 210)}
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrSeriesMismatch, "series of unequal length would pair the wrong bars")

	in = validInput(t)
	in.MACD = flatMACD(1, 1)
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrNotEnoughCandles, "one histogram point cannot describe a cross")

	in = validInput(t)
	in.ComputedAt = time.Time{}
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrSnapshotIncomplete, "a set with no computation time cannot be replayed")

	in = validInput(t)
	in.MovingAverages = []MASeries{{Kind: "WMA", Period: 20, Values: in.Closes}}
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrInvalidDerived, "an unsupported average kind must be refused")

	in = validInput(t)
	in.MovingAverages = []MASeries{{Kind: MASimple, Period: 20, Values: rampCloses(5)}}
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrNotEnoughCandles, "fewer values than the period means the average is not an average")
}

func TestParamsValidation(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*IndicatorParams){
		"fast equals slow": func(p *IndicatorParams) { p.MACDFast, p.MACDSlow = 26, 26 },
		"fast above slow":  func(p *IndicatorParams) { p.MACDFast, p.MACDSlow = 30, 26 },
		"missing slow":     func(p *IndicatorParams) { p.MACDSlow = 0 },
		"signal zero":      func(p *IndicatorParams) { p.MACDSignal = 0 },
		"period one":       func(p *IndicatorParams) { p.SMAPeriods = []int64{1} },
		"period over cap":  func(p *IndicatorParams) { p.SMAPeriods = []int64{MaxIndicatorPeriod + 1} },
		"negative period":  func(p *IndicatorParams) { p.EMAPeriods = []int64{-5} },
		"too many averages": func(p *IndicatorParams) {
			p.SMAPeriods = []int64{2, 3, 4, 5, 6}
			p.EMAPeriods = []int64{7, 8, 9, 10}
		},
	} {
		p := DefaultIndicatorParams()
		mutate(&p)
		require.ErrorIsf(t, p.Validate(), ErrIndicatorParamsInvalid, "%s must be refused", name)
	}

	p := DefaultIndicatorParams()
	p.RSIPeriod = 0
	p.ATRPeriod = 0
	p.BollingerPeriod = 0
	require.NoError(t, p.Validate(), "the optional indicators may be switched off")
}

func TestNonFiniteValuesAreRefused(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.MACD = flatMACD(210, 1)
	in.MACD.Histogram[len(in.MACD.Histogram)-1] = math.Inf(1)
	_, err := BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrInvalidDecimal,
		"an infinite value must be refused rather than reaching a panicking decimal constructor")

	in = validInput(t)
	in.MovingAverages = []MASeries{maOf(20, in.Closes, 0)}
	in.MovingAverages[0].Values[len(in.MovingAverages[0].Values)-1] = math.NaN()
	_, err = BuildIndicatorSet(&in)
	require.ErrorIs(t, err, ErrInvalidDecimal, "a NaN must be refused")

	// A non-finite value outside the carried window is trimmed before rendering
	// and so is never converted. That is not a hole: the window is what the
	// model sees and what the facts are derived from, and neither touches it.
	in = validInput(t)
	in.MACD = flatMACD(210, 1)
	in.MACD.Histogram[5] = math.NaN()
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "a value outside the carried window must not fail the assembly")
	assert.Len(t, set.MACD.Histogram, MaxIndicatorPoints, "the window should still be the cap")
}

func TestValuesNeverUseExponentNotation(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	in.MACD = flatMACD(210, 1e-9)
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "a tiny value must still assemble")
	for _, value := range set.MACD.Histogram {
		assert.NotContains(t, value, "e", "a computed value should not be written in exponent form")
		assert.NotContains(t, value, "E", "a computed value should not be written in exponent form")
	}
}

func TestDecimalFromFloatMatchesTheSnapshotConvention(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{0, 1, -1, 0.001, 12345.6789, 1e-9, -2.5e-7} {
		text, err := DecimalFromFloat(value)
		require.NoErrorf(t, err, "%v must render", value)
		assert.NotContainsf(t, text, "e", "%v should render in plain notation", value)
		assert.NoErrorf(t, ValidateSignedDecimal(text), "%v should satisfy the contract's signed rule", value)
	}

	_, err := DecimalFromFloat(math.Inf(-1))
	require.ErrorIs(t, err, ErrInvalidDecimal, "a non-finite value has no decimal form")

	back, err := ParseFloatValues([]string{"1", "-2.5", "0.001"})
	require.NoError(t, err, "stored values must parse back")
	assert.Equal(t, []float64{1, -2.5, 0.001}, back, "the round trip should be lossless for these values")

	_, err = ParseFloatValues([]string{"1", "abc"})
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable value must be refused")
}

func TestAssembledSetSurvivesSnapshotValidation(t *testing.T) {
	t.Parallel()

	in := validInput(t)
	set, err := BuildIndicatorSet(&in)
	require.NoError(t, err, "the fixture must assemble")

	s := testSnapshot(t)
	s.Indicators = *set
	require.NoError(t, s.Validate(),
		"an assembled indicator set must satisfy the snapshot's own validation, or the two disagree about the contract")
}
