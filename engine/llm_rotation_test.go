package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// rotationSource is a fake exchange for the rotation. It records what was asked
// for, which is how the scheduling is checked rather than assumed.
type rotationSource struct {
	enabled    currency.Pairs
	enabledErr error

	// candles, when set, is what GetHistoricCandles returns. Tests that only
	// care about the schedule leave it nil and get an empty window.
	candles *kline.Item

	fetched   []string
	windows   []time.Duration
	fetchErr  map[string]error
	tickers   int
	tickerErr error
	perMarket int
}

func (s *rotationSource) GetEnabledPairs(asset.Item) (currency.Pairs, error) {
	return s.enabled, s.enabledErr
}

func (s *rotationSource) GetHistoricCandles(_ context.Context, pair currency.Pair, _ asset.Item, _ kline.Interval, start, end time.Time) (*kline.Item, error) {
	id := pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String()
	if err := s.fetchErr[id]; err != nil {
		return nil, err
	}
	s.fetched = append(s.fetched, id)
	s.windows = append(s.windows, end.Sub(start))
	if s.candles != nil {
		return s.candles, nil
	}
	return &kline.Item{Interval: kline.OneHour}, nil
}

func (s *rotationSource) UpdateTicker(context.Context, currency.Pair, asset.Item) (*ticker.Price, error) {
	s.perMarket++
	return &ticker.Price{}, nil
}

func (s *rotationSource) UpdateTickers(context.Context, asset.Item) error {
	s.tickers++
	return s.tickerErr
}

func pairsOf(ids ...string) currency.Pairs {
	pairs := make(currency.Pairs, 0, len(ids))
	for _, id := range ids {
		base, quote, _ := splitPairID(id)
		pairs = append(pairs, currency.NewPairWithDelimiter(base, quote, "_"))
	}
	return pairs
}

func splitPairID(id string) (exchangeName, pairName string, ok bool) {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '_' {
			return id[:i], id[i+1:], true
		}
	}
	return id, "", false
}

// fixedClock is an injectable clock so a step's timing is reproducible.
type fixedClock struct{ at time.Time }

func (c *fixedClock) now() time.Time          { return c.at }
func (c *fixedClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newRotator(t *testing.T, src *rotationSource, clock *fixedClock, markets int) *Rotator {
	t.Helper()
	ids := make([]string, 0, markets)
	for i := range markets {
		// The base must not contain the delimiter: currency normalises a code,
		// so "SYM_A" would come back as "SYMA".
		ids = append(ids, "SYM"+string(rune('A'+i))+"_USDT")
	}
	src.enabled = pairsOf(ids...)
	r, err := NewRotator(src, &RotationConfig{
		Asset:    asset.USDTMarginedFutures,
		Interval: kline.OneHour,
		Plan:     coverage.DefaultPlan(),
		Now:      clock.now,
	})
	require.NoError(t, err, "the rotator must build")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the markets must load")
	return r
}

func TestRotatorCoversEveryEnabledMarketOncePerCycle(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 5)

	// The first step opens the sweep with the batched ticker call.
	first, err := r.Step(context.Background())
	require.NoError(t, err, "the first step must succeed")
	assert.Equal(t, StepTickerBatch, first.Kind, "a sweep should open with the batched ticker refresh")
	assert.Equal(t, 1, src.tickers, "one call should cover every market")

	// Then one visit per market, in the plan's order.
	for i := range 5 {
		outcome, stepErr := r.Step(context.Background())
		require.NoErrorf(t, stepErr, "step %d must succeed", i)
		assert.Equal(t, StepKlineIncrement, outcome.Kind, "every following step visits one market")
		assert.NotEmpty(t, outcome.Market, "the visited market should be named")
	}
	assert.Len(t, src.fetched, 5, "every enabled market should be visited once")
	assert.Equal(t, []string{"SYMA_USDT", "SYMB_USDT", "SYMC_USDT", "SYMD_USDT", "SYME_USDT"},
		r.Markets(), "the cycle must be in the plan's sorted order")

	// The next step closes the sweep and immediately starts the next one, so a
	// completed sweep costs no idle step.
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "closing the sweep must succeed")
	assert.True(t, outcome.CycleCompleted, "the sweep should be reported as completed")
	assert.Equal(t, int64(1), r.Stats().CyclesCompleted, "the completed sweep should be counted")
	assert.Equal(t, StepKlineIncrement, outcome.Kind, "the step that closes a sweep also starts the next")
	assert.Equal(t, "SYMA_USDT", outcome.Market, "the new sweep should begin at the first market")
	assert.Equal(t, 1, r.Stats().VisitedThisCycle, "the visit that started the new sweep should be counted")

	// The batched ticker is not refreshed here: it is tied to the cycle period,
	// which has not elapsed.
	assert.Equal(t, 1, src.tickers, "the ticker refresh should follow the cycle period, not the sweep boundary")

	// Every increment asked for a window sized from the plan's limit, not an
	// arbitrary span: layer one only needs enough bars to notice a close.
	for _, window := range src.windows {
		assert.Equal(t, 5*time.Hour, window, "the increment window should be the plan's limit in bars times the interval")
	}
}

