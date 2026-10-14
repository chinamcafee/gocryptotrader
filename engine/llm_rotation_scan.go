package engine

import (
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// Feeding the scanner from the hourly reload.
//
// This is the seam between layer one and the trigger scan, and the design was
// decided by one fact: an incremental window is five bars, and MACD needs
// thirty-three. A scan fed by increments could not compute the indicators it
// compares, so the scan runs after the full reload instead — which is also what
// the coverage design says ("after the hourly reload, scan the contracts").
//
// It is cheap: the reload already paid the exchange weight, and computing three
// indicators over 250 bars is microseconds. Paying it twice would not be.

// factFromWindow derives a trigger fact from a market's reloaded candle window.
//
// The indicator state is computed from the window rather than taken from a
// snapshot, because no snapshot exists at this point: snapshots are assembled on
// demand for the market being decided on, and this runs for every tracked market
// on the hour.
func factFromWindow(item *kline.Item, interval snapshot.Interval, params *snapshot.IndicatorParams, previous *trigger.Fact, now time.Time) (trigger.Fact, error) {
	if item == nil || len(item.Candles) == 0 {
		return trigger.Fact{}, fmt.Errorf("%w: no candles to derive a trigger fact from", snapshot.ErrSnapshotIncomplete)
	}
	id := item.Pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String()
	fact := trigger.Fact{
		MarketID: id,
		Interval: interval,
	}

	// The newest closed candle. A window straight from the exchange may include
	// the bar still forming, which has not closed and must not be compared.
	closed := closedCandles(item, now)
	if len(closed) == 0 {
		return trigger.Fact{}, fmt.Errorf("%w: no closed candles for %s", snapshot.ErrSnapshotIncomplete, id)
	}
	fact.LastClosedAt = closed[len(closed)-1].Time
	if len(closed) > 1 {
		fact.PreviousClosedAt = closed[len(closed)-2].Time
	}

	if err := deriveSignals(item, &fact, params, now); err != nil {
		return trigger.Fact{}, err
	}

	if previous != nil {
		fact.PreviousHistogramSign = previous.HistogramSign
		fact.PreviousPriceVsMA = previous.PriceVsMA
		fact.PreviousATR = previous.ATR
	}
	return fact, nil
}

// closedCandles splits a window at the newest candle that has actually closed.
//
// A candle is closed once its interval has elapsed. The split is by time rather
// than by dropping the last element, because the exchange does not always
// include the forming bar and dropping a closed one would compare the wrong two
// bars — which is exactly the error a sign-change trigger cannot tolerate.
func closedCandles(item *kline.Item, now time.Time) []kline.Candle {
	seconds := item.Interval.Duration()
	closed := make([]kline.Candle, 0, len(item.Candles))
	for i := range item.Candles {
		if item.Candles[i].Time.Add(seconds).After(now) {
			continue
		}
		closed = append(closed, item.Candles[i])
	}
	return closed
}

// deriveSignals fills the sign and position facts from the window.
func deriveSignals(item *kline.Item, fact *trigger.Fact, params *snapshot.IndicatorParams, now time.Time) error {
	if params.MACDFast == 0 {
		defaults := snapshot.DefaultIndicatorParams()
		params = &defaults
	}
	// Check the minimum here rather than letting the engine's own error through,
	// so the refusal carries this package's sentinel and reads as "too few
	// candles" instead of a provider message about granularity.
	if minimum := snapshot.MinCandlesForMACD(params.MACDFast, params.MACDSlow, params.MACDSignal); int64(len(item.Candles)) < minimum {
		return fmt.Errorf("%w: %d candles for MACD, %d needed", snapshot.ErrNotEnoughCandles,
			len(item.Candles), minimum)
	}
	macd, err := item.GetMovingAverageConvergenceDivergenceOnClose(params.MACDFast, params.MACDSlow, params.MACDSignal)
	if err != nil {
		return fmt.Errorf("computing MACD for %s: %w", fact.MarketID, err)
	}
	if len(macd.Histogram) > 0 {
		fact.HistogramSign = trigger.SignOf(macd.Histogram[len(macd.Histogram)-1])
	}

	// The longest average the window can support is the line a crossing is read
	// against. Reporting every period would raise several events for one move,
	// and refusing the whole scan because the longest one is unavailable would
	// discard the close trigger too — which is what a newly listed market with
	// a short history would hit every hour.
	longest := int64(0)
	for _, period := range params.EMAPeriods {
		if period <= int64(len(item.Candles)) {
			longest = max(longest, period)
		}
	}
	for _, period := range params.SMAPeriods {
		if period <= int64(len(item.Candles)) {
			longest = max(longest, period)
		}
	}
	if longest > 0 {
		averages, averageErr := item.GetSimpleMovingAverageOnClose(longest)
		if averageErr != nil {
			return fmt.Errorf("computing SMA(%d) for %s: %w", longest, fact.MarketID, averageErr)
		}
		closed := closedCandles(item, now)
		if len(averages) > 0 && len(closed) > 0 {
			latest := averages[len(averages)-1]
			closePrice := closed[len(closed)-1].Close
			if closePrice > latest {
				fact.PriceVsMA = snapshot.PriceAbove
			} else {
				fact.PriceVsMA = snapshot.PriceBelow
			}
			fact.MAPeriod = longest
		}
	}

	if params.ATRPeriod > 0 {
		atr, atrErr := averageTrueRange(item, params.ATRPeriod)
		if atrErr != nil {
			return fmt.Errorf("computing ATR for %s: %w", fact.MarketID, atrErr)
		}
		fact.ATR = atr
	}
	return nil
}

// averageTrueRange computes a simple average of the true ranges over period
// candles.
//
// It is a mean of true ranges rather than Wilder's smoothing: the trigger only
// asks whether volatility expanded relative to the bar before, and both forms
// answer that. The simpler one is easier to check by hand against a window.
func averageTrueRange(item *kline.Item, period int64) (float64, error) {
	if period <= 0 {
		return 0, fmt.Errorf("%w: ATR period %d", snapshot.ErrIndicatorParamsInvalid, period)
	}
	candles := item.Candles
	if int64(len(candles)) < period+1 {
		return 0, fmt.Errorf("%w: %d candles for ATR(%d)", snapshot.ErrNotEnoughCandles, len(candles), period)
	}
	var sum float64
	for i := int64(len(candles)) - period; i < int64(len(candles)); i++ {
		previousClose := candles[i-1].Close
		high, low := candles[i].High, candles[i].Low
		trueRange := high - low
		if gap := absFloat(high - previousClose); gap > trueRange {
			trueRange = gap
		}
		if gap := absFloat(low - previousClose); gap > trueRange {
			trueRange = gap
		}
		sum += trueRange
	}
	return sum / float64(period), nil
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

// attachScanner gives the rotation a scanner to feed.
//
// It is a setter rather than a constructor argument because the scanner is
// optional: a rotation with no scanner is a valid configuration, and the
// rotation's own tests do not want one.
func (r *Rotator) attachScanner(s *TriggerScanner, params *snapshot.IndicatorParams, interval snapshot.Interval) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scanner = s
	r.scanParams = params
	r.scanInterval = interval
	r.scanFacts = make(map[string]trigger.Fact, len(r.markets))
}

// Scanner returns the attached scanner, or nil.
func (r *Rotator) Scanner() *TriggerScanner {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scanner
}

// scanAfterReload feeds the scanner from a market's freshly reloaded window.
//
// Called only after a reload, because that is the only fetch with enough history
// to compute the indicators the triggers compare.
func (r *Rotator) scanAfterReload(item *kline.Item, now time.Time) error {
	if r == nil || r.scanner == nil || item == nil {
		return nil
	}
	id := item.Pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String()
	previous, had := r.scanFacts[id]
	var previousPtr *trigger.Fact
	if had {
		previousPtr = &previous
	}
	fact, err := factFromWindow(item, r.scanInterval, r.scanParams, previousPtr, now)
	if err != nil {
		return err
	}
	if _, err = r.scanner.Observe(&fact); err != nil {
		return err
	}
	r.scanFacts[id] = fact
	return nil
}
