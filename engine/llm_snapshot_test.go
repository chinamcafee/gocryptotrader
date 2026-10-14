package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchange/accounts"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// fakeSource stands in for an exchange. The assembler reaches the exchange
// through iMarketDataSource precisely so this can exist: the conversion and the
// freshness wiring are testable without a running bot or a network.
type fakeSource struct {
	candles   *kline.Item
	candleErr error

	ticker    *ticker.Price
	tickerErr error

	book    *orderbook.Book
	bookErr error

	balances   accounts.SubAccounts
	balanceErr error

	// seenRecords records the arguments of the last candle call, so a test can
	// assert the fetch window rather than trusting it.
	seenStart    time.Time
	seenEnd      time.Time
	seenPair     currency.Pair
	seenAsset    asset.Item
	seenInterval kline.Interval
	callCount    int
}

func (f *fakeSource) GetHistoricCandles(_ context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error) {
	f.callCount++
	f.seenStart, f.seenEnd, f.seenPair, f.seenAsset, f.seenInterval = start, end, pair, a, interval
	if f.candleErr != nil {
		return nil, f.candleErr
	}
	return f.candles, nil
}

func (f *fakeSource) GetCachedTicker(currency.Pair, asset.Item) (*ticker.Price, error) {
	return f.ticker, f.tickerErr
}

func (f *fakeSource) GetCachedOrderbook(currency.Pair, asset.Item) (*orderbook.Book, error) {
	return f.book, f.bookErr
}

func (f *fakeSource) GetCachedSubAccounts(context.Context, asset.Item) (accounts.SubAccounts, error) {
	return f.balances, f.balanceErr
}

func (f *fakeSource) GetFuturesPositions(context.Context, *futures.PositionsRequest) ([]futures.PositionDetails, error) {
	return nil, nil
}

// testNow is the instant every fixture is measured against.
var testNow = time.Date(2026, 10, 10, 12, 30, 0, 0, time.UTC)

// buildFakeCandles returns n closed hourly candles ending at the hour before
// now, plus optionally the bar still forming.
func buildFakeCandles(n int, withLive bool) *kline.Item {
	item := &kline.Item{
		Exchange: "Binance",
		Pair:     currency.NewBTCUSDT(),
		Asset:    asset.USDTMarginedFutures,
		Interval: kline.OneHour,
	}
	// The newest closed bar opened at the top of the hour before now.
	lastClosedOpen := testNow.Truncate(time.Hour).Add(-time.Hour)
	for i := range n {
		open := lastClosedOpen.Add(-time.Duration(n-1-i) * time.Hour)
		price := 100 + float64(i)*0.5
		item.Candles = append(item.Candles, kline.Candle{
			Time: open, Open: price, High: price + 1, Low: price - 1, Close: price + 0.25, Volume: 10,
		})
	}
	if withLive {
		open := testNow.Truncate(time.Hour)
		item.Candles = append(item.Candles, kline.Candle{
			Time: open, Open: 200, High: 205, Low: 199, Close: 204, Volume: 3,
		})
	}
	return item
}

func goodTicker() *ticker.Price {
	return &ticker.Price{
		Last: 204, Bid: 203.9, Ask: 204.1, High: 210, Low: 99,
		BaseVolume: 1234, QuoteVolume: 250000, PercentChange24Hour: 1.25,
		LastUpdated: testNow.Add(-5 * time.Second),
	}
}

func goodBook() *orderbook.Book {
	return &orderbook.Book{
		Bids: orderbook.Levels{
			{Price: 203.9, StrPrice: "203.90000000", Amount: 2, StrAmount: "2.00000000"},
			{Price: 203.5, StrPrice: "203.50000000", Amount: 5, StrAmount: "5.00000000"},
		},
		Asks: orderbook.Levels{
			{Price: 204.1, StrPrice: "204.10000000", Amount: 1.5, StrAmount: "1.50000000"},
		},
		LastUpdated: testNow.Add(-3 * time.Second),
	}
}

func newFake() *fakeSource {
	return &fakeSource{
		candles: buildFakeCandles(250, true),
		ticker:  goodTicker(),
		book:    goodBook(),
	}
}

