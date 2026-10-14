// Package coverage plans how the bot keeps hundreds of perpetual markets warm
// without exhausting the exchange rate limit.
//
// The problem it solves (docs/llm-dev/04-数据层-K线与指标.md §7.4): Binance
// USDT-M lists over five hundred perpetual contracts and the bot is meant to
// watch all of them, but a decision needs a snapshot that is seconds old. Those
// two requirements pull in opposite directions, so they are met by two layers:
//
//   - Layer one keeps every market warm on a slow rotation. It discovers
//     triggers and maintains indicator state, and nothing it fetches is used as
//     decision input.
//   - Layer two fetches fresh data for the one market being decided on, at the
//     moment of the decision.
//
// This package implements layer one's arithmetic: what to fetch, how much it
// costs, and when to stop. It is deliberately pure. Executing the plan is the
// engine's job, so the budget can be tested without a network or a clock.
package coverage

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Weight constants, in Binance request-weight units.
//
// These are the numbers the budget arithmetic is built on, so they are named
// rather than inlined. The kline tiers follow Binance's published table, which
// is a half-open range: limit 99 costs 1 and limit 100 costs 2. The reference
// document writes the boundary as "limit ≤ 100 → weight 1", which is off by one
// against the live table; computing the tier from KlineWeight keeps the budget
// correct whichever reading is used.
const (
	// WeightTickerBatch is the cost of the all-symbol 24h ticker call. Batching
	// is what makes watching hundreds of markets affordable at all: one call
	// covers every symbol.
	WeightTickerBatch = 40
	// WeightFundingBatch is the cost of the all-symbol funding call. It is an
	// estimate kept on the high side, because under-counting spend makes a
	// budget gate useless. Verifying the live figure belongs to the smoke test.
	WeightFundingBatch = 10
)

// ExchangeWeightPerMinute is Binance USDT-M's limit. It is a constant rather
// than a setting because the gate exists to respect it, not to negotiate it.
const ExchangeWeightPerMinute = 2400

// Scope values.
const (
	// ScopeAllUSDTMPerpetual covers every USDT-margined perpetual the exchange
	// lists, discovered at startup rather than written down.
	ScopeAllUSDTMPerpetual = "ALL_USDTM_PERPETUAL"
)

// Limits and defaults for a rotation plan.
const (
	// DefaultRotationCycleMinutes is the designed rotation period. The reference
	// document specifies 5-10 minutes; the default sits at the slow end because
	// a slower cycle only delays trigger discovery, while a faster one spends
	// budget that decision-time fetches need.
	DefaultRotationCycleMinutes = 10
	// MaxRotationCycleMinutes bounds the period so a mis-set value cannot turn
	// rotation off by accident.
	MaxRotationCycleMinutes = 60
	// MinRotationCycleMinutes bounds the period from below. Below a minute the
	// rotation would compete with decision-time fetches for the same budget.
	MinRotationCycleMinutes = 1
	// DefaultKlineIncrementLimit is small on purpose: rotation only needs enough
	// bars to notice a close and refresh the unclosed one.
	DefaultKlineIncrementLimit = 5
	// MaxKlineIncrementLimit keeps the increment call in the cheapest weight
	// tier.
	MaxKlineIncrementLimit = 99
	// DefaultKlineFullLimit supplies enough history for the indicators. It is
	// the reference document's measured value.
	DefaultKlineFullLimit = 250
	// MinKlineFullLimit must be enough for the longest indicator lookback.
	MinKlineFullLimit = 100
	// MaxKlineFullLimit is Binance's ceiling for the mid weight tier.
	MaxKlineFullLimit = 500
	// DefaultFullReloadSpreadMinutes is how long the hourly reload is spread
	// over, so the top of the hour does not become a spike.
	DefaultFullReloadSpreadMinutes = 5
	// DefaultWeightBudgetFraction leaves the majority of the limit for user
	// queries and decision-time fetches. Rotation must never be able to starve
	// a decision of its data.
	DefaultWeightBudgetFraction = 0.6
)

// DefaultPersistIntervals are the candle intervals written to the database.
//
// Shorter intervals are deliberately absent: at one minute across hundreds of
// markets the row count reaches hundreds of millions a year, which does not fit
// the chosen storage. Shorter intervals are fetched on demand and kept in
// memory instead of being persisted.
var DefaultPersistIntervals = []string{"1h", "4h", "1d"}