func TestBatchedTickerRunsAtMostOncePerCycle(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 3)

	// One sweep: ticker, then three visits.
	for i := range 4 {
		_, err := r.Step(context.Background())
		require.NoErrorf(t, err, "step %d must succeed", i)
	}
	assert.Equal(t, 1, src.tickers, "the batched ticker should not be called again inside the same cycle period")

	// Let the cycle period elapse: the ticker is refreshed again, because a
	// ticker older than one cycle is exactly what layer one exists to prevent.
	clock.advance(10 * time.Minute)
	_, err := r.Step(context.Background())
	require.NoError(t, err, "a step after the cycle period must succeed")
	assert.Equal(t, 2, src.tickers, "the ticker should be refreshed once the cycle period has elapsed")
}

func defaultScannerParams() *snapshot.IndicatorParams {
	p := snapshot.DefaultIndicatorParams()
	return &p
}

func TestRotationKeepsGoingWhenOneMarketFails(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{"SYMB_USDT": errors.New("market unavailable")}}
	r := newRotator(t, src, clock, 3)

	_, err := r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed")

	var failed int
	for range 3 {
		outcome, stepErr := r.Step(context.Background())
		require.NoError(t, stepErr, "a failing market must not fail the step: one unreachable market must not stop the sweep")
		if outcome.Failed {
			failed++
			assert.Equal(t, "SYMB_USDT", outcome.Market, "the failing market should be named so an operator can see it")
		}
	}
	assert.Equal(t, 1, failed, "exactly one market failed")
	assert.Equal(t, int64(1), r.Stats().Failures, "the failure should be counted")

	// The cursor advanced past the failure, so the sweep is not stuck on it.
	assert.Len(t, src.fetched, 2, "the two healthy markets should still have been fetched")
	assert.Equal(t, int64(0), r.Stats().CyclesCompleted, "the sweep is not finished yet")
}

func TestRotationPausesWhenTheWeightBudgetIsFull(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}

	// A budget that cannot hold the batched ticker call, so the very first step
	// is refused rather than pushing the window over the limit.
	plan := coverage.DefaultPlan()
	plan.WeightBudgetFraction = 0.015 // 36 weight/min, under the batch call's 40
	r, err := NewRotator(src, &RotationConfig{
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Plan: plan, Now: clock.now,
	})
	require.NoError(t, err, "the rotator must build")
	src.enabled = pairsOf("BTC_USDT")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the markets must load")

	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "a full budget is a wait, not an error")
	assert.True(t, outcome.Paused, "the step should report that it was paused")
	assert.Positive(t, outcome.RetryAfter, "a paused step should say how long to wait")
	assert.Empty(t, outcome.Kind, "no work should have run")
	assert.Equal(t, 0, src.tickers, "the exchange should not have been called")

	stats := r.Stats()
	assert.Equal(t, 36, stats.WeightBudgetPerMinute, "the budget should be the plan's own figure")
	assert.LessOrEqual(t, stats.WeightUsedThisMinute, stats.WeightBudgetPerMinute,
		"pausing must leave the window inside its budget")
}

