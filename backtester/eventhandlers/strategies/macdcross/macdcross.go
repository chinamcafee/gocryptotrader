package macdcross

import (
	"fmt"
	"time"

	"github.com/thrasher-corp/gct-ta/indicators"
	"github.com/thrasher-corp/gocryptotrader/backtester/common"
	"github.com/thrasher-corp/gocryptotrader/backtester/data"
	"github.com/thrasher-corp/gocryptotrader/backtester/eventhandlers/portfolio"
	"github.com/thrasher-corp/gocryptotrader/backtester/eventhandlers/strategies/base"
	"github.com/thrasher-corp/gocryptotrader/backtester/eventtypes/signal"
	"github.com/thrasher-corp/gocryptotrader/backtester/funding"
	gctcommon "github.com/thrasher-corp/gocryptotrader/common"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

const (
	// Name is the strategy name
	Name            = "macdcross"
	fastPeriodKey   = "macd-fast"
	slowPeriodKey   = "macd-slow"
	signalPeriodKey = "macd-signal"
	description     = `The MACD cross strategy buys when the MACD line crosses above the signal line (golden cross) and sells when it crosses below (death cross). It is the fixed-rule baseline (Q24=A) the LLM decisions are compared against.`
)

// Strategy is an implementation of the Handler interface
type Strategy struct {
	base.Strategy
	fastPeriod   decimal.Decimal
	slowPeriod   decimal.Decimal
	signalPeriod decimal.Decimal
}

// Name returns the name of the strategy
func (s *Strategy) Name() string {
	return Name
}

// Description provides a nice overview of the strategy
func (s *Strategy) Description() string {
	return description
}

// OnSignal handles a data event and returns what action the strategy believes should occur.
// A golden cross (MACD above signal after being at or below) buys; a death cross sells.
func (s *Strategy) OnSignal(d data.Handler, _ funding.IFundingTransferer, _ portfolio.Handler) (signal.Event, error) {
	if d == nil {
		return nil, common.ErrNilEvent
	}
	es, err := s.GetBaseData(d)
	if err != nil {
		return nil, err
	}

	latest, err := d.Latest()
	if err != nil {
		return nil, err
	}

	es.SetPrice(latest.GetClosePrice())

	warmup := s.slowPeriod.IntPart() + s.signalPeriod.IntPart()
	if offset := latest.GetOffset(); offset <= warmup {
		es.AppendReason("Not enough data for signal generation")
		es.SetDirection(order.DoNothing)
		return &es, nil
	}

	dataRange, err := d.StreamClose()
	if err != nil {
		return nil, err
	}
	closes, err := s.backfillMissingData(dataRange, latest.GetTime())
	if err != nil {
		return nil, err
	}
	macdLine, signalLine, _ := indicators.MACD(closes, int(s.fastPeriod.IntPart()), int(s.slowPeriod.IntPart()), int(s.signalPeriod.IntPart()))
	if len(macdLine) < 2 || len(signalLine) < 2 {
		es.AppendReason("Not enough data for signal generation")
		es.SetDirection(order.DoNothing)
		return &es, nil
	}
	prevMACD := decimal.MustFromFloat(macdLine[len(macdLine)-2])
	prevSignal := decimal.MustFromFloat(signalLine[len(signalLine)-2])
	currMACD := decimal.MustFromFloat(macdLine[len(macdLine)-1])
	currSignal := decimal.MustFromFloat(signalLine[len(signalLine)-1])

	hasDataAtTime, err := d.HasDataAtTime(latest.GetTime())
	if err != nil {
		return nil, err
	}
	if !hasDataAtTime {
		es.SetDirection(order.MissingData)
		es.AppendReasonf("missing data at %v, cannot perform any actions. MACD %v signal %v", latest.GetTime(), currMACD, currSignal)
		return &es, nil
	}

	switch {
	case prevMACD.LessThanOrEqual(prevSignal) && currMACD.GreaterThan(currSignal):
		es.SetDirection(order.Buy)
	case prevMACD.GreaterThanOrEqual(prevSignal) && currMACD.LessThan(currSignal):
		es.SetDirection(order.Sell)
	default:
		es.SetDirection(order.DoNothing)
	}
	es.AppendReasonf("MACD %v signal %v", currMACD, currSignal)

	return &es, nil
}

// SupportsSimultaneousProcessing highlights whether the strategy can handle multiple currency calculation
func (s *Strategy) SupportsSimultaneousProcessing() bool {
	return true
}

// OnSimultaneousSignals analyses multiple data points simultaneously, allowing flexibility
// in allowing a strategy to only place an order for X currency if Y currency's price is Z
func (s *Strategy) OnSimultaneousSignals(d []data.Handler, _ funding.IFundingTransferer, _ portfolio.Handler) ([]signal.Event, error) {
	var resp []signal.Event
	var errs error
	for i := range d {
		latest, err := d[i].Latest()
		if err != nil {
			return nil, err
		}
		sigEvent, err := s.OnSignal(d[i], nil, nil)
		if err != nil {
			errs = gctcommon.AppendError(errs, fmt.Errorf("%v %v %v %w",
				latest.GetExchange(),
				latest.GetAssetType(),
				latest.Pair(),
				err))
		} else {
			resp = append(resp, sigEvent)
		}
	}
	return resp, errs
}

// SetCustomSettings allows a user to modify the MACD periods in their config
func (s *Strategy) SetCustomSettings(customSettings map[string]any) error {
	for k, v := range customSettings {
		switch k {
		case fastPeriodKey:
			fastPeriod, ok := v.(float64)
			if !ok || fastPeriod <= 0 {
				return fmt.Errorf("%w provided macd-fast value could not be parsed: %v", base.ErrInvalidCustomSettings, v)
			}
			s.fastPeriod = decimal.MustFromFloat(fastPeriod)
		case slowPeriodKey:
			slowPeriod, ok := v.(float64)
			if !ok || slowPeriod <= 0 {
				return fmt.Errorf("%w provided macd-slow value could not be parsed: %v", base.ErrInvalidCustomSettings, v)
			}
			s.slowPeriod = decimal.MustFromFloat(slowPeriod)
		case signalPeriodKey:
			signalPeriod, ok := v.(float64)
			if !ok || signalPeriod <= 0 {
				return fmt.Errorf("%w provided macd-signal value could not be parsed: %v", base.ErrInvalidCustomSettings, v)
			}
			s.signalPeriod = decimal.MustFromFloat(signalPeriod)
		default:
			return fmt.Errorf("%w unrecognised custom setting key %v with value %v. Cannot apply", base.ErrInvalidCustomSettings, k, v)
		}
	}

	if !s.fastPeriod.IsZero() && !s.slowPeriod.IsZero() && s.fastPeriod.GreaterThanOrEqual(s.slowPeriod) {
		return fmt.Errorf("%w macd-fast %v must be smaller than macd-slow %v", base.ErrInvalidCustomSettings, s.fastPeriod, s.slowPeriod)
	}

	return nil
}

// SetDefaults sets the custom settings to their default values
func (s *Strategy) SetDefaults() {
	s.fastPeriod = decimal.NewFromInt(12)
	s.slowPeriod = decimal.NewFromInt(26)
	s.signalPeriod = decimal.NewFromInt(9)
}

// backfillMissingData will replace missing data with the previous candle's data
// this will ensure that MACD can be calculated correctly. A streak of missing
// data longer than the indicator's slow+signal warmup distorts the result and
// is refused.
func (s *Strategy) backfillMissingData(d []decimal.Decimal, t time.Time) ([]float64, error) {
	resp := make([]float64, len(d))
	var missingDataStreak int64
	limit := s.slowPeriod.IntPart() + s.signalPeriod.IntPart()
	for i := range d {
		if d[i].IsZero() && i > int(limit) {
			d[i] = d[i-1]
			missingDataStreak++
		} else {
			missingDataStreak = 0
		}
		if missingDataStreak >= limit {
			return nil, fmt.Errorf("missing data exceeds MACD warmup length of %v at %s and will distort results. %w",
				limit,
				t.Format(time.DateTime),
				base.ErrTooMuchBadData)
		}
		resp[i] = d[i].InexactFloat64()
	}
	return resp, nil
}