// Sentinel errors.
var (
	// ErrPlanDisabled marks a plan that is switched off. It is an error rather
	// than a silent empty cycle so a caller cannot mistake "off" for "nothing
	// to do right now".
	ErrPlanDisabled = errors.New("coverage rotation is disabled")
	// ErrNoMarkets marks a cycle with nothing to cover.
	ErrNoMarkets = errors.New("no markets to cover")
	// ErrInvalidCycle marks a rotation period outside the accepted range.
	ErrInvalidCycle = errors.New("rotation cycle is out of range")
	// ErrInvalidLimit marks a candle limit outside its accepted range.
	ErrInvalidLimit = errors.New("candle limit is out of range")
	// ErrInvalidSpread marks a reload spread outside the accepted range.
	ErrInvalidSpread = errors.New("reload spread is out of range")
	// ErrInvalidFraction marks a budget fraction outside (0, 1].
	ErrInvalidFraction = errors.New("weight budget fraction must be in (0, 1]")
	// ErrInvalidInterval marks a persistence interval that is not recognised.
	ErrInvalidInterval = errors.New("unrecognised persistence interval")
	// ErrBudgetExceeded marks a plan whose own weight does not fit its budget.
	// A plan that cannot run must fail at configuration time, not at three in
	// the morning when the gate starts refusing requests.
	ErrBudgetExceeded = errors.New("rotation plan exceeds the weight budget")
	// ErrGateBudget marks a gate built with no budget.
	ErrGateBudget = errors.New("gate budget must be positive")
	// ErrUnknownScope marks a coverage scope with no selection rule behind it.
	// Falling back to "all symbols" would plan coverage of markets the design
	// never intended, so an unknown scope is refused.
	ErrUnknownScope = errors.New("unknown coverage scope")
)

// Kind identifies what a scheduled call fetches.
type Kind string

// Scheduled call kinds. The order they appear in a cycle is the order they run,
// so the cheapest and most useful data lands first.
const (
	// KindTickerBatch is the all-symbol 24h ticker call.
	KindTickerBatch Kind = "TICKER_BATCH"
	// KindFundingBatch is the all-symbol funding call.
	KindFundingBatch Kind = "FUNDING_BATCH"
	// KindKlineIncrement refreshes the newest candles of one market.
	KindKlineIncrement Kind = "KLINE_INCREMENT"
	// KindKlineFullReload rebuilds the indicator history of one market. It runs
	// on a slower cadence than the increment.
	KindKlineFullReload Kind = "KLINE_FULL_RELOAD"
)

// Request is one scheduled call.
type Request struct {
	// Kind is what to fetch.
	Kind Kind
	// Market is the pair identifier, empty for a batch call.
	Market string
	// Weight is the cost in exchange weight units.
	Weight int
	// IntervalMS is the intended spacing from the previous call of the same
	// kind. Batch calls carry zero because there is only one of them.
	IntervalMS int
}

// Plan is the rotation configuration.
type Plan struct {
	// Enabled switches the rotation on.
	Enabled bool
	// Scope is the reference document's coverage scope.
	Scope string
	// RotationCycleMinutes is the target period for a full sweep.
	RotationCycleMinutes int
	// KlineIncrementLimit is how many candles the incremental call asks for.
	KlineIncrementLimit int
	// KlineFullLimit is how many candles the reload asks for.
	KlineFullLimit int
	// FullReloadSpreadMinutes is how long the hourly reload is spread over.
	FullReloadSpreadMinutes int
	// WeightBudgetFraction is the share of the exchange limit rotation may use.
	WeightBudgetFraction float64
	// PersistIntervals are the candle intervals written to the database.
	PersistIntervals []string
}

// DefaultPlan returns the designed defaults.
func DefaultPlan() Plan {
	return Plan{
		Enabled:                 true,
		Scope:                   ScopeAllUSDTMPerpetual,
		RotationCycleMinutes:    DefaultRotationCycleMinutes,
		KlineIncrementLimit:     DefaultKlineIncrementLimit,
		KlineFullLimit:          DefaultKlineFullLimit,
		FullReloadSpreadMinutes: DefaultFullReloadSpreadMinutes,
		WeightBudgetFraction:    DefaultWeightBudgetFraction,
		PersistIntervals:        slices.Clone(DefaultPersistIntervals),
	}
}

