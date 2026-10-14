package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// The rotation is layer one of the coverage design
// (docs/llm-dev/04-数据层-K线与指标.md §7.4): it keeps every tracked market
// warm so a trigger can be noticed anywhere, and it is deliberately slow. Layer
// two — fetching a fresh snapshot for the one market being decided on — is the
// assembler in llm_snapshot.go.
//
// What this file owns is the schedule and the bookkeeping: which market to visit
// next, whether the exchange's weight budget allows the visit, and the numbers a
// health page shows. What it does not own is the fetch itself, which sits behind
// a four-method interface so this logic is testable without an exchange.

// Errors the rotator raises.
var (
	// ErrRotatorNotPlanned marks a step taken before the market list was read.
	ErrRotatorNotPlanned = errors.New("rotator has no cycle; call RefreshMarkets first")
	// ErrNilRotationSource marks a rotator built without a source.
	ErrNilRotationSource = errors.New("rotation source is nil")
)

// iRotationSource is what the rotation needs from the exchange.
//
// It is four methods, not iExchangeManager: the rotation reads the enabled list,
// fetches one candle window, and refreshes the ticker (per market and in bulk).
// Keeping it this size is what lets the tests below run against a fake.
type iRotationSource interface {
	// GetEnabledPairs returns the markets being tracked. The rotation covers
	// these, not everything the exchange lists: an untracked market has no
	// warm data and nothing would read it.
	GetEnabledPairs(a asset.Item) (currency.Pairs, error)
	// GetHistoricCandles fetches a candle window.
	GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
	// UpdateTicker refreshes one market's cached ticker.
	UpdateTicker(ctx context.Context, p currency.Pair, a asset.Item) (*ticker.Price, error)
	// UpdateTickers refreshes every market's cached ticker in one call. It is
	// the batched form the coverage plan relies on: one request covers all
	// markets, which is what makes watching hundreds of them affordable.
	UpdateTickers(ctx context.Context, a asset.Item) error
}

// RotationConfig configures a rotator.
type RotationConfig struct {
	// Asset is the asset class to cover.
	Asset asset.Item
	// Interval is the candle interval the indicators use.
	Interval kline.Interval
	// Plan is the coverage plan: cycle length, limits and the weight budget.
	Plan coverage.Plan
	// Cache, when set, absorbs every reload's window: the same history the
	// scan just consumed becomes available to the snapshot and tool fetches,
	// which is one exchange weight not paid twice. Nil disables the feed.
	Cache *KlineCache
	// Now supplies the current instant. It is injected so a step is
	// reproducible and so tests do not sleep. Nil uses the wall clock.
	Now func() time.Time
}

// RotationStats is what a health surface reports about layer one.
//
// Every field is a measured value: the plan's arithmetic is available from
// coverage.BuildCycle before anything runs, and mixing the two is how a
// dashboard ends up showing a plan as though it were an observation.
type RotationStats struct {
	// Markets covered, and the cycle length.
	Markets       int           `json:"markets"`
	CycleDuration time.Duration `json:"cycleDuration"`
	// IncrementSpacing is the gap between two visits.
	IncrementSpacing time.Duration `json:"incrementSpacing"`
	// Weight accounting, in exchange weight units.
	WeightBudgetPerMinute int `json:"weightBudgetPerMinute"`
	WeightUsedThisMinute  int `json:"weightUsedThisMinute"`
	// VisitedThisCycle and CycleProgressPercent describe the sweep.
	VisitedThisCycle     int     `json:"visitedThisCycle"`
	CycleProgressPercent float64 `json:"cycleProgressPercent"`
	// OldestMarketAge is how long ago the least recently visited market was
	// visited, which is the number that answers "how warm is the coldest market".
	OldestMarketAge time.Duration `json:"oldestMarketAge"`
	// LastTickerRefresh and LastReload are the batch calls' last run times.
	LastTickerRefresh time.Time `json:"lastTickerRefresh"`
	LastReload        time.Time `json:"lastReload"`
	// CyclesCompleted counts finished sweeps.
	CyclesCompleted int64 `json:"cyclesCompleted"`
	// LastCycleCompletedAt is when the last full sweep ended; zero until one
	// has. "上次周期完成时刻" is what tells an operator the sweep is alive.
	LastCycleCompletedAt time.Time `json:"lastCycleCompletedAt"`
	// ReloadsThisHour and ReloadMarkets describe the hour's full reload.
	ReloadsThisHour int `json:"reloadsThisHour"`
	ReloadMarkets   int `json:"reloadMarkets"`
	// Failures counts failed visits since the rotator was built. A market that
	// keeps failing is a market an operator needs to see.
	Failures int64 `json:"failures"`
}

