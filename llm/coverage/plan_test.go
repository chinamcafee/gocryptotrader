package coverage

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The measured shape of the exchange, from the reference document's live probe
// on 2026-10-10: 525 USDT-margined perpetual contracts in TRADING state.
const measuredMarkets = 525

func marketList(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, "COIN"+string(rune('A'+i%26))+itoa(i)+"_USDT")
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// planPtr lets a fixture result be passed where the API wants a pointer.
func TestDefaultPlanFitsTheBudgetForTheWholeExchange(t *testing.T) {
	t.Parallel()

	cycle, err := BuildCycle(new(DefaultPlan()), marketList(measuredMarkets))
	require.NoError(t, err, "the designed defaults must fit the measured market count")

	// The arithmetic an operator has to be able to check by hand.
	assert.Equal(t, WeightTickerBatch+WeightFundingBatch+measuredMarkets, cycle.TotalWeight,
		"one sweep must cost the batch calls plus one incremental call per market")
	assert.InDelta(t, 57.5, cycle.Budget.RotationPerMinute, 0.01,
		"a full sweep spread over ten minutes must cost about 57 weight per minute")
	assert.Equal(t, 1050, cycle.Budget.ReloadTotalWeight,
		"an hourly reload of every market must cost the measured reload weight")
	assert.InDelta(t, 210, cycle.Budget.ReloadPerMinute, 0.01,
		"the reload spread over five minutes must cost about 210 weight per minute")
	assert.InDelta(t, 267.5, cycle.Budget.PeakPerMinute, 0.01,
		"the peak is the rotation and the reload together")
	assert.Equal(t, 1440, cycle.Budget.PerMinute,
		"the default budget is sixty percent of the exchange limit")
	assert.Greater(t, cycle.Budget.HeadroomPerMinute, 1000.0,
		"most of the limit must remain for decision-time fetches and user queries")
	assert.Less(t, cycle.Budget.PeakPerMinute, float64(cycle.Budget.PerMinute),
		"the whole point: the plan must fit its own budget")
}

func TestRotationSpreadsAcrossTheCycle(t *testing.T) {
	t.Parallel()

	cycle, err := BuildCycle(new(DefaultPlan()), marketList(measuredMarkets))
	require.NoError(t, err, "the defaults must build")

	assert.Equal(t, 10*time.Minute, cycle.CycleDuration, "the default cycle should be ten minutes")
	// 600 seconds over 525 markets. The duration keeps full precision; the
	// millisecond field on the request is the truncated convenience form.
	assert.Equal(t, 600*time.Second/525, cycle.IncrementSpacing,
		"incremental calls must be evenly spaced, not fired as a burst")
	assert.Equal(t, 1142, cycle.Increment.IntervalMS,
		"the millisecond form must be the truncated spacing")

	elapsed := cycle.IncrementSpacing * time.Duration(len(cycle.Markets))
	assert.LessOrEqual(t, elapsed, cycle.CycleDuration,
		"the scheduled calls must finish inside the cycle, or the sweep falls behind")

	// A decision costs roughly ten to fifteen weight. The rotation must not be
	// able to consume the budget a decision needs.
	assert.Greater(t, cycle.Budget.HeadroomPerMinute, 15.0,
		"the headroom must exceed one decision's cost by a wide margin")
}

func TestKlineWeightTiers(t *testing.T) {
	t.Parallel()

	for limit, want := range map[int]int{
		1: 1, 5: 1, 99: 1,
		100: 2, 250: 2, 499: 2,
		500: 5, 999: 5,
		1000: 10, 1500: 10,
	} {
		assert.Equalf(t, want, KlineWeight(limit), "limit %d should cost %d", limit, want)
	}
	// The reference document writes the first boundary as "≤ 100 → 1", which is
	// off by one against the live table. The tier function follows the table so
	// the budget stays correct either way.
	assert.Equal(t, 2, KlineWeight(MaxKlineIncrementLimit+1),
		"the increment ceiling must be the last value in the cheapest tier")
}

func TestPlanNormaliseFillsDefaults(t *testing.T) {
	t.Parallel()

	p := Plan{Enabled: true}
	p.Normalise()
	assert.Equal(t, DefaultPlan(), p, "a plan with only Enabled set should normalise to the defaults")

	var nilPlan *Plan
	nilPlan.Normalise()
	assert.Nil(t, nilPlan, "normalising a nil plan should not panic")
}