func baseRequest() *SnapshotRequest {
	return &SnapshotRequest{
		Exchange: "Binance",
		Pair:     currency.NewBTCUSDT(),
		Asset:    asset.USDTMarginedFutures,
		Interval: kline.OneHour,
		Now:      testNow,
	}
}

func TestBuildMarketSnapshotHappyPath(t *testing.T) {
	t.Parallel()

	src := newFake()
	result, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "a complete source must assemble")

	s := result.Snapshot
	require.NotNil(t, s, "a snapshot must be returned")
	assert.Equal(t, "Binance", s.Exchange, "the exchange should be recorded")
	assert.Equal(t, "BTC_USDT", s.Pair,
		"the pair must be recorded with a fixed delimiter, so the market identifier does not change spelling with configuration")
	assert.NotEmpty(t, s.ID, "the snapshot should carry an identifier for candidates to cite")
	assert.Equal(t, "usdtmarginedfutures", s.Asset, "the asset should be recorded")
	assert.Equal(t, testNow, s.BuiltAt, "the build time should be the injected instant")
	assert.Equal(t, snapshot.Interval1h, s.Candles.Interval, "the contract's interval spelling should be used")

	// SourceTime is the close of the newest closed candle, not the fetch time.
	require.NotEmpty(t, s.Candles.Closed, "closed candles must be carried")
	newestClosed := s.Candles.Closed[len(s.Candles.Closed)-1]
	assert.Equal(t, newestClosed.Time, s.SourceTime,
		"the source time must be the newest closed candle's open, which is the newest moment the window describes")
	require.NotNil(t, s.LiveCandle, "the bar still forming must be carried separately")
	assert.Equal(t, testNow.Truncate(time.Hour), s.LiveCandle.Time, "the live bar is the one that opened this hour")
	assert.True(t, s.LiveCandle.Time.After(s.SourceTime), "the live bar should be newer than the closed window")

	require.NotNil(t, s.Indicators.MACD, "indicators must be assembled")
	assert.Equal(t, int64(12), s.Indicators.MACD.Fast, "the default parameter set should be applied")
	assert.Len(t, s.Indicators.MovingAverages, 7, "SMA 20/50/200 and EMA 12/26/50/200 should all be present")
	require.NotNil(t, s.Ticker, "the ticker must be carried")
	require.NotNil(t, s.OrderBook, "the book must be carried")

	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(snapshot.CategoryCandles), "candles come from the exchange")
	assert.Equal(t, candidate.QualityDerived, s.QualityOf(snapshot.CategoryIndicators), "indicators are derived")
	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(snapshot.CategoryTicker), "the ticker comes from the exchange")
	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(snapshot.CategoryOrderBook), "the book comes from the exchange")

	require.NoError(t, s.Validate(), "the assembled snapshot must pass the contract's own validation")
	assert.True(t, result.Freshness.MayDecide(), "a snapshot built from readings seconds old should be decidable")
	assert.Empty(t, result.Freshness.BlockingStale, "nothing should be stale")
}

func TestFetchWindowCoversTheIndicators(t *testing.T) {
	t.Parallel()

	src := newFake()
	_, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "the fixture must assemble")

	assert.Equal(t, testNow, src.seenEnd, "the window should end at the injected instant, not at the wall clock")
	// Default bars = 200 (longest period) + 30 margin, plus one for the forming bar.
	wantLookback := time.Duration(231) * time.Hour
	assert.Equal(t, testNow.Add(-wantLookback), src.seenStart,
		"the window must reach back far enough for the longest indicator plus the margin")
	assert.Equal(t, currency.NewBTCUSDT(), src.seenPair, "the pair should be passed through")
	assert.Equal(t, asset.USDTMarginedFutures, src.seenAsset, "the asset should be passed through")
	assert.Equal(t, kline.OneHour, src.seenInterval, "the interval should be passed through")
	assert.Equal(t, 1, src.callCount, "one assembly should fetch candles once")
}