// StepOutcome describes what one step did.
type StepOutcome struct {
	// Kind is what ran, empty when nothing was due or the gate paused the work.
	Kind string `json:"kind"`
	// Market is the visited market, empty for a batch call.
	Market string `json:"market"`
	// Paused reports that the weight budget refused the work. The caller should
	// wait for RetryAfter rather than spin.
	Paused bool `json:"paused"`
	// RetryAfter is how long to wait when Paused.
	RetryAfter time.Duration `json:"retryAfter"`
	// Failed reports that the fetch failed. Rotation continues either way: one
	// unreachable market must not stop the sweep.
	Failed bool `json:"failed"`
	// CycleCompleted reports that this step finished a sweep.
	CycleCompleted bool `json:"cycleCompleted"`
}

// Step kinds.
const (
	// StepTickerBatch is the batched ticker refresh, one per cycle.
	StepTickerBatch = "TICKER_BATCH"
	// StepKlineIncrement is one market's incremental candle fetch.
	StepKlineIncrement = "KLINE_INCREMENT"
	// StepKlineFullReload is one market's full history fetch, which rebuilds the
	// indicator window after a candle closes on the hour.
	StepKlineFullReload = "KLINE_FULL_RELOAD"
)

// Rotator walks the coverage plan.
type Rotator struct {
	mu  sync.Mutex
	src iRotationSource
	cfg *RotationConfig
	now func() time.Time

	plan    coverage.Plan
	cycle   *coverage.Cycle
	gate    *coverage.Gate
	markets []currency.Pair

	// scanner is optional: a rotation with no scanner is a valid configuration.
	scanner      *TriggerScanner
	scanParams   *snapshot.IndicatorParams
	scanInterval snapshot.Interval
	scanFacts    map[string]trigger.Fact

	// delist watches the enabled list for markets disappearing and applies the
	// 10 §7 row 18 response chain. Optional; attached before the loop starts
	// via attachDelistWatcher, so the loop's reads need no lock.
	delist *MarketDelistWatcher

	cursor               int
	reloadCursor         int
	reloadHour           time.Time
	visited              map[string]bool
	lastVisit            map[string]time.Time
	cycleStartedAt       time.Time
	lastTicker           time.Time
	lastReload           time.Time
	lastCycleCompletedAt time.Time
	cycles               int64
	failures             int64
}

// NewRotator builds a rotator. The market list is read by RefreshMarkets, so
// construction cannot fail on an exchange that is not ready yet.
func NewRotator(src iRotationSource, cfg *RotationConfig) (*Rotator, error) {
	if src == nil {
		return nil, ErrNilRotationSource
	}
	cfg.Plan.Normalise()
	if err := cfg.Plan.Validate(); err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Rotator{
		src:       src,
		cfg:       cfg,
		now:       now,
		plan:      cfg.Plan,
		visited:   make(map[string]bool),
		lastVisit: make(map[string]time.Time),
	}, nil
}

// attachDelistWatcher wires the market-delist watcher and points it at this
// rotator's own market list. It must be called before the rotation loop
// starts; a nil watcher leaves the watch off, which is a valid configuration.
func (r *Rotator) attachDelistWatcher(w *MarketDelistWatcher) {
	if r == nil || w == nil {
		return
	}
	w.Markets = r.Markets
	r.delist = w
}