// KlineWeight returns the exchange weight of a klines call for limit candles.
//
// The tiers are half-open: fewer than 100 candles costs 1, up to 499 costs 2,
// up to 999 costs 5, and anything larger costs 10.
func KlineWeight(limit int) int {
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

// Normalise fills unset values with defaults so a partly-specified plan is
// usable rather than rejected for a field the operator never heard of.
//
// Zero means unset: there is no way to distinguish "left blank" from "set to
// zero", so a plan that must be rejected needs a value outside the valid range
// rather than exactly zero.
func (p *Plan) Normalise() {
	if p == nil {
		return
	}
	if p.RotationCycleMinutes == 0 {
		p.RotationCycleMinutes = DefaultRotationCycleMinutes
	}
	if p.KlineIncrementLimit == 0 {
		p.KlineIncrementLimit = DefaultKlineIncrementLimit
	}
	if p.KlineFullLimit == 0 {
		p.KlineFullLimit = DefaultKlineFullLimit
	}
	if p.FullReloadSpreadMinutes == 0 {
		p.FullReloadSpreadMinutes = DefaultFullReloadSpreadMinutes
	}
	if p.WeightBudgetFraction == 0 {
		p.WeightBudgetFraction = DefaultWeightBudgetFraction
	}
	if p.Scope == "" {
		p.Scope = ScopeAllUSDTMPerpetual
	}
	if len(p.PersistIntervals) == 0 {
		p.PersistIntervals = slices.Clone(DefaultPersistIntervals)
	}
}

// Validate checks the plan's own fields.
func (p *Plan) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrPlanDisabled)
	}
	if p.RotationCycleMinutes < MinRotationCycleMinutes || p.RotationCycleMinutes > MaxRotationCycleMinutes {
		return fmt.Errorf("%w: %d must be %d..%d",
			ErrInvalidCycle, p.RotationCycleMinutes, MinRotationCycleMinutes, MaxRotationCycleMinutes)
	}
	if p.KlineIncrementLimit < 1 || p.KlineIncrementLimit > MaxKlineIncrementLimit {
		return fmt.Errorf("%w: increment %d must be 1..%d",
			ErrInvalidLimit, p.KlineIncrementLimit, MaxKlineIncrementLimit)
	}
	if p.KlineFullLimit < MinKlineFullLimit || p.KlineFullLimit > MaxKlineFullLimit {
		return fmt.Errorf("%w: reload %d must be %d..%d",
			ErrInvalidLimit, p.KlineFullLimit, MinKlineFullLimit, MaxKlineFullLimit)
	}
	if p.FullReloadSpreadMinutes < 1 || p.FullReloadSpreadMinutes > p.RotationCycleMinutes {
		return fmt.Errorf("%w: %d must be 1..cycle(%d)",
			ErrInvalidSpread, p.FullReloadSpreadMinutes, p.RotationCycleMinutes)
	}
	if p.WeightBudgetFraction <= 0 || p.WeightBudgetFraction > 1 {
		return fmt.Errorf("%w: got %v", ErrInvalidFraction, p.WeightBudgetFraction)
	}
	for _, interval := range p.PersistIntervals {
		if !slices.Contains(DefaultPersistIntervals, interval) {
			return fmt.Errorf("%w: %q", ErrInvalidInterval, interval)
		}
	}
	return nil
}

// Budget is the arithmetic behind a cycle, kept as a value so an operator can
// see why a plan was accepted instead of having to redo the sums.
type Budget struct {
	// ExchangePerMinute is the exchange limit the plan must respect.
	ExchangePerMinute int
	// PerMinute is the share rotation is allowed to spend.
	PerMinute int
	// RotationPerMinute is what the incremental rotation actually spends.
	RotationPerMinute float64
	// ReloadPerMinute is what the hourly reload spends while it is spread out.
	ReloadPerMinute float64
	// PeakPerMinute is the two together, which is the number that must fit.
	PeakPerMinute float64
	// HeadroomPerMinute is what is left for decision-time fetches and user
	// queries once the peak is accounted for.
	HeadroomPerMinute float64
	// ReloadTotalWeight is the whole reload's cost in one number.
	ReloadTotalWeight int
}

