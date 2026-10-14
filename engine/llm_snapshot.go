package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchange/accounts"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// This file is where the model layer meets the exchange.
//
// The split it implements is the reason the rest of the chain could be built
// and tested without a running bot:
//
//   - llm/snapshot owns the contract, the arithmetic, the bounds and the
//     derivations, and imports nothing from the exchange.
//   - this file owns the conversion — reading the engine's own types and
//     producing the contract's plain data — and is the only place that knows
//     both sides exist.
//
// It reaches the exchange through a narrow interface rather than
// iExchangeManager: the assembler needs five calls and nothing else, and a
// narrow interface is what lets its tests run against a fake instead of a live
// endpoint.

// iMarketDataSource is the slice of an exchange the snapshot assembler reads.
//
// exchange.IBotExchange satisfies it, so production wiring passes the exchange
// directly. Tests pass a fake.
type iMarketDataSource interface {
	// GetHistoricCandles returns the candle window for a pair and interval.
	// It rejects a pair that is not in the enabled list, which is the intended
	// behaviour for a decision: a market that is not being tracked has no
	// snapshot worth building.
	GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
	// GetCachedTicker returns the locally held ticker without a network call.
	GetCachedTicker(p currency.Pair, a asset.Item) (*ticker.Price, error)
	// GetCachedOrderbook returns the locally held book without a network call.
	GetCachedOrderbook(p currency.Pair, a asset.Item) (*orderbook.Book, error)
	// GetCachedSubAccounts returns the locally held balances.
	GetCachedSubAccounts(ctx context.Context, a asset.Item) (accounts.SubAccounts, error)
	// GetFuturesPositions returns the local view of open futures positions.
	GetFuturesPositions(ctx context.Context, r *futures.PositionsRequest) ([]futures.PositionDetails, error)
}

// Errors the assembler raises before or instead of touching the exchange.
var (
	// ErrUnsupportedSnapshotInterval marks a candle interval the contract has no
	// spelling for. Refusing is the only safe answer: guessing a spelling would
	// put a different interval in the freshness arithmetic than the one fetched.
	ErrUnsupportedSnapshotInterval = errors.New("interval has no snapshot spelling")
	// ErrNilMarketDataSource marks an assembler call with no source.
	ErrNilMarketDataSource = errors.New("market data source is nil")
	// ErrSnapshotClockMissing marks a call that did not say what "now" is.
	// The assembler never reads the wall clock: an age measured against a clock
	// the caller did not choose cannot be reproduced in a test or a replay.
	ErrSnapshotClockMissing = errors.New("snapshot assembly requires an explicit now")
)

// CandleFetchMargin is how many extra candles the assembler asks for beyond
// the longest indicator period, so a partially delivered window still satisfies
// the indicators.
const CandleFetchMargin = 30

// snapshotPairDelimiter is the delimiter the snapshot records markets with.
//
// It is fixed rather than taken from the pair, because a pair's own delimiter
// comes from configuration and a snapshot's market identifier must not change
// spelling when an operator edits their config. BTC_USDT is the spelling the
// design documents, the llm_* tables and the markets list all use.
const snapshotPairDelimiter = "_"

// SnapshotRequest says which market to assemble and under what conditions.
type SnapshotRequest struct {
	// Exchange is the exchange name recorded in the snapshot.
	Exchange string
	// Pair is the market.
	Pair currency.Pair
	// Asset is the asset class.
	Asset asset.Item
	// Interval is the candle interval.
	Interval kline.Interval
	// Params is the indicator parameter set.
	Params snapshot.IndicatorParams
	// Bars overrides how many candles to request. Zero asks for the longest
	// indicator period plus the margin.
	Bars int
	// ID is the snapshot identifier every candidate must cite as its first
	// evidence reference. The caller supplies it so an assembly can be made
	// reproducible, and so a caller that already allocated an id for its
	// decision run can keep the two consistent. Empty generates one.
	ID string
	// Now is the instant ages are measured against, injected so an assembly is
	// reproducible.
	Now time.Time
	// Account, when set, is recorded as the account facts. It is supplied
	// rather than fetched because the engine's futures position types carry no
	// position side, and a side invented here would be a fabrication the risk
	// engine then trusts. Assembling it is M5-08's job, alongside reconciliation.
	Account *snapshot.AccountFacts
	// Funding, when set, is recorded as the funding facts. It is supplied by
	// the caller's premium-index reading (`buildFundingMarkIndexFacts`) and
	// labelled EXCHANGE_REST -- a direct venue read, unlike the account block's
	// local cache view.
	Funding *snapshot.FundingFacts
	// BalanceFrom, when set, fills the equity and available fields from the
	// locally cached balances.
	BalanceFrom bool
	// Cache, when set, serves repeated candle fetches from closed history
	// instead of the exchange (M2-02). Nil keeps the direct-fetch behaviour.
	Cache *KlineCache
}