func TestPlanValidation(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*Plan){
		"negative cycle":       func(p *Plan) { p.RotationCycleMinutes = -1 },
		"cycle too long":       func(p *Plan) { p.RotationCycleMinutes = MaxRotationCycleMinutes + 1 },
		"negative increment":   func(p *Plan) { p.KlineIncrementLimit = -1 },
		"increment too large":  func(p *Plan) { p.KlineIncrementLimit = MaxKlineIncrementLimit + 1 },
		"reload too small":     func(p *Plan) { p.KlineFullLimit = MinKlineFullLimit - 1 },
		"reload too large":     func(p *Plan) { p.KlineFullLimit = MaxKlineFullLimit + 1 },
		"negative spread":      func(p *Plan) { p.FullReloadSpreadMinutes = -1 },
		"spread over cycle":    func(p *Plan) { p.FullReloadSpreadMinutes = p.RotationCycleMinutes + 1 },
		"fraction over one":    func(p *Plan) { p.WeightBudgetFraction = 1.5 },
		"negative fraction":    func(p *Plan) { p.WeightBudgetFraction = -0.5 },
		"unknown interval":     func(p *Plan) { p.PersistIntervals = []string{"1m"} },
		"one unknown interval": func(p *Plan) { p.PersistIntervals = []string{"1h", "5m"} },
	} {
		p := DefaultPlan()
		mutate(&p)
		// Normalise is what a caller does first; it must not paper over a value
		// the operator actually set.
		p.Normalise()
		require.Errorf(t, p.Validate(), "%s must be refused", name)
	}

	require.ErrorIs(t, (*Plan)(nil).Validate(), ErrPlanDisabled, "a nil plan must be refused")
}

func TestBuildCycleRejectsUnusablePlans(t *testing.T) {
	t.Parallel()

	p := DefaultPlan()
	p.Enabled = false
	_, err := BuildCycle(&p, marketList(10))
	require.ErrorIs(t, err, ErrPlanDisabled, "a disabled rotation must say so rather than return an empty cycle")

	_, err = BuildCycle(new(DefaultPlan()), nil)
	require.ErrorIs(t, err, ErrNoMarkets, "a cycle with no markets must be refused")

	_, err = BuildCycle(new(DefaultPlan()), []string{"", "   ", "\t"})
	require.ErrorIs(t, err, ErrNoMarkets, "blank market names must not count as coverage")

	_, err = BuildCycle(new(DefaultPlan()), marketList(3))
	require.NoError(t, err, "a small market list must build")
}

func TestBuildCycleRefusesAPlanThatCannotFit(t *testing.T) {
	t.Parallel()

	p := DefaultPlan()
	p.WeightBudgetFraction = 0.01
	// The budget becomes 24 weight per minute while rotation alone wants 57.5
	// and the reload wants 210.
	_, err := BuildCycle(&p, marketList(measuredMarkets))
	require.ErrorIs(t, err, ErrBudgetExceeded,
		"a plan that cannot run must fail at configuration time, not at three in the morning")
	assert.Contains(t, err.Error(), "over budget", "the message should name the problem")

	// A single market with a tight but honest budget must still fit.
	p = DefaultPlan()
	p.WeightBudgetFraction = 0.1
	cycle, err := BuildCycle(&p, marketList(1))
	require.NoError(t, err, "a small coverage set must fit a small budget")
	assert.Less(t, cycle.Budget.PeakPerMinute, float64(cycle.Budget.PerMinute), "the peak should still fit")
}

func TestMarketsAreCleanedAndOrdered(t *testing.T) {
	t.Parallel()

	cycle, err := BuildCycle(new(DefaultPlan()), []string{" SOL_USDT ", "BTC_USDT", "SOL_USDT", "", "ETH_USDT", "btc_usdt"})
	require.NoError(t, err, "a messy list must still build")

	assert.Equal(t, []string{"BTC_USDT", "ETH_USDT", "SOL_USDT", "btc_usdt"}, cycle.Markets,
		"markets must be trimmed, de-duplicated exactly and sorted")

	// Determinism matters: a stored plan must be comparable with a later one.
	again, err := BuildCycle(new(DefaultPlan()), []string{"btc_usdt", "ETH_USDT", "SOL_USDT", "BTC_USDT"})
	require.NoError(t, err, "the reordered list must build")
	assert.Equal(t, cycle.Markets, again.Markets, "the same set should produce the same schedule")
}

