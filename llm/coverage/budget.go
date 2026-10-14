package coverage

import (
	"errors"
	"sync"
	"time"
)

// DefaultGateWindow is the period the exchange limit is measured over.
const DefaultGateWindow = time.Minute

// Gate is a sliding-window weight counter.
//
// It is the hard part of the coverage design: the plan says what rotation would
// like to spend, and the gate makes sure it never does, whatever else the
// process is doing. When the window is full, rotation waits. A decision-time
// fetch therefore never fails because a background sweep got there first.
//
// The gate counts only what it is told about. Callers must pass every
// rotation-attributed request through it; the exchange's own limiter remains the
// backstop for everything else.
type Gate struct {
	mu        sync.Mutex
	perMinute int
	window    time.Duration
	events    []event
}

type event struct {
	at     time.Time
	weight int
}

// NewGate builds a gate for the given per-minute budget.
func NewGate(perMinute int) (*Gate, error) {
	if perMinute <= 0 {
		return nil, ErrGateBudget
	}
	return &Gate{perMinute: perMinute, window: DefaultGateWindow}, nil
}

// NewGateFromPlan builds a gate using the plan's own budget calculation, so the
// gate and the plan can never disagree about how much rotation may spend.
func NewGateFromPlan(p *Plan) (*Gate, error) {
	p.Normalise()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return NewGate(BudgetPerMinute(p))
}

// BudgetPerMinute returns the rotation budget a plan implies.
func BudgetPerMinute(p *Plan) int {
	return int(float64(ExchangeWeightPerMinute) * p.WeightBudgetFraction)
}

// PerMinute returns the configured budget.
func (g *Gate) PerMinute() int {
	if g == nil {
		return 0
	}
	return g.perMinute
}

// Used returns the weight recorded inside the window ending at now.
func (g *Gate) Used(now time.Time) int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usedLocked(now)
}

// Allow reports whether a request of the given weight fits, and records it when
// it does.
//
// A refused request is not recorded, so a caller that retries in a tight loop
// cannot push the window further over the limit than it already is.
func (g *Gate) Allow(weight int, now time.Time) bool {
	if g == nil {
		return false
	}
	if weight <= 0 {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.usedLocked(now)+weight > g.perMinute {
		return false
	}
	g.events = append(g.events, event{at: now, weight: weight})
	return true
}

// Reserve takes weight from the window or reports that it could not.
//
// It is Allow under a name that reads better at a call site which is about to
// spend the weight rather than ask about it.
func (g *Gate) Reserve(weight int, now time.Time) error {
	if g.Allow(weight, now) {
		return nil
	}
	return errors.New("rotation weight budget exhausted")
}

// RetryAfter returns how long a caller should wait before the same request would
// fit. It returns zero when the request fits now.
//
// Rotation sleeps for this rather than polling: a busy loop against a full
// window would burn the very CPU the budget exists to protect.
func (g *Gate) RetryAfter(weight int, now time.Time) time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.usedLocked(now)+weight <= g.perMinute {
		return 0
	}
	// The window frees up when the oldest events age out. Walk from the oldest
	// until enough has expired.
	running := g.usedLocked(now)
	for _, e := range g.events {
		if !e.at.Add(g.window).After(now) {
			continue
		}
		running -= e.weight
		wait := e.at.Add(g.window).Sub(now)
		if running+weight <= g.perMinute {
			return wait
		}
	}
	return g.window
}

// Reset clears the window. It exists for tests and for an operator action that
// deliberately abandons the current window.
func (g *Gate) Reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = g.events[:0]
}

func (g *Gate) usedLocked(now time.Time) int {
	cutoff := now.Add(-g.window)
	// The window is the half-open interval (now-window, now]: an event that has
	// just expired is out, and an event stamped after now is out too. The
	// future case only arises from clock skew, but counting it would make a
	// gate refuse work that has not been done yet.
	kept := g.events[:0]
	total := 0
	for _, e := range g.events {
		if e.at.After(cutoff) && !e.at.After(now) {
			kept = append(kept, e)
			total += e.weight
		}
	}
	g.events = kept
	return total
}
