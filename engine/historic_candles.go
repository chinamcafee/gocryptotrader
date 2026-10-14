package engine

import (
	"context"
	"errors"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	exchangeDB "github.com/thrasher-corp/gocryptotrader/database/repository/exchange"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

// iHistoricCandlesSource is the slice of an exchange the shared fetch needs.
type iHistoricCandlesSource interface {
	GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
	GetHistoricCandlesExtended(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
}

var _ iHistoricCandlesSource = exchange.IBotExchange(nil)

// historicCandlesOptions mirrors the four fetch modes of the historic candles
// request: use-db, exchange-request, sync and fill-missing-with-trades.
type historicCandlesOptions struct {
	ExchangeName          string
	UseDB                 bool
	ExRequest             bool
	Sync                  bool
	Force                 bool
	FillMissingWithTrades bool
}

// getHistoricCandlesShared is the single fetch path shared by the RPC handler
// and the LLM read-only tools. It deliberately performs no enabled-pair
// validation: the RPC layer gates on that (checkParams), while the LLM layer
// substitutes its own market-scope check -- the exchange wrappers themselves
// do not require a pair to be enabled.
func getHistoricCandlesShared(ctx context.Context, exch iHistoricCandlesSource, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time, opts historicCandlesOptions) (*kline.Item, error) {
	var (
		klineItem *kline.Item
		err       error
	)
	switch {
	case opts.UseDB:
		klineItem, err = kline.LoadFromDatabase(opts.ExchangeName,
			pair,
			a,
			interval,
			start,
			end)
	case opts.ExRequest:
		klineItem, err = exch.GetHistoricCandlesExtended(ctx, pair, a, interval, start, end)
	default:
		klineItem, err = exch.GetHistoricCandles(ctx, pair, a, interval, start, end)
	}
	if err != nil {
		return nil, err
	}

	if opts.FillMissingWithTrades {
		var tradeDataKline *kline.Item
		tradeDataKline, err = fillMissingCandlesWithStoredTrades(start, end, klineItem)
		if err != nil {
			return nil, err
		}
		klineItem.Candles = append(klineItem.Candles, tradeDataKline.Candles...)
	}

	if opts.Sync && !opts.UseDB {
		_, err = kline.StoreInDatabase(klineItem, opts.Force)
		if err != nil {
			if errors.Is(err, exchangeDB.ErrNoExchangeFound) {
				return nil, errors.New("exchange was not found in database, you can seed existing data or insert a new exchange via the dbseed")
			}
			return nil, err
		}
	}

	return klineItem, nil
}