func TestStatsReportMeasuredState(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 4)

	before := r.Stats()
	assert.Equal(t, 4, before.Markets, "the covered count should be reported")
	assert.Equal(t, 10*time.Minute, before.CycleDuration, "the cycle length should come from the plan")
	assert.Equal(t, 1440, before.WeightBudgetPerMinute, "the budget should be sixty percent of the exchange limit")
	assert.Zero(t, before.VisitedThisCycle, "nothing is visited before the first step")
	assert.Zero(t, before.CycleProgressPercent, "progress starts at zero")

	_, err := r.Step(context.Background()) // ticker
	require.NoError(t, err, "the ticker step must succeed")
	_, err = r.Step(context.Background()) // first market
	require.NoError(t, err, "the first visit must succeed")

	after := r.Stats()
	assert.Equal(t, 1, after.VisitedThisCycle, "one market has been visited")
	assert.InDelta(t, 25.0, after.CycleProgressPercent, 0.01, "progress should be a percentage of the covered markets")
	assert.Equal(t, coverage.WeightTickerBatch+1, after.WeightUsedThisMinute,
		"the window must hold exactly the batch call and the increment")
	assert.False(t, after.LastTickerRefresh.IsZero(), "the ticker refresh time should be recorded")
	assert.Zero(t, after.OldestMarketAge, "the second market was just visited, so the coldest age is measured from the sweep start")

	// Time passes without a visit: the coldest market gets colder, which is the
	// number that answers "how warm is the coldest market".
	clock.advance(3 * time.Minute)
	later := r.Stats()
	assert.Equal(t, 3*time.Minute, later.OldestMarketAge,
		"an unvisited market's age must grow with time, measured from the sweep start")
}

func TestRefreshMarketsHandlesAnEmptyList(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r, err := NewRotator(src, &RotationConfig{
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Plan: coverage.DefaultPlan(), Now: clock.now,
	})
	require.NoError(t, err, "the rotator must build")

	// Nothing enabled is a normal state at startup, not a configuration error.
	require.NoError(t, r.RefreshMarkets(context.Background()), "an empty enabled list must not be an error")
	assert.Empty(t, r.Markets(), "nothing is covered")
	assert.Zero(t, r.Stats().Markets, "the stats should report no coverage")

	_, err = r.Step(context.Background())
	require.ErrorIs(t, err, ErrRotatorNotPlanned,
		"a step with no cycle must be refused rather than reporting an empty outcome")

	// Enabling a market makes it covered without rebuilding the rotator, which
	// is what an operator enabling a pair expects.
	src.enabled = pairsOf("BTC_USDT")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the list must reload")
	assert.Equal(t, []string{"BTC_USDT"}, r.Markets(), "the newly enabled market should be covered")
	_, err = r.Step(context.Background())
	require.NoError(t, err, "a step must work once a market is covered")
}

func TestRotationRefusesMisuse(t *testing.T) {
	t.Parallel()

	_, err := NewRotator(nil, &RotationConfig{})
	require.ErrorIs(t, err, ErrNilRotationSource, "a nil source must be refused")

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	_, err = NewRotator(src, &RotationConfig{
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour,
		Plan: coverage.Plan{Enabled: true, RotationCycleMinutes: 999},
		Now:  clock.now,
	})
	require.ErrorIs(t, err, coverage.ErrInvalidCycle, "an impossible plan must be refused at construction")

	src.enabledErr = errors.New("exchange not ready")
	r, err := NewRotator(src, &RotationConfig{
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Plan: coverage.DefaultPlan(), Now: clock.now,
	})
	require.NoError(t, err, "construction must not depend on the exchange being ready")
	err = r.RefreshMarkets(context.Background())
	require.Error(t, err, "a failed read must surface")
	assert.Contains(t, err.Error(), "exchange not ready", "the reason should survive the wrapping")

	var nilRotator *Rotator
	_, err = nilRotator.Step(context.Background())
	require.ErrorIs(t, err, ErrNilRotator, "a nil rotator must refuse rather than panic")
	assert.Empty(t, nilRotator.Markets(), "a nil rotator covers nothing")
	assert.Zero(t, nilRotator.NextSpacing(), "a nil rotator has no schedule")
	require.ErrorIs(t, nilRotator.RefreshMarkets(context.Background()), ErrNilRotator,
		"a nil rotator must refuse rather than panic")
}

