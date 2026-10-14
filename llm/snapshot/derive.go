package snapshot

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// Sentinel errors for the derivation helpers.
var (
	// ErrTooFewPoints marks a series too short to derive from. Returning a
	// default would invent a fact, and an invented trend is worse than a gap.
	ErrTooFewPoints = errors.New("not enough points to derive a fact")
	// ErrNonPositivePrice marks a price that cannot be used as a denominator.
	ErrNonPositivePrice = errors.New("price must be positive")
	// ErrUnknownInterval marks an interval without a known duration.
	ErrUnknownInterval = errors.New("interval has no known duration")
)

// DeriveMACDState computes the MACD cross state, how many bars have passed
// since it, and which way the histogram is moving.
//
// Only the histogram is needed: its sign change is the cross, and its magnitude
// change is the trend. The caller passes the trimmed series the model will also
// see, so the derived reading and the visible data cannot disagree.
func DeriveMACDState(histogram []string) (crossState string, barsSinceCross int, trend string, err error) {
	if len(histogram) < 2 {
		return "", 0, "", fmt.Errorf("%w: MACD needs at least two histogram points, got %d",
			ErrTooFewPoints, len(histogram))
	}
	values := make([]float64, len(histogram))
	for i, point := range histogram {
		parsed, parseErr := strconv.ParseFloat(point, 64)
		if parseErr != nil {
			return "", 0, "", fmt.Errorf("%w: histogram[%d]: %q", ErrInvalidDecimal, i, point)
		}
		values[i] = parsed
	}

	// The newest sign change wins, so scan backwards from the end.
	crossState = CrossStateNone
	barsSinceCross = 0
	for i := len(values) - 1; i > 0; i-- {
		previous, current := values[i-1], values[i]
		switch {
		case previous <= 0 && current > 0:
			crossState = CrossStateGolden
		case previous >= 0 && current < 0:
			crossState = CrossStateDeath
		default:
			continue
		}
		// The cross sits between i-1 and i; counting from the newest bar, the
		// number of bars since is len-1-i.
		barsSinceCross = len(values) - 1 - i
		break
	}

	// A flat histogram counts as flat rather than as expanding: two equal
	// readings are not evidence of a growing move.
	prev, last := values[len(values)-2], values[len(values)-1]
	switch diff := math.Abs(last) - math.Abs(prev); {
	case diff > histogramFlatEpsilon:
		trend = TrendExpanding
	case diff < -histogramFlatEpsilon:
		trend = TrendContracting
	default:
		trend = TrendFlat
	}
	return crossState, barsSinceCross, trend, nil
}

// DeriveMAState computes whether the close sits above or below a moving average
// and which way the average is going.
//
// The slope looks back three points, which smooths the one-point jitter that
// would otherwise report a direction change on every bar.
//
// Only the values it reads are checked: the position comes from the newest close
// and average, and the slope from the average three points back. The snapshot's
// own validation covers every point of the series before it reaches here, so
// re-checking the whole series would be duplicate work rather than extra
// safety.
func DeriveMAState(closes, averages []string) (priceVsMA, slope string, err error) {
	if len(averages) == 0 || len(closes) == 0 {
		return "", "", fmt.Errorf("%w: moving average needs a close and an average", ErrTooFewPoints)
	}
	if len(averages) != len(closes) {
		return "", "", fmt.Errorf("%w: %d closes against %d averages", ErrSeriesMismatch, len(closes), len(averages))
	}
	lastClose, err := strconv.ParseFloat(closes[len(closes)-1], 64)
	if err != nil {
		return "", "", fmt.Errorf("%w: close: %q", ErrInvalidDecimal, closes[len(closes)-1])
	}
	lastAverage, err := strconv.ParseFloat(averages[len(averages)-1], 64)
	if err != nil {
		return "", "", fmt.Errorf("%w: average: %q", ErrInvalidDecimal, averages[len(averages)-1])
	}
	if lastClose > lastAverage {
		priceVsMA = PriceAbove
	} else {
		priceVsMA = PriceBelow
	}

	if len(averages) < 4 {
		// Too short to look back three points. The slope is reported as flat
		// rather than guessed, and the caller still gets the position fact.
		return priceVsMA, SlopeFlat, nil
	}
	reference, err := strconv.ParseFloat(averages[len(averages)-4], 64)
	if err != nil {
		return "", "", fmt.Errorf("%w: average lookback: %q", ErrInvalidDecimal, averages[len(averages)-4])
	}
	// The threshold scales with price so that a one-cent drift on a fifty
	// thousand dollar contract does not read as a direction.
	threshold := math.Abs(reference) * slopeFlatFraction
	switch delta := lastAverage - reference; {
	case delta > threshold:
		slope = SlopeUp
	case delta < -threshold:
		slope = SlopeDown
	default:
		slope = SlopeFlat
	}
	return priceVsMA, slope, nil
}

