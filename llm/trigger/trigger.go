// Package trigger turns a market's indicator state into the events that ask for
// a decision.
//
// It is layer one's reason to exist. Keeping every market warm is not valuable
// by itself; what is valuable is that a market which was not being watched can
// announce that something happened in it. Without this, a full-coverage sweep
// produces warm data that nothing reads.
//
// Two properties shape the design:
//
//   - An event is an observation, not a decision. "The MACD histogram turned
//     positive" is a fact about the past, on the same footing as the crossover
//     it is derived from. The package produces no recommendation, and the
//     pipeline treats an event as a reason to look rather than a reason to act
//     (docs/llm-dev/01-需求与产品目标.md §6).
//   - A signal that fires on a short interval is confirmed on a longer one. A
//     one-hour histogram flip is noise often enough that acting on it directly
//     would make the system twitchy, so the scan records it and a later pass
//     either confirms or drops it (Q5b).
package trigger

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// Kind identifies what happened.
type Kind string

// Event kinds. The set is closed: a new trigger is a new constant and a new
// branch in Detect, not a string a caller invents.
const (
	// KindKlineClose marks a candle closing on the interval. It is the most
	// predictable trigger and the only one whose rate is fully known in advance,
	// which is why it is on by default.
	KindKlineClose Kind = "KLINE_CLOSE"
	// KindMACDCross marks the MACD histogram changing sign.
	KindMACDCross Kind = "MACD_CROSS"
	// KindMACross marks the close moving from one side of a moving average to
	// the other.
	KindMACross Kind = "MA_CROSS"
	// KindATRSpike marks volatility expanding past a multiple of its recent
	// reading.
	KindATRSpike Kind = "ATR_SPIKE"
	// KindFundingWindow marks approaching a funding settlement.
	KindFundingWindow Kind = "FUNDING_WINDOW"
	// KindAccountEvent marks a fill, liquidation or margin change. It is not
	// produced by this package: the account side raises it, because only there
	// is the event visible.
	KindAccountEvent Kind = "ACCOUNT_EVENT"
	// KindManual marks an operator asking for a decision. Also not produced
	// here.
	KindManual Kind = "MANUAL"
)

// Defaults for the confirmation rule.
const (
	// DefaultConfirmInterval is how much longer the confirming interval is than
	// the scanning one. A one-hour signal is confirmed on four hours.
	DefaultConfirmInterval = 4 * time.Hour
)

// Errors.
var (
	// ErrNoMarket marks a fact without a market identifier.
	ErrNoMarket = errors.New("trigger fact has no market")
	// ErrConfirmIntervalInvalid marks a confirmation interval that cannot be
	// used, such as one shorter than the scan interval, which would confirm
	// everything it saw.
	ErrConfirmIntervalInvalid = errors.New("confirmation interval must exceed the scan interval")
)

// Policy says which triggers are armed and how a signal is confirmed.
//
// The defaults are Q5's decision: the minimal set (candle close, account events,
// manual) plus the indicator crossings. Crosses are armed because the operator
// chose the option that includes them, and they are treated as reasons to look
// rather than reasons to act — which is why a crossing is held for a confirming
// pass instead of driving a decision on its own.
//
// ATR spikes and the funding window are off by default: both are configuration
// options in Q5, and an armed-by-default volatility trigger would fire across
// many markets at once during exactly the moments when the system is least able
// to afford it.
type Policy struct {
	// KlineClose arms the candle-close trigger. Default true.
	KlineClose bool
	// MACDCross arms the histogram sign change. Default true (Q5).
	MACDCross bool
	// MACross arms the moving-average crossing. Default true (Q5).
	MACross bool
	// ATRSpike arms the volatility expansion. Default false (Q5).
	ATRSpike bool
	// FundingWindow arms the settlement-proximity trigger. Default false (Q5).
	FundingWindow bool
	// MinInterval throttles any market to at most one event per period. It is
	// the de-bouncing rule: several triggers firing together describe one
	// moment, not several.
	MinInterval time.Duration
	// ConfirmInterval is how long a signal waits for a confirming pass. Zero
	// disables confirmation, which is only correct for the candle-close trigger
	// because a close cannot un-close.
	ConfirmInterval time.Duration
	// ATRSpikeMultiple is how far the current ATR must exceed its previous
	// reading to count as a spike. Default 1.5.
	ATRSpikeMultiple float64
	// FundingWindowLead is how close a settlement must be, measured from the
	// fact's candle close, to raise the funding-window trigger. It only matters
	// when FundingWindow is armed; a zero lead would fire on every bar that has
	// a known settlement time, which is why Validate refuses it.
	FundingWindowLead time.Duration
}

