package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

func newScanner(t *testing.T, policy trigger.Policy, clock *fixedClock) *TriggerScanner {
	t.Helper()
	s, err := NewTriggerScanner(policy, clock.now)
	require.NoError(t, err, "the scanner must build")
	return s
}

// closeFact is a market whose candle closed, which the default policy always
// arms.
func closeFact(market string, closedAt time.Time) *trigger.Fact {
	return &trigger.Fact{
		MarketID:         market,
		Interval:         snapshot.Interval1h,
		LastClosedAt:     closedAt,
		PreviousClosedAt: closedAt.Add(-time.Hour),
	}
}

func TestScannerQueuesACloseEvent(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	s := newScanner(t, trigger.DefaultPolicy(), clock)

	raised, err := s.Observe(closeFact("BTC_USDT", testNow))
	require.NoError(t, err, "a close must be observable")
	require.Len(t, raised, 1, "the close must raise one event")
	assert.Equal(t, trigger.KindKlineClose, raised[0].Kind, "the event should be the close")

	queued := s.Drain(10)
	require.Len(t, queued, 1, "the event must be queued for the pipeline")
	assert.Equal(t, "BTC_USDT", queued[0].MarketID, "the queued event should name the market")
	assert.Empty(t, s.Drain(10), "draining should remove what was taken")

	stats := s.Stats()
	assert.Zero(t, stats.Queued, "the queue should be empty after a drain")
	assert.Equal(t, []string{
		string(trigger.KindKlineClose), string(trigger.KindMACDCross), string(trigger.KindMACross),
	}, stats.ArmedKinds,
		"the armed set must be reported so a health surface can show what is listening")
}

func TestScannerThrottlesOneMarketToOneReasonToLook(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	policy := trigger.DefaultPolicy()
	policy.MinInterval = time.Minute
	s := newScanner(t, policy, clock)

	// A close at the same instant is the same moment twice, so the market is not
	// asked about again.
	_, err := s.Observe(closeFact("BTC_USDT", testNow))
	require.NoError(t, err, "the first close must be observable")
	_, err = s.Observe(closeFact("BTC_USDT", testNow))
	require.NoError(t, err, "a repeat must be observable")

	assert.Len(t, s.Drain(10), 1, "only one event may survive the throttle")

	// A different market is a different reason to look.
	_, err = s.Observe(closeFact("ETH_USDT", testNow))
	require.NoError(t, err, "another market must be observable")
	assert.Len(t, s.Drain(10), 1, "the throttle should be per market, not global")

	// Once the interval has passed the market may be heard from again.
	clock.advance(time.Minute)
	_, err = s.Observe(closeFact("BTC_USDT", testNow.Add(time.Hour)))
	require.NoError(t, err, "a later close must be observable")
	assert.Len(t, s.Drain(10), 1, "the throttle should release after its interval")
}

func TestScannerHoldsASignalUntilTheConfirmingPass(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	policy := trigger.DefaultPolicy()
	policy.KlineClose = false
	policy.MACDCross = true
	policy.MinInterval = 0
	policy.ConfirmInterval = 4 * time.Hour
	s := newScanner(t, policy, clock)

	golden := func(at time.Time) *trigger.Fact {
		return &trigger.Fact{
			MarketID:              "BTC_USDT",
			Interval:              snapshot.Interval1h,
			LastClosedAt:          at,
			PreviousClosedAt:      at.Add(-time.Hour),
			HistogramSign:         trigger.SignPositive,
			PreviousHistogramSign: trigger.SignNegative,
		}
	}

	// The first sighting is held, not raised: a one-hour flip is noise often
	// enough that it must survive a later pass before it drives anything.
	raised, err := s.Observe(golden(testNow))
	require.NoError(t, err, "the first sighting must be observable")
	assert.Empty(t, raised, "an unconfirmed signal should not be raised")
	assert.Empty(t, s.Drain(10), "it should not be queued either")
	assert.Equal(t, 1, s.Stats().Pending, "it should be held as pending")

	// Seen again inside the window: still held, and the clock is not restarted.
	clock.advance(2 * time.Hour)
	raised, err = s.Observe(golden(testNow.Add(2 * time.Hour)))
	require.NoError(t, err, "a second sighting must be observable")
	assert.Empty(t, raised, "a signal inside its window should stay held")
	assert.Equal(t, 1, s.Stats().Pending, "it should still be pending")

	// The confirming pass has arrived and the signal fires again, so it is
	// promoted.
	clock.advance(3 * time.Hour)
	raised, err = s.Observe(golden(testNow.Add(5 * time.Hour)))
	require.NoError(t, err, "the confirming pass must be observable")
	require.Len(t, raised, 1, "a signal that survived its window must be raised")
	assert.Equal(t, trigger.KindMACDCross, raised[0].Kind, "the promoted event should be the crossing")
	assert.Zero(t, s.Stats().Pending, "a promoted signal should leave the pending set")
}