// DeriveSpreadBPS computes the spread between the best bid and ask in basis
// points of the midpoint.
//
// It runs in decimal, like the midpoint it divides by. Doing it in float would
// put the two derived numbers on different arithmetic, and (100, 100.1) is
// already enough to show the float result drifting in its twelfth digit.
func DeriveSpreadBPS(bid, ask string) (decimal.Decimal, error) {
	bidValue, err := decimal.NewFromString(bid)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: bid: %q", ErrInvalidDecimal, bid)
	}
	askValue, err := decimal.NewFromString(ask)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: ask: %q", ErrInvalidDecimal, ask)
	}
	if !bidValue.IsPositive() || !askValue.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("%w: bid %s, ask %s", ErrNonPositivePrice, bid, ask)
	}
	if askValue.LessThan(bidValue) {
		return decimal.Decimal{}, fmt.Errorf("%w: bid %s, ask %s", ErrOrderBookCrossed, bid, ask)
	}
	midpoint := bidValue.Add(askValue).Div(decimal.NewFromInt(2))
	return askValue.Sub(bidValue).Div(midpoint).Mul(decimal.NewFromInt(bpsScale)), nil
}

// DeriveMidPrice computes the midpoint of the best bid and ask.
//
// It runs in decimal rather than float: the midpoint is a price, and a price
// that has been through a float is a price that changed in its last digits.
// Summing before dividing keeps the result exact rather than an average of two
// rounded halves.
func DeriveMidPrice(bid, ask string) (decimal.Decimal, error) {
	bidValue, err := decimal.NewFromString(bid)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: bid: %q", ErrInvalidDecimal, bid)
	}
	askValue, err := decimal.NewFromString(ask)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: ask: %q", ErrInvalidDecimal, ask)
	}
	if !bidValue.IsPositive() || !askValue.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("%w: bid %s, ask %s", ErrNonPositivePrice, bid, ask)
	}
	if askValue.LessThan(bidValue) {
		return decimal.Decimal{}, fmt.Errorf("%w: bid %s, ask %s", ErrOrderBookCrossed, bid, ask)
	}
	return bidValue.Add(askValue).Div(decimal.NewFromInt(2)), nil
}

// DeriveAnnualisedFundingPct converts a per-settlement funding rate into an
// annualised percentage.
//
// Funding is a cost the model may weigh, never a reason the engine refuses:
// a predictable cost is not a risk, and blocking on it would forbid the
// carry-style trades that are the point of a perpetual.
func DeriveAnnualisedFundingPct(rate string, settlementsPerDay int64) (decimal.Decimal, error) {
	value, err := decimal.NewFromString(rate)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: rate: %q", ErrInvalidDecimal, rate)
	}
	if settlementsPerDay <= 0 {
		return decimal.Decimal{}, fmt.Errorf("%w: settlements per day %d", ErrInvalidDerived, settlementsPerDay)
	}
	// rate × settlements × 365 × 100, all in decimal so a rate like 0.0001 keeps
	// every significant digit on the way to a percentage.
	scale := decimal.NewFromInt(settlementsPerDay).
		Mul(decimal.NewFromInt(365)).
		Mul(decimal.NewFromInt(100))
	return value.Mul(scale), nil
}

// DeriveExecutablePrice computes the volume-weighted price of filling a
// notional against one side of the book, and the base quantity that would fill.
//
// This is what makes the slippage check meaningful: a decision that looks
// profitable at the midpoint may not be executable at the midpoint, and the
// difference is exactly what this returns. It returns an error rather than a
// partial price when the book cannot absorb the size, because a partial fill
// reported as a full one would understate the slippage.
//
// The result is an estimate, not an exact price: it divides, and fixed-precision
// division leaves artefacts in the low-order digits that differ between the two
// decimal backends. A caller comparing it against a slippage threshold in basis
// points is unaffected.
func DeriveExecutablePrice(levels []OrderBookLevel, notional decimal.Decimal) (price, size decimal.Decimal, err error) {
	if len(levels) == 0 {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: empty book side", ErrSnapshotIncomplete)
	}
	if !notional.IsPositive() {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: notional %s", ErrNonPositivePrice, notional)
	}

	zero := decimal.NewFromInt(0)
	spent := zero
	filled := zero
	for _, level := range levels {
		levelPrice, parseErr := decimal.NewFromString(level.Price)
		if parseErr != nil {
			return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: level price: %q", ErrInvalidDecimal, level.Price)
		}
		levelSize, parseErr := decimal.NewFromString(level.Size)
		if parseErr != nil {
			return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: level size: %q", ErrInvalidDecimal, level.Size)
		}
		if !levelPrice.IsPositive() || !levelSize.IsPositive() {
			return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: price %s size %s",
				ErrNonPositivePrice, level.Price, level.Size)
		}
		available := levelPrice.Mul(levelSize)
		if spent.Add(available).GreaterThanOrEqual(notional) {
			// Take only what is left of the notional at this level, so the
			// average is not dragged towards a price the order never pays.
			remaining := notional.Sub(spent)
			taken := remaining.Div(levelPrice)
			filled = filled.Add(taken)
			spent = spent.Add(remaining)
			return spent.Div(filled), filled, nil
		}
		spent = spent.Add(available)
		filled = filled.Add(levelSize)
	}
	// A partial fill reported as a full one would understate the slippage, so
	// an unabsorbable notional is a failure rather than a best effort.
	return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: the book cannot absorb %s", ErrSnapshotIncomplete, notional)
}

