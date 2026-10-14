package snapshot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// freshSnapshotAt builds a snapshot whose every fact was read at `at`, so a
// test can age exactly the fact it means to age.
func freshSnapshotAt(t *testing.T, at time.Time) *MarketSnapshot {
	t.Helper()
	s := testSnapshot(t)
	s.BuiltAt = at
	s.SourceTime = at.Add(-30 * time.Second)
	s.Candles.Closed = []CandleFact{candle(s.SourceTime, "100", "110", "95", "105", "10")}
	s.Candles.Start = s.SourceTime
	s.Candles.End = s.SourceTime
	s.Candles.Count = 1
	s.Ticker = &TickerFacts{Last: "105", Bid: "104.9", Ask: "105.1", ReadAt: at}
	s.OrderBook = &OrderBookFacts{
		Bids:   []OrderBookLevel{{Price: "104.9", Size: "1"}},
		Asks:   []OrderBookLevel{{Price: "105.1", Size: "1"}},
		ReadAt: at,
	}
	s.Funding = &FundingFacts{LatestRate: "0.0001", SettlementsPerDay: 3, ReadAt: at}
	s.Account = &AccountFacts{TotalEquityUSD: "100", AvailableBalanceUSD: "100", ReadAt: at}
	s.Quality = map[string]candidate.DataQuality{
		CategoryCandles:   candidate.QualityExchangeREST,
		CategoryTicker:    candidate.QualityExchangeREST,
		CategoryOrderBook: candidate.QualityExchangeREST,
		CategoryFunding:   candidate.QualityExchangeREST,
		CategoryAccount:   candidate.QualityExchangeREST,
	}
	return s
}

func TestCandleMaxAge(t *testing.T) {
	t.Parallel()

	for interval, want := range map[Interval]time.Duration{
		Interval1m:  2*time.Minute + CandleGrace,
		Interval15m: 30*time.Minute + CandleGrace,
		Interval1h:  2*time.Hour + CandleGrace,
		Interval4h:  8*time.Hour + CandleGrace,
		Interval1d:  48*time.Hour + CandleGrace,
	} {
		got, err := CandleMaxAge(interval)
		require.NoErrorf(t, err, "%s must have a limit", interval)
		assert.Equalf(t, want, got, "%s should allow two bars plus the grace period", interval)
	}

	_, err := CandleMaxAge("7h")
	require.ErrorIs(t, err, ErrUnknownInterval, "an unknown interval has no limit, so the gate must refuse rather than guess")
}

func TestFreshSnapshotMayDecide(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	report, err := EvaluateFreshness(s, at.Add(10*time.Second))
	require.NoError(t, err, "a freshly built snapshot must be judgeable")

	assert.True(t, report.MayDecide(), "a snapshot ten seconds old should not be refused")
	assert.Empty(t, report.BlockingStale, "nothing should be stale")
	assert.Empty(t, report.NonBlockingStale, "nothing should be stale")
	assert.Empty(t, report.ReasonCodes, "a clean snapshot carries no reason code")
	// The candle is the oldest fact here: it closed 30s before the build and the
	// evaluation is 10s after it, so 40s. The account reading is the newest.
	assert.Equal(t, 40*time.Second, report.WorstAge(), "the worst age should be the oldest fact's, not the snapshot's")
	assert.Len(t, report.Facts, 5, "every carried category should be assessed")
}

func TestEachBlockingFactCanStopADecision(t *testing.T) {
	t.Parallel()

	at := testTime()
	// One case per blocking category: ageing it past its own limit while the
	// rest stay fresh must refuse the decision. A gate that only reacts to the
	// snapshot as a whole would pass this for the wrong reason.
	for name, age := range map[string]func(*MarketSnapshot){
		CategoryTicker: func(s *MarketSnapshot) { s.Ticker.ReadAt = at.Add(-TickerMaxAge - time.Second) },
		CategoryOrderBook: func(s *MarketSnapshot) {
			s.OrderBook.ReadAt = at.Add(-OrderBookMaxAge - time.Second)
		},
		CategoryAccount: func(s *MarketSnapshot) {
			s.Account.ReadAt = at.Add(-LocalPositionMaxAge - time.Second)
		},
		CategoryCandles: func(s *MarketSnapshot) {
			stale := at.Add(-2*time.Hour - CandleGrace - time.Second)
			s.Candles.Interval = Interval1h
			s.Candles.Closed = []CandleFact{candle(stale, "100", "110", "95", "105", "10")}
			s.SourceTime = stale
		},
	} {
		s := freshSnapshotAt(t, at)
		// The candle case needs the 1h limit; the fixture defaults to 1h already.
		age(s)
		report, err := EvaluateFreshness(s, at)
		require.NoErrorf(t, err, "%s must be judgeable", name)
		assert.Falsef(t, report.MayDecide(), "a stale %s should stop the decision", name)
		assert.Containsf(t, report.BlockingStale, name, "the %s category should be reported", name)
		assert.Containsf(t, report.ReasonCodes, CodeRiskSnapshotStale, "the %s case should carry the stale code", name)
	}
}