func TestCycleRebuildDropsTheOldCursor(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 4)

	_, err := r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed")
	for i := range 3 {
		_, err = r.Step(context.Background())
		require.NoErrorf(t, err, "visit %d must succeed", i)
	}
	require.Equal(t, 3, r.Stats().VisitedThisCycle, "three markets must have been visited")

	// The list changes. Carrying the old cursor over would skip the markets that
	// moved position in the new ordering, so the sweep restarts.
	src.enabled = pairsOf("AAA_USDT", "BBB_USDT", "CCC_USDT", "DDD_USDT", "EEE_USDT")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the list must reload")
	assert.Equal(t, 5, r.Stats().Markets, "the new count should be reported")
	assert.Zero(t, r.Stats().VisitedThisCycle, "a rebuilt cycle should start with nothing visited")

	// The first step after a rebuild is a visit, not a ticker refresh: the
	// ticker is tied to the cycle period, which has not elapsed.
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "the first visit after a rebuild must succeed")
	assert.Equal(t, "AAA_USDT", outcome.Market, "the sweep should start at the beginning of the new ordering")
}

func TestRotationSatisfiesItsInterfaceWithTheExchange(_ *testing.T) {
	// A compile-time assertion: the exchange itself must satisfy the rotation's
	// narrow interface, so production wiring passes the exchange with no adapter.
	var _ iRotationSource = exchange.IBotExchange(nil)
}

func TestRunPacesItselfAndStopsOnContext(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 4)

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		// The clock advances with the sleep, so the next step sees a later
		// instant without any real time passing.
		clock.advance(d)
		return nil
	}

	// Four markets over a ten-minute cycle is a 150-second spacing, so four
	// steps take exactly one period. Within that period the loop makes one
	// batched ticker call and visits three markets; the fourth visit would land
	// on the period boundary, where the ticker refresh takes precedence. That
	// interaction is the reason the sweep can take slightly longer than the
	// nominal cycle, and it is asserted rather than assumed.
	ran, err := r.Run(context.Background(), 4, sleep)
	require.NoError(t, err, "the loop must run")
	assert.Equal(t, 4, ran, "every requested unit should run when nothing pauses")
	assert.Len(t, waits, 4, "the loop should wait after each unit so the sweep is spread out")
	for _, wait := range waits {
		assert.Equal(t, r.NextSpacing(), wait,
			"the wait must be the plan's own spacing: a caller-chosen delay would defeat the even spread")
	}
	assert.Equal(t, 1, src.tickers, "one batched ticker call inside one cycle period")
	assert.Len(t, src.fetched, 3, "three of the four markets are visited inside one period")

	// The next step crosses the period boundary, so it refreshes the ticker
	// instead of visiting the last market.
	ran, err = r.Run(context.Background(), 1, sleep)
	require.NoError(t, err, "the boundary step must succeed")
	assert.Equal(t, 1, ran, "the boundary step is still work")
	assert.Equal(t, 2, src.tickers, "the ticker should be refreshed once the cycle period has elapsed")
	assert.Len(t, src.fetched, 3, "no market is visited on the boundary step")
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 4)

	ctx, cancel := context.WithCancel(context.Background())
	units := 0
	sleep := func(context.Context, time.Duration) error {
		units++
		if units == 2 {
			cancel()
		}
		return nil
	}
	ran, err := r.Run(ctx, 10, sleep)
	require.ErrorIs(t, err, context.Canceled, "a cancelled context must end the run")
	assert.Less(t, ran, 10, "the run should stop early rather than finish the requested units")
}

func TestRunWaitsOutAPausedBudget(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}

	// A budget that cannot hold the batched call, so every step is refused.
	plan := coverage.DefaultPlan()
	plan.WeightBudgetFraction = 0.015
	r, err := NewRotator(src, &RotationConfig{
		Asset: asset.USDTMarginedFutures, Interval: kline.OneHour, Plan: plan, Now: clock.now,
	})
	require.NoError(t, err, "the rotator must build")
	src.enabled = pairsOf("BTC_USDT")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the markets must load")

	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		clock.advance(d)
		return nil
	}
	ran, err := r.Run(context.Background(), 2, sleep)
	require.NoError(t, err, "a paused budget is a wait, not a failure")
	assert.Zero(t, ran, "no unit completed while the budget was full")
	assert.Len(t, waits, 2, "the loop should wait rather than spin")
	for _, wait := range waits {
		assert.Positive(t, wait, "the wait should be the gate's own figure, not zero")
	}
	assert.Equal(t, 0, src.tickers, "the exchange should not have been called")
}