func TestCycleRequestsExpandExactlyOncePerMarket(t *testing.T) {
	t.Parallel()

	cycle, err := BuildCycle(new(DefaultPlan()), marketList(7))
	require.NoError(t, err, "the defaults must build")

	requests := cycle.Requests()
	require.Len(t, requests, len(cycle.Batch)+7, "the expansion must hold the batch calls and one call per market")
	assert.Equal(t, KindTickerBatch, requests[0].Kind, "the batched ticker should run first")
	assert.Equal(t, KindFundingBatch, requests[1].Kind, "the batched funding call should run second")

	total := 0
	seen := make(map[string]bool, 7)
	for _, request := range requests[2:] {
		assert.Equal(t, KindKlineIncrement, request.Kind, "every remaining call is an incremental fetch")
		assert.NotEmpty(t, request.Market, "an incremental call should name its market")
		assert.False(t, seen[request.Market], "each market should be visited once")
		seen[request.Market] = true
		total += request.Weight
	}
	assert.Len(t, seen, 7, "every market should be visited")

	assert.Equal(t, cycle.TotalWeight, cycle.Weight(), "the reported total should match the expansion")
	assert.Equal(t, cycle.TotalWeight, total+requests[0].Weight+requests[1].Weight,
		"the expansion's weights must sum to the same total")

	// The prototype must not be mutated by expanding.
	assert.Empty(t, cycle.Increment.Market, "expansion should not leak a market onto the prototype")

	var nilCycle *Cycle
	assert.Empty(t, nilCycle.Requests(), "a nil cycle should not panic")
	assert.Zero(t, nilCycle.Weight(), "a nil cycle has no weight")
}

func newTestGate(t *testing.T, perMinute int) *Gate {
	t.Helper()
	gate, err := NewGate(perMinute)
	require.NoError(t, err, "a positive budget must build a gate")
	return gate
}

func TestGateAllowsUpToTheBudget(t *testing.T) {
	t.Parallel()

	gate := newTestGate(t, 100)
	now := time.Now()

	assert.True(t, gate.Allow(40, now), "the first call should fit")
	assert.Equal(t, 40, gate.Used(now), "the call should be recorded")
	assert.True(t, gate.Allow(60, now), "a call that exactly fills the budget should fit")
	assert.Equal(t, 100, gate.Used(now), "the window should be full")
	assert.False(t, gate.Allow(1, now), "a full window should refuse the next call")
	assert.Equal(t, 100, gate.Used(now), "a refused call should not be recorded")
	assert.Equal(t, 100, gate.PerMinute(), "the budget should be reported")
}

func TestGateWindowSlides(t *testing.T) {
	t.Parallel()

	gate := newTestGate(t, 100)
	start := time.Now()
	require.True(t, gate.Allow(100, start), "the window starts empty")

	assert.False(t, gate.Allow(40, start.Add(30*time.Second)), "the first call is still inside the window")
	assert.Equal(t, 100, gate.Used(start.Add(30*time.Second)), "the window still holds the first call")

	// Just before expiry the weight is still counted, at expiry it is gone.
	assert.Equal(t, 100, gate.Used(start.Add(DefaultGateWindow-time.Millisecond)),
		"the window must count a call right up to its expiry")
	assert.Zero(t, gate.Used(start.Add(DefaultGateWindow)), "a call should age out exactly at the window boundary")

	assert.True(t, gate.Allow(100, start.Add(DefaultGateWindow)), "the window should free up once the call ages out")
}

func TestGateRetryAfter(t *testing.T) {
	t.Parallel()

	// Each case gets its own gate: the window slides with the queried time, so
	// reusing one gate across different instants would silently prune it.
	freshGate := func(t *testing.T, weight int) (*Gate, time.Time) {
		t.Helper()
		gate := newTestGate(t, 100)
		start := time.Now()
		require.True(t, gate.Allow(weight, start), "the window starts empty")
		return gate, start
	}

	t.Run("an empty window asks for no wait", func(t *testing.T) {
		t.Parallel()
		gate, start := freshGate(t, 100)
		assert.Zero(t, gate.RetryAfter(10, start.Add(-DefaultGateWindow)),
			"a request that fits must not ask the caller to wait")
	})

	t.Run("a request that fits asks for no wait", func(t *testing.T) {
		t.Parallel()
		gate, start := freshGate(t, 40)
		assert.Zero(t, gate.RetryAfter(60, start),
			"a request that fits alongside the recorded weight must not wait")
	})

	t.Run("a full window reports the time until it frees up", func(t *testing.T) {
		t.Parallel()
		gate, start := freshGate(t, 100)
		now := start.Add(20 * time.Second)
		wait := gate.RetryAfter(40, now)
		assert.Equal(t, 40*time.Second, wait, "the wait should be until enough weight ages out")

		// Waiting the reported time must actually make the request fit, or a
		// caller that sleeps it would be refused again.
		assert.True(t, gate.Allow(40, now.Add(wait)),
			"the reported wait must be accurate")
	})

	t.Run("an emptied window asks for no wait", func(t *testing.T) {
		t.Parallel()
		gate, start := freshGate(t, 100)
		assert.Zero(t, gate.RetryAfter(100, start.Add(DefaultGateWindow)),
			"the request fits once every recorded call has aged out")
	})

	t.Run("a nil gate is safe", func(t *testing.T) {
		t.Parallel()
		var nilGate *Gate
		assert.False(t, nilGate.Allow(1, time.Now()), "a nil gate should refuse rather than panic")
		assert.Zero(t, nilGate.Used(time.Now()), "a nil gate holds nothing")
		assert.Zero(t, nilGate.PerMinute(), "a nil gate has no budget")
		nilGate.Reset()
	})
}

