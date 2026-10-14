package snapshot

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Indicator assembly.
//
// What this file does and does not do matters, because there is already an
// indicator implementation in the repository (exchanges/kline/technical_analysis.go)
// and a second one would eventually disagree with the first.
//
// It does NOT compute moving averages or MACD. The engine's implementation does
// that, and the webUI shows those same numbers, so the decision and the screen
// must be reading one calculation rather than two.
//
// It DOES everything around that calculation: enforce the minimum number of
// candles each indicator needs, convert the float64 series into the decimal
// strings the contract requires, bound every series to the model's cap, derive
// the facts the contract carries (cross state, slope, trend), record the
// parameters so a decision can be replayed, and refuse to hand back an
// IndicatorSet that fails its own validation.
//
// The split also follows the package boundary: llm/ may not import
// exchanges/kline (see llm/boundary_test.go), so the type that carries the
// results has to be plain data. It is: nothing here knows what a candle item is.

// Sentinel errors for indicator assembly.
var (
	// ErrNotEnoughCandles marks a window too short for the requested indicator.
	// Returning a shorter series would silently move the indicator's meaning.
	ErrNotEnoughCandles = errors.New("not enough candles for the requested indicator")
	// ErrIndicatorParamsInvalid marks a parameter set that cannot produce anything.
	ErrIndicatorParamsInvalid = errors.New("indicator parameters are invalid")
	// ErrIndicatorSeriesMissing marks a parameter set that asks for an indicator
	// whose series was not supplied.
	ErrIndicatorSeriesMissing = errors.New("indicator series was not supplied")
)

// MaxRenderedFractionDigits is how many fraction digits a value this package
// computed may keep when it is written into the contract.
//
// A float64's shortest plain rendering can need more fraction digits than the
// contract's string rule allows: an indicator of 0.0035044457052606504 carries
// nineteen, and the rule bounds it at eighteen. Rounding at a documented
// precision is the honest resolution — an indicator is evidence, not money, and
// twelve fraction digits is far more resolution than any price in scope has.
//
// The alternative, widening the contract's decimal rule, would loosen the bound
// that exists to keep money from ever passing through a float.
const MaxRenderedFractionDigits = 12

// Indicator parameter limits. They are wide enough for any conventional
// setting and narrow enough that a typo cannot ask for a thousand-period
// average from a two-hundred-candle window.
const (
	// MinIndicatorPeriod is the shortest period accepted.
	MinIndicatorPeriod = 2
	// MaxIndicatorPeriod is the longest period accepted. It is bounded by the
	// candle window the coverage plan fetches (klineFullLimit = 250), because a
	// period longer than the window can never be satisfied.
	MaxIndicatorPeriod = 250
	// MaxMovingAverages bounds how many averages one snapshot may carry.
	MaxMovingAverages = 8
)

// DefaultIndicatorParams is the parameter set from
// docs/llm-dev/04-数据层-K线与指标.md §2.2.
//
// The MACD default is the textbook 12/26/9 because it is the one most
// represented in the training material the models saw, which makes it the
// setting they are least likely to misread. RSI and Bollinger are on by default
// (Q16); ATR is carried because it is the reference for stop distance.
func DefaultIndicatorParams() IndicatorParams {
	return IndicatorParams{
		MACDFast:        12,
		MACDSlow:        26,
		MACDSignal:      9,
		SMAPeriods:      []int64{20, 50, 200},
		EMAPeriods:      []int64{12, 26, 50, 200},
		RSIPeriod:       14,
		ATRPeriod:       14,
		BollingerPeriod: 20,
	}
}

// MinCandlesForMACD is the shortest window the MACD calculation accepts.
//
// The engine's implementation documents `len(data) >= slow + signal - 2`, and
// asking for fewer returns its own error. Knowing the figure here means the
// assembler can refuse before the call, with a message that names the shortfall.
func MinCandlesForMACD(_, slow, signal int64) int64 {
	return slow + signal - 2
}