func TestExactBoundaryIsFresh(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	// Exactly at the limit is inside it: the threshold is a maximum age, and a
	// value of exactly the maximum has not exceeded it.
	s.Ticker.ReadAt = at.Add(-TickerMaxAge)
	report, err := EvaluateFreshness(s, at)
	require.NoError(t, err, "the boundary must be judgeable")
	assert.NotContains(t, report.BlockingStale, CategoryTicker, "exactly at the limit should be accepted")

	s.Ticker.ReadAt = at.Add(-TickerMaxAge - time.Nanosecond)
	report, err = EvaluateFreshness(s, at)
	require.NoError(t, err, "one nanosecond past the limit must be judgeable")
	assert.Contains(t, report.BlockingStale, CategoryTicker, "one nanosecond past the limit should be refused")
}

func TestFundingIsStaleButNotBlocking(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Funding.ReadAt = at.Add(-FundingMaxAge - time.Minute)

	report, err := EvaluateFreshness(s, at)
	require.NoError(t, err, "a stale funding read must be judgeable")
	assert.True(t, report.MayDecide(),
		"a stale funding read must not stop trading: it settles every eight hours and refusing on it would halt most of the cycle")
	assert.Contains(t, report.NonBlockingStale, CategoryFunding, "it should still be recorded as stale")
	assert.Empty(t, report.BlockingStale, "it should not appear among the blocking categories")
	assert.Empty(t, report.ReasonCodes, "a tolerated staleness carries no refusal code")
	assert.Contains(t, report.StaleCategories(), CategoryFunding, "the stale list should still name it")
}

func TestBlockingAndNonBlockingMix(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Funding.ReadAt = at.Add(-FundingMaxAge - time.Minute)
	s.OrderBook.ReadAt = at.Add(-OrderBookMaxAge - time.Second)

	report, err := EvaluateFreshness(s, at)
	require.NoError(t, err, "the mixture must be judgeable")
	assert.False(t, report.MayDecide(), "a stale book should stop the decision even when funding is the only other problem")
	assert.Equal(t, []string{CategoryOrderBook}, report.BlockingStale, "only the book should block")
	assert.Equal(t, []string{CategoryFunding}, report.NonBlockingStale, "funding should stay tolerated")
	assert.Equal(t, []string{CategoryOrderBook, CategoryFunding}, report.StaleCategories(),
		"the combined list must hold both, blocking first")
}

func TestAbsentCategoriesAreNotStale(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Ticker = nil
	s.Funding = nil

	report, err := EvaluateFreshness(s, at)
	require.NoError(t, err, "a snapshot without a ticker must still be judgeable")
	assert.True(t, report.MayDecide(),
		"an absent fact is not an old fact; whether it is required is the caller's question")

	var tickerAge *FactAge
	for i := range report.Facts {
		if report.Facts[i].Category == CategoryTicker {
			tickerAge = &report.Facts[i]
		}
	}
	require.NotNil(t, tickerAge, "an absent category must still be listed, so a reader can see it was considered")
	assert.True(t, tickerAge.Absent, "it should be marked absent")
	assert.False(t, tickerAge.Stale, "absent is not stale")
	assert.Zero(t, tickerAge.Age, "an absent fact has no age")
}

