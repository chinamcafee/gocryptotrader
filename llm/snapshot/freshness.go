package snapshot

import (
	"errors"
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// Freshness thresholds, from docs/llm-dev/04-数据层-K线与指标.md §6.
//
// The values are set against the polling period, not against a push feed:
// Binance USDT-M has no WebSocket and every live fact arrives from a REST poll
// whose interval defaults to fifteen seconds. A threshold tighter than the poll
// would mark every fact stale and stop the system from doing anything, which is
// why they are generous relative to one poll and still far inside the horizon
// where a price could have moved to a different decision.
const (
	// TickerMaxAge bounds a ticker read.
	TickerMaxAge = 60 * time.Second
	// OrderBookMaxAge bounds a book read.
	OrderBookMaxAge = 60 * time.Second
	// FundingMaxAge bounds a funding read. It is the loosest threshold because
	// funding settles every eight hours and barely moves between settlements.
	FundingMaxAge = 10 * time.Minute
	// LocalPositionMaxAge bounds a locally tracked position. The order manager
	// syncs every ten seconds, so a minute is six syncs of slack.
	LocalPositionMaxAge = 60 * time.Second
	// CandleGrace is added to twice the candle interval to allow for a close
	// that has happened but has not yet been fetched by the next poll.
	CandleGrace = 60 * time.Second

	// ExecutionPositionMaxAge bounds the exchange position read taken
	// immediately before signing. It is deliberately two orders of magnitude
	// tighter than the analysis threshold: an analysis may reason about a
	// slightly old position, but the size that reaches the exchange must come
	// from a reading taken moments ago.
	ExecutionPositionMaxAge = 5 * time.Second
	// ExecutionBookMaxAge bounds the pre-submission book read used for the
	// slippage check, for the same reason.
	ExecutionBookMaxAge = 5 * time.Second

	// AutoCandidateWindow is how long an automatic candidate stays valid past
	// its snapshot's source time (docs/llm-dev/04 §6, Q9 = C).
	AutoCandidateWindow = 60 * time.Second
)

// Reason codes. They are the risk vocabulary's own spellings
// (docs/llm-dev/07-策略与自动决策引擎.md §5), so a report can be stored and
// rendered without translation.
const (
	// CodeRiskSnapshotStale marks a snapshot whose facts are too old to decide on.
	CodeRiskSnapshotStale = "RISK_SNAPSHOT_STALE"
	// CodeRiskSnapshotQuality marks a fact whose quality blocks a decision.
	CodeRiskSnapshotQuality = "RISK_SNAPSHOT_QUALITY"
	// CodeRiskAccountInventoryStale marks an account reading too old for the
	// step it is being used for.
	CodeRiskAccountInventoryStale = "RISK_ACCOUNT_INVENTORY_STALE"
)

// Sentinel errors for the gate itself. They mark a snapshot the gate cannot
// judge, which is a failure rather than a verdict.
var (
	// ErrMissingReadTime marks a fact with no read timestamp. Without one its
	// age is unknown, and unknown age is not fresh.
	ErrMissingReadTime = errors.New("fact has no read time")
	// ErrFactFromTheFuture marks a fact stamped after the evaluation instant.
	ErrFactFromTheFuture = errors.New("fact is stamped after the evaluation time")
)

// Threshold is one category's age limit and what breaching it means.
type Threshold struct {
	// Category is the fact category the limit applies to.
	Category string
	// MaxAge is the oldest acceptable age.
	MaxAge time.Duration
	// Blocking reports whether breaching the limit forbids a decision.
	//
	// Funding is the one non-blocking category: it is a cost that moves slowly,
	// and refusing every decision while a funding read is ten minutes old would
	// stop trading for most of each eight-hour settlement cycle.
	Blocking bool
}

// Thresholds returns every category's limit for a given candle interval.
func Thresholds(interval Interval) (map[string]Threshold, error) {
	candleMax, err := CandleMaxAge(interval)
	if err != nil {
		return nil, err
	}
	return map[string]Threshold{
		CategoryCandles:   {Category: CategoryCandles, MaxAge: candleMax, Blocking: true},
		CategoryTicker:    {Category: CategoryTicker, MaxAge: TickerMaxAge, Blocking: true},
		CategoryOrderBook: {Category: CategoryOrderBook, MaxAge: OrderBookMaxAge, Blocking: true},
		CategoryFunding:   {Category: CategoryFunding, MaxAge: FundingMaxAge, Blocking: false},
		CategoryAccount:   {Category: CategoryAccount, MaxAge: LocalPositionMaxAge, Blocking: true},
	}, nil
}

// CandleMaxAge is the limit for the newest closed candle.
//
// Twice the interval plus a grace period: one interval for the bar that has not
// closed yet, one for the polling delay that may not have delivered the close,
// and the grace for a slow poll on top of that.
func CandleMaxAge(interval Interval) (time.Duration, error) {
	seconds, err := interval.Duration()
	if err != nil {
		return 0, err
	}
	return 2*time.Duration(seconds)*time.Second + CandleGrace, nil
}

// AutoCandidateDeadline returns the instant an automatic candidate stops being
// usable.
//
// It is the snapshot's source time plus the window, and it may be pulled
// earlier by a pipeline deadline so the two cannot disagree about when the run
// must be over.
func AutoCandidateDeadline(sourceTime, pipelineDeadline time.Time) time.Time {
	deadline := sourceTime.Add(AutoCandidateWindow)
	if !pipelineDeadline.IsZero() && pipelineDeadline.Before(deadline) {
		return pipelineDeadline
	}
	return deadline
}

// FactAge is one category's assessed age.
type FactAge struct {
	// Category is the fact category.
	Category string
	// Age is now minus the fact's own read time.
	Age time.Duration
	// MaxAge is the category's limit.
	MaxAge time.Duration
	// Blocking reports whether breaching the limit forbids a decision.
	Blocking bool
	// Stale reports whether the limit was breached.
	Stale bool
	// Absent reports that the snapshot carries no fact of this category. An
	// absent category is not stale; whether it is required is the caller's
	// question, not the gate's.
	Absent bool
}

// FreshnessReport is the gate's verdict for one snapshot at one instant.
type FreshnessReport struct {
	// EvaluatedAt is the instant the ages were measured against.
	EvaluatedAt time.Time
	// Facts holds one entry per category the snapshot carries.
	Facts []FactAge
	// BlockingStale lists categories whose age forbids a decision.
	BlockingStale []string
	// NonBlockingStale lists categories that are stale but tolerated.
	NonBlockingStale []string
	// ReasonCodes are the risk codes a caller should record.
	ReasonCodes []string
}

// MayDecide reports whether nothing in the report forbids a decision.
func (r *FreshnessReport) MayDecide() bool {
	return r != nil && len(r.BlockingStale) == 0
}

// WorstAge returns the largest age among the categories the report covers, or
// zero when it covers none. It is what a dashboard shows as "data age".
func (r *FreshnessReport) WorstAge() time.Duration {
	if r == nil {
		return 0
	}
	var worst time.Duration
	for _, fact := range r.Facts {
		if fact.Absent {
			continue
		}
		if fact.Age > worst {
			worst = fact.Age
		}
	}
	return worst
}

// StaleCategories returns every stale category, blocking or not.
func (r *FreshnessReport) StaleCategories() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.BlockingStale)+len(r.NonBlockingStale))
	out = append(out, r.BlockingStale...)
	out = append(out, r.NonBlockingStale...)
	return out
}