// Validate checks the parameter set.
func (p *IndicatorParams) Validate() error {
	pairs := []struct {
		name   string
		period int64
	}{
		{"macdFast", p.MACDFast},
		{"macdSlow", p.MACDSlow},
		{"macdSignal", p.MACDSignal},
		{"rsiPeriod", p.RSIPeriod},
		{"atrPeriod", p.ATRPeriod},
		{"bollingerPeriod", p.BollingerPeriod},
	}
	for _, pair := range pairs {
		if pair.period == 0 {
			continue
		}
		if pair.period < MinIndicatorPeriod || pair.period > MaxIndicatorPeriod {
			return fmt.Errorf("%w: %s = %d must be %d..%d",
				ErrIndicatorParamsInvalid, pair.name, pair.period, MinIndicatorPeriod, MaxIndicatorPeriod)
		}
	}
	if p.MACDFast == 0 || p.MACDSlow == 0 || p.MACDSignal == 0 {
		return fmt.Errorf("%w: MACD needs all three periods, got fast=%d slow=%d signal=%d",
			ErrIndicatorParamsInvalid, p.MACDFast, p.MACDSlow, p.MACDSignal)
	}
	if p.MACDFast >= p.MACDSlow {
		return fmt.Errorf("%w: macdFast %d must be below macdSlow %d",
			ErrIndicatorParamsInvalid, p.MACDFast, p.MACDSlow)
	}
	for _, period := range slices.Concat(p.SMAPeriods, p.EMAPeriods) {
		if period < MinIndicatorPeriod || period > MaxIndicatorPeriod {
			return fmt.Errorf("%w: moving average period %d must be %d..%d",
				ErrIndicatorParamsInvalid, period, MinIndicatorPeriod, MaxIndicatorPeriod)
		}
	}
	if len(p.SMAPeriods)+len(p.EMAPeriods) > MaxMovingAverages {
		return fmt.Errorf("%w: %d moving averages over the cap of %d",
			ErrIndicatorParamsInvalid, len(p.SMAPeriods)+len(p.EMAPeriods), MaxMovingAverages)
	}
	return nil
}

// LongestPeriod returns the longest period the parameter set asks for, which is
// the number of candles the window must supply.
func (p *IndicatorParams) LongestPeriod() int64 {
	longest := int64(0)
	for _, period := range slices.Concat(p.SMAPeriods, p.EMAPeriods) {
		longest = max(longest, period)
	}
	return max(longest, MinCandlesForMACD(p.MACDFast, p.MACDSlow, p.MACDSignal))
}

// MASeries is one moving average's computed output.
//
// Kind and Period are named together because the same period means different
// things for the two kinds, and a caller that supplied values without saying
// which kind produced them would leave the snapshot unable to say what it read.
type MASeries struct {
	// Kind is MASimple or MAExponential.
	Kind string
	// Period is the window length.
	Period int64
	// Values are the computed averages, oldest first, aligned with the closes.
	Values []float64
}

// MACDSeries is the MACD output.
type MACDSeries struct {
	// DIF is the fast-minus-slow line, oldest first.
	DIF []float64
	// DEA is the signal line, oldest first.
	DEA []float64
	// Histogram is the bar, oldest first.
	Histogram []float64
}

// SeriesInput is one simple series with its period.
type SeriesInput struct {
	// Period is the window length.
	Period int64
	// Values are the computed values, oldest first.
	Values []float64
}

// BollingerInput is the band output.
type BollingerInput struct {
	// Period is the window length.
	Period int64
	// StandardDeviationUp and Down are the multipliers actually used, recorded
	// because a band drawn at two deviations and one drawn at three look alike
	// in a chart and not at all alike in a decision.
	StandardDeviationUp   float64
	StandardDeviationDown float64
	// Upper, Middle and Lower are the computed bands, oldest first.
	Upper  []float64
	Middle []float64
	Lower  []float64
}

// IndicatorInput is everything the assembler needs.
type IndicatorInput struct {
	// Params is the parameter set the series were computed with.
	Params IndicatorParams
	// Closes are the candle closes the indicators were computed from, oldest
	// first. They are needed for the position and slope facts.
	Closes []float64
	// MovingAverages are the computed averages.
	MovingAverages []MASeries
	// MACD is the computed MACD, required whenever the params specify one.
	MACD *MACDSeries
	// RSI and ATR are the optional series.
	RSI *SeriesInput
	ATR *SeriesInput
	// Bollinger is the optional band.
	Bollinger *BollingerInput
	// ComputedAt is when the computation ran.
	ComputedAt time.Time
}