// Cycle is one complete rotation.
type Cycle struct {
	// Markets covered, in the order they will be visited.
	Markets []string
	// Batch are the once-per-cycle calls.
	Batch []Request
	// Increment is the per-market call prototype. A runner sends one copy per
	// market on the schedule below.
	Increment Request
	// IncrementSpacing is how far apart incremental calls should be so the
	// cycle finishes inside its period.
	IncrementSpacing time.Duration
	// CycleDuration is the planned period.
	CycleDuration time.Duration
	// TotalWeight is what one full sweep costs.
	TotalWeight int
	// Budget is the arithmetic that justifies the plan.
	Budget Budget
}

// BuildCycle plans one rotation over the supplied markets.
//
// It fails rather than returns a plan the exchange would reject: a rotation
// that cannot fit its budget is a configuration error, and finding out at
// configuration time is worth far more than finding out from a ban.
func BuildCycle(p *Plan, markets []string) (*Cycle, error) {
	p.Normalise()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if !p.Enabled {
		return nil, ErrPlanDisabled
	}
	cleaned := cleanMarkets(markets)
	if len(cleaned) == 0 {
		return nil, ErrNoMarkets
	}

	incrementWeight := KlineWeight(p.KlineIncrementLimit)
	increment := Request{
		Kind:   KindKlineIncrement,
		Weight: incrementWeight,
	}
	rotationWeight := WeightTickerBatch + WeightFundingBatch + incrementWeight*len(cleaned)

	cycleDuration := time.Duration(p.RotationCycleMinutes) * time.Minute
	rotationPerMinute := float64(rotationWeight) / cycleDuration.Minutes()

	reloadTotal := KlineWeight(p.KlineFullLimit) * len(cleaned)
	reloadPerMinute := float64(reloadTotal) / float64(p.FullReloadSpreadMinutes)

	perMinute := int(math.Floor(float64(ExchangeWeightPerMinute) * p.WeightBudgetFraction))
	peak := rotationPerMinute + reloadPerMinute
	budget := Budget{
		ExchangePerMinute: ExchangeWeightPerMinute,
		PerMinute:         perMinute,
		RotationPerMinute: rotationPerMinute,
		ReloadPerMinute:   reloadPerMinute,
		PeakPerMinute:     peak,
		HeadroomPerMinute: float64(perMinute) - peak,
		ReloadTotalWeight: reloadTotal,
	}
	if peak > float64(perMinute) {
		return nil, fmt.Errorf("%w: peak %.1f weight/min over budget %d (rotation %.1f + reload %.1f)",
			ErrBudgetExceeded, peak, perMinute, rotationPerMinute, reloadPerMinute)
	}

	// Spread the incremental calls evenly so the sweep completes on time
	// instead of arriving as a burst. The minimum of one millisecond keeps a
	// very large market list from producing a zero spacing.
	spacing := max(cycleDuration/time.Duration(len(cleaned)), time.Millisecond)
	increment.IntervalMS = int(spacing.Milliseconds())

	return &Cycle{
		Markets: cleaned,
		Batch: []Request{
			{Kind: KindTickerBatch, Weight: WeightTickerBatch},
			{Kind: KindFundingBatch, Weight: WeightFundingBatch},
		},
		Increment:        increment,
		IncrementSpacing: spacing,
		CycleDuration:    cycleDuration,
		TotalWeight:      rotationWeight,
		Budget:           budget,
	}, nil
}

// Requests expands the whole cycle into the calls to send, in order.
//
// Building the list is useful for reporting and for a test that wants to assert
// the total, but a runner should use the spacing rather than firing the whole
// slice at once, which would produce exactly the burst the plan avoids.
func (c *Cycle) Requests() []Request {
	if c == nil {
		return nil
	}
	out := make([]Request, 0, len(c.Batch)+len(c.Markets))
	out = append(out, c.Batch...)
	for _, market := range c.Markets {
		step := c.Increment
		step.Market = market
		out = append(out, step)
	}
	return out
}

// Weight returns the planned weight of the expanded request list.
func (c *Cycle) Weight() int {
	if c == nil {
		return 0
	}
	total := 0
	for _, request := range c.Batch {
		total += request.Weight
	}
	return total + c.Increment.Weight*len(c.Markets)
}

// cleanMarkets trims, drops blanks, de-duplicates and sorts so a cycle is
// deterministic: two runs over the same set must produce the same schedule, or
// a stored plan could not be compared with a later one.
func cleanMarkets(markets []string) []string {
	seen := make(map[string]struct{}, len(markets))
	out := make([]string, 0, len(markets))
	for _, market := range markets {
		trimmed := strings.TrimSpace(market)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	slices.Sort(out)
	return out
}