func TestGateReset(t *testing.T) {
	t.Parallel()

	gate := newTestGate(t, 100)
	now := time.Now()
	require.True(t, gate.Allow(100, now), "the window starts empty")
	gate.Reset()
	assert.Zero(t, gate.Used(now), "a reset should clear the window")
	assert.True(t, gate.Allow(100, now), "a reset window should accept again")
}

func TestNewGateValidation(t *testing.T) {
	t.Parallel()

	_, err := NewGate(0)
	require.ErrorIs(t, err, ErrGateBudget, "a zero budget must be refused")

	_, err = NewGate(-10)
	require.ErrorIs(t, err, ErrGateBudget, "a negative budget must be refused")

	p := DefaultPlan()
	gate, err := NewGateFromPlan(&p)
	require.NoError(t, err, "the default plan must produce a gate")
	assert.Equal(t, 1440, gate.PerMinute(), "the gate should use the plan's own budget")

	assert.Equal(t, gate.PerMinute(), BudgetPerMinute(&p), "the two budget paths should agree")

	_, err = NewGateFromPlan(&Plan{Enabled: true, RotationCycleMinutes: 999})
	require.Error(t, err, "an invalid plan must not produce a gate")
}

func TestGateRefusesAnUnfittableWeight(t *testing.T) {
	t.Parallel()

	// A weight larger than the whole budget can never be admitted. The gate must
	// say so rather than admit it once and blow the limit.
	gate := newTestGate(t, 10)
	require.False(t, gate.Allow(40, time.Now()), "a call larger than the budget must be refused")
	assert.Equal(t, DefaultGateWindow, gate.RetryAfter(40, time.Now()),
		"the wait must fall back to the window when the call can never fit")
}

func TestGateIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	gate := newTestGate(t, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	start := time.Now()

	for range 50 {
		wg.Go(func() {
			if gate.Allow(25, start) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	assert.Equal(t, 40, admitted, "the gate should admit exactly the budget's worth under contention")
	assert.Equal(t, 1000, gate.Used(start), "the window should hold exactly the budget")
}

func TestSimulatedSweepStaysInsideTheBudget(t *testing.T) {
	t.Parallel()

	// The end-to-end property the design exists for: run a whole sweep plus an
	// hourly reload through the real gate and check that nothing overflowed and
	// that a decision-time fetch still finds room at every instant.
	plan := DefaultPlan()
	cycle, err := BuildCycle(&plan, marketList(measuredMarkets))
	require.NoError(t, err, "the defaults must build")

	gate, err := NewGateFromPlan(&plan)
	require.NoError(t, err, "the gate must build")

	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	decisionWeight := 15

	// The reload runs at the top of the hour alongside the rotation. Feed both
	// through the gate in one-minute slices and assert that a decision always
	// fits on top.
	reloadWeight := cycle.Budget.ReloadTotalWeight
	reloadPerTick := reloadWeight / plan.FullReloadSpreadMinutes
	rotationPerTick := int(cycle.Budget.RotationPerMinute) + 1

	for minute := range plan.RotationCycleMinutes {
		now := start.Add(time.Duration(minute) * time.Minute)

		remaining := rotationPerTick
		for remaining > 0 {
			step := min(remaining, 40)
			if !gate.Allow(step, now) {
				// Contention is allowed to delay rotation: that is the gate
				// working. What must never happen is the decision failing.
				break
			}
			remaining -= step
		}

		if minute < plan.FullReloadSpreadMinutes {
			reload := reloadPerTick
			for reload > 0 {
				step := min(reload, 40)
				if !gate.Allow(step, now) {
					break
				}
				reload -= step
			}
		}

		assert.True(t, gate.Allow(decisionWeight, now),
			"minute %d: a decision-time fetch must always find room", minute)
		assert.LessOrEqual(t, gate.Used(now), gate.PerMinute(),
			"minute %d: the window must never exceed the budget", minute)
	}
}