// EvaluateFreshness measures every fact in the snapshot against its limit.
//
// The age of a fact comes from its own read timestamp rather than from the
// snapshot's source time, because the facts are fetched at different moments: a
// candle that closed a second ago can sit beside a book read a minute ago, and
// one number cannot describe both.
//
// It returns an error only for a snapshot the gate cannot judge at all — a
// missing or future read timestamp is a broken builder, not stale data, and
// reporting it as "stale" would send someone looking at the exchange instead of
// at the assembler.
func EvaluateFreshness(s *MarketSnapshot, now time.Time) (*FreshnessReport, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: nil snapshot", ErrSnapshotIncomplete)
	}
	limits, err := Thresholds(s.Candles.Interval)
	if err != nil {
		return nil, err
	}
	report := &FreshnessReport{EvaluatedAt: now}

	// Candles are aged from the close of the newest closed candle, which is the
	// newest moment the window actually describes.
	candleRead := s.SourceTime
	if len(s.Candles.Closed) > 0 {
		candleRead = s.Candles.Closed[len(s.Candles.Closed)-1].Time
	}
	if err := report.assess(limits[CategoryCandles], candleRead, now, s.carriesData(CategoryCandles)); err != nil {
		return nil, err
	}
	if err := report.assess(limits[CategoryTicker], readTimeOfTicker(s.Ticker), now, s.Ticker != nil); err != nil {
		return nil, err
	}
	if err := report.assess(limits[CategoryOrderBook], readTimeOfBook(s.OrderBook), now, s.OrderBook != nil); err != nil {
		return nil, err
	}
	if err := report.assess(limits[CategoryFunding], readTimeOfFunding(s.Funding), now, s.Funding != nil); err != nil {
		return nil, err
	}
	if err := report.assess(limits[CategoryAccount], readTimeOfAccount(s.Account), now, s.Account != nil); err != nil {
		return nil, err
	}

	for _, fact := range report.Facts {
		if !fact.Stale {
			continue
		}
		if fact.Blocking {
			report.BlockingStale = append(report.BlockingStale, fact.Category)
		} else {
			report.NonBlockingStale = append(report.NonBlockingStale, fact.Category)
		}
	}
	report.deriveReasonCodes(s)
	return report, nil
}

