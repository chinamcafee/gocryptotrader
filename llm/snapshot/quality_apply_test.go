package snapshot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

func closedFact(at time.Time, closeAt string) CandleFact {
	return CandleFact{Time: at, Open: closeAt, High: closeAt, Low: closeAt, Close: closeAt, Volume: "1"}
}

func TestCandleGaps(t *testing.T) {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	hour := time.Hour

	t.Run("contiguous window has no gaps", func(t *testing.T) {
		closed := []CandleFact{
			closedFact(base, "1"),
			closedFact(base.Add(hour), "2"),
			closedFact(base.Add(2*hour), "3"),
		}
		assert.Empty(t, CandleGaps(closed, hour))
	})

	t.Run("a missing bar is one gap with the right range", func(t *testing.T) {
		closed := []CandleFact{
			closedFact(base, "1"),
			closedFact(base.Add(2*hour), "3"), // 01:00 is missing
			closedFact(base.Add(3*hour), "4"),
		}
		gaps := CandleGaps(closed, hour)
		require.Len(t, gaps, 1)
		assert.Equal(t, base.Add(hour), gaps[0].Start)
		assert.Equal(t, base.Add(hour), gaps[0].End)
		assert.Equal(t, int64(1), gaps[0].MissingBars)
	})

	t.Run("a wide hole counts every missing bar", func(t *testing.T) {
		closed := []CandleFact{
			closedFact(base, "1"),
			closedFact(base.Add(4*hour), "5"),
		}
		gaps := CandleGaps(closed, hour)
		require.Len(t, gaps, 1)
		assert.Equal(t, int64(3), gaps[0].MissingBars)
		assert.Equal(t, base.Add(3*hour), gaps[0].End)
	})

	t.Run("degenerate inputs are safe", func(t *testing.T) {
		assert.Empty(t, CandleGaps(nil, hour))
		assert.Empty(t, CandleGaps([]CandleFact{closedFact(base, "1")}, hour))
		assert.Empty(t, CandleGaps([]CandleFact{closedFact(base, "1"), closedFact(base.Add(hour), "2")}, 0))
	})
}

func TestZeroCandleDetection(t *testing.T) {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	padded := CandleFact{Time: base, Open: "0", High: "0", Low: "0", Close: "0", Volume: "0"}
	assert.True(t, IsZeroCandle(&padded), "the all-zero OHLCV shape is range padding wearing a candle's clothes")
	assert.False(t, IsZeroCandle(new(closedFact(base, "1"))))
	partialZero := padded
	partialZero.Volume = "1"
	assert.False(t, IsZeroCandle(&partialZero), "a real trade with zero move is not padding")
	assert.Equal(t, 1, CountZeroCandles([]CandleFact{padded, closedFact(base, "1")}))
}

func gapsFixture() *MarketSnapshot {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	built := &MarketSnapshot{
		Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		SourceTime: base, BuiltAt: base,
		Quality: map[string]candidate.DataQuality{},
	}
	built.Candles = CandleWindow{
		Interval: Interval1h,
		Start:    base,
		End:      base.Add(3 * time.Hour),
		Count:    3,
		Closed: []CandleFact{
			closedFact(base, "1"),
			closedFact(base.Add(2*time.Hour), "3"), // hole at 01:00
			closedFact(base.Add(3*time.Hour), "4"),
		},
	}
	setQuality := func(q candidate.DataQuality) {
		built.Quality[CategoryCandles] = q
		built.Quality[CategoryIndicators] = q
	}
	setQuality(candidate.QualityExchangeREST)
	return built
}

func TestApplyCandleGapsDowngradesAndRecords(t *testing.T) {
	built := gapsFixture()
	require.NoError(t, built.ApplyCandleGaps())
	assert.Equal(t, candidate.QualityGap, built.QualityOf(CategoryCandles))
	assert.Equal(t, candidate.QualityGap, built.QualityOf(CategoryIndicators),
		"indicators computed across a gap are a fabrication and fall with the candles")
	require.Len(t, built.Gaps, 1)
	assert.Equal(t, int64(1), built.Gaps[0].MissingBars)
}

func TestApplyCandleGapsOnlyLowers(t *testing.T) {
	built := gapsFixture()
	// A more specific reason already degraded the window: the gap mark must
	// not overwrite it (docs 04 §5 -- quality only ever downgrades).
	built.Quality[CategoryCandles] = candidate.QualityStale
	require.NoError(t, built.ApplyCandleGaps())
	assert.Equal(t, candidate.QualityStale, built.QualityOf(CategoryCandles), "the earlier reason survives")
	assert.Equal(t, candidate.QualityGap, built.QualityOf(CategoryIndicators))
}

func TestApplyCandleGapsCleanWindowUntouched(t *testing.T) {
	built := gapsFixture()
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	built.Candles.Closed = []CandleFact{
		closedFact(base, "1"),
		closedFact(base.Add(time.Hour), "2"),
		closedFact(base.Add(2*time.Hour), "3"),
	}
	require.NoError(t, built.ApplyCandleGaps())
	assert.Equal(t, candidate.QualityExchangeREST, built.QualityOf(CategoryCandles))
	assert.Empty(t, built.Gaps)

	// The same down-grade fires for padded candles even when spacing is even.
	built.Candles.Closed[1] = CandleFact{Time: base.Add(time.Hour), Open: "0", High: "0", Low: "0", Close: "0", Volume: "0"}
	require.NoError(t, built.ApplyCandleGaps())
	assert.Equal(t, candidate.QualityGap, built.QualityOf(CategoryCandles))
	assert.Empty(t, built.Gaps, "padding is not a missing-slot range; the category label carries it")
}