func TestGeneratedSnapshotIDAndSuppliedID(t *testing.T) {
	t.Parallel()

	// An id is per assembly, not per content: two assemblies at different
	// instants are different snapshots and must be distinguishable in the
	// decision record.
	a, err := BuildMarketSnapshot(context.Background(), newFake(), baseRequest())
	require.NoError(t, err, "the assembly must succeed")
	b, err := BuildMarketSnapshot(context.Background(), newFake(), baseRequest())
	require.NoError(t, err, "the assembly must succeed")
	assert.NotEmpty(t, a.Snapshot.ID, "an id should be generated when none is supplied")
	assert.NotEqual(t, a.Snapshot.ID, b.Snapshot.ID, "two assemblies should not share an id")

	// A supplied id is honoured, which is what lets a caller keep the snapshot
	// and the decision run that cites it consistent.
	req := baseRequest()
	req.ID = "22222222-2222-4222-8222-222222222222"
	supplied, err := BuildMarketSnapshot(context.Background(), newFake(), req)
	require.NoError(t, err, "the assembly must succeed")
	assert.Equal(t, req.ID, supplied.Snapshot.ID, "a supplied id should be used verbatim")
	require.NoError(t, supplied.Snapshot.Validate(), "the snapshot must be valid with an id")
}

func TestBuildRefusesMisuse(t *testing.T) {
	t.Parallel()

	_, err := BuildMarketSnapshot(context.Background(), nil, baseRequest())
	require.ErrorIs(t, err, ErrNilMarketDataSource, "a nil source must be refused")

	req := baseRequest()
	req.Now = time.Time{}
	_, err = BuildMarketSnapshot(context.Background(), newFake(), req)
	require.ErrorIs(t, err, ErrSnapshotClockMissing,
		"the assembler must not read the wall clock: an age measured against an unstated instant cannot be reproduced")

	req = baseRequest()
	req.Interval = kline.ThreeDay
	_, err = BuildMarketSnapshot(context.Background(), newFake(), req)
	require.ErrorIs(t, err, ErrUnsupportedSnapshotInterval,
		"an interval the contract cannot spell must be refused rather than defaulted, because the freshness limit depends on it")

	req = baseRequest()
	req.Params = snapshot.IndicatorParams{MACDFast: 30, MACDSlow: 26, MACDSignal: 9}
	_, err = BuildMarketSnapshot(context.Background(), newFake(), req)
	require.ErrorIs(t, err, snapshot.ErrIndicatorParamsInvalid, "an impossible parameter set must be refused")
}

func TestProviderErrorsAreNotSwallowed(t *testing.T) {
	t.Parallel()

	// A disabled pair is a decision the engine already made; the assembler must
	// pass it up rather than produce a snapshot for a market it cannot read.
	src := newFake()
	src.candleErr = errors.New("currency not enabled")
	_, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.Error(t, err, "the fetch error must surface")
	assert.Contains(t, err.Error(), "currency not enabled", "the reason should survive the wrapping")

	src = newFake()
	src.candles = &kline.Item{Interval: kline.OneHour}
	_, err = BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.ErrorIs(t, err, snapshot.ErrSnapshotIncomplete, "an empty window must be refused")

	src = newFake()
	src.candles = nil
	_, err = BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.ErrorIs(t, err, snapshot.ErrSnapshotIncomplete, "a nil window must be refused")
}

func TestMissingCachedReadingsDoNotFailTheAssembly(t *testing.T) {
	t.Parallel()

	// The caches are populated by the engine's own loops. A cold cache is a
	// normal state at startup and must not make assembly impossible; the
	// freshness gate is what reports the category as unusable.
	src := newFake()
	src.ticker, src.tickerErr = nil, errors.New("ticker not found")
	src.book, src.bookErr = nil, errors.New("orderbook not found")

	result, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "a cold cache must still assemble")

	assert.Nil(t, result.Snapshot.Ticker, "an absent ticker should stay absent, not be invented")
	assert.Nil(t, result.Snapshot.OrderBook, "an absent book should stay absent")
	assert.Equal(t, candidate.QualityUnknown, result.Snapshot.QualityOf(snapshot.CategoryTicker),
		"a category nobody supplied must not default to trusted")
	assert.NotContains(t, result.Freshness.BlockingStale, snapshot.CategoryTicker,
		"an absent fact is not a stale fact, so the gate must not report it as one")
	assert.True(t, result.Freshness.MayDecide(), "the snapshot is still decidable on what it has")
}