// BuildIndicatorSet assembles and validates an IndicatorSet.
//
// Every series is trimmed to the cap the contract allows before the facts are
// derived from it, so the reading and the visible data describe the same
// points. Deriving from a longer series than the one carried would produce a
// cross state the model cannot see evidence for.
func BuildIndicatorSet(in *IndicatorInput) (*IndicatorSet, error) {
	if err := in.Params.Validate(); err != nil {
		return nil, err
	}
	if len(in.Closes) == 0 {
		return nil, fmt.Errorf("%w: closes", ErrNotEnoughCandles)
	}
	needed := in.Params.LongestPeriod()
	if int64(len(in.Closes)) < needed {
		return nil, fmt.Errorf("%w: %d closes supplied, %d needed for the longest period",
			ErrNotEnoughCandles, len(in.Closes), needed)
	}
	if in.ComputedAt.IsZero() {
		return nil, fmt.Errorf("%w: computation time", ErrSnapshotIncomplete)
	}

	set := IndicatorSet{ComputedAt: in.ComputedAt, Params: in.Params}

	macd, err := buildMACD(in)
	if err != nil {
		return nil, err
	}
	set.MACD = macd

	averages, err := buildMovingAverages(in)
	if err != nil {
		return nil, err
	}
	set.MovingAverages = averages

	if in.RSI != nil {
		series, seriesErr := buildSeriesFact("rsi", in.RSI)
		if seriesErr != nil {
			return nil, seriesErr
		}
		set.RSI = series
	}
	if in.ATR != nil {
		series, seriesErr := buildSeriesFact("atr", in.ATR)
		if seriesErr != nil {
			return nil, seriesErr
		}
		set.ATR = series
	}
	if in.Bollinger != nil {
		bands, bandErr := buildBollinger(in.Bollinger)
		if bandErr != nil {
			return nil, bandErr
		}
		set.Bollinger = bands
	}

	// The assembled set must pass the same validation a stored one would, so a
	// defect shows up here rather than when a decision is being read back.
	if err = set.validate(); err != nil {
		return nil, fmt.Errorf("assembled indicator set is invalid: %w", err)
	}
	return &set, nil
}

func buildMACD(in *IndicatorInput) (*MACDFact, error) {
	if in.MACD == nil {
		return nil, fmt.Errorf("%w: MACD", ErrIndicatorSeriesMissing)
	}
	dif, dea, histogram := in.MACD.DIF, in.MACD.DEA, in.MACD.Histogram
	if len(dif) != len(dea) || len(dif) != len(histogram) {
		return nil, fmt.Errorf("%w: dif %d, dea %d, histogram %d",
			ErrSeriesMismatch, len(dif), len(dea), len(histogram))
	}
	if len(histogram) < 2 {
		return nil, fmt.Errorf("%w: MACD needs at least two points, got %d", ErrNotEnoughCandles, len(histogram))
	}

	difTail := lastFloats(dif, MaxIndicatorPoints)
	deaTail := lastFloats(dea, MaxIndicatorPoints)
	histTail := lastFloats(histogram, MaxIndicatorPoints)

	histStrings, err := decimalsOf(histTail)
	if err != nil {
		return nil, fmt.Errorf("macd histogram: %w", err)
	}
	crossState, barsSinceCross, trend, err := DeriveMACDState(histStrings)
	if err != nil {
		return nil, fmt.Errorf("deriving the MACD state: %w", err)
	}
	difStrings, err := decimalsOf(difTail)
	if err != nil {
		return nil, fmt.Errorf("macd dif: %w", err)
	}
	deaStrings, err := decimalsOf(deaTail)
	if err != nil {
		return nil, fmt.Errorf("macd dea: %w", err)
	}

	return &MACDFact{
		Fast:           in.Params.MACDFast,
		Slow:           in.Params.MACDSlow,
		Signal:         in.Params.MACDSignal,
		DIF:            difStrings,
		DEA:            deaStrings,
		Histogram:      histStrings,
		CrossState:     crossState,
		BarsSinceCross: barsSinceCross,
		HistogramTrend: trend,
	}, nil
}

func buildMovingAverages(in *IndicatorInput) ([]MovingAverageFact, error) {
	if len(in.MovingAverages) == 0 {
		return nil, nil
	}
	closesTail := lastFloats(in.Closes, MaxIndicatorPoints)

	// Deterministic order: a snapshot's JSON must be identical between two runs
	// over the same inputs, or its hash cannot be compared with a recorded one.
	ordered := slices.Clone(in.MovingAverages)
	slices.SortFunc(ordered, func(a, b MASeries) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return int(a.Period - b.Period)
	})

	out := make([]MovingAverageFact, 0, len(ordered))
	for _, series := range ordered {
		if series.Kind != MASimple && series.Kind != MAExponential {
			return nil, fmt.Errorf("%w: moving average kind %q", ErrInvalidDerived, series.Kind)
		}
		if series.Period < MinIndicatorPeriod || series.Period > MaxIndicatorPeriod {
			return nil, fmt.Errorf("%w: moving average period %d", ErrIndicatorParamsInvalid, series.Period)
		}
		if int64(len(series.Values)) < series.Period {
			return nil, fmt.Errorf("%w: %s(%d) received %d values",
				ErrNotEnoughCandles, series.Kind, series.Period, len(series.Values))
		}
		tail := lastFloats(series.Values, MaxIndicatorPoints)
		valueStrings, err := decimalsOf(tail)
		if err != nil {
			return nil, fmt.Errorf("%s(%d): %w", series.Kind, series.Period, err)
		}
		// The closes are aligned to the same tail length, so the position fact
		// compares the newest close with the newest average.
		alignedCloses := lastFloats(closesTail, len(tail))
		closeStrings, err := decimalsOf(alignedCloses)
		if err != nil {
			return nil, fmt.Errorf("%s(%d) closes: %w", series.Kind, series.Period, err)
		}
		position, slope, err := DeriveMAState(closeStrings, valueStrings)
		if err != nil {
			return nil, fmt.Errorf("deriving %s(%d): %w", series.Kind, series.Period, err)
		}
		latest := ""
		if len(valueStrings) > 0 {
			latest = valueStrings[len(valueStrings)-1]
		}
		out = append(out, MovingAverageFact{
			Type:      series.Kind,
			Period:    series.Period,
			Values:    valueStrings,
			Latest:    latest,
			PriceVsMA: position,
			Slope:     slope,
		})
	}
	return out, nil
}