func TestScannerDropsASignalThatDoesNotSurvive(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	policy := trigger.DefaultPolicy()
	policy.KlineClose = false
	policy.MACDCross = true
	policy.MinInterval = 0
	policy.ConfirmInterval = 4 * time.Hour
	s := newScanner(t, policy, clock)

	// The flip reverses before the confirming pass, so the later pass raises a
	// different signal and the first one is never promoted.
	flip := trigger.Fact{
		MarketID: "BTC_USDT", Interval: snapshot.Interval1h,
		LastClosedAt: testNow, PreviousClosedAt: testNow.Add(-time.Hour),
		HistogramSign: trigger.SignPositive, PreviousHistogramSign: trigger.SignNegative,
	}
	raised, err := s.Observe(&flip)
	require.NoError(t, err, "the flip must be observable")
	assert.Empty(t, raised, "the first sighting is held")
	assert.Equal(t, 1, s.Stats().Pending, "it should be pending")

	clock.advance(5 * time.Hour)
	reversed := flip
	reversed.LastClosedAt = testNow.Add(5 * time.Hour)
	reversed.PreviousClosedAt = testNow.Add(4 * time.Hour)
	reversed.HistogramSign = trigger.SignNegative
	reversed.PreviousHistogramSign = trigger.SignPositive
	raised, err = s.Observe(&reversed)
	require.NoError(t, err, "the reversal must be observable")
	assert.Empty(t, raised,
		"a reversal must not promote the earlier signal: the two are opposite readings of the same line")

	// The two are distinct signals and both are held. Keying a signal by its kind
	// alone would have made the reversal look like the earlier signal confirming
	// itself, which is how a death cross would end up endorsing a golden one.
	assert.Equal(t, 2, s.Stats().Pending, "the reversal should be held in its own right")

	// The earlier signal ages out first, because it was seen earlier.
	clock.advance(5 * time.Hour)
	assert.Equal(t, 1, s.Settle(), "the first signal should be dropped once it can no longer confirm")
	assert.Equal(t, 1, s.Stats().Pending, "the reversal is still inside its own window")
}

func TestScannerSettlesSignalsThatNeverConfirm(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	policy := trigger.DefaultPolicy()
	policy.KlineClose = false
	policy.MACDCross = true
	policy.MinInterval = 0
	policy.ConfirmInterval = 4 * time.Hour
	s := newScanner(t, policy, clock)

	_, err := s.Observe(&trigger.Fact{
		MarketID: "BTC_USDT", Interval: snapshot.Interval1h,
		LastClosedAt: testNow, PreviousClosedAt: testNow.Add(-time.Hour),
		HistogramSign: trigger.SignPositive, PreviousHistogramSign: trigger.SignNegative,
	})
	require.NoError(t, err, "the signal must be observable")
	assert.Equal(t, 1, s.Stats().Pending, "it should be pending")

	// Inside twice the interval it is still waiting.
	clock.advance(5 * time.Hour)
	assert.Zero(t, s.Settle(), "a signal still inside its window should not be settled")

	// Past twice the interval it is dropped, so a market that fired once and
	// never again does not sit in the pending set forever.
	clock.advance(5 * time.Hour)
	assert.Equal(t, 1, s.Settle(), "an unconfinable signal should be dropped")
	assert.Zero(t, s.Stats().Pending, "the pending set should be empty again")
}

func TestScannerQueueIsBounded(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: testNow}
	policy := trigger.DefaultPolicy()
	policy.MinInterval = 0
	s := newScanner(t, policy, clock)
	s.limit = 3

	for i := range 5 {
		market := string(rune('A'+i)) + "TC_USDT"
		_, err := s.Observe(closeFact(market, testNow))
		require.NoErrorf(t, err, "market %d must be observable", i)
	}

	stats := s.Stats()
	assert.Equal(t, 3, stats.Queued, "the queue should respect its bound rather than growing without limit")
	assert.Equal(t, int64(2), stats.Dropped, "the dropped count should be reported, so a slow consumer is visible")

	// The newest events survive, because an operator asking what just happened
	// wants the recent answer.
	queued := s.Drain(10)
	require.Len(t, queued, 3, "three events must be available")
	assert.Equal(t, "ETC_USDT", queued[len(queued)-1].MarketID, "the newest event should be kept")
	assert.Equal(t, "CTC_USDT", queued[0].MarketID, "the oldest events should be the ones dropped")
}

func TestScannerRefusesMisuse(t *testing.T) {
	t.Parallel()

	bad := trigger.DefaultPolicy()
	bad.MinInterval = -time.Second
	_, err := NewTriggerScanner(bad, nil)
	require.Error(t, err, "an invalid policy must be refused at construction")

	var nilScanner *TriggerScanner
	_, err = nilScanner.Observe(closeFact("BTC_USDT", testNow))
	require.ErrorIs(t, err, ErrNilRotator, "a nil scanner must refuse rather than panic")
	assert.Nil(t, nilScanner.Drain(10), "a nil scanner has nothing to drain")
	assert.Zero(t, nilScanner.Settle(), "a nil scanner has nothing to settle")
	assert.Equal(t, TriggerStats{}, nilScanner.Stats(), "a nil scanner has no state")

	s := newScanner(t, trigger.DefaultPolicy(), &fixedClock{at: testNow})
	assert.Nil(t, s.Drain(0), "draining nothing should return nothing")
	assert.Nil(t, s.Drain(-1), "a negative drain should return nothing rather than misbehave")
}

