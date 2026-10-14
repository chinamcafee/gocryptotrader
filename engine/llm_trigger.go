package engine

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// The scanner sits between layer one and the decision pipeline.
//
// llm/trigger decides what a market's state means; this owns the two things a
// single fact cannot answer on its own:
//
//   - Whether the market was heard from too recently. Several triggers firing
//     together in one market describe one moment, so the throttle collapses them
//     into one reason to look.
//   - Whether a short-interval signal survives a longer-interval pass. A
//     one-hour flip is noise often enough that acting on it directly would make
//     the system twitchy, so a signal that asks for confirmation is held until a
//     later scan sees it again (Q5b).
//
// It holds the queue rather than pushing events out, because the pipeline is not
// built yet and a scanner that called into a non-existent pipeline could not be
// tested. A caller drains it.

// DefaultTriggerQueueLimit bounds the pending queue.
//
// The bound exists because events are produced by something that never stops and
// consumed by something that may be slow or absent. An unbounded queue in that
// shape is a memory leak with extra steps.
const DefaultTriggerQueueLimit = 256

// pendingSignal is a signal awaiting its confirming pass.
type pendingSignal struct {
	kind      trigger.Kind
	firstSeen time.Time
	confirmAt time.Time
}

// TriggerScanner de-bounces and confirms trigger events.
type TriggerScanner struct {
	mu        sync.Mutex
	policy    trigger.Policy
	now       func() time.Time
	limit     int
	previous  map[string]*trigger.Fact
	lastHeard map[string]time.Time
	pending   map[string]pendingSignal
	queue     []trigger.Event
	dropped   int64
}

// NewTriggerScanner builds a scanner. A nil clock uses the wall clock.
func NewTriggerScanner(policy trigger.Policy, now func() time.Time) (*TriggerScanner, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &TriggerScanner{
		policy:    policy,
		now:       now,
		limit:     DefaultTriggerQueueLimit,
		previous:  make(map[string]*trigger.Fact),
		lastHeard: make(map[string]time.Time),
		pending:   make(map[string]pendingSignal),
	}, nil
}

// Observe feeds one market's state and returns whatever it raised.
//
// The returned events are also queued, so a caller either uses the return value
// or drains the queue, not both.
func (s *TriggerScanner) Observe(fact *trigger.Fact) ([]trigger.Event, error) {
	if s == nil {
		return nil, ErrNilRotator
	}
	detected, err := trigger.Detect(fact, s.policy)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()

	var raised []trigger.Event
	for _, event := range detected {
		if event.NeedsConfirmation && s.policy.ConfirmInterval > 0 {
			if !s.confirmLocked(&event, fact, now) {
				continue
			}
		}
		if !s.throttleAllowsLocked(&event, now) {
			continue
		}
		raised = append(raised, event)
		s.enqueueLocked(&event)
		s.lastHeard[event.MarketID] = now
	}
	s.previous[fact.MarketID] = fact
	return raised, nil
}

// RaiseAccountEvent reports an account-side change (a fill, a liquidation, a
// margin change) as a trigger.
//
// The account side is the only place these are visible, so the scanner cannot
// discover them on its own; this is the entry point that producer calls. The
// event needs no confirmation - it is a fact that happened, not a reading to be
// re-checked - and it passes the same per-market throttle as every other
// trigger, because a burst of fills describes one moment of account change.
func (s *TriggerScanner) RaiseAccountEvent(marketID, detail string, at time.Time) ([]trigger.Event, error) {
	if s == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(marketID) == "" {
		return nil, fmt.Errorf("%w: market is required", trigger.ErrNoMarket)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = s.now()
	}
	event := trigger.Event{
		MarketID: marketID,
		Kind:     trigger.KindAccountEvent,
		At:       at,
		Detail:   detail,
	}
	if !s.throttleAllowsLocked(&event, at) {
		return nil, nil
	}
	s.enqueueLocked(&event)
	s.lastHeard[marketID] = at
	return []trigger.Event{event}, nil
}

// confirmLocked promotes a signal whose confirming pass has arrived and which
// fires again, and records one otherwise.
//
// The confirming pass is a later observation of the same market raising the same
// kind. It is not a separate timer, because a timer would confirm a signal that
// had already reversed — which is exactly the noise the rule exists to filter.
func (s *TriggerScanner) confirmLocked(event *trigger.Event, fact *trigger.Fact, now time.Time) bool {
	key := pendingKey(event.MarketID, event.Kind, event.Subject)
	waiting, ok := s.pending[key]
	if !ok {
		// First sighting: hold it and say when it may be confirmed.
		s.pending[key] = pendingSignal{
			kind:      event.Kind,
			firstSeen: now,
			confirmAt: now.Add(s.policy.ConfirmInterval),
		}
		return false
	}
	if now.Before(waiting.confirmAt) {
		// Seen again inside the window: still waiting. The clock is not reset,
		// because restarting it on every sighting would let a signal that keeps
		// flickering never confirm.
		return false
	}
	delete(s.pending, key)
	// The signal survived a pass at the longer interval, so it is promoted. The
	// event keeps its own instant: the moment it is about has not changed.
	_ = fact
	return true
}

func (s *TriggerScanner) throttleAllowsLocked(event *trigger.Event, now time.Time) bool {
	throttle := s.policy.MinInterval
	if throttle <= 0 {
		return true
	}
	last, ok := s.lastHeard[event.MarketID]
	if !ok {
		return true
	}
	return now.Sub(last) >= throttle
}

