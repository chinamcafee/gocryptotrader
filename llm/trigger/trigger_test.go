package trigger

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func baseFact() *Fact {
	return &Fact{
		MarketID:              "BTC_USDT",
		Interval:              snapshot.Interval1h,
		LastClosedAt:          testNow,
		PreviousClosedAt:      testNow.Add(-time.Hour),
		HistogramSign:         SignPositive,
		PreviousHistogramSign: SignPositive,
		PriceVsMA:             "ABOVE",
		PreviousPriceVsMA:     "ABOVE",
		MAPeriod:              200,
		ATR:                   1.0,
		PreviousATR:           1.0,
	}
}

func TestDefaultPolicyArmsThePredictableTriggersOnly(t *testing.T) {
	t.Parallel()

	p := DefaultPolicy()
	require.NoError(t, p.Validate(), "the default policy must be valid")

	// Q5 decided the minimal set plus the indicator crossings. The crosses are
	// armed, which is only safe because they are held for a confirming pass
	// rather than acted on directly.
	assert.True(t, p.KlineClose, "the candle close is the most predictable trigger and should be armed")
	assert.True(t, p.MACDCross, "the histogram crossing is armed: Q5 chose the option that includes the crosses")
	assert.True(t, p.MACross, "the moving-average crossing is armed for the same reason")
	assert.False(t, p.ATRSpike, "a volatility spike is a configuration option and off by default")
	assert.False(t, p.FundingWindow, "the settlement window is a configuration option and off by default")
	assert.Equal(t, time.Minute, p.MinInterval, "the de-bouncing interval should have a default")
	assert.Equal(t, DefaultConfirmInterval, p.ConfirmInterval,
		"a short-interval signal must wait for a longer-interval confirmation")

	assert.Equal(t, []Kind{KindKlineClose, KindMACDCross, KindMACross}, p.Armed(),
		"the minimal set plus the crossings must be armed")
}

func TestPolicyValidation(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*Policy){
		"negative throttle":      func(p *Policy) { p.MinInterval = -time.Second },
		"negative confirmation":  func(p *Policy) { p.ConfirmInterval = -time.Hour },
		"ATR multiple below one": func(p *Policy) { p.ATRSpikeMultiple = 0.5 },
	} {
		p := DefaultPolicy()
		mutate(&p)
		require.ErrorIsf(t, p.Validate(), ErrConfirmIntervalInvalid, "%s must be refused", name)
	}

	p := DefaultPolicy()
	p.ATRSpikeMultiple = 0
	require.NoError(t, p.Validate(), "a zero multiple means the spike trigger carries no threshold of its own")
}

func TestKlineCloseFiresOncePerNewClose(t *testing.T) {
	t.Parallel()

	p := DefaultPolicy()
	events, err := Detect(baseFact(), p)
	require.NoError(t, err, "a fact must be detectable")
	require.Len(t, events, 1, "a new close must be the only event")
	assert.Equal(t, KindKlineClose, events[0].Kind, "the event should be the close")
	assert.Equal(t, "BTC_USDT", events[0].MarketID, "the market should be named")
	assert.Equal(t, testNow, events[0].At,
		"the event must be stamped with the candle's close, not the scan time, so a decision records the moment it is about")
	assert.False(t, events[0].NeedsConfirmation,
		"a close cannot un-close, so it needs no confirming pass")
	assert.Contains(t, events[0].Detail, "1h", "the detail should name the interval")
	assert.Contains(t, events[0].String(), "KLINE_CLOSE", "the log form should name the trigger")

	// Re-reading the same candle is not a new close. Without this the scan
	// would fire on every sweep of a slow interval.
	same := baseFact()
	same.PreviousClosedAt = same.LastClosedAt
	events, err = Detect(same, p)
	require.NoError(t, err, "a re-read must be detectable")
	assert.Empty(t, events, "a re-read of the same candle should not fire a trigger")

	// A market with no close at all fires nothing rather than firing on a zero
	// time, which would be a trigger about the epoch.
	none := baseFact()
	none.LastClosedAt = time.Time{}
	none.PreviousClosedAt = time.Time{}
	events, err = Detect(none, p)
	require.NoError(t, err, "an empty window must be detectable")
	assert.Empty(t, events, "no candle means no close")
}