func TestFullReloadRunsInsideItsWindowAndAsksForTheFullHistory(t *testing.T) {
	t.Parallel()

	// Start ten minutes into the hour, so the ticker is already due and the
	// reload window (five minutes) has passed.
	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 3)

	_, err := r.Step(context.Background()) // ticker
	require.NoError(t, err, "the ticker step must succeed")
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "an increment must succeed")
	assert.Equal(t, StepKlineIncrement, outcome.Kind, "outside the reload window the rotation increments")
	assert.Zero(t, r.Stats().ReloadsThisHour, "no reload outside its window")

	// Move to the top of the next hour: the reload begins and takes precedence
	// over the increments, because the indicator window is only as good as the
	// history it was computed from.
	clock.at = time.Date(2026, 10, 10, 13, 0, 30, 0, time.UTC)
	src.fetched, src.windows = nil, nil

	// The hour boundary refreshes the batched ticker first, then the reload runs.
	outcome, err = r.Step(context.Background())
	require.NoError(t, err, "the hour boundary step must succeed")
	assert.Equal(t, StepTickerBatch, outcome.Kind, "the batched ticker is refreshed at the hour boundary")

	for i := range 3 {
		outcome, err = r.Step(context.Background())
		require.NoErrorf(t, err, "reload %d must succeed", i)
		assert.Equal(t, StepKlineFullReload, outcome.Kind, "the reload should take precedence inside its window")
		assert.NotEmpty(t, outcome.Market, "each reload should name its market")
	}
	assert.Equal(t, 3, r.Stats().ReloadsThisHour, "every market should be reloaded once this hour")
	assert.Len(t, src.fetched, 3, "each reload should fetch once")

	// The reload asks for the full indicator history, not the increment window.
	// Getting this wrong would rebuild the indicators from a window too short to
	// compute them.
	wantWindow := time.Duration(r.Plan().KlineFullLimit+1) * time.Hour
	for _, window := range src.windows {
		assert.Equal(t, wantWindow, window, "the reload should ask for the full indicator history")
	}

	// Once this hour's reload is done, the rotation returns to increments.
	outcome, err = r.Step(context.Background())
	require.NoError(t, err, "the step after the reload must succeed")
	assert.Equal(t, StepKlineIncrement, outcome.Kind, "once reloaded, the sweep resumes")
}

func TestFullReloadDoesNotRepeatWithinTheSameHour(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 0, 30, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 2)

	// The first step of all is the batched ticker, then the reload.
	_, err := r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed")
	for i := range 2 {
		_, err = r.Step(context.Background())
		require.NoErrorf(t, err, "reload %d must succeed", i)
	}
	assert.Equal(t, 2, r.Stats().ReloadsThisHour, "both markets should be reloaded")

	// Still inside the same hour and the same window: the reload must not start
	// again, or a market would be reloaded repeatedly for the whole window.
	clock.advance(30 * time.Second)
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "a later step must succeed")
	assert.NotEqual(t, StepKlineFullReload, outcome.Kind, "a completed reload should not repeat inside the same hour")
	assert.Equal(t, 2, r.Stats().ReloadsThisHour, "the count should not grow")

	// The next hour reloads again. The batched ticker is checked first, so it is
	// refreshed before the reload begins — both belong to the hour boundary and
	// neither may be skipped.
	clock.at = time.Date(2026, 10, 10, 13, 0, 10, 0, time.UTC)
	outcome, err = r.Step(context.Background())
	require.NoError(t, err, "the next hour must succeed")
	assert.Equal(t, StepTickerBatch, outcome.Kind, "the hour boundary refreshes the ticker first")
	assert.Zero(t, r.Stats().ReloadsThisHour, "the reload has not begun yet")

	outcome, err = r.Step(context.Background())
	require.NoError(t, err, "the reload must follow")
	assert.Equal(t, StepKlineFullReload, outcome.Kind, "the reload should run once per hour")
	assert.Equal(t, 1, r.Stats().ReloadsThisHour, "the hour's count should restart")
}

