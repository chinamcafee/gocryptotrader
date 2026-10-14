package sma200trend

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
	Name         = "sma200trend"
	smaPeriodKey = "sma-period"
	description  = `The SMA trend strategy buys when the close crosses above the simple moving average (default 200 periods) and sells when it crosses below. It is the fixed-rule baseline (Q24=A) the LLM decisions are compared against.`
)

// Strategy is an implementation of the Handler interface
type Strategy struct {
	base.Strategy
	smaPeriod decimal.Decimal
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
// A close crossing above the SMA buys; a close crossing below sells.
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

	if offset := latest.GetOffset(); offset <= s.smaPeriod.IntPart() {
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
	sma := indicators.SMA(closes, int(s.smaPeriod.IntPart()))
	if len(sma) < 2 {
		es.AppendReason("Not enough data for signal generation")
		es.SetDirection(order.DoNothing)
		return &es, nil
	}
	prevClose := decimal.MustFromFloat(closes[len(closes)-2])
	prevSMA := decimal.MustFromFloat(sma[len(sma)-2])
	currClose := latest.GetClosePrice()
	currSMA := decimal.MustFromFloat(sma[len(sma)-1])

	hasDataAtTime, err := d.HasDataAtTime(latest.GetTime())
	if err != nil {
		return nil, err
	}
	if !hasDataAtTime {
		es.SetDirection(order.MissingData)
		es.AppendReasonf("missing data at %v, cannot perform any actions. close %v SMA %v", latest.GetTime(), currClose, currSMA)
		return &es, nil
	}

	switch {
	case prevClose.LessThanOrEqual(prevSMA) && currClose.GreaterThan(currSMA):
		es.SetDirection(order.Buy)
	case prevClose.GreaterThanOrEqual(prevSMA) && currClose.LessThan(currSMA):
		es.SetDirection(order.Sell)
	default:
		es.SetDirection(order.DoNothing)
	}
	es.AppendReasonf("close %v SMA %v", currClose, currSMA)

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

// SetCustomSettings allows a user to modify the SMA period in their config
func (s *Strategy) SetCustomSettings(customSettings map[string]any) error {
	for k, v := range customSettings {
		switch k {
		case smaPeriodKey:
			smaPeriod, ok := v.(float64)
			if !ok || smaPeriod <= 0 {
				return fmt.Errorf("%w provided sma-period value could not be parsed: %v", base.ErrInvalidCustomSettings, v)
			}
			s.smaPeriod = decimal.MustFromFloat(smaPeriod)
		default:
			return fmt.Errorf("%w unrecognised custom setting key %v with value %v. Cannot apply", base.ErrInvalidCustomSettings, k, v)
		}
	}

	return nil
}

// SetDefaults sets the custom settings to their default values
func (s *Strategy) SetDefaults() {
	s.smaPeriod = decimal.NewFromInt(200)
}

// backfillMissingData will replace missing data with the previous candle's data
// this will ensure that the SMA can be calculated correctly. A streak of missing
// data longer than the period distorts the result and is refused.
func (s *Strategy) backfillMissingData(d []decimal.Decimal, t time.Time) ([]float64, error) {
	resp := make([]float64, len(d))
	var missingDataStreak int64
	for i := range d {
		if d[i].IsZero() && i > int(s.smaPeriod.IntPart()) {
			d[i] = d[i-1]
			missingDataStreak++
		} else {
			missingDataStreak = 0
		}
		if missingDataStreak >= s.smaPeriod.IntPart() {
			return nil, fmt.Errorf("missing data exceeds SMA period length of %v at %s and will distort results. %w",
				s.smaPeriod,
				t.Format(time.DateTime),
				base.ErrTooMuchBadData)
		}
		resp[i] = d[i].InexactFloat64()
	}
	return resp, nil
}