func TestSignOfAndTheFlatCase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, SignPositive, SignOf(0.5), "a positive value should read positive")
	assert.Equal(t, SignNegative, SignOf(-0.5), "a negative value should read negative")
	assert.Equal(t, SignZero, SignOf(0), "zero should read flat")
	assert.Equal(t, "+", SignPositive.String(), "signs should render for a log line")
	assert.Equal(t, "-", SignNegative.String(), "signs should render for a log line")
	assert.Equal(t, "0", SignZero.String(), "zero should render as flat")
}

func TestMACDCrossRequiresARealCrossing(t *testing.T) {
	t.Parallel()

	// The close trigger is disarmed so each case isolates the crossing: with it
	// armed every fact also raises a close, which would hide a crossing that
	// failed to fire.
	p := DefaultPolicy()
	p.KlineClose = false
	p.MACDCross = true

	golden := baseFact()
	golden.PreviousHistogramSign = SignNegative
	events, err := Detect(golden, p)
	require.NoError(t, err, "a crossing must be detectable")
	require.Len(t, events, 1, "the crossing must fire")
	assert.Equal(t, KindMACDCross, events[0].Kind, "the event should be the crossing")
	assert.Contains(t, events[0].Detail, "positive", "the direction should be stated")
	assert.True(t, events[0].NeedsConfirmation,
		"an indicator crossing must wait for a confirming pass: acting on a one-hour flip directly makes the system twitchy")

	death := baseFact()
	death.HistogramSign = SignNegative
	death.PreviousHistogramSign = SignPositive
	events, err = Detect(death, p)
	require.NoError(t, err, "a death cross must be detectable")
	require.Len(t, events, 1, "the death cross must fire")
	assert.Contains(t, events[0].Detail, "negative", "the direction should be stated")

	// Passing through exactly zero is not a crossing between the two sides until
	// it reaches the other side. Reporting it would double-fire every flip.
	throughZero := baseFact()
	throughZero.HistogramSign = SignZero
	throughZero.PreviousHistogramSign = SignNegative
	events, err = Detect(throughZero, p)
	require.NoError(t, err, "a flat reading must be detectable")
	assert.Empty(t, events, "a move to flat is not a crossing")

	fromZero := baseFact()
	fromZero.HistogramSign = SignPositive
	fromZero.PreviousHistogramSign = SignZero
	events, err = Detect(fromZero, p)
	require.NoError(t, err, "a move off flat must be detectable")
	assert.Empty(t, events, "a move off flat is not a crossing either, because flat is not a side")

	// Staying on one side is not a crossing, however many times it is scanned.
	staying := baseFact()
	events, err = Detect(staying, p)
	require.NoError(t, err, "an unchanged sign must be detectable")
	assert.Empty(t, events, "an unchanged sign should not fire")
}

func TestMACrossRequiresASideChange(t *testing.T) {
	t.Parallel()

	p := DefaultPolicy()
	p.KlineClose = false
	p.MACross = true

	crossed := baseFact()
	crossed.PriceVsMA = "BELOW"
	crossed.PreviousPriceVsMA = "ABOVE"
	events, err := Detect(crossed, p)
	require.NoError(t, err, "a crossing must be detectable")
	require.Len(t, events, 1, "the crossing must fire")
	assert.Equal(t, KindMACross, events[0].Kind, "the event should be the crossing")
	assert.Contains(t, events[0].Detail, "200", "the detail should name the average that was crossed")
	assert.True(t, events[0].NeedsConfirmation, "a crossing needs confirmation")

	// A missing side is not a crossing: an absent fact must not be read as one.
	absent := baseFact()
	absent.PriceVsMA = ""
	events, err = Detect(absent, p)
	require.NoError(t, err, "an absent side must be detectable")
	assert.Empty(t, events, "an absent position fact should not fire a crossing")
}

func TestATRSpikeUsesItsMultiple(t *testing.T) {
	t.Parallel()

	p := DefaultPolicy()
	p.KlineClose = false
	p.ATRSpike = true
	p.ATRSpikeMultiple = 1.5

	spike := baseFact()
	spike.PreviousATR = 1
	spike.ATR = 1.5
	events, err := Detect(spike, p)
	require.NoError(t, err, "a spike must be detectable")
	require.Len(t, events, 1, "exactly the multiple must fire")
	assert.Equal(t, KindATRSpike, events[0].Kind, "the event should be the spike")
	assert.Contains(t, events[0].Detail, "1.5 times", "the detail should state the ratio")
	assert.True(t, events[0].NeedsConfirmation, "a spike needs confirmation")

	below := baseFact()
	below.PreviousATR = 1
	below.ATR = 1.4
	events, err = Detect(below, p)
	require.NoError(t, err, "a sub-threshold move must be detectable")
	assert.Empty(t, events, "a move below the multiple should not fire")

	// A missing previous reading cannot be compared, and treating it as zero
	// would make every market look like a spike on its first scan.
	test, err := Detect(baseFact(), p)
	require.NoError(t, err, "equal readings must be detectable")
	assert.Empty(t, test, "unchanged volatility should not fire")

	fromNothing := baseFact()
	fromNothing.PreviousATR = 0
	fromNothing.ATR = 100
	events, err = Detect(fromNothing, p)
	require.NoError(t, err, "a first reading must be detectable")
	assert.Empty(t, events, "a first reading should not fire a spike")
}