func TestReloadWeightMatchesThePlansAssumption(t *testing.T) {
	t.Parallel()

	// The reload's per-request cost must be the one the plan's budget arithmetic
	// assumed, or the budget describes a load the rotation does not produce.
	plan := coverage.DefaultPlan()
	cycle, err := coverage.BuildCycle(&plan, []string{"BTC_USDT", "ETH_USDT", "SOL_USDT"})
	require.NoError(t, err, "the plan must build")

	perMarket := cycle.Budget.ReloadTotalWeight / len(cycle.Markets)
	assert.Equal(t, perMarket, klineWeightFor(plan.KlineFullLimit),
		"the reload's charged weight must equal the plan's per-market reload assumption")

	for limit, want := range map[int]int{5: 1, 99: 1, 250: 2, 499: 2, 500: 5, 1000: 10} {
		assert.Equalf(t, want, klineWeightFor(limit), "limit %d should cost %d", limit, want)
	}
}

// scanWindow builds a candle window long enough for MACD, with a shape that
// makes the histogram sign definite: a fall then a rise turns it positive.
func scanWindow(pair currency.Pair, n int, endAt time.Time, shape func(i int) float64) *kline.Item {
	item := &kline.Item{
		Exchange: "Binance", Pair: pair, Asset: asset.USDTMarginedFutures, Interval: kline.OneHour,
	}
	// Hour-aligned so the newest bar is the one that opened this hour: that bar is
	// still forming at the evaluation instant, which is the case the scan has to
	// handle.
	lastOpen := endAt.Truncate(time.Hour)
	for i := range n {
		open := lastOpen.Add(-time.Duration(n-i-1) * time.Hour)
		price := shape(i)
		item.Candles = append(item.Candles, kline.Candle{
			Time: open, Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 1,
		})
	}
	return item
}

// fallingThenRising gives the histogram a definite sign. A linear ramp would
// not: the fast and slow averages converge to a constant gap, so the histogram
// settles at exactly zero and the sign fact is legitimately flat.
func fallingThenRising(n int) func(int) float64 {
	return func(i int) float64 {
		if i < n/2 {
			return 200 - float64(i)
		}
		return 200 - float64(n/2) + float64(i-n/2)*3
	}
}

func TestReloadFeedsTheScanner(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 0, 30, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 2)

	// The fake returns a window long enough for the indicators, so the scan has
	// something to compute from.
	// 220 candles so the 200-period average is available, which is what the
	// rotation's own 250-bar reload provides.
	src.candles = scanWindow(currency.NewBTCUSDT(), 220, clock.at, fallingThenRising(220))
	src.candles.Pair = currency.NewPairWithDelimiter("SYMA", "USDT", "_")

	scanner, err := NewTriggerScanner(trigger.DefaultPolicy(), clock.now)
	require.NoError(t, err, "the scanner must build")
	r.attachScanner(scanner, defaultScannerParams(), snapshot.Interval1h)

	// The first step is the batched ticker; the next is a reload, after which
	// the scanner must have been fed.
	_, err = r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed")
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "the reload must succeed")
	require.Equal(t, StepKlineFullReload, outcome.Kind, "the step must be a reload")

	queued := scanner.Drain(10)
	require.Len(t, queued, 1, "the reload must feed the scanner exactly once")
	assert.Equal(t, trigger.KindKlineClose, queued[0].Kind, "a reload after a new close should raise a close trigger")
	assert.Equal(t, "SYMA_USDT", queued[0].MarketID, "the event should name the reloaded market")
}