// DefaultPolicy is the armed set from Q5.
func DefaultPolicy() Policy {
	return Policy{
		KlineClose:        true,
		MACDCross:         true,
		MACross:           true,
		ATRSpike:          false,
		FundingWindow:     false,
		MinInterval:       time.Minute,
		ConfirmInterval:   DefaultConfirmInterval,
		ATRSpikeMultiple:  1.5,
		FundingWindowLead: 15 * time.Minute,
	}
}

// Validate checks the policy.
func (p Policy) Validate() error {
	if p.MinInterval < 0 {
		return fmt.Errorf("%w: min interval %s", ErrConfirmIntervalInvalid, p.MinInterval)
	}
	if p.ConfirmInterval < 0 {
		return fmt.Errorf("%w: %s", ErrConfirmIntervalInvalid, p.ConfirmInterval)
	}
	if p.FundingWindow && p.FundingWindowLead <= 0 {
		return fmt.Errorf("%w: an armed funding window needs a positive lead", ErrConfirmIntervalInvalid)
	}
	if p.ATRSpikeMultiple > 0 && p.ATRSpikeMultiple < 1 {
		return fmt.Errorf("%w: an ATR multiple below one would fire on any contraction",
			ErrConfirmIntervalInvalid)
	}
	return nil
}

// Armed returns every trigger the policy arms, in a stable order.
func (p Policy) Armed() []Kind {
	var out []Kind
	for _, entry := range []struct {
		kind  Kind
		armed bool
	}{
		{KindKlineClose, p.KlineClose},
		{KindMACDCross, p.MACDCross},
		{KindMACross, p.MACross},
		{KindATRSpike, p.ATRSpike},
		{KindFundingWindow, p.FundingWindow},
	} {
		if entry.armed {
			out = append(out, entry.kind)
		}
	}
	return out
}

// Sign is the sign of a histogram value, as three states rather than a number.
//
// Three states rather than a float because the only question the trigger asks is
// which side of zero the value is on, and comparing floats for a sign change
// invites the flat case to be read as both crossings in turn.
type Sign int

// Signs.
const (
	// SignNegative is below zero.
	SignNegative Sign = -1
	// SignZero is exactly zero.
	SignZero Sign = 0
	// SignPositive is above zero.
	SignPositive Sign = 1
)

// SignOf classifies a histogram value.
func SignOf(value float64) Sign {
	switch {
	case value > 0:
		return SignPositive
	case value < 0:
		return SignNegative
	default:
		return SignZero
	}
}

func (s Sign) String() string {
	switch s {
	case SignPositive:
		return "+"
	case SignNegative:
		return "-"
	default:
		return "0"
	}
}

// Fact is one market's compact trigger state.
//
// It carries the previous value alongside the current one rather than a
// previous Fact, because the only thing a trigger compares is a value against
// its predecessor, and holding whole prior facts would mean retaining every
// market's full history to answer one question.
type Fact struct {
	// MarketID is the market the fact describes.
	MarketID string
	// Interval is the candle interval the facts were read on.
	Interval snapshot.Interval
	// LastClosedAt is the newest closed candle's time, which is what a
	// candle-close trigger reports.
	LastClosedAt time.Time
	// PreviousClosedAt is the close before it, so a close can be distinguished
	// from a re-read of the same candle.
	PreviousClosedAt time.Time

	// HistogramSign and PreviousHistogramSign bracket the newest bar.
	HistogramSign         Sign
	PreviousHistogramSign Sign

	// PriceVsMA and PreviousPriceVsMA are the position fact's two newest values.
	PriceVsMA         string
	PreviousPriceVsMA string
	// MAPeriod names the average the position refers to, so an event can say
	// which line was crossed.
	MAPeriod int64

	// ATR and PreviousATR are the volatility readings.
	ATR         float64
	PreviousATR float64

	// NextFundingAt is when funding next settles, when it is known.
	NextFundingAt time.Time
}

// Event is one trigger firing.
type Event struct {
	// MarketID is the market to look at.
	MarketID string `json:"marketId"`
	// Kind is what happened.
	Kind Kind `json:"kind"`
	// At is the instant of the fact that caused it — the candle's close, not
	// the scan time, so a decision records the moment it is about.
	At time.Time `json:"at"`
	// Subject identifies what changed within the kind, when the kind can change
	// in more than one direction. It is what makes a confirmation meaningful: a
	// death cross must not confirm a golden cross, and without a discriminator
	// the two are the same event as far as a key is concerned.
	Subject string `json:"subject,omitempty"`
	// Detail is a short human-readable description. It is written for an audit
	// log rather than for a model.
	Detail string `json:"detail"`
	// NeedsConfirmation reports that the signal is waiting for a longer-interval
	// pass before it may drive a decision.
	NeedsConfirmation bool `json:"needsConfirmation,omitempty"`
}