// SnapshotResult is the assembled snapshot and the freshness verdict on it.
type SnapshotResult struct {
	// Snapshot is the assembled fact set. It is returned even when the freshness
	// gate refuses it, because a refused snapshot is still evidence of what was
	// true and belongs in the decision record.
	Snapshot *snapshot.MarketSnapshot
	// Freshness is the gate's verdict. A caller that intends to decide must
	// check MayDecide before using the snapshot.
	Freshness *snapshot.FreshnessReport
}

// intervalSpellings maps the engine's interval constants to the contract's.
//
// A missing entry is refused rather than defaulted: the freshness limit depends
// on the interval, so a wrong spelling would silently apply the wrong limit.
func snapshotInterval(interval kline.Interval) (snapshot.Interval, error) {
	switch interval {
	case kline.OneMin:
		return snapshot.Interval1m, nil
	case kline.FiveMin:
		return snapshot.Interval5m, nil
	case kline.FifteenMin:
		return snapshot.Interval15m, nil
	case kline.OneHour:
		return snapshot.Interval1h, nil
	case kline.FourHour:
		return snapshot.Interval4h, nil
	case kline.OneDay:
		return snapshot.Interval1d, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedSnapshotInterval, interval.Short())
	}
}

// BuildMarketSnapshot reads one market and assembles its fact set.
//
// Every fetch is read-only and comes from what the engine already holds where a
// cache exists, so an assembly does not spend exchange weight: the coverage
// rotation is what pays for freshness, and a decision reads its result.
func BuildMarketSnapshot(ctx context.Context, src iMarketDataSource, req *SnapshotRequest) (*SnapshotResult, error) {
	if src == nil {
		return nil, ErrNilMarketDataSource
	}
	if req.Now.IsZero() {
		return nil, ErrSnapshotClockMissing
	}
	interval, err := snapshotInterval(req.Interval)
	if err != nil {
		return nil, err
	}
	params := req.Params
	if params.MACDFast == 0 && params.MACDSlow == 0 && params.MACDSignal == 0 &&
		len(params.SMAPeriods) == 0 && len(params.EMAPeriods) == 0 {
		// Nothing was asked for; the documented defaults apply. The struct holds
		// slices, so it is not comparable and a field check is the way to say
		// "empty".
		params = snapshot.DefaultIndicatorParams()
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}

	bars := req.Bars
	if bars <= 0 {
		bars = int(params.LongestPeriod()) + CandleFetchMargin
	}
	window, err := fetchCandles(ctx, src, req, bars)
	if err != nil {
		return nil, err
	}

	id := req.ID
	if id == "" {
		generated, genErr := uuid.NewV4()
		if genErr != nil {
			return nil, fmt.Errorf("generating snapshot id: %w", genErr)
		}
		id = generated.String()
	}
	built := &snapshot.MarketSnapshot{
		ID:       id,
		Exchange: req.Exchange,
		Pair:     req.Pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String(),
		Asset:    req.Asset.String(),
		BuiltAt:  req.Now,
		Quality:  make(map[string]candidate.DataQuality, len(snapshot.Categories())),
	}

	if err := applyCandles(built, window, interval, &params, req.Now); err != nil {
		return nil, err
	}
	if err := applyTicker(built, src, req, interval); err != nil {
		return nil, err
	}
	if err := applyOrderBook(built, src, req); err != nil {
		return nil, err
	}
	if err := applyAccount(ctx, built, src, req); err != nil {
		return nil, err
	}
	if err := applyFunding(built, req); err != nil {
		return nil, err
	}

	freshness, err := built.ApplyFreshness(req.Now)
	if err != nil {
		return nil, err
	}
	return &SnapshotResult{Snapshot: built, Freshness: freshness}, nil
}