// scannerSnapshot builds the minimum a snapshot needs for a scanner fact: the
// pair, the closed window, and the derived readings the triggers compare.
func scannerSnapshot() *snapshot.MarketSnapshot {
	closed := []snapshot.CandleFact{
		{Time: testNow.Add(-2 * time.Hour), Open: "100", High: "110", Low: "95", Close: "105", Volume: "1"},
		{Time: testNow.Add(-time.Hour), Open: "105", High: "112", Low: "104", Close: "108", Volume: "1"},
	}
	return &snapshot.MarketSnapshot{
		ID:         "11111111-1111-4111-8111-111111111111",
		Exchange:   "Binance",
		Pair:       "BTC_USDT",
		Asset:      "usdtmarginedfutures",
		SourceTime: closed[1].Time,
		BuiltAt:    testNow,
		Candles: snapshot.CandleWindow{
			Interval: snapshot.Interval1h,
			Start:    closed[0].Time,
			End:      closed[1].Time,
			Count:    len(closed),
			Closed:   closed,
		},
		Indicators: snapshot.IndicatorSet{
			ComputedAt: testNow,
			MACD: &snapshot.MACDFact{
				Fast: 12, Slow: 26, Signal: 9,
				DIF: []string{"0.5"}, DEA: []string{"0.4"}, Histogram: []string{"0.1"},
				CrossState: snapshot.CrossStateNone, HistogramTrend: snapshot.TrendFlat,
			},
			MovingAverages: []snapshot.MovingAverageFact{
				{
					Type: snapshot.MASimple, Period: 20, Values: []string{"100"}, Latest: "100",
					PriceVsMA: snapshot.PriceAbove, Slope: snapshot.SlopeUp,
				},
				{
					Type: snapshot.MASimple, Period: 200, Values: []string{"90"}, Latest: "90",
					PriceVsMA: snapshot.PriceBelow, Slope: snapshot.SlopeUp,
				},
			},
			ATR: &snapshot.SeriesFact{Period: 14, Values: []string{"1.25"}, Latest: "1.25"},
		},
		Quality: map[string]candidate.DataQuality{
			snapshot.CategoryCandles: candidate.QualityExchangeREST,
		},
	}
}

func TestFactFromSnapshotReadsTheDerivedFacts(t *testing.T) {
	t.Parallel()

	s := scannerSnapshot()
	fact, err := FactFromSnapshot(s, nil)
	require.NoError(t, err, "an assembled snapshot must produce a fact")

	assert.Equal(t, "BTC_USDT", fact.MarketID, "the market should come from the snapshot's pair")
	assert.Equal(t, snapshot.Interval1h, fact.Interval, "the interval should come from the window")
	assert.Equal(t, s.SourceTime, fact.LastClosedAt, "the close should come from the source time")
	assert.Equal(t, s.Candles.Closed[0].Time, fact.PreviousClosedAt,
		"the previous close must be the bar before the newest")
	assert.Equal(t, trigger.SignPositive, fact.HistogramSign, "the histogram sign should come from the newest point")
	assert.Equal(t, int64(200), fact.MAPeriod, "the crossing should be reported against the longest average")
	assert.Equal(t, snapshot.PriceBelow, fact.PriceVsMA, "the position should come from that average")
	assert.InDelta(t, 1.25, fact.ATR, 1e-9, "the volatility reading should come from the ATR latest value")

	// A second pass compares against the first, which is how a two-reading
	// trigger gets its pair.
	previous := fact
	flipped := scannerSnapshot()
	flipped.Indicators.MACD.Histogram = []string{"-0.1"}
	flipped.Indicators.MovingAverages[1].PriceVsMA = snapshot.PriceAbove
	flipped.SourceTime = testNow
	flipped.Candles.Closed[1].Time = testNow

	next, err := FactFromSnapshot(flipped, &previous)
	require.NoError(t, err, "a second pass must produce a fact")
	assert.Equal(t, trigger.SignNegative, next.HistogramSign, "the new sign should be read")
	assert.Equal(t, trigger.SignPositive, next.PreviousHistogramSign,
		"the previous sign must come from the previous fact, so a comparison is possible")
	assert.Equal(t, snapshot.PriceBelow, next.PreviousPriceVsMA, "the previous position should come from the previous fact")

	// A re-read of the same candle must not look like a change: the comparison
	// has to be against the same bar rather than one two steps back.
	same, err := FactFromSnapshot(scannerSnapshot(), &previous)
	require.NoError(t, err, "a re-read must produce a fact")
	assert.Equal(t, same.HistogramSign, same.PreviousHistogramSign,
		"a re-read of one candle must not appear as a sign change")
	assert.Equal(t, same.PriceVsMA, same.PreviousPriceVsMA,
		"a re-read of one candle must not appear as a position change")

	_, err = FactFromSnapshot(nil, nil)
	require.ErrorIs(t, err, snapshot.ErrSnapshotIncomplete, "a nil snapshot must be refused")
}