// RefreshMarkets reads the tracked markets and rebuilds the cycle.
//
// It is separate from construction and is meant to be called again when the
// enabled list changes, because an operator enabling a market expects it to
// start being covered rather than to wait for a restart.
func (r *Rotator) RefreshMarkets(_ context.Context) error {
	if r == nil {
		return ErrNilRotator
	}
	pairs, err := r.src.GetEnabledPairs(r.cfg.Asset)
	if err != nil {
		return fmt.Errorf("reading enabled pairs: %w", err)
	}
	markets := make([]string, 0, len(pairs))
	byID := make(map[string]currency.Pair, len(pairs))
	for i := range pairs {
		// The identifier the plan works in, so it matches the snapshot's pair
		// spelling rather than whatever delimiter the config happens to use.
		id := pairs[i].Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String()
		markets = append(markets, id)
		byID[id] = pairs[i]
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	cycle, err := coverage.BuildCycle(&r.plan, markets)
	if err != nil {
		// An empty market list is a normal state (nothing enabled yet) and must
		// not be an error the caller has to special-case: the rotator simply has
		// no work until something is enabled.
		if errors.Is(err, coverage.ErrNoMarkets) {
			r.cycle = nil
			r.markets = nil
			return nil
		}
		return fmt.Errorf("building the coverage cycle: %w", err)
	}
	gate, err := coverage.NewGate(coverage.BudgetPerMinute(&r.plan))
	if err != nil {
		return fmt.Errorf("building the weight gate: %w", err)
	}
	r.cycle = cycle
	r.gate = gate
	r.markets = make([]currency.Pair, 0, len(cycle.Markets))
	for _, id := range cycle.Markets {
		r.markets = append(r.markets, byID[id])
	}
	// A rebuilt cycle starts clean: the previous cursor referred to a different
	// list, and carrying it over would skip markets.
	r.cursor = 0
	r.reloadCursor = 0
	r.reloadHour = time.Time{}
	r.visited = make(map[string]bool, len(r.markets))
	r.lastVisit = make(map[string]time.Time, len(r.markets))
	r.cycleStartedAt = r.now()
	return nil
}

// ReloadDue reports whether the full reload for this hour is still running.
//
// The reload is spread over the plan's own window after the hour, because
// fetching every market's history at once is the one path in this file whose
// instantaneous weight would be the sum of every market rather than one
// market's.
func (r *Rotator) reloadWindowLocked(now time.Time) (time.Time, bool) {
	hour := now.Truncate(time.Hour)
	spread := time.Duration(r.plan.FullReloadSpreadMinutes) * time.Minute
	if now.Sub(hour) >= spread {
		return hour, false
	}
	if r.reloadHour.Equal(hour) && r.reloadCursor >= len(r.markets) {
		// This hour's reload is finished.
		return hour, false
	}
	return hour, true
}

// startReloadLocked begins this hour's reload if it has not begun.
func (r *Rotator) startReloadLocked(hour time.Time) {
	if r.reloadHour.Equal(hour) {
		return
	}
	r.reloadHour = hour
	r.reloadCursor = 0
}

// Step performs at most one unit of rotation work.
//
// The weight budget is checked before the work rather than after, so a step
// never pushes the window over the limit and a decision-time fetch always finds
// room. When the budget refuses, the outcome says so and how long to wait; it is
// not an error, because waiting is the designed behaviour.
func (r *Rotator) Step(ctx context.Context) (*StepOutcome, error) {
	if r == nil {
		return nil, ErrNilRotator
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cycle == nil || r.gate == nil {
		return nil, ErrRotatorNotPlanned
	}

	now := r.now()
	outcome := &StepOutcome{}

	// The batched ticker refresh runs once per cycle period, not once per sweep.
	// It refreshes every market's ticker for the price of one request, which is
	// what the whole plan rests on; tying it to the period rather than to the
	// sweep means a sweep that runs long does not leave the tickers stale for
	// longer than the period.
	if r.lastTicker.IsZero() || now.Sub(r.lastTicker) >= r.cycle.CycleDuration {
		if !r.gate.Allow(coverage.WeightTickerBatch, now) {
			outcome.Paused = true
			outcome.RetryAfter = r.gate.RetryAfter(coverage.WeightTickerBatch, now)
			return outcome, nil
		}
		if err := r.src.UpdateTickers(ctx, r.cfg.Asset); err != nil {
			r.failures++
			outcome.Failed = true
			// The batch failed, so no market's ticker was refreshed. Recording
			// the attempt anyway would claim a freshness the rotation does not
			// have, so the timestamp is left alone and the next step retries.
			outcome.Kind = StepTickerBatch
			return outcome, nil
		}
		r.lastTicker = now
		outcome.Kind = StepTickerBatch
		return outcome, nil
	}

	// A full reload takes precedence inside its window: the indicators are only
	// as good as the window they were computed from, and an increment cannot
	// rebuild them.
	if hour, due := r.reloadWindowLocked(now); due {
		r.startReloadLocked(hour)
		if r.reloadCursor < len(r.markets) {
			weight := klineWeightFor(r.plan.KlineFullLimit)
			if !r.gate.Allow(weight, now) {
				outcome.Paused = true
				outcome.RetryAfter = r.gate.RetryAfter(weight, now)
				return outcome, nil
			}
			pair := r.markets[r.reloadCursor]
			outcome.Kind = StepKlineFullReload
			outcome.Market = pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String()
			r.reloadCursor++
			window, err := r.fetchFullWindow(ctx, pair, now)
			if err != nil {
				r.failures++
				outcome.Failed = true
				return outcome, nil
			}
			// The reload window is the freshest full history there is, so it is
			// handed to the cache before the scan: even a scan failure must not
			// throw away a fetch that already happened.
			if r.cfg.Cache != nil {
				if _, mergeErr := r.cfg.Cache.merge(pair, r.cfg.Asset, r.cfg.Interval, window, now); mergeErr != nil {
					log.Warnf(log.LLMMgr, "reload window could not be cached for %s: %v", pair, mergeErr)
				}
			}
			// The scan runs here and nowhere else: a reload is the only fetch
			// with enough history to compute the indicators the triggers compare.
			if err = r.scanAfterReload(window, now); err != nil {
				// A scan failure is not a fetch failure: the reload did happen,
				// so the market is warm and only the trigger is missing.
				r.failures++
				outcome.Failed = true
			}
			return outcome, nil
		}
	}

	if r.cursor >= len(r.markets) {
		// The sweep finished; the next one starts after the cycle period.
		r.cycles++
		r.lastCycleCompletedAt = now
		r.cursor = 0
		r.visited = make(map[string]bool, len(r.markets))
		r.cycleStartedAt = now
		outcome.CycleCompleted = true
	}

	pair := r.markets[r.cursor]
	id := pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String()
	weight := r.cycle.Increment.Weight
	if !r.gate.Allow(weight, now) {
		outcome.Paused = true
		outcome.RetryAfter = r.gate.RetryAfter(weight, now)
		return outcome, nil
	}

	outcome.Kind = StepKlineIncrement
	outcome.Market = id
	// Advance the cursor before the fetch: a market that fails must not be
	// retried in a tight loop while every other market waits behind it.
	r.cursor++
	if err := r.fetchIncrement(ctx, pair, now); err != nil {
		r.failures++
		outcome.Failed = true
	}
	r.visited[id] = true
	r.lastVisit[id] = now
	return outcome, nil
}

// fetchFullWindow pulls enough history to rebuild a market's indicators.
//
// It returns the window rather than discarding it: the reload is what feeds the
// trigger scan, and fetching the same history twice would pay the exchange
// weight twice for one answer.
func (r *Rotator) fetchFullWindow(ctx context.Context, pair currency.Pair, now time.Time) (*kline.Item, error) {
	seconds := r.cfg.Interval.Duration()
	lookback := time.Duration(r.plan.KlineFullLimit+1) * seconds
	window, err := r.src.GetHistoricCandles(ctx, pair, r.cfg.Asset, r.cfg.Interval, now.Add(-lookback), now)
	if err != nil {
		return nil, fmt.Errorf("full reload for %s: %w", pair, err)
	}
	return window, nil
}

// fetchIncrement pulls the newest few candles for one market.
//
// The window is deliberately tiny — enough to notice a close and refresh the
// unclosed bar — because layer one only keeps the market warm. The snapshot a
// decision reads is fetched fresh by the assembler instead.
func (r *Rotator) fetchIncrement(ctx context.Context, pair currency.Pair, now time.Time) error {
	seconds := r.cfg.Interval.Duration()
	lookback := time.Duration(r.plan.KlineIncrementLimit) * seconds
	if _, err := r.src.GetHistoricCandles(ctx, pair, r.cfg.Asset, r.cfg.Interval, now.Add(-lookback), now); err != nil {
		return fmt.Errorf("increment for %s: %w", pair, err)
	}
	return nil
}

// Sleeper waits for a duration or until the context ends. It is injected so the
// run loop below is testable without real time passing.
type Sleeper func(ctx context.Context, d time.Duration) error

// Sleep is the production sleeper.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Run performs up to steps units of work, sleeping between the paced ones.
//
// It returns how many units ran, so a caller can tell a quiet rotation from one
// that was refused by the budget the whole time.
//
// The pacing lives here rather than in the caller because the spacing is the
// plan's own figure: a caller sleeping on its own schedule would defeat the
// even spread that keeps the sweep inside its period and the weight window
// inside its budget.
func (r *Rotator) Run(ctx context.Context, steps int, sleep Sleeper) (int, error) {
	if r == nil {
		return 0, ErrNilRotator
	}
	if sleep == nil {
		sleep = Sleep
	}
	ran := 0
	for range steps {
		if err := ctx.Err(); err != nil {
			return ran, err
		}
		outcome, err := r.Step(ctx)
		switch {
		case err != nil:
			// No cycle yet is a normal state, not a failure: nothing is
			// enabled, so there is nothing to do and the caller should check
			// again later rather than treat it as an error.
			if errors.Is(err, ErrRotatorNotPlanned) {
				if sleepErr := sleep(ctx, r.NextSpacing()); sleepErr != nil {
					return ran, sleepErr
				}
				continue
			}
			return ran, err
		case outcome.Paused:
			if waitErr := sleep(ctx, outcome.RetryAfter); waitErr != nil {
				return ran, waitErr
			}
		default:
			ran++
			if sleepErr := sleep(ctx, r.NextSpacing()); sleepErr != nil {
				return ran, sleepErr
			}
		}
	}
	return ran, nil
}

// klineWeightFor mirrors coverage's kline weight tiers so the reload's cost is
// charged at the rate the plan assumed. It is a local copy because the tier
// table belongs to the plan package and is not exported; the plan's own budget
// arithmetic is what the value must agree with, and a test asserts that.
func klineWeightFor(limit int) int {
	switch {
	case limit < 100:
		return 1
	case limit < 500:
		return 2
	case limit < 1000:
		return 5
	default:
		return 10
	}
}

// Stats reports the rotation's measured state.
func (r *Rotator) Stats() RotationStats {
	if r == nil {
		return RotationStats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	stats := RotationStats{
		CyclesCompleted:      r.cycles,
		Failures:             r.failures,
		LastTickerRefresh:    r.lastTicker,
		LastReload:           r.lastReload,
		LastCycleCompletedAt: r.lastCycleCompletedAt,
	}
	if r.cycle == nil {
		return stats
	}
	stats.Markets = len(r.markets)
	stats.CycleDuration = r.cycle.CycleDuration
	stats.ReloadMarkets = len(r.markets)
	if r.reloadHour.Equal(now.Truncate(time.Hour)) {
		stats.ReloadsThisHour = r.reloadCursor
	}
	stats.IncrementSpacing = r.cycle.IncrementSpacing
	stats.WeightBudgetPerMinute = r.gate.PerMinute()
	stats.WeightUsedThisMinute = r.gate.Used(now)
	stats.VisitedThisCycle = len(r.visited)
	if len(r.markets) > 0 {
		stats.CycleProgressPercent = float64(len(r.visited)) / float64(len(r.markets)) * 100
	}
	// The coldest market is the one whose visit is furthest in the past. A
	// market never visited is as cold as the sweep is old, which is the honest
	// answer at the start of a cycle.
	var oldest time.Duration
	for _, id := range r.cycle.Markets {
		visitedAt, ok := r.lastVisit[id]
		if !ok {
			visitedAt = r.cycleStartedAt
		}
		if age := now.Sub(visitedAt); age > oldest {
			oldest = age
		}
	}
	stats.OldestMarketAge = oldest
	return stats
}

// NextSpacing returns how long to wait between steps.
func (r *Rotator) NextSpacing() time.Duration {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cycle == nil {
		return 0
	}
	return r.cycle.IncrementSpacing
}

// Markets returns the covered market identifiers.
func (r *Rotator) Markets() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cycle == nil {
		return nil
	}
	return append([]string(nil), r.cycle.Markets...)
}

// Plan returns the coverage plan in force.
func (r *Rotator) Plan() coverage.Plan {
	if r == nil {
		return coverage.Plan{}
	}
	return r.plan
}

// Interval returns the candle interval being covered, which the assembler needs
// so both layers read the same window.
func (r *Rotator) Interval() kline.Interval {
	if r == nil {
		return 0
	}
	return r.cfg.Interval
}

// Params returns the indicator parameters the layer-one fetches are sized for.
func (r *Rotator) Params() snapshot.IndicatorParams {
	return snapshot.DefaultIndicatorParams()
}