func TestDisabledTriggersDoNotFire(t *testing.T) {
	t.Parallel()

	// A policy with everything off is quiet no matter what the fact says. This
	// is the test that keeps an armed-by-accident trigger from going unnoticed.
	p := Policy{MinInterval: time.Minute}
	fact := Fact{
		MarketID:              "BTC_USDT",
		Interval:              snapshot.Interval1h,
		LastClosedAt:          testNow,
		PreviousClosedAt:      testNow.Add(-time.Hour),
		HistogramSign:         SignPositive,
		PreviousHistogramSign: SignNegative,
		PriceVsMA:             "BELOW",
		PreviousPriceVsMA:     "ABOVE",
		MAPeriod:              200,
		ATR:                   10,
		PreviousATR:           1,
	}
	events, err := Detect(&fact, p)
	require.NoError(t, err, "a quiet policy must still be valid")
	assert.Empty(t, events, "every trigger is off, so nothing may fire")
	assert.Empty(t, p.Armed(), "nothing should be reported as armed")
}

func TestDetectNeedsAMarket(t *testing.T) {
	t.Parallel()

	_, err := Detect(&Fact{}, DefaultPolicy())
	require.ErrorIs(t, err, ErrNoMarket, "an event about no market must be refused")

	_, err = Detect(&Fact{MarketID: "   "}, DefaultPolicy())
	require.ErrorIs(t, err, ErrNoMarket, "a blank market must be refused")

	bad := DefaultPolicy()
	bad.ATRSpikeMultiple = 0.5
	_, err = Detect(baseFact(), bad)
	require.ErrorIs(t, err, ErrConfirmIntervalInvalid,
		"an invalid policy must be refused before it produces anything")
}

func TestSignificantEventsAreStable(t *testing.T) {
	t.Parallel()

	// Two scans of the same fact must produce identical events: a trigger that
	// varied between runs would produce decisions that cannot be explained.
	p := DefaultPolicy()
	p.KlineClose = false
	p.MACDCross, p.MACross, p.ATRSpike = true, true, true
	fact := baseFact()
	fact.PreviousHistogramSign = SignNegative
	fact.PriceVsMA, fact.PreviousPriceVsMA = "BELOW", "ABOVE"
	fact.PreviousATR, fact.ATR = 1, 2

	first, err := Detect(fact, p)
	require.NoError(t, err, "the fact must be detectable")
	second, err := Detect(fact, p)
	require.NoError(t, err, "the fact must be detectable twice")
	require.Len(t, first, 3, "all three armed triggers must fire")
	assert.Equal(t, []Kind{KindMACDCross, KindMACross, KindATRSpike}, p.Armed(),
		"the armed set must be the three that were switched on")
	assert.Equal(t, first, second, "the same fact should produce the same events")

	// The order must be stable too, so a stored transcript reads the same way
	// twice.
	assert.Equal(t, []Kind{KindMACDCross, KindMACross, KindATRSpike}, []Kind{
		first[0].Kind, first[1].Kind, first[2].Kind,
	}, "the event order must be fixed")
}

func TestNextScanInstant(t *testing.T) {
	t.Parallel()

	next, err := NextScanInstant(testNow, snapshot.Interval1h)
	require.NoError(t, err, "1h must have a next instant")
	assert.Equal(t, testNow.Add(time.Hour), next, "a 1h market closes an hour later")

	next, err = NextScanInstant(testNow, snapshot.Interval15m)
	require.NoError(t, err, "15m must have a next instant")
	assert.Equal(t, testNow.Add(15*time.Minute), next, "a 15m market closes a quarter later")

	_, err = NextScanInstant(testNow, snapshot.Interval("7h"))
	require.ErrorIs(t, err, snapshot.ErrUnknownInterval,
		"an interval with no duration must be refused rather than defaulted")
}