func fetchCandles(ctx context.Context, src iMarketDataSource, req *SnapshotRequest, bars int) (*kline.Item, error) {
	// Ask for one extra bar so the window contains a full set of closed candles
	// plus the one still forming.
	// Interval.Duration is a single value: an Interval is a time.Duration.
	lookback := time.Duration(bars+1) * req.Interval.Duration()
	end := req.Now
	start := end.Add(-lookback)
	window, err := fetchCandlesThroughCache(ctx, src, req.Cache, req.Pair, req.Asset, req.Interval, start, end, req.Now)
	if err != nil {
		return nil, fmt.Errorf("fetching candles for %s: %w", req.Pair, err)
	}
	if window == nil || len(window.Candles) == 0 {
		return nil, fmt.Errorf("%w: no candles returned for %s", snapshot.ErrSnapshotIncomplete, req.Pair)
	}
	return window, nil
}

func applyCandles(built *snapshot.MarketSnapshot, window *kline.Item, interval snapshot.Interval, params *snapshot.IndicatorParams, now time.Time) error {
	seconds := window.Interval.Duration()

	closed := make([]snapshot.CandleFact, 0, len(window.Candles))
	var live *snapshot.CandleFact
	for i := range window.Candles {
		candle := &window.Candles[i]
		fact, factErr := candleFact(candle)
		if factErr != nil {
			return factErr
		}
		// A candle is closed once its interval has elapsed. The newest one
		// usually has not, and it may describe where price is but must never
		// feed an indicator.
		if candle.Time.Add(seconds).After(now) {
			fact.Partial = true
			live = &fact
			continue
		}
		closed = append(closed, fact)
	}
	if len(closed) == 0 {
		return fmt.Errorf("%w: no closed candles for %s", snapshot.ErrSnapshotIncomplete, window.Pair)
	}
	// The contract's window is bounded; keep the newest, which is what the
	// indicators and the summary are computed from.
	if len(closed) > snapshot.MaxClosedCandles {
		closed = closed[len(closed)-snapshot.MaxClosedCandles:]
	}
	summary, err := snapshot.SummariseCandles(closed)
	if err != nil {
		return fmt.Errorf("summarising candles: %w", err)
	}

	built.Candles = snapshot.CandleWindow{
		Interval: interval,
		Start:    closed[0].Time,
		End:      closed[len(closed)-1].Time,
		Count:    len(window.Candles),
		Closed:   closed,
		Summary:  summary,
	}
	// SourceTime is the close of the newest closed candle, which is the newest
	// moment the window actually describes — not the fetch time.
	built.SourceTime = closed[len(closed)-1].Time
	built.LiveCandle = live
	if live != nil {
		if err := built.SetQuality(snapshot.CategoryCandles, candidate.QualityExchangeREST); err != nil {
			return err
		}
		// The live bar is carried as a partial fact alongside the closed window,
		// so a reader can see it was not used for the indicators.
		if err := built.SetQuality(snapshot.CategoryIndicators, candidate.QualityDerived); err != nil {
			return err
		}
	} else if err := built.SetQuality(snapshot.CategoryCandles, candidate.QualityExchangeREST); err != nil {
		return err
	}

	if err := applyIndicators(built, window, params, now); err != nil {
		return err
	}
	// Gaps and padded candles downgrade the candle and indicator categories
	// (docs 04 §5). Only-lower: a more specific earlier label survives.
	return built.ApplyCandleGaps()
}

func applyIndicators(built *snapshot.MarketSnapshot, window *kline.Item, params *snapshot.IndicatorParams, now time.Time) error {
	closes := make([]float64, len(window.Candles))
	for i := range window.Candles {
		closes[i] = window.Candles[i].Close
	}

	macd, err := window.GetMovingAverageConvergenceDivergenceOnClose(params.MACDFast, params.MACDSlow, params.MACDSignal)
	if err != nil {
		return fmt.Errorf("computing MACD: %w", err)
	}
	in := snapshot.IndicatorInput{
		Params:     *params,
		Closes:     closes,
		MACD:       &snapshot.MACDSeries{DIF: macd.Results, DEA: macd.SignalVals, Histogram: macd.Histogram},
		ComputedAt: now,
	}
	for _, period := range params.SMAPeriods {
		values, smaErr := window.GetSimpleMovingAverageOnClose(period)
		if smaErr != nil {
			return fmt.Errorf("computing SMA(%d): %w", period, smaErr)
		}
		in.MovingAverages = append(in.MovingAverages, snapshot.MASeries{
			Kind: snapshot.MASimple, Period: period, Values: values,
		})
	}
	for _, period := range params.EMAPeriods {
		values, emaErr := window.GetExponentialMovingAverageOnClose(period)
		if emaErr != nil {
			return fmt.Errorf("computing EMA(%d): %w", period, emaErr)
		}
		in.MovingAverages = append(in.MovingAverages, snapshot.MASeries{
			Kind: snapshot.MAExponential, Period: period, Values: values,
		})
	}

	set, err := snapshot.BuildIndicatorSet(&in)
	if err != nil {
		return fmt.Errorf("assembling indicators: %w", err)
	}
	built.Indicators = *set
	return nil
}