func buildSeriesFact(name string, in *SeriesInput) (*SeriesFact, error) {
	if in.Period < MinIndicatorPeriod || in.Period > MaxIndicatorPeriod {
		return nil, fmt.Errorf("%w: %s period %d", ErrIndicatorParamsInvalid, name, in.Period)
	}
	// A single value is enough to report a level even when the period is long,
	// because the level comes from the provider; only the absence of any value
	// makes the fact meaningless.
	if len(in.Values) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotEnoughCandles, name)
	}
	tail := lastFloats(in.Values, MaxIndicatorPoints)
	valueStrings, err := decimalsOf(tail)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	latest := valueStrings[len(valueStrings)-1]
	return &SeriesFact{Period: in.Period, Values: valueStrings, Latest: latest}, nil
}

func buildBollinger(in *BollingerInput) (*BollingerFact, error) {
	if in.Period < MinIndicatorPeriod || in.Period > MaxIndicatorPeriod {
		return nil, fmt.Errorf("%w: bollinger period %d", ErrIndicatorParamsInvalid, in.Period)
	}
	if len(in.Middle) == 0 {
		return nil, fmt.Errorf("%w: bollinger middle band", ErrNotEnoughCandles)
	}
	upper, err := decimalsOf(lastFloats(in.Upper, MaxIndicatorPoints))
	if err != nil {
		return nil, fmt.Errorf("bollinger upper: %w", err)
	}
	middle, err := decimalsOf(lastFloats(in.Middle, MaxIndicatorPoints))
	if err != nil {
		return nil, fmt.Errorf("bollinger middle: %w", err)
	}
	lower, err := decimalsOf(lastFloats(in.Lower, MaxIndicatorPoints))
	if err != nil {
		return nil, fmt.Errorf("bollinger lower: %w", err)
	}
	return &BollingerFact{
		Period:                in.Period,
		StandardDeviationUp:   in.StandardDeviationUp,
		StandardDeviationDown: in.StandardDeviationDown,
		Upper:                 upper,
		Middle:                middle,
		Lower:                 lower,
		LatestUpper:           latestOf(upper),
		LatestMiddle:          latestOf(middle),
		LatestLower:           latestOf(lower),
	}, nil
}

// decimalsOf renders a float series as the contract's plain decimal strings.
func decimalsOf(values []float64) ([]string, error) {
	out := make([]string, 0, len(values))
	for i, value := range values {
		text, err := renderComputed(value)
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		out = append(out, text)
	}
	return out, nil
}

// renderComputed turns a computed float into a plain decimal string.
//
// It rounds to MaxRenderedFractionDigits and drops trailing zeros, so a value
// has exactly one spelling — which is what lets a stored snapshot's hash be
// compared with a recomputed one. It refuses a non-finite value rather than
// letting it reach a constructor that would panic.
func renderComputed(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("%w: non-finite value", ErrInvalidDecimal)
	}
	text := strconv.FormatFloat(value, 'f', MaxRenderedFractionDigits, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	if text == "" || text == "-" {
		text = "0"
	}
	if err := ValidateSignedDecimal(text); err != nil {
		return "", err
	}
	return text, nil
}

func latestOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func lastFloats(values []float64, n int) []float64 {
	if n <= 0 || len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

// DecimalFromFloat renders one computed value as a plain decimal string. It is
// exported so a caller outside this package can render a value the same way the
// snapshot will, rather than inventing a second convention.
func DecimalFromFloat(value float64) (string, error) {
	return renderComputed(value)
}

// ParseFloatValues converts decimal strings back into floats, for a caller that
// has stored series and needs to feed them to the engine's indicators.
func ParseFloatValues(values []string) ([]float64, error) {
	out := make([]float64, 0, len(values))
	for i, value := range values {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: value %d: %q", ErrInvalidDecimal, i, value)
		}
		out = append(out, parsed)
	}
	return out, nil
}