// String renders the event for a log line.
func (e Event) String() string {
	return fmt.Sprintf("%s %s at %s: %s", e.MarketID, e.Kind, e.At.Format(time.RFC3339), e.Detail)
}

// Detect reports the triggers a fact raises.
//
// The throttle is not applied here: whether a market was heard from too recently
// is a property of the sequence of scans, which this function does not see.
// Callers apply Policy.MinInterval through a Scanner.
func Detect(fact *Fact, p Policy) ([]Event, error) {
	if strings.TrimSpace(fact.MarketID) == "" {
		return nil, ErrNoMarket
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var events []Event

	if p.KlineClose && !fact.LastClosedAt.IsZero() && fact.LastClosedAt.After(fact.PreviousClosedAt) {
		events = append(events, Event{
			MarketID: fact.MarketID,
			Kind:     KindKlineClose,
			At:       fact.LastClosedAt,
			Detail:   fmt.Sprintf("%s candle closed", fact.Interval),
			// A close cannot un-close, so this trigger needs no confirmation.
		})
	}

	if p.MACDCross && fact.HistogramSign != fact.PreviousHistogramSign &&
		fact.HistogramSign != SignZero && fact.PreviousHistogramSign != SignZero {
		direction := "turned positive"
		if fact.HistogramSign == SignNegative {
			direction = "turned negative"
		}
		events = append(events, Event{
			MarketID:          fact.MarketID,
			Kind:              KindMACDCross,
			At:                fact.LastClosedAt,
			Subject:           fact.HistogramSign.String(),
			Detail:            "MACD histogram " + direction,
			NeedsConfirmation: p.ConfirmInterval > 0,
		})
	}

	if p.MACross && fact.PriceVsMA != "" && fact.PreviousPriceVsMA != "" &&
		fact.PriceVsMA != fact.PreviousPriceVsMA {
		events = append(events, Event{
			MarketID:          fact.MarketID,
			Kind:              KindMACross,
			At:                fact.LastClosedAt,
			Subject:           fact.PriceVsMA,
			Detail:            fmt.Sprintf("close moved %s %d-period average", strings.ToLower(fact.PriceVsMA), fact.MAPeriod),
			NeedsConfirmation: p.ConfirmInterval > 0,
		})
	}

	if p.ATRSpike && p.ATRSpikeMultiple > 0 && fact.PreviousATR > 0 && fact.ATR > 0 {
		if fact.ATR >= fact.PreviousATR*p.ATRSpikeMultiple {
			events = append(events, Event{
				MarketID: fact.MarketID,
				Kind:     KindATRSpike,
				At:       fact.LastClosedAt,
				Detail: fmt.Sprintf("ATR expanded from %.6g to %.6g (%.1f times)",
					fact.PreviousATR, fact.ATR, fact.ATR/fact.PreviousATR),
				NeedsConfirmation: p.ConfirmInterval > 0,
			})
		}
	}

	if p.FundingWindow && p.FundingWindowLead > 0 && !fact.NextFundingAt.IsZero() && !fact.LastClosedAt.IsZero() {
		lead := fact.NextFundingAt.Sub(fact.LastClosedAt)
		if lead > 0 && lead <= p.FundingWindowLead {
			events = append(events, Event{
				MarketID:          fact.MarketID,
				Kind:              KindFundingWindow,
				At:                fact.LastClosedAt,
				Detail:            fmt.Sprintf("funding settles in %s", lead.Round(time.Minute)),
				NeedsConfirmation: p.ConfirmInterval > 0,
			})
		}
	}

	return events, nil
}

// NextScanInstant returns the next instant a market's candle should be closed by.
//
// It exists so the scan can be driven by the interval rather than by a fixed
// timer: a 1h market closes on the hour and a 15m market closes every quarter,
// and a scan that ignored that would either re-read the same bar or miss one.
func NextScanInstant(lastClosed time.Time, interval snapshot.Interval) (time.Time, error) {
	seconds, err := interval.Duration()
	if err != nil {
		return time.Time{}, err
	}
	return lastClosed.Add(time.Duration(seconds) * time.Second), nil
}