// intervalDurations maps an interval to its length. It is written down rather
// than derived from a duration string so an unknown interval is refused instead
// of silently treated as some number of nanoseconds.
var intervalDurations = map[Interval]int64{
	Interval1m:  60,
	Interval5m:  300,
	Interval15m: 900,
	Interval1h:  3600,
	Interval4h:  14400,
	Interval1d:  86400,
}

// Duration returns the interval length in seconds.
func (i Interval) Duration() (int64, error) {
	seconds, ok := intervalDurations[i]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownInterval, string(i))
	}
	return seconds, nil
}

// SummariseCandles builds the bounded summary the model reads instead of the
// full window.
func SummariseCandles(candles []CandleFact) (CandleSummary, error) {
	if len(candles) == 0 {
		return CandleSummary{}, fmt.Errorf("%w: no candles to summarise", ErrTooFewPoints)
	}
	first, err := strconv.ParseFloat(candles[0].Open, 64)
	if err != nil {
		return CandleSummary{}, fmt.Errorf("%w: first open: %q", ErrInvalidDecimal, candles[0].Open)
	}
	last, err := strconv.ParseFloat(candles[len(candles)-1].Close, 64)
	if err != nil {
		return CandleSummary{}, fmt.Errorf("%w: last close: %q", ErrInvalidDecimal, candles[len(candles)-1].Close)
	}

	highest := math.Inf(-1)
	lowest := math.Inf(1)
	var volume float64
	var rises int
	for i := range candles {
		high, highErr := strconv.ParseFloat(candles[i].High, 64)
		if highErr != nil {
			return CandleSummary{}, fmt.Errorf("%w: high: %q", ErrInvalidDecimal, candles[i].High)
		}
		low, lowErr := strconv.ParseFloat(candles[i].Low, 64)
		if lowErr != nil {
			return CandleSummary{}, fmt.Errorf("%w: low: %q", ErrInvalidDecimal, candles[i].Low)
		}
		vol, volErr := strconv.ParseFloat(candles[i].Volume, 64)
		if volErr != nil {
			return CandleSummary{}, fmt.Errorf("%w: volume: %q", ErrInvalidDecimal, candles[i].Volume)
		}
		closeValue, closeErr := strconv.ParseFloat(candles[i].Close, 64)
		if closeErr != nil {
			return CandleSummary{}, fmt.Errorf("%w: close: %q", ErrInvalidDecimal, candles[i].Close)
		}
		openValue, openErr := strconv.ParseFloat(candles[i].Open, 64)
		if openErr != nil {
			return CandleSummary{}, fmt.Errorf("%w: open: %q", ErrInvalidDecimal, candles[i].Open)
		}
		highest = math.Max(highest, high)
		lowest = math.Min(lowest, low)
		volume += vol
		if closeValue >= openValue {
			rises++
		}
	}

	changePct := 0.0
	if first != 0 {
		changePct = (last - first) / first * 100
	}
	// DirectionScore is the share of bars that closed at or above their open,
	// in [0, 1]. It is a reading of the window's texture, not a signal: a
	// recommendation derived here would sit outside the audited decision path.
	score := float64(rises) / float64(len(candles))

	return CandleSummary{
		FirstOpen:      candles[0].Open,
		LastClose:      candles[len(candles)-1].Close,
		HighestHigh:    formatFloat(highest),
		LowestLow:      formatFloat(lowest),
		TotalVolume:    formatFloat(volume),
		ChangePct:      formatFloat(changePct),
		DirectionScore: formatFloat(score),
	}, nil
}

// formatFloat renders a computed value in plain notation, never in exponent
// form, so the result satisfies ValidateDecimal and ValidateSignedDecimal.
//
// It rounds to the same precision the indicator path uses, because a float64's
// shortest rendering can need more fraction digits than the contract allows —
// an indicator of 0.0035044457052606504 carries nineteen against a bound of
// eighteen. One precision for every computed value keeps the rule single.
func formatFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', MaxRenderedFractionDigits, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(text, "0")
		text = strings.TrimSuffix(text, ".")
	}
	if text == "" || text == "-" {
		text = "0"
	}
	return text
}