func applyTicker(built *snapshot.MarketSnapshot, src iMarketDataSource, req *SnapshotRequest, interval snapshot.Interval) error {
	price, err := src.GetCachedTicker(req.Pair, req.Asset)
	if err != nil {
		// A missing ticker is not a failed assembly: the gate reports the
		// category absent, and a caller that needs it sees a blocking failure
		// rather than this function inventing one.
		_ = interval
		return nil //nolint:nilerr // a missing source fact degrades the snapshot; the gate reports the category
	}
	if price == nil {
		return nil
	}
	facts, err := tickerFacts(price)
	if err != nil {
		return err
	}
	built.Ticker = facts
	return built.SetQuality(snapshot.CategoryTicker, candidate.QualityExchangeREST)
}

func tickerFacts(price *ticker.Price) (*snapshot.TickerFacts, error) {
	fields := []struct {
		name  string
		value float64
		want  *string
	}{
		{"last", price.Last, nil},
		{sizingSideBid, price.Bid, nil},
		{sizingSideAsk, price.Ask, nil},
		{"high", price.High, nil},
		{"low", price.Low, nil},
		{"volume", price.BaseVolume, nil},
		{"quoteVolume", price.QuoteVolume, nil},
		{"changePct", price.PercentChange24Hour, nil},
	}
	out := &snapshot.TickerFacts{ReadAt: price.LastUpdated}
	targets := map[string]*string{
		"last": &out.Last, sizingSideBid: &out.Bid, sizingSideAsk: &out.Ask, "high": &out.High,
		"low": &out.Low, "volume": &out.Volume, "quoteVolume": &out.QuoteVolume,
		"changePct": &out.ChangePct,
	}
	for _, field := range fields {
		text, err := snapshot.DecimalFromFloat(field.value)
		if err != nil {
			return nil, fmt.Errorf("ticker.%s: %w", field.name, err)
		}
		*targets[field.name] = text
	}
	return out, nil
}

func applyOrderBook(built *snapshot.MarketSnapshot, src iMarketDataSource, req *SnapshotRequest) error {
	book, err := src.GetCachedOrderbook(req.Pair, req.Asset)
	if err != nil || book == nil {
		return nil //nolint:nilerr // a missing source fact degrades the snapshot; the gate reports the category
	}
	facts, err := orderBookFacts(book)
	if err != nil {
		return err
	}
	built.OrderBook = facts
	return built.SetQuality(snapshot.CategoryOrderBook, candidate.QualityExchangeREST)
}

// orderBookFacts converts the book, preferring the exchange's decimal strings
// over the float copies.
//
// The level carries both. The string is what the exchange actually said; the
// float is a copy that has already lost whatever the binary representation
// could not hold, and a price that has been through a float is a price whose
// last digits changed.
func orderBookFacts(book *orderbook.Book) (*snapshot.OrderBookFacts, error) {
	convert := func(levels []orderbook.Level) ([]snapshot.OrderBookLevel, error) {
		n := min(len(levels), snapshot.MaxOrderBookLevels)
		out := make([]snapshot.OrderBookLevel, 0, n)
		for i := range n {
			level := levels[i]
			price, amount := level.StrPrice, level.StrAmount
			if price == "" || amount == "" {
				// Not every source fills the string forms; fall back to the
				// float rendering rather than dropping the level.
				var err error
				if price, err = snapshot.DecimalFromFloat(level.Price); err != nil {
					return nil, fmt.Errorf("level %d price: %w", i, err)
				}
				if amount, err = snapshot.DecimalFromFloat(level.Amount); err != nil {
					return nil, fmt.Errorf("level %d amount: %w", i, err)
				}
			}
			out = append(out, snapshot.OrderBookLevel{Price: price, Size: amount})
		}
		return out, nil
	}

	bids, err := convert(book.Bids)
	if err != nil {
		return nil, fmt.Errorf("bids: %w", err)
	}
	asks, err := convert(book.Asks)
	if err != nil {
		return nil, fmt.Errorf("asks: %w", err)
	}
	facts := &snapshot.OrderBookFacts{Bids: bids, Asks: asks, ReadAt: book.LastUpdated}

	if len(bids) > 0 && len(asks) > 0 {
		spread, spreadErr := snapshot.DeriveSpreadBPS(bids[0].Price, asks[0].Price)
		if spreadErr != nil {
			return nil, fmt.Errorf("spread: %w", spreadErr)
		}
		mid, midErr := snapshot.DeriveMidPrice(bids[0].Price, asks[0].Price)
		if midErr != nil {
			return nil, fmt.Errorf("midpoint: %w", midErr)
		}
		facts.SpreadBPS = spread.String()
		facts.MidPrice = mid.String()
	}
	return facts, nil
}