func TestScannerKeepsPerMarketPreviousStateAcrossReloads(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 0, 30, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 1)

	pair := currency.NewPairWithDelimiter("SYMA", "USDT", "_")
	src.candles = scanWindow(pair, 220, clock.at, fallingThenRising(220))

	policy := trigger.DefaultPolicy()
	policy.MinInterval = 0
	scanner, err := NewTriggerScanner(policy, clock.now)
	require.NoError(t, err, "the scanner must build")
	r.attachScanner(scanner, defaultScannerParams(), snapshot.Interval1h)

	// First hour: the scanner learns this market's state.
	_, err = r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed")
	_, err = r.Step(context.Background())
	require.NoError(t, err, "the first reload must succeed")
	scanner.Drain(10)

	// Second hour, same shape: one close, and no crossing, because the state is
	// unchanged. Without the per-market memory this would fire a crossing on
	// every scan by comparing against a zero-valued previous.
	clock.at = time.Date(2026, 10, 10, 13, 0, 30, 0, time.UTC)
	src.candles = scanWindow(pair, 220, clock.at, fallingThenRising(220))
	_, err = r.Step(context.Background()) // ticker
	require.NoError(t, err, "the ticker step must succeed")
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "the second reload must succeed")
	require.Equal(t, StepKlineFullReload, outcome.Kind, "the step must be a reload")

	events := scanner.Drain(10)
	require.Len(t, events, 1, "only the close must fire: the market's state did not change")
	assert.Equal(t, trigger.KindKlineClose, events[0].Kind,
		"an unchanged state must not be reported as a crossing")
}

func TestScannerIsOptional(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 0, 30, 0, time.UTC)}
	src := &rotationSource{fetchErr: map[string]error{}}
	r := newRotator(t, src, clock, 2)

	// No scanner attached: the rotation must work exactly as before, because a
	// rotation without a scanner is a valid configuration.
	assert.Nil(t, r.Scanner(), "no scanner means nil, not a zero-value scanner")

	_, err := r.Step(context.Background())
	require.NoError(t, err, "the ticker step must succeed without a scanner")
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "a reload must succeed without a scanner")
	assert.Equal(t, StepKlineFullReload, outcome.Kind, "the reload should still run")
	assert.False(t, outcome.Failed, "the absence of a scanner is not a failure")

	require.NoError(t, nilRotatorScan(context.Background()), "a nil-receiver scan must be a no-op")
}

// nilRotatorScan exercises the nil-receiver path through a typed nil.
func nilRotatorScan(_ context.Context) error {
	var r *Rotator
	return r.scanAfterReload(nil, time.Now())
}

func TestFactFromWindowUsesOnlyClosedCandles(t *testing.T) {
	t.Parallel()

	pair := currency.NewPairWithDelimiter("SYMA", "USDT", "_")
	// The window ends with a bar that has not closed, so the newest closed bar is
	// the one before it. Comparing against the forming bar would report a
	// crossing on data that is still moving.
	now := time.Date(2026, 10, 10, 12, 30, 0, 0, time.UTC)
	item := scanWindow(pair, 220, now, fallingThenRising(220))

	fact, err := factFromWindow(item, snapshot.Interval1h, defaultScannerParams(), nil, now)
	require.NoError(t, err, "a long enough window must produce a fact")
	assert.Equal(t, "SYMA_USDT", fact.MarketID, "the market should come from the pair")
	assert.Equal(t, snapshot.Interval1h, fact.Interval, "the interval should be carried")
	assert.Equal(t, now.Truncate(time.Hour).Add(-time.Hour), fact.LastClosedAt,
		"the newest closed bar is the one that opened an hour ago; the bar that opened this hour is still forming at the evaluation instant")
	assert.Equal(t, now.Truncate(time.Hour).Add(-2*time.Hour), fact.PreviousClosedAt,
		"the previous close must be the bar before it, so a close can be told from a re-read")
	assert.NotZero(t, fact.HistogramSign, "a rising window should produce a definite sign")
	assert.NotEmpty(t, fact.PriceVsMA, "the position against the longest average should be derived")
	assert.Positive(t, fact.ATR, "the volatility reading should be derived")
	assert.Equal(t, int64(200), fact.MAPeriod,
		"with enough history the longest configured average must be the one reported")

	// A shorter history falls back to the longest average the window can support
	// rather than losing the fact: a newly listed market must still be scannable.
	short, err := factFromWindow(scanWindow(pair, 60, now, fallingThenRising(60)), snapshot.Interval1h,
		defaultScannerParams(), nil, now)
	require.NoError(t, err, "a 60-candle window must still produce a fact")
	assert.Equal(t, int64(50), short.MAPeriod,
		"the longest average the window supports must be used rather than skipping the fact")
	assert.NotEmpty(t, short.PriceVsMA, "the position should still be derived")

	// Too short for the indicators: refused rather than reporting a default.
	_, err = factFromWindow(scanWindow(pair, 5, now, fallingThenRising(5)), snapshot.Interval1h, defaultScannerParams(), nil, now)
	require.ErrorIs(t, err, snapshot.ErrNotEnoughCandles,
		"a window too short for MACD must be refused, which is why the scan runs after a reload")

	_, err = factFromWindow(nil, snapshot.Interval1h, defaultScannerParams(), nil, now)
	require.ErrorIs(t, err, snapshot.ErrSnapshotIncomplete, "a nil window must be refused")
}