func (s *TriggerScanner) enqueueLocked(event *trigger.Event) {
	if len(s.queue) >= s.limit {
		// The oldest event is dropped rather than the newest: an operator asking
		// "what just happened" wants the recent answer, and a queue that refused
		// new events would keep replaying a stale moment.
		s.queue = s.queue[1:]
		s.dropped++
	}
	s.queue = append(s.queue, *event)
}

// Drain takes up to max events from the queue.
func (s *TriggerScanner) Drain(limit int) []trigger.Event {
	if s == nil || limit <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	if limit > len(s.queue) {
		limit = len(s.queue)
	}
	out := append([]trigger.Event(nil), s.queue[:limit]...)
	s.queue = append([]trigger.Event(nil), s.queue[limit:]...)
	return out
}

// Settle drops pending signals that have waited longer than twice their
// confirming interval without being confirmed.
//
// Without this a market that fires once and never again would sit in the pending
// map forever, and the map would grow with every market that ever produced a
// signal.
func (s *TriggerScanner) Settle() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	dropped := 0
	for key, waiting := range s.pending {
		// Twice the interval: one period to wait for the confirming pass, and one
		// more because the pass itself is paced by the sweep.
		if now.Sub(waiting.firstSeen) > 2*s.policy.ConfirmInterval {
			delete(s.pending, key)
			dropped++
		}
	}
	return dropped
}

// TriggerStats reports the scanner's state for a health surface.
type TriggerStats struct {
	Queued     int      `json:"queued"`
	Pending    int      `json:"pending"`
	Dropped    int64    `json:"dropped"`
	ArmedKinds []string `json:"armedKinds"`
}

// Stats reports the scanner's measured state.
func (s *TriggerScanner) Stats() TriggerStats {
	if s == nil {
		return TriggerStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	armed := make([]string, 0, len(s.policy.Armed()))
	for _, kind := range s.policy.Armed() {
		armed = append(armed, string(kind))
	}
	return TriggerStats{
		Queued:     len(s.queue),
		Pending:    len(s.pending),
		Dropped:    s.dropped,
		ArmedKinds: armed,
	}
}

// pendingKey identifies a signal by what changed, not only by which trigger
// fired. Keying on the market and kind alone would let a death cross confirm a
// golden cross, because the two are the same kind.
func pendingKey(market string, kind trigger.Kind, subject string) string {
	return market + "|" + string(kind) + "|" + subject
}

// FactFromSnapshot builds a scanner fact from an assembled snapshot.
//
// This is the join between the assembler and the scanner: the snapshot already
// carries the derived facts the triggers read, so nothing is recomputed here.
// The caller keeps the returned fact as the market's previous state and passes it
// back on the next pass, which is what turns two readings into a comparison.
func FactFromSnapshot(s *snapshot.MarketSnapshot, previous *trigger.Fact) (trigger.Fact, error) {
	if s == nil {
		return trigger.Fact{}, fmt.Errorf("%w: nil snapshot", snapshot.ErrSnapshotIncomplete)
	}
	fact := trigger.Fact{
		MarketID:     s.Pair,
		Interval:     s.Candles.Interval,
		LastClosedAt: s.SourceTime,
	}
	if s.Indicators.MACD != nil {
		value, err := lastDecimalAsFloat(s.Indicators.MACD.Histogram)
		if err != nil {
			return trigger.Fact{}, fmt.Errorf("macd histogram: %w", err)
		}
		fact.HistogramSign = trigger.SignOf(value)
	}
	if len(s.Candles.Closed) > 1 {
		fact.PreviousClosedAt = s.Candles.Closed[len(s.Candles.Closed)-2].Time
	}
	// The longest moving average is the line a crossing is reported against: it
	// is the one a trend change is read from, and reporting every period would
	// raise several events for one move.
	var chosen *snapshot.MovingAverageFact
	for i := range s.Indicators.MovingAverages {
		average := &s.Indicators.MovingAverages[i]
		if chosen == nil || average.Period > chosen.Period {
			chosen = average
		}
	}
	if chosen != nil {
		fact.PriceVsMA = chosen.PriceVsMA
		fact.MAPeriod = chosen.Period
	}
	if s.Funding != nil && !s.Funding.NextFundingTime.IsZero() {
		fact.NextFundingAt = s.Funding.NextFundingTime
	}
	if s.Indicators.ATR != nil && s.Indicators.ATR.Latest != "" {
		value, err := strconv.ParseFloat(s.Indicators.ATR.Latest, 64)
		if err != nil {
			return trigger.Fact{}, fmt.Errorf("atr: %w", snapshot.ErrInvalidDecimal)
		}
		fact.ATR = value
	}
	if previous != nil {
		fact.PreviousHistogramSign = previous.HistogramSign
		fact.PreviousPriceVsMA = previous.PriceVsMA
		fact.PreviousATR = previous.ATR
		// When the newest candle has not changed, the "previous" comparison must
		// come from the same bar rather than from an older one, or a re-read of
		// one candle would be compared against a bar two steps back and read as a
		// change.
		if previous.LastClosedAt.Equal(fact.LastClosedAt) {
			fact.PreviousHistogramSign = fact.HistogramSign
			fact.PreviousPriceVsMA = fact.PriceVsMA
			fact.PreviousATR = fact.ATR
		}
	}
	return fact, nil
}

// lastDecimalAsFloat parses the newest value of a decimal string series.
func lastDecimalAsFloat(values []string) (float64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	return strconv.ParseFloat(values[len(values)-1], 64)
}