func TestOrderBookKeepsTheExchangesDecimalStrings(t *testing.T) {
	t.Parallel()

	src := newFake()
	result, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "the fixture must assemble")

	book := result.Snapshot.OrderBook
	require.NotNil(t, book, "the book must be carried")
	require.Len(t, book.Bids, 2, "every level must survive")
	assert.Equal(t, "203.90000000", book.Bids[0].Price,
		"the exchange's decimal string must be carried, not the float copy: a price that has been through a float is a different price")
	assert.Equal(t, "2.00000000", book.Bids[0].Size, "the amount string should be carried too")
	assert.NotEmpty(t, book.SpreadBPS, "the spread should be derived")
	assert.NotEmpty(t, book.MidPrice, "the midpoint should be derived")
	assert.Equal(t, testNow.Add(-3*time.Second), book.ReadAt, "the book's own read time should be recorded")
}

func TestFreshnessGateIsAppliedBeforeReturning(t *testing.T) {
	t.Parallel()

	src := newFake()
	src.ticker.LastUpdated = testNow.Add(-5 * time.Minute)
	result, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "a stale reading must not fail the assembly")

	assert.False(t, result.Freshness.MayDecide(), "a five-minute-old ticker should stop the decision")
	assert.Contains(t, result.Freshness.BlockingStale, snapshot.CategoryTicker, "the ticker should be named")
	assert.Contains(t, result.Freshness.ReasonCodes, snapshot.CodeRiskSnapshotStale, "the stale code should be present")
	assert.Equal(t, candidate.QualityStale, result.Snapshot.QualityOf(snapshot.CategoryTicker),
		"the snapshot must be marked, so a later reader sees why without re-measuring")
	require.NotNil(t, result.Snapshot, "a refused snapshot is still evidence of what was true and must be returned")
}

func TestSuppliedAccountAndBalances(t *testing.T) {
	t.Parallel()

	src := newFake()
	src.balances = accounts.SubAccounts{
		&accounts.SubAccount{
			AssetType: asset.USDTMarginedFutures,
			Balances: accounts.CurrencyBalances{
				currency.USDT: {Total: 150.5, AvailableWithoutBorrow: 120.25, UpdatedAt: testNow.Add(-8 * time.Second)},
				currency.BTC:  {Total: 1, AvailableWithoutBorrow: 1, UpdatedAt: testNow},
			},
		},
	}

	req := baseRequest()
	req.BalanceFrom = true
	result, err := BuildMarketSnapshot(context.Background(), src, req)
	require.NoError(t, err, "balances must fill in")
	require.NotNil(t, result.Snapshot.Account, "the account facts must be present")
	assert.Equal(t, "150.5", result.Snapshot.Account.TotalEquityUSD,
		"only USDT must be summed: adding other currencies needs rates the snapshot must not invent")
	assert.Equal(t, "120.25", result.Snapshot.Account.AvailableBalanceUSD, "the available figure should come from the balance")
	assert.Equal(t, testNow.Add(-8*time.Second), result.Snapshot.Account.ReadAt, "the balance's own read time should be used")

	// A supplied position view wins, and the balances fill only what it lacked.
	req.Account = &snapshot.AccountFacts{
		TotalEquityUSD: "1", AvailableBalanceUSD: "1", ReadAt: testNow,
		Positions: []snapshot.PositionFact{{Symbol: "BTC_USDT", Side: "LONG", Size: "0.01"}},
	}
	result, err = BuildMarketSnapshot(context.Background(), src, req)
	require.NoError(t, err, "a supplied account must be accepted")
	assert.Len(t, result.Snapshot.Account.Positions, 1, "the supplied positions should survive")
	assert.Equal(t, "150.5", result.Snapshot.Account.TotalEquityUSD, "the balance should fill the equity it did not carry")

	// No balances and no supplied account leaves the category absent.
	src.balances = nil
	req.Account = nil
	result, err = BuildMarketSnapshot(context.Background(), src, req)
	require.NoError(t, err, "the assembly must succeed without account facts")
	assert.Nil(t, result.Snapshot.Account, "no account facts means the category is absent")
	assert.Equal(t, candidate.QualityUnknown, result.Snapshot.QualityOf(snapshot.CategoryAccount),
		"an absent account must not be assumed good")
}