func TestAverageTrueRange(t *testing.T) {
	t.Parallel()

	pair := currency.NewPairWithDelimiter("SYMA", "USDT", "_")
	// Flat closes with a two-point range: every true range is 2, so the mean is 2.
	flat := &kline.Item{Exchange: "Binance", Pair: pair, Asset: asset.USDTMarginedFutures, Interval: kline.OneHour}
	for i := range 20 {
		flat.Candles = append(flat.Candles, kline.Candle{
			Time: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour),
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
		})
	}
	atr, err := averageTrueRange(flat, 14)
	require.NoError(t, err, "a long enough window must produce an ATR")
	assert.InDelta(t, 2, atr, 1e-9, "a flat series with a two-point range should average two")

	_, err = averageTrueRange(flat, 0)
	require.ErrorIs(t, err, snapshot.ErrIndicatorParamsInvalid, "a zero period must be refused")
	_, err = averageTrueRange(flat, 25)
	require.ErrorIs(t, err, snapshot.ErrNotEnoughCandles, "a period longer than the window must be refused")
}

func TestFullReloadFeedsTheKlineCache(t *testing.T) {
	t.Parallel()

	clock := &fixedClock{at: time.Date(2026, 10, 10, 12, 10, 0, 0, time.UTC)}
	candles := make([]kline.Candle, 0, 7)
	for i := range 7 {
		candles = append(candles, kline.Candle{
			Time: time.Date(2026, 10, 10, 7+i, 0, 0, 0, time.UTC),
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
		})
	}
	src := &rotationSource{
		enabled:  pairsOf("SYMA_USDT"),
		candles:  &kline.Item{Interval: kline.OneHour, Candles: candles},
		fetchErr: map[string]error{},
	}
	cache, err := NewKlineCache(4)
	require.NoError(t, err, "the cache must build")

	r, err := NewRotator(src, &RotationConfig{
		Asset:    asset.USDTMarginedFutures,
		Interval: kline.OneHour,
		Plan:     coverage.DefaultPlan(),
		Now:      clock.now,
		Cache:    cache,
	})
	require.NoError(t, err, "the rotator must build")
	require.NoError(t, r.RefreshMarkets(context.Background()), "the markets must load")

	_, err = r.Step(context.Background()) // ticker
	require.NoError(t, err, "the ticker step must succeed")
	_, err = r.Step(context.Background()) // increment
	require.NoError(t, err, "the increment step must succeed")
	assert.Zero(t, cache.Len(),
		"an increment must not be cached: only the reload's full window warms the cache")

	// The top of the next hour is when the reload runs.
	clock.at = time.Date(2026, 10, 10, 13, 0, 30, 0, time.UTC)
	outcome, err := r.Step(context.Background())
	require.NoError(t, err, "the hour boundary step must succeed")
	assert.Equal(t, StepTickerBatch, outcome.Kind, "the ticker is refreshed first")
	outcome, err = r.Step(context.Background())
	require.NoError(t, err, "the reload must succeed")
	require.Equal(t, StepKlineFullReload, outcome.Kind, "the reload runs inside its window")

	require.Equal(t, 1, cache.Len(), "the reload must warm exactly its market's key")
	pair := pairsOf("SYMA_USDT")[0]
	got := cache.closedCandles(pair, asset.USDTMarginedFutures, kline.OneHour,
		time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), clock.at)
	require.Len(t, got, 6, "six candles are closed at 13:00:30; the forming bar is never cached")
	assert.Equal(t, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), got[5].Time,
		"the newest cached candle is the one that closed at the top of the hour")

	// A pair leaving the enabled set must not keep being served from the cache.
	assert.Equal(t, 1, cache.InvalidatePair(pair, asset.USDTMarginedFutures),
		"the disabled market's key must drop")
	assert.Zero(t, cache.Len())
}