func TestMissingReadTimeIsRefusedNotStale(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Ticker.ReadAt = time.Time{}

	_, err := EvaluateFreshness(s, at)
	require.ErrorIs(t, err, ErrMissingReadTime,
		"a fact with no read time is a broken builder, not stale data; reporting it as stale would send someone to the exchange instead of the assembler")
	assert.Contains(t, err.Error(), CategoryTicker, "the message should name the category")
}

func TestFutureFactIsRefused(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.OrderBook.ReadAt = at.Add(time.Second)

	_, err := EvaluateFreshness(s, at)
	require.ErrorIs(t, err, ErrFactFromTheFuture,
		"a fact stamped after the evaluation instant means the clock or the source is wrong, and its real age is unknown")

	// A snapshot with no facts at all is still judgeable: there is nothing whose
	// age could be wrong.
	empty := &MarketSnapshot{ID: testUUID("9"), Candles: CandleWindow{Interval: Interval1h}}
	report, err := EvaluateFreshness(empty, at)
	require.NoError(t, err, "an empty snapshot must be judgeable")
	assert.True(t, report.MayDecide(), "nothing present means nothing stale")
	assert.Zero(t, report.WorstAge(), "an empty snapshot has no age")

	_, err = EvaluateFreshness(nil, at)
	require.ErrorIs(t, err, ErrSnapshotIncomplete, "a nil snapshot must be refused")
}

func TestInvalidIntervalIsRefused(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Candles.Interval = "3h"
	_, err := EvaluateFreshness(s, at)
	require.ErrorIs(t, err, ErrUnknownInterval,
		"without a known interval the candle limit is unknown, and an unknown limit must not be assumed")
}

func TestApplyFreshnessMarksStaleOnly(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	s.OrderBook.ReadAt = at.Add(-OrderBookMaxAge - time.Second)
	s.Funding.ReadAt = at.Add(-FundingMaxAge - time.Minute)

	report, err := s.ApplyFreshness(at)
	require.NoError(t, err, "marking must succeed")
	assert.False(t, report.MayDecide(), "the book should still block")

	assert.Equal(t, candidate.QualityStale, s.QualityOf(CategoryOrderBook),
		"a stale blocking category must be labelled stale on the snapshot")
	assert.Equal(t, candidate.QualityStale, s.QualityOf(CategoryFunding),
		"a stale tolerated category must be labelled too, so a reader sees it")
	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(CategoryTicker),
		"a fresh category must keep its label")
	assert.Contains(t, s.DegradedCategories(), CategoryOrderBook,
		"the marked snapshot must now report the degraded category through the ordinary path")
}

func TestApplyFreshnessDoesNotWeakenAnExistingLabel(t *testing.T) {
	t.Parallel()

	at := testTime()
	s := freshSnapshotAt(t, at)
	// A gap is a stronger statement than staleness: the data is not merely old,
	// it is missing. Marking must not replace it with the weaker label.
	require.NoError(t, s.SetQuality(CategoryCandles, candidate.QualityGap), "the fixture must label a gap")
	stale := at.Add(-2*time.Hour - CandleGrace - time.Second)
	s.Candles.Closed = []CandleFact{candle(stale, "100", "110", "95", "105", "10")}
	s.SourceTime = stale

	_, err := s.ApplyFreshness(at)
	require.NoError(t, err, "marking must succeed")
	assert.Equal(t, candidate.QualityGap, s.QualityOf(CategoryCandles),
		"a stronger degradation must survive a staleness marking")
}

func TestAutoCandidateDeadline(t *testing.T) {
	t.Parallel()

	source := testTime()
	assert.Equal(t, source.Add(AutoCandidateWindow), AutoCandidateDeadline(source, time.Time{}),
		"with no pipeline deadline the window decides")
	assert.Equal(t, source.Add(AutoCandidateWindow), AutoCandidateDeadline(source, source.Add(time.Hour)),
		"a later pipeline deadline does not extend the window")
	assert.Equal(t, source.Add(30*time.Second), AutoCandidateDeadline(source, source.Add(30*time.Second)),
		"an earlier pipeline deadline wins, so the two cannot disagree about when the run ends")
}