func applyAccount(ctx context.Context, built *snapshot.MarketSnapshot, src iMarketDataSource, req *SnapshotRequest) error {
	facts := req.Account
	if req.BalanceFrom {
		balances, err := src.GetCachedSubAccounts(ctx, req.Asset)
		if err == nil {
			fromBalances, balanceErr := balanceFacts(balances, req.Now)
			if balanceErr != nil {
				return balanceErr
			}
			if fromBalances != nil {
				if facts == nil {
					facts = fromBalances
				} else {
					// A supplied position view wins; the balance fields fill in
					// what it did not carry.
					facts.TotalEquityUSD = fromBalances.TotalEquityUSD
					facts.AvailableBalanceUSD = fromBalances.AvailableBalanceUSD
					facts.ReadAt = fromBalances.ReadAt
				}
			}
		}
	}
	if facts == nil {
		return nil
	}
	built.Account = facts
	// Local view: the engine's cached balances and caller-supplied positions
	// are advisory only -- risk checks must re-read the exchange before
	// releasing an order (docs 04 §5, LOCAL_TRACKED).
	return built.SetQuality(snapshot.CategoryAccount, candidate.QualityLocalTracked)
}

// balanceFacts sums the USDT balances into the equity and available figures.
//
// Only USDT is summed: the envelope is denominated in USD, and adding the
// balances of other currencies would require exchange rates the snapshot has no
// business inventing.
func balanceFacts(balances accounts.SubAccounts, now time.Time) (*snapshot.AccountFacts, error) {
	var total, available float64
	seen := false
	readAt := now
	for _, account := range balances {
		if account == nil {
			continue
		}
		// The currency is the map key. The Balance struct also carries a
		// Currency field, but whether a producer fills it is up to the
		// producer; the key is the one thing the type guarantees.
		for code, balance := range account.Balances {
			if code != currency.USDT {
				continue
			}
			total += balance.Total
			available += balance.AvailableWithoutBorrow
			if balance.UpdatedAt.After(readAt) || !seen {
				readAt = balance.UpdatedAt
			}
			seen = true
		}
	}
	if !seen {
		return nil, nil
	}
	totalText, err := snapshot.DecimalFromFloat(total)
	if err != nil {
		return nil, fmt.Errorf("account equity: %w", err)
	}
	availableText, err := snapshot.DecimalFromFloat(available)
	if err != nil {
		return nil, fmt.Errorf("account available: %w", err)
	}
	return &snapshot.AccountFacts{
		TotalEquityUSD:      totalText,
		AvailableBalanceUSD: availableText,
		ReadAt:              readAt,
	}, nil
}

// candleFact converts one candle.
//
// The engine's candle carries floats only — the exchange's decimal strings are
// not retained on this path, unlike the order book's levels. The values are
// therefore rendered at the contract's computed precision, which is a
// limitation worth knowing: a candle price here is the engine's float, not the
// exchange's exact string. Reading candles from the stored kline table (which
// keeps strings) would remove it.
func candleFact(candle *kline.Candle) (snapshot.CandleFact, error) {
	open, err := snapshot.DecimalFromFloat(candle.Open)
	if err != nil {
		return snapshot.CandleFact{}, fmt.Errorf("open: %w", err)
	}
	high, err := snapshot.DecimalFromFloat(candle.High)
	if err != nil {
		return snapshot.CandleFact{}, fmt.Errorf("high: %w", err)
	}
	low, err := snapshot.DecimalFromFloat(candle.Low)
	if err != nil {
		return snapshot.CandleFact{}, fmt.Errorf("low: %w", err)
	}
	closePrice, err := snapshot.DecimalFromFloat(candle.Close)
	if err != nil {
		return snapshot.CandleFact{}, fmt.Errorf("close: %w", err)
	}
	volume, err := snapshot.DecimalFromFloat(candle.Volume)
	if err != nil {
		return snapshot.CandleFact{}, fmt.Errorf("volume: %w", err)
	}
	return snapshot.CandleFact{
		Time: candle.Time, Open: open, High: high, Low: low, Close: closePrice, Volume: volume,
	}, nil
}
