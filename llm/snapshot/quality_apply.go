package snapshot

import (
	"fmt"
	"strconv"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// GapRange describes a stretch of missing closed candles.
type GapRange struct {
	// Start is the open time of the first missing slot.
	Start time.Time `json:"start"`
	// End is the open time of the last missing slot.
	End time.Time `json:"end"`
	// MissingBars counts the absent slots in [Start,End].
	MissingBars int64 `json:"missingBars"`
}

// CandleGaps finds missing slots between consecutive closed candles. Data must
// be listed oldest first. A window whose candles are not one interval apart has
// a hole -- and a hole in the history is a fact the model must see, because
// every indicator computed across it is an interpolation the exchange never
// provided (docs 04 §5 GAP).
func CandleGaps(closed []CandleFact, interval time.Duration) []GapRange {
	if interval <= 0 || len(closed) < 2 {
		return nil
	}
	var gaps []GapRange
	for i := 1; i < len(closed); i++ {
		previous := closed[i-1].Time
		current := closed[i].Time
		delta := current.Sub(previous)
		if delta <= interval {
			continue
		}
		missing := int64(delta/interval) - 1
		gaps = append(gaps, GapRange{
			Start:       previous.Add(interval),
			End:         current.Add(-interval),
			MissingBars: missing,
		})
	}
	return gaps
}

// IsZeroCandle reports whether a candle carries the all-zero OHLCV shape that
// range padding inserts. It is a gap wearing a candle's clothes: the exchange
// never traded there, and a zero close poisons any average it enters.
func IsZeroCandle(c *CandleFact) bool {
	for _, value := range []string{c.Open, c.High, c.Low, c.Close, c.Volume} {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed != 0 {
			return false
		}
	}
	return true
}

// CountZeroCandles counts padded candles in a closed window.
func CountZeroCandles(closed []CandleFact) int {
	count := 0
	for i := range closed {
		if IsZeroCandle(&closed[i]) {
			count++
		}
	}
	return count
}

// ApplyCandleGaps marks the candle and indicator categories GAP when the closed
// window has holes or padded candles. The marking goes through SetQuality, so
// it can only lower: a window already degraded for a more specific reason
// keeps that reason (docs 04 §5 -- quality only ever downgrades).
//
// The indicator category falls with the candles because every indicator here
// is computed across the same window: a gap in the input is a fabrication in
// the output.
func (s *MarketSnapshot) ApplyCandleGaps() error {
	if s == nil {
		return ErrSnapshotIncomplete
	}
	intervalSeconds, err := s.Candles.Interval.Duration()
	if err != nil || intervalSeconds <= 0 || len(s.Candles.Closed) == 0 {
		return nil //nolint:nilerr // an undecodable interval simply skips gap labelling
	}
	interval := time.Duration(intervalSeconds) * time.Second
	gaps := CandleGaps(s.Candles.Closed, interval)
	zeros := CountZeroCandles(s.Candles.Closed)
	if len(gaps) == 0 && zeros == 0 {
		return nil
	}
	s.Gaps = gaps
	// Skip categories already degraded: a more specific earlier reason (STALE,
	// UNKNOWN) must survive -- GAP and STALE share a rank, so the only-lower
	// guard alone would let this overwrite them (docs 04 §5).
	if !s.QualityOf(CategoryCandles).IsDegraded() {
		if err := s.SetQuality(CategoryCandles, candidate.QualityGap); err != nil {
			return fmt.Errorf("marking candle gaps: %w", err)
		}
	}
	if !s.QualityOf(CategoryIndicators).IsDegraded() {
		if err := s.SetQuality(CategoryIndicators, candidate.QualityGap); err != nil {
			return fmt.Errorf("marking indicator gaps: %w", err)
		}
	}
	return nil
}