func TestExecutionFreshness(t *testing.T) {
	t.Parallel()

	now := testTime()

	fresh, err := EvaluateExecutionFreshness(now.Add(-time.Second), now.Add(-2*time.Second), now)
	require.NoError(t, err, "a two-second-old reading must be judgeable")
	assert.True(t, fresh.MaySubmit(), "readings seconds old should permit submission")
	assert.Equal(t, time.Second, fresh.PositionAge, "the position age should be measured")
	assert.Equal(t, 2*time.Second, fresh.BookAge, "the book age should be measured")
	assert.Empty(t, fresh.ReasonCodes, "a fresh pre-submission check carries no code")

	stalePosition, err := EvaluateExecutionFreshness(now.Add(-6*time.Second), now, now)
	require.NoError(t, err, "a stale position must be judgeable")
	assert.False(t, stalePosition.MaySubmit(), "a six-second-old position should block submission")
	assert.True(t, stalePosition.PositionStale, "the position should be the failing reading")
	assert.Contains(t, stalePosition.ReasonCodes, CodeRiskAccountInventoryStale, "the code should be the account one")
	assert.Less(t, ExecutionPositionMaxAge, LocalPositionMaxAge,
		"the pre-submission limit must be far tighter than the analysis limit: the size that leaves the machine comes from a moment-old reading")

	staleBook, err := EvaluateExecutionFreshness(now, now.Add(-6*time.Second), now)
	require.NoError(t, err, "a stale book must be judgeable")
	assert.False(t, staleBook.MaySubmit(), "a six-second-old book should block submission")
	assert.True(t, staleBook.BookStale, "the book should be the failing reading")

	// Exactly at the limit is inside it.
	boundary, err := EvaluateExecutionFreshness(now.Add(-ExecutionPositionMaxAge), now.Add(-ExecutionBookMaxAge), now)
	require.NoError(t, err, "the boundary must be judgeable")
	assert.True(t, boundary.MaySubmit(), "exactly at the limit should be accepted")

	_, err = EvaluateExecutionFreshness(time.Time{}, now, now)
	require.ErrorIs(t, err, ErrMissingReadTime, "a missing position read time must be refused")
	_, err = EvaluateExecutionFreshness(now.Add(time.Minute), now, now)
	require.ErrorIs(t, err, ErrFactFromTheFuture, "a future reading must be refused")

	var nilCheck *ExecutionFreshness
	assert.False(t, nilCheck.MaySubmit(), "a nil check should not permit submission")
}

func TestThresholdsCoverEveryBlockingCategory(t *testing.T) {
	t.Parallel()

	limits, err := Thresholds(Interval1h)
	require.NoError(t, err, "1h must have limits")

	// Every category the snapshot can carry and the gate must judge. A category
	// missing from the table would be silently unassessed, which is the failure
	// this test exists to prevent.
	for _, category := range Categories() {
		if category == CategoryIndicators {
			// Indicators are derived inside the process from candles, so they
			// have no independent age to measure; their freshness is the
			// candles' freshness.
			continue
		}
		limit, ok := limits[category]
		require.Truef(t, ok, "category %s must have a freshness limit", category)
		assert.Positivef(t, limit.MaxAge, "category %s should have a positive limit", category)
		assert.Equal(t, category, limit.Category, "the limit should name its own category")
	}

	assert.False(t, limits[CategoryFunding].Blocking, "funding should be the tolerated category")
	for _, category := range []string{CategoryCandles, CategoryTicker, CategoryOrderBook, CategoryAccount} {
		assert.Truef(t, limits[category].Blocking, "%s should block a decision when stale", category)
	}
}

func TestGateAgreesWithTheDegradedCategoryPath(t *testing.T) {
	t.Parallel()

	// The two refusal paths must not contradict each other: a snapshot the gate
	// refuses must also be one the quality path reports, so a caller that checks
	// only one of them does not get a different answer.
	at := testTime()
	s := freshSnapshotAt(t, at)
	s.Ticker.ReadAt = at.Add(-TickerMaxAge - time.Second)

	report, err := s.ApplyFreshness(at)
	require.NoError(t, err, "marking must succeed")
	assert.False(t, report.MayDecide(), "the gate should refuse")
	assert.Contains(t, s.DegradedCategories(), CategoryTicker,
		"the quality path must agree that the ticker is unusable")
	assert.Contains(t, report.ReasonCodes, CodeRiskSnapshotStale, "the stale code should be present")
	assert.Contains(t, report.ReasonCodes, CodeRiskSnapshotQuality,
		"the quality code must follow once the label is applied")
}