// deriveReasonCodes rebuilds the codes from the report and the snapshot's
// current labels.
//
// It is called again after marking so the two paths cannot disagree: a caller
// that reads the codes from the report and a caller that reads the labels from
// the snapshot must reach the same conclusion about why a snapshot was refused.
func (r *FreshnessReport) deriveReasonCodes(s *MarketSnapshot) {
	r.ReasonCodes = nil
	if len(r.BlockingStale) > 0 {
		r.ReasonCodes = append(r.ReasonCodes, CodeRiskSnapshotStale)
	}
	// A degraded quality label is a separate reason from an old one: the label
	// may have been set for a gap or an unreadable source, neither of which the
	// age says anything about.
	if s.DegradedCategories() != nil {
		r.ReasonCodes = append(r.ReasonCodes, CodeRiskSnapshotQuality)
	}
}

func (r *FreshnessReport) assess(limit Threshold, readAt, now time.Time, present bool) error {
	if !present {
		r.Facts = append(r.Facts, FactAge{
			Category: limit.Category, MaxAge: limit.MaxAge, Blocking: limit.Blocking, Absent: true,
		})
		return nil
	}
	if readAt.IsZero() {
		return fmt.Errorf("%w: %s", ErrMissingReadTime, limit.Category)
	}
	if readAt.After(now) {
		// A fact stamped in the future means the clock or the source is wrong.
		// Its real age is unknown, and unknown age is not fresh.
		return fmt.Errorf("%w: %s (read %s, now %s)", ErrFactFromTheFuture, limit.Category, readAt, now)
	}
	age := now.Sub(readAt)
	r.Facts = append(r.Facts, FactAge{
		Category: limit.Category,
		Age:      age,
		MaxAge:   limit.MaxAge,
		Blocking: limit.Blocking,
		Stale:    age > limit.MaxAge,
	})
	return nil
}

// ApplyFreshness evaluates the gate and marks every stale category on the
// snapshot, so a later reader sees why the snapshot was refused without having
// to re-measure.
//
// Marking goes through SetQuality, which only lowers: a category already
// degraded for a stronger reason keeps that reason.
func (s *MarketSnapshot) ApplyFreshness(now time.Time) (*FreshnessReport, error) {
	report, err := EvaluateFreshness(s, now)
	if err != nil {
		return nil, err
	}
	for _, fact := range report.Facts {
		if !fact.Stale {
			continue
		}
		// Do not overwrite a label that is already degraded. GAP and STALE share
		// a rank, so the downgrade check would allow the replacement — but the
		// two say different things, and losing "there was a gap" in favour of
		// "it is old" throws away the more specific reason the refusal rests on.
		if s.QualityOf(fact.Category).IsDegraded() {
			continue
		}
		if err = s.SetQuality(fact.Category, candidate.QualityStale); err != nil {
			return nil, fmt.Errorf("marking %s stale: %w", fact.Category, err)
		}
	}
	report.deriveReasonCodes(s)
	return report, nil
}

// ExecutionFreshness is the pre-submission check (docs/llm-dev/04 §6).
//
// Two readings are taken immediately before an order is signed: the exchange's
// own position, and the book the slippage estimate is computed from. Both must
// be seconds old, not a minute old, because they decide the size and the price
// that actually leave the machine.
type ExecutionFreshness struct {
	// PositionAge and BookAge are the measured ages.
	PositionAge time.Duration
	BookAge     time.Duration
	// PositionStale and BookStale report the verdicts.
	PositionStale bool
	BookStale     bool
	// ReasonCodes are the risk codes for whatever failed.
	ReasonCodes []string
}

// MaySubmit reports whether both pre-submission readings are fresh enough.
func (e *ExecutionFreshness) MaySubmit() bool {
	return e != nil && !e.PositionStale && !e.BookStale
}

// EvaluateExecutionFreshness checks the two pre-submission readings.
func EvaluateExecutionFreshness(positionReadAt, bookReadAt, now time.Time) (*ExecutionFreshness, error) {
	out := &ExecutionFreshness{}
	for _, reading := range []struct {
		name   string
		readAt time.Time
		limit  time.Duration
		stale  *bool
	}{
		{"position", positionReadAt, ExecutionPositionMaxAge, &out.PositionStale},
		{"book", bookReadAt, ExecutionBookMaxAge, &out.BookStale},
	} {
		if reading.readAt.IsZero() {
			return nil, fmt.Errorf("%w: %s", ErrMissingReadTime, reading.name)
		}
		if reading.readAt.After(now) {
			return nil, fmt.Errorf("%w: %s", ErrFactFromTheFuture, reading.name)
		}
		age := now.Sub(reading.readAt)
		*reading.stale = age > reading.limit
		switch reading.name {
		case "position":
			out.PositionAge = age
		case "book":
			out.BookAge = age
		}
	}
	if out.PositionStale || out.BookStale {
		out.ReasonCodes = append(out.ReasonCodes, CodeRiskAccountInventoryStale)
	}
	return out, nil
}

func readTimeOfTicker(t *TickerFacts) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.ReadAt
}

func readTimeOfBook(b *OrderBookFacts) time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.ReadAt
}

func readTimeOfFunding(f *FundingFacts) time.Time {
	if f == nil {
		return time.Time{}
	}
	return f.ReadAt
}

func readTimeOfAccount(a *AccountFacts) time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.ReadAt
}