func TestCandleWindowIsBoundedAndTheLiveBarIsSeparated(t *testing.T) {
	t.Parallel()

	src := newFake()
	src.candles = buildFakeCandles(400, true)
	result, err := BuildMarketSnapshot(context.Background(), src, baseRequest())
	require.NoError(t, err, "a long window must assemble")

	closed := result.Snapshot.Candles.Closed
	require.Len(t, closed, snapshot.MaxClosedCandles, "the closed window must be capped at the contract's bound")
	// The newest closed bar must survive the trim, or the source time and the
	// indicators would describe a bar that is not carried.
	assert.Equal(t, result.Snapshot.SourceTime, closed[len(closed)-1].Time, "the newest closed bar should be the one kept")
	assert.Equal(t, 401, result.Snapshot.Candles.Count, "the count should reflect what was fetched, not what was carried")
	require.NoError(t, result.Snapshot.Validate(), "a trimmed snapshot must still be valid")
}

func TestAssemblyIsDeterministic(t *testing.T) {
	t.Parallel()

	// The same inputs must produce byte-identical JSON, or a stored snapshot's
	// hash cannot be compared with a recomputed one.
	req := baseRequest()
	req.ID = "11111111-1111-4111-8111-111111111111"
	first, err := BuildMarketSnapshot(context.Background(), newFake(), req)
	require.NoError(t, err, "the first assembly must succeed")
	second, err := BuildMarketSnapshot(context.Background(), newFake(), req)
	require.NoError(t, err, "the second assembly must succeed")

	firstJSON, err := json.Marshal(first.Snapshot)
	require.NoError(t, err, "the snapshot must marshal")
	secondJSON, err := json.Marshal(second.Snapshot)
	require.NoError(t, err, "the snapshot must marshal")
	assert.JSONEq(t, string(firstJSON), string(secondJSON),
		"two assemblies over the same facts must be byte-identical, or replay cannot verify them")
}

func TestEverySupportedIntervalMaps(t *testing.T) {
	t.Parallel()

	for engineInterval, want := range map[kline.Interval]snapshot.Interval{
		kline.OneMin:     snapshot.Interval1m,
		kline.FiveMin:    snapshot.Interval5m,
		kline.FifteenMin: snapshot.Interval15m,
		kline.OneHour:    snapshot.Interval1h,
		kline.FourHour:   snapshot.Interval4h,
		kline.OneDay:     snapshot.Interval1d,
	} {
		got, err := snapshotInterval(engineInterval)
		require.NoErrorf(t, err, "%s must map", engineInterval.Short())
		assert.Equalf(t, want, got, "%s should map to %s", engineInterval.Short(), want)
	}

	// Every interval the contract supports must be reachable, or the contract
	// would advertise a spelling no caller can produce.
	reachable := make(map[snapshot.Interval]bool, len(snapshot.SupportedIntervals()))
	for _, engineInterval := range []kline.Interval{
		kline.OneMin, kline.FiveMin, kline.FifteenMin, kline.OneHour, kline.FourHour, kline.OneDay,
	} {
		mapped, err := snapshotInterval(engineInterval)
		require.NoErrorf(t, err, "%s must map", engineInterval.Short())
		reachable[mapped] = true
	}
	for _, contractInterval := range snapshot.SupportedIntervals() {
		assert.True(t, reachable[contractInterval],
			"the contract interval %s must be reachable from the engine", contractInterval)
	}
	assert.Len(t, reachable, len(snapshot.SupportedIntervals()),
		"the two lists must have the same size, so neither supports something the other cannot express")
}

// The exchange interface must satisfy the assembler's narrow one, so production
// wiring can pass the exchange itself with no adapter. This is a compile-time
// assertion, not a runtime test: if the exchange ever loses one of these five
// methods the package stops building, which is the earliest possible warning.
var _ iMarketDataSource = exchange.IBotExchange(nil)
