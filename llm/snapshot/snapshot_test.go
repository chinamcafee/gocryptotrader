package snapshot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

func testUUID(suffix string) string { return "11111111-1111-4111-8111-11111111111" + suffix }

// assertDecimalNear compares two decimals within a tolerance, which is what a
// derived ratio deserves: the answer is the value, not the division artefact.
func assertDecimalNear(t *testing.T, want string, got decimal.Decimal, tolerance float64, msg string) {
	t.Helper()
	expected, err := decimal.NewFromString(want)
	require.NoError(t, err, "the expected literal must parse")
	gap := got.Sub(expected).Abs().InexactFloat64()
	assert.LessOrEqual(t, gap, tolerance, msg)
}

func testTime() time.Time {
	return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
}

func candle(at time.Time, open, high, low, closePrice, volume string) CandleFact {
	return CandleFact{Time: at, Open: open, High: high, Low: low, Close: closePrice, Volume: volume}
}

// testSnapshot is a minimal valid snapshot: one candle, no optional sections.
func testSnapshot(t *testing.T) *MarketSnapshot {
	t.Helper()
	built := testTime()
	candles := []CandleFact{
		candle(built.Add(-2*time.Hour), "100", "110", "95", "105", "10"),
		candle(built.Add(-time.Hour), "105", "112", "104", "108", "12"),
	}
	summary, err := SummariseCandles(candles)
	require.NoError(t, err, "the fixture candles must summarise")
	return &MarketSnapshot{
		ID:         testUUID("1"),
		Exchange:   "Binance",
		Pair:       "BTC_USDT",
		Asset:      "usdtmarginedfutures",
		SourceTime: built.Add(-time.Hour),
		BuiltAt:    built,
		Candles: CandleWindow{
			Interval: Interval1h,
			Start:    candles[0].Time,
			End:      candles[len(candles)-1].Time,
			Count:    500,
			Closed:   candles,
			Summary:  summary,
		},
		Indicators: IndicatorSet{
			ComputedAt: built,
			Params:     IndicatorParams{MACDFast: 12, MACDSlow: 26, MACDSignal: 9},
		},
		Quality: map[string]candidate.DataQuality{CategoryCandles: candidate.QualityExchangeREST},
	}
}

func TestValidSnapshotPasses(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	require.NoError(t, s.Validate(), "the fixture must be valid")
}

func TestValidateRejectsIncompleteSnapshots(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*MarketSnapshot){
		"no id":         func(s *MarketSnapshot) { s.ID = "" },
		"no exchange":   func(s *MarketSnapshot) { s.Exchange = "  " },
		"no pair":       func(s *MarketSnapshot) { s.Pair = "" },
		"no asset":      func(s *MarketSnapshot) { s.Asset = "" },
		"no source":     func(s *MarketSnapshot) { s.SourceTime = time.Time{} },
		"no build time": func(s *MarketSnapshot) { s.BuiltAt = time.Time{} },
		"bad interval":  func(s *MarketSnapshot) { s.Candles.Interval = "7h" },
		"count below":   func(s *MarketSnapshot) { s.Candles.Count = 1 },
	} {
		s := testSnapshot(t)
		mutate(s)
		require.Errorf(t, s.Validate(), "%s must be refused", name)
	}

	var nilSnapshot *MarketSnapshot
	require.ErrorIs(t, nilSnapshot.Validate(), ErrSnapshotIncomplete, "a nil snapshot must be refused")
}

func TestValidateRejectsFutureData(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.SourceTime = s.BuiltAt.Add(time.Second)
	require.ErrorIs(t, s.Validate(), ErrTimeOrder,
		"a snapshot whose data is newer than its build time would mean the clock or the source is wrong")
}

func TestValidateRejectsOverBoundCollections(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	at := testTime()
	for i := 0; i <= MaxClosedCandles; i++ {
		s.Candles.Closed = append(s.Candles.Closed, candle(at.Add(time.Duration(i)*time.Hour), "1", "2", "1", "1", "1"))
	}
	require.ErrorIs(t, s.Validate(), ErrOverBound, "more candles than the cap must be refused")

	s = testSnapshot(t)
	s.Candles.Closed = nil
	s.Indicators.MACD = &MACDFact{Histogram: make([]string, MaxIndicatorPoints+1)}
	for i := range s.Indicators.MACD.Histogram {
		s.Indicators.MACD.Histogram[i] = "1"
	}
	s.Indicators.MACD.DIF = make([]string, len(s.Indicators.MACD.Histogram))
	s.Indicators.MACD.DEA = make([]string, len(s.Indicators.MACD.Histogram))
	require.ErrorIs(t, s.Validate(), ErrOverBound, "an oversized indicator series must be refused")

	s = testSnapshot(t)
	s.Candles.Closed = nil
	s.Indicators = IndicatorSet{}
	s.OrderBook = &OrderBookFacts{}
	for i := 0; i <= MaxOrderBookLevels; i++ {
		s.OrderBook.Bids = append(s.OrderBook.Bids, OrderBookLevel{Price: "100", Size: "1"})
	}
	require.ErrorIs(t, s.Validate(), ErrOverBound, "too many book levels must be refused")

	s = testSnapshot(t)
	s.Candles.Closed = nil
	s.Indicators = IndicatorSet{}
	s.Funding = &FundingFacts{History: make([]string, MaxFundingRates+1)}
	for i := range s.Funding.History {
		s.Funding.History[i] = "0.0001"
	}
	require.ErrorIs(t, s.Validate(), ErrOverBound, "too much funding history must be refused")

	s = testSnapshot(t)
	s.Candles.Closed = nil
	s.Indicators = IndicatorSet{}
	s.Account = &AccountFacts{Positions: make([]PositionFact, MaxPositions+1)}
	for i := range s.Account.Positions {
		s.Account.Positions[i] = PositionFact{Symbol: "BTC_USDT"}
	}
	require.ErrorIs(t, s.Validate(), ErrOverBound, "too many positions must be refused")
}

func TestValidateRejectsBadDecimals(t *testing.T) {
	t.Parallel()

	for name, price := range map[string]string{
		"empty":        "",
		"negative":     "-100",
		"exponent":     "1e5",
		"plus sign":    "+100",
		"text":         "one hundred",
		"trailing dot": "100.",
	} {
		s := testSnapshot(t)
		s.Candles.Closed[0].Open = price
		require.ErrorIsf(t, s.Validate(), ErrInvalidDecimal, "%s must be refused as a price", name)
	}
}

func TestValidateRejectsHighBelowLow(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.Candles.Closed[0].High = "90"
	s.Candles.Closed[0].Low = "95"
	require.ErrorIs(t, s.Validate(), ErrInvalidDecimal, "a candle whose high is below its low is not a candle")
}

func TestValidateRejectsUnorderedCandles(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.Candles.Closed[0], s.Candles.Closed[1] = s.Candles.Closed[1], s.Candles.Closed[0]
	require.ErrorIs(t, s.Validate(), ErrSnapshotIncomplete,
		"candles out of order would make the newest bar ambiguous")

	s = testSnapshot(t)
	s.Candles.Closed[1].Time = s.Candles.Closed[0].Time
	require.ErrorIs(t, s.Validate(), ErrSnapshotIncomplete, "duplicate timestamps must be refused")
}

func TestValidateRejectsCrossedBook(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.OrderBook = &OrderBookFacts{
		Bids: []OrderBookLevel{{Price: "101", Size: "1"}},
		Asks: []OrderBookLevel{{Price: "101", Size: "1"}},
	}
	require.ErrorIs(t, s.Validate(), ErrOrderBookCrossed,
		"a crossed book would make the slippage estimate negative")

	s.OrderBook = &OrderBookFacts{
		Bids: []OrderBookLevel{{Price: "102", Size: "1"}},
		Asks: []OrderBookLevel{{Price: "101", Size: "1"}},
	}
	require.ErrorIs(t, s.Validate(), ErrOrderBookCrossed, "a bid above the ask must be refused")
}

func TestValidateRejectsIndicatorSeriesMismatch(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.Indicators.MACD = &MACDFact{
		DIF:            []string{"1", "2"},
		DEA:            []string{"1"},
		Histogram:      []string{"1", "2"},
		CrossState:     CrossStateNone,
		HistogramTrend: TrendFlat,
	}
	require.ErrorIs(t, s.Validate(), ErrSeriesMismatch,
		"series of unequal length would pair the wrong bars")

	s.Indicators.MACD.DEA = []string{"1", "2"}
	s.Indicators.MACD.CrossState = "SOMETIMES"
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "an invented cross state must be refused")

	s.Indicators.MACD.CrossState = CrossStateGolden
	s.Indicators.MACD.HistogramTrend = "WOBBLING"
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "an invented trend must be refused")

	s.Indicators.MACD.HistogramTrend = TrendFlat
	s.Indicators.MACD.BarsSinceCross = -1
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "a negative bar count must be refused")

	s.Indicators.MACD.BarsSinceCross = 0
	s.Indicators.MovingAverages = []MovingAverageFact{{Type: "WMA", Period: 20, PriceVsMA: PriceAbove, Slope: SlopeUp}}
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "an unsupported average type must be refused")

	s.Indicators.MovingAverages[0].Type = MASimple
	s.Indicators.MovingAverages[0].PriceVsMA = "SIDEWAYS"
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "an invented position value must be refused")

	s.Indicators.MovingAverages[0].PriceVsMA = PriceAbove
	s.Indicators.MovingAverages[0].Slope = "SIDEWAYS"
	require.ErrorIs(t, s.Validate(), ErrInvalidDerived, "an invented slope must be refused")
}

func TestOptionalSectionsMayBeAbsent(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	require.NoError(t, s.Validate(), "a snapshot without ticker, book, funding or account must still be valid")

	// A nil receiver on each optional section's validator must be safe, because
	// a partly-built snapshot is a normal intermediate state.
	var candles *CandleWindow
	require.NoError(t, candles.validate(), "a nil candle window must be safe")
	var indicators *IndicatorSet
	require.NoError(t, indicators.validate(), "nil indicators must be safe")
	var ticker *TickerFacts
	require.NoError(t, ticker.validate(), "a nil ticker must be safe")
	var book *OrderBookFacts
	require.NoError(t, book.validate(), "a nil book must be safe")
	var funding *FundingFacts
	require.NoError(t, funding.validate(), "nil funding must be safe")
	var account *AccountFacts
	require.NoError(t, account.validate(), "a nil account must be safe")
	var macd *MACDFact
	require.NoError(t, macd.validate(), "a nil MACD must be safe")
	var ma *MovingAverageFact
	require.NoError(t, ma.validate(), "a nil moving average must be safe")
	var series *SeriesFact
	require.NoError(t, series.validate(), "a nil series must be safe")
	var bollinger *BollingerFact
	require.NoError(t, bollinger.validate(), "nil bands must be safe")
	var summary *CandleSummary
	require.NoError(t, summary.validate(), "a nil summary must be safe")
}

func TestLiveCandleIsValidated(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.LiveCandle = &CandleFact{Open: "1", High: "2", Low: "1", Close: "1", Volume: "1"}
	require.ErrorIs(t, s.Validate(), ErrSnapshotIncomplete, "a live candle without a time must be refused")

	s.LiveCandle.Time = s.BuiltAt
	require.NoError(t, s.Validate(), "a well-formed live candle must pass")
}

func TestQualityDefaultIsUnknown(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(CategoryCandles), "a recorded label should be returned")
	assert.Equal(t, candidate.QualityUnknown, s.QualityOf(CategoryOrderBook),
		"a category nobody assessed must not default to trusted")

	var nilSnapshot *MarketSnapshot
	assert.Equal(t, candidate.QualityUnknown, nilSnapshot.QualityOf(CategoryCandles), "a nil snapshot trusts nothing")
}

func TestSetQualityOnlyLowers(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	require.NoError(t, s.SetQuality(CategoryTicker, candidate.QualityExchangeREST), "the first label must be recorded")
	require.NoError(t, s.SetQuality(CategoryTicker, candidate.QualityStale), "lowering must be allowed")
	assert.Equal(t, candidate.QualityStale, s.QualityOf(CategoryTicker), "the lower label should win")

	// The invariant the whole scheme rests on: a later stage must not be able to
	// re-trust a fact an earlier stage discredited.
	require.ErrorIs(t, s.SetQuality(CategoryTicker, candidate.QualityExchangeREST),
		candidate.ErrQualityUpgrade, "raising a label must be refused")
	assert.Equal(t, candidate.QualityStale, s.QualityOf(CategoryTicker), "a refused raise should leave the label alone")

	var nilSnapshot *MarketSnapshot
	require.ErrorIs(t, nilSnapshot.SetQuality(CategoryTicker, candidate.QualityStale),
		ErrSnapshotIncomplete, "a nil snapshot must not be mutated")
}

func TestDegradedCategories(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	assert.Empty(t, s.DegradedCategories(), "a fresh snapshot has nothing degraded")

	// A category that is absent and unlabelled is simply not part of this
	// snapshot: a candle-only analysis must not be blocked for having no ticker.
	assert.NotContains(t, s.Quality, CategoryTicker, "the fixture should not label the absent ticker")
	assert.Empty(t, s.DegradedCategories(), "an absent category should not block")

	// Data with no label is nobody's assessment, which is not the same as trust.
	s.Ticker = &TickerFacts{Last: "100", Bid: "99", Ask: "101", ReadAt: s.BuiltAt}
	assert.Contains(t, s.DegradedCategories(), CategoryTicker,
		"data carrying no label must block rather than be assumed good")
	require.NoError(t, s.SetQuality(CategoryTicker, candidate.QualityExchangeREST), "labelling must succeed")
	assert.Empty(t, s.DegradedCategories(), "a labelled, fresh ticker should not block")

	// A degraded label blocks even with no data: it asserts a failed fetch.
	s.OrderBook = &OrderBookFacts{}
	require.NoError(t, s.SetQuality(CategoryOrderBook, candidate.QualityUnknown), "labelling must succeed")

	s.Funding = &FundingFacts{}
	require.NoError(t, s.SetQuality(CategoryFunding, candidate.QualityStale), "labelling must succeed")

	assert.Equal(t, []string{CategoryOrderBook, CategoryFunding}, s.DegradedCategories(),
		"both blocking categories must be reported, in the stable category order")

	// A degraded label on a category with no data still blocks.
	bare := testSnapshot(t)
	require.NoError(t, bare.SetQuality(CategoryAccount, candidate.QualityStale), "labelling must succeed")
	assert.Equal(t, []string{CategoryAccount}, bare.DegradedCategories(),
		"a degraded label must block even when the section is empty")

	var nilSnapshot *MarketSnapshot
	assert.Empty(t, nilSnapshot.DegradedCategories(), "a nil snapshot reports nothing")
}

func TestCategoriesCoverEveryOptionalSection(t *testing.T) {
	t.Parallel()

	// A category with no way to be set would be dead vocabulary; a section with
	// no category could never be degraded.
	assert.Len(t, Categories(), 6, "the category list should be complete")
	assert.Equal(t, []string{
		CategoryCandles, CategoryIndicators, CategoryTicker, CategoryOrderBook, CategoryFunding, CategoryAccount,
	}, Categories(), "the order must be stable so a stored snapshot reads the same way twice")
}

func TestCloneIsDeep(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.OrderBook = &OrderBookFacts{Bids: []OrderBookLevel{{Price: "100", Size: "1"}}}
	s.Funding = &FundingFacts{History: []string{"0.0001"}}
	s.Account = &AccountFacts{Positions: []PositionFact{{Symbol: "BTC_USDT"}}}
	s.Ticker = &TickerFacts{Last: "100", Bid: "99", Ask: "101"}
	s.LiveCandle = &CandleFact{Time: s.BuiltAt, Open: "1", High: "2", Low: "1", Close: "1", Volume: "1"}
	s.Indicators.MACD = &MACDFact{
		DIF: []string{"1"}, DEA: []string{"1"}, Histogram: []string{"1"},
		CrossState: CrossStateNone, HistogramTrend: TrendFlat,
	}
	s.Indicators.MovingAverages = []MovingAverageFact{{
		Type: MASimple, Period: 20, Values: []string{"1"},
		PriceVsMA: PriceAbove, Slope: SlopeUp,
	}}

	clone := s.Clone()
	require.NoError(t, clone.Validate(), "a clone must be valid")

	clone.Candles.Closed[0].Open = "999"
	clone.OrderBook.Bids[0].Price = "999"
	clone.Funding.History[0] = "999"
	clone.Account.Positions[0].Symbol = "changed"
	clone.Ticker.Last = "999"
	clone.LiveCandle.Open = "999"
	clone.Indicators.MACD.Histogram[0] = "999"
	clone.Indicators.MovingAverages[0].Values[0] = "999"
	clone.Quality[CategoryCandles] = candidate.QualityUnknown

	assert.Equal(t, "100", s.Candles.Closed[0].Open, "the original candles should be untouched")
	assert.Equal(t, "100", s.OrderBook.Bids[0].Price, "the original book should be untouched")
	assert.Equal(t, "0.0001", s.Funding.History[0], "the original funding history should be untouched")
	assert.Equal(t, "BTC_USDT", s.Account.Positions[0].Symbol, "the original positions should be untouched")
	assert.Equal(t, "100", s.Ticker.Last, "the original ticker should be untouched")
	assert.Equal(t, "1", s.LiveCandle.Open, "the original live candle should be untouched")
	assert.Equal(t, "1", s.Indicators.MACD.Histogram[0], "the original indicators should be untouched")
	assert.Equal(t, "1", s.Indicators.MovingAverages[0].Values[0], "the original averages should be untouched")
	assert.Equal(t, candidate.QualityExchangeREST, s.QualityOf(CategoryCandles), "the original labels should be untouched")

	var nilSnapshot *MarketSnapshot
	assert.Nil(t, nilSnapshot.Clone(), "a nil snapshot should clone to nil")
}

func TestValidateDecimalRules(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"0", "1", "100", "0.001", "12345678901234567890.123456789012345678"} {
		assert.NoErrorf(t, ValidateDecimal(ok), "%q should be accepted", ok)
	}
	for _, bad := range []string{"", "-1", "+1", "1e5", "1.", ".1", " 1", "1 ", "abc", "1,000"} {
		assert.ErrorIsf(t, ValidateDecimal(bad), ErrInvalidDecimal, "%q should be refused", bad)
	}

	assert.NoError(t, ValidateSignedDecimal("-1.5"), "the signed rule should accept a negative change")
	assert.NoError(t, ValidateSignedDecimal("1.5"), "the signed rule should accept a positive change")
	assert.ErrorIs(t, ValidateSignedDecimal("--1"), ErrInvalidDecimal, "only one sign is allowed")
	assert.ErrorIs(t, ValidateSignedDecimal("-1e5"), ErrInvalidDecimal, "an exponent is still refused")
}

func TestIntervalRules(t *testing.T) {
	t.Parallel()

	for _, interval := range SupportedIntervals() {
		require.NoErrorf(t, interval.Validate(), "%s must be supported", interval)
		seconds, err := interval.Duration()
		require.NoErrorf(t, err, "%s must have a duration", interval)
		assert.Positivef(t, seconds, "%s should be a positive duration", interval)
	}
	require.ErrorIs(t, Interval("7h").Validate(), ErrInvalidInterval, "an unsupported interval must be refused")
	_, err := Interval("7h").Duration()
	require.ErrorIs(t, err, ErrUnknownInterval, "an unsupported interval must not fall back to a number")
	_, err = Interval("").Duration()
	require.ErrorIs(t, err, ErrUnknownInterval, "an empty interval must not fall back to zero seconds")
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	t.Parallel()

	s := testSnapshot(t)
	s.OrderBook = &OrderBookFacts{
		Bids:      []OrderBookLevel{{Price: "100.5", Size: "2"}},
		Asks:      []OrderBookLevel{{Price: "100.7", Size: "3"}},
		SpreadBPS: "19.9001",
		MidPrice:  "100.6",
		ReadAt:    s.BuiltAt,
	}
	s.Funding = &FundingFacts{LatestRate: "-0.0001", AnnualisedPct: "-10.95", SettlementsPerDay: 3, ReadAt: s.BuiltAt}

	raw, err := json.Marshal(s)
	require.NoError(t, err, "a snapshot must marshal")

	var back MarketSnapshot
	require.NoError(t, json.Unmarshal(raw, &back), "a snapshot must unmarshal")
	require.NoError(t, back.Validate(), "a round-tripped snapshot must still be valid")

	// Decimals must survive as the exact strings they were written as. A float
	// in the middle would change the last digits and, with them, the order.
	assert.Contains(t, string(raw), `"price":"100.5"`, "prices should serialise as strings")
	assert.Contains(t, string(raw), `"latestRate":"-0.0001"`, "a signed rate should serialise as a string")
	assert.Equal(t, s.Pair, back.Pair, "the pair should round-trip")
	assert.Equal(t, s.QualityOf(CategoryCandles), back.QualityOf(CategoryCandles), "the label should round-trip")
}

func TestSummariseCandles(t *testing.T) {
	t.Parallel()

	at := testTime()
	candles := []CandleFact{
		candle(at, "100", "110", "95", "105", "10"),
		candle(at.Add(time.Hour), "105", "120", "100", "118", "20"),
		candle(at.Add(2*time.Hour), "118", "119", "110", "112", "30"),
	}
	summary, err := SummariseCandles(candles)
	require.NoError(t, err, "a well-formed window must summarise")

	assert.Equal(t, "100", summary.FirstOpen, "the first open should be reported")
	assert.Equal(t, "112", summary.LastClose, "the last close should be reported")
	assert.Equal(t, "120", summary.HighestHigh, "the highest high should be reported")
	assert.Equal(t, "95", summary.LowestLow, "the lowest low should be reported")
	assert.Equal(t, "60", summary.TotalVolume, "volumes should be summed")
	assert.Equal(t, "12", summary.ChangePct, "the change should be a percentage, not a ratio")
	// Rounded to MaxRenderedFractionDigits: two of three bars rose, and the
	// score keeps twelve fraction digits rather than the float's nineteen.
	assert.Equal(t, "0.666666666667", summary.DirectionScore,
		"the direction score must be the share of bars that closed at or above their open")

	// The summary's own values must satisfy the snapshot's decimal rules, or a
	// snapshot carrying a summary could not be validated.
	require.NoError(t, summary.validate(), "a computed summary must satisfy its own rules")

	_, err = SummariseCandles(nil)
	require.ErrorIs(t, err, ErrTooFewPoints, "an empty window must be refused rather than summarised as zero")
}

func TestSummariseCandlesDoesNotUseExponentNotation(t *testing.T) {
	t.Parallel()

	// A tiny change on a large price is where an exponent would appear and where
	// the snapshot's plain-decimal rule would reject the result.
	at := testTime()
	candles := []CandleFact{
		candle(at, "0.00000001", "0.00000002", "0.00000001", "0.00000001", "0.00000001"),
		candle(at.Add(time.Hour), "0.00000001", "0.00000002", "0.00000001", "0.000000012", "0.00000001"),
	}
	summary, err := SummariseCandles(candles)
	require.NoError(t, err, "the fixture must summarise")
	assert.NotContains(t, summary.ChangePct, "e", "a computed value should not be written in exponent form")
	assert.NotContains(t, summary.ChangePct, "E", "a computed value should not be written in exponent form")
	require.NoError(t, summary.validate(), "the computed summary must satisfy the decimal rules")
}

func TestSummariseCandlesRejectsBadNumbers(t *testing.T) {
	t.Parallel()

	at := testTime()
	bad := []CandleFact{candle(at, "100", "110", "95", "not a number", "10")}
	_, err := SummariseCandles(bad)
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable close must be refused")
}

func requireMACDStateError(t *testing.T, hist []string, msg string) {
	t.Helper()
	cross, bars, trend, err := DeriveMACDState(hist)
	require.ErrorIs(t, err, ErrTooFewPoints, msg)
	assert.Empty(t, cross)
	assert.Zero(t, bars)
	assert.Empty(t, trend)
}

func TestDeriveMACDState(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		histogram []string
		cross     string
		bars      int
		trend     string
	}{
		"golden on the newest bar": {[]string{"-1", "-0.5", "0.2"}, CrossStateGolden, 0, TrendContracting},
		"golden three bars back":   {[]string{"-1", "0.5", "0.6", "0.7"}, CrossStateGolden, 2, TrendExpanding},
		"death on the newest bar":  {[]string{"1", "0.5", "-0.2"}, CrossStateDeath, 0, TrendContracting},
		"no cross":                 {[]string{"1", "2", "3"}, CrossStateNone, 0, TrendExpanding},
		"no cross, shrinking":      {[]string{"3", "2", "1"}, CrossStateNone, 0, TrendContracting},
		"flat":                     {[]string{"1", "1"}, CrossStateNone, 0, TrendFlat},
		"zero counts as flat":      {[]string{"0", "0"}, CrossStateNone, 0, TrendFlat},
		"cross from exact zero":    {[]string{"0", "0.5"}, CrossStateGolden, 0, TrendExpanding},
		"down through zero":        {[]string{"0", "-0.5"}, CrossStateDeath, 0, TrendExpanding},
	} {
		cross, bars, trend, err := DeriveMACDState(tc.histogram)
		require.NoErrorf(t, err, "%s must derive", name)
		assert.Equalf(t, tc.cross, cross, "%s: the cross state should be right", name)
		assert.Equalf(t, tc.bars, bars, "%s: the bar count should be right", name)
		assert.Equalf(t, tc.trend, trend, "%s: the trend should be right", name)
	}

	requireMACDStateError(t, []string{"1"}, "one point cannot describe a cross")
	requireMACDStateError(t, nil, "no points cannot describe a cross")
	cross, bars, trend, err := DeriveMACDState([]string{"1", "abc"})
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable point must be refused, not treated as zero")
	assert.Empty(t, cross)
	assert.Zero(t, bars)
	assert.Empty(t, trend)
}

func TestDeriveMACDStateNewestCrossWins(t *testing.T) {
	t.Parallel()

	// Two crosses in the window: the recent one is what the model needs, and
	// reporting the older one would describe a state that no longer holds.
	cross, bars, _, err := DeriveMACDState([]string{"-1", "0.5", "-0.5", "0.9"})
	require.NoError(t, err, "the fixture must derive")
	assert.Equal(t, CrossStateGolden, cross, "the newest cross should win")
	assert.Equal(t, 0, bars, "the newest cross should be reported as current")
}

func TestDeriveMAState(t *testing.T) {
	t.Parallel()

	closes := []string{"10", "11", "12", "13"}
	averages := []string{"9", "10", "11", "12"}
	position, slope, err := DeriveMAState(closes, averages)
	require.NoError(t, err, "the fixture must derive")
	assert.Equal(t, PriceAbove, position, "a close above its average should read as above")
	assert.Equal(t, SlopeUp, slope, "a rising average should read as up")

	position, slope, err = DeriveMAState([]string{"8", "8", "8", "8"}, averages)
	require.NoError(t, err, "the fixture must derive")
	assert.Equal(t, PriceBelow, position, "a close below its average should read as below")
	assert.Equal(t, SlopeUp, slope, "the slope follows the average, not the close")

	// A drift below the flat threshold must not read as a direction: one cent on
	// a large number is noise, not a trend.
	flatAverages := []string{"50000.00", "50000.00", "50000.00", "50000.01"}
	_, slope, err = DeriveMAState([]string{"50000", "50000", "50000", "50000"}, flatAverages)
	require.NoError(t, err, "the fixture must derive")
	assert.Equal(t, SlopeFlat, slope, "a drift below the threshold should read as flat")

	_, slope, err = DeriveMAState([]string{"1", "1"}, []string{"1", "2"})
	require.NoError(t, err, "a short series must still yield a position")
	assert.Equal(t, SlopeFlat, slope, "too short to look back should report flat rather than guess")

	_, _, err = DeriveMAState(nil, averages)
	require.ErrorIs(t, err, ErrTooFewPoints, "no closes must be refused")
	_, _, err = DeriveMAState(closes, []string{"1"})
	require.ErrorIs(t, err, ErrSeriesMismatch, "mismatched lengths would pair the wrong bars")
	// Only the values the derivation actually reads are checked, so the junk has
	// to sit in the newest close to be seen. The snapshot's own validation is
	// what covers a whole series.
	_, _, err = DeriveMAState([]string{"1", "1", "1", "abc"}, averages)
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable newest close must be refused")
	_, _, err = DeriveMAState(closes, []string{"1", "1", "1", "abc"})
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable newest average must be refused")
}

func TestDeriveSpreadAndMid(t *testing.T) {
	t.Parallel()

	spread, err := DeriveSpreadBPS("100", "100.1")
	require.NoError(t, err, "a normal book must derive")
	// (100.1-100)/((100+100.1)/2)*10000 = 9.99500249875062...
	// Asserted numerically rather than by literal, because the two decimal
	// backends truncate division at different precisions and the exact trailing
	// digits are an artefact rather than the answer.
	assertDecimalNear(t, "9.99500249875062", spread, 1e-9, "the spread must be in basis points of the midpoint")

	zero, err := DeriveSpreadBPS("100", "100")
	require.NoError(t, err, "a zero spread is legal, if unappealing")
	assert.True(t, zero.IsZero(), "an equal bid and ask should produce a zero spread")

	mid, err := DeriveMidPrice("100.5", "100.7")
	require.NoError(t, err, "a normal book must derive")
	assert.Equal(t, "100.6", mid.String(), "the midpoint should be exact")

	// Exactness matters: the sum-then-halve form keeps the result a true
	// midpoint rather than a rounded average of two halves.
	mid, err = DeriveMidPrice("0.1", "0.2")
	require.NoError(t, err, "the fixture must derive")
	assert.Equal(t, "0.15", mid.String(), "a midpoint that does not divide evenly should still be exact")

	for name, tc := range map[string]struct{ bid, ask string }{
		"empty bid":    {"", "100"},
		"zero bid":     {"0", "100"},
		"negative bid": {"-1", "100"},
		"crossed":      {"101", "100"},
		"junk":         {"abc", "100"},
	} {
		_, err = DeriveSpreadBPS(tc.bid, tc.ask)
		require.Errorf(t, err, "%s must be refused", name)
		_, err = DeriveMidPrice(tc.bid, tc.ask)
		require.Errorf(t, err, "%s must be refused by the midpoint too", name)
	}
}

func TestDeriveAnnualisedFunding(t *testing.T) {
	t.Parallel()

	// A rate of 0.0001 settled three times a day is 0.0001*3*365*100 = 10.95%.
	annualised, err := DeriveAnnualisedFundingPct("0.0001", 3)
	require.NoError(t, err, "a normal rate must derive")
	assert.Equal(t, "10.95", annualised.String(), "the annualised percentage should be right")

	negative, err := DeriveAnnualisedFundingPct("-0.0001", 3)
	require.NoError(t, err, "a negative rate is normal for a perpetual")
	assert.Equal(t, "-10.95", negative.String(), "the sign should be preserved: shorts pay longs when it is negative")

	_, err = DeriveAnnualisedFundingPct("0.0001", 0)
	require.ErrorIs(t, err, ErrInvalidDerived, "an unknown settlement count must be refused")
	_, err = DeriveAnnualisedFundingPct("junk", 3)
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable rate must be refused")
}

func TestDeriveExecutablePrice(t *testing.T) {
	t.Parallel()

	// A thin book: crossing the first level is cheap, crossing the second is not.
	levels := []OrderBookLevel{
		{Price: "100", Size: "1"},
		{Price: "110", Size: "10"},
	}
	notional, err := decimal.NewFromString("150")
	require.NoError(t, err, "the fixture notional must parse")

	price, size, err := DeriveExecutablePrice(levels, notional)
	require.NoError(t, err, "a book that can absorb the order must derive")
	// All 100 of the first level, then 50 more from the second: the fill is
	// 1 + 50/110 base for 150 quote, so the average price is 150/(1.4545...) .
	assertDecimalNear(t, "103.125", price, 1e-9, "the average price must weight the levels actually crossed")
	assertDecimalNear(t, "1.45454545454545", size, 1e-9, "the filled size must be the base quantity")

	// A notional that fits inside one level must pay that level's price exactly.
	small, err := decimal.NewFromString("50")
	require.NoError(t, err, "the fixture notional must parse")
	price, size, err = DeriveExecutablePrice(levels, small)
	require.NoError(t, err, "a small order must derive")
	assert.Equal(t, "100", price.String(), "an order inside the best level should pay the best price")
	assert.Equal(t, "0.5", size.String(), "the size should be the base quantity")

	// The slippage a decision has to see is the difference between this and the
	// midpoint, so an unabsorbable order must fail rather than report a partial.
	huge, err := decimal.NewFromString("100000")
	require.NoError(t, err, "the fixture notional must parse")
	_, _, err = DeriveExecutablePrice(levels, huge)
	require.ErrorIs(t, err, ErrSnapshotIncomplete,
		"a partial fill reported as a full one would understate the slippage")

	_, _, err = DeriveExecutablePrice(nil, small)
	require.ErrorIs(t, err, ErrSnapshotIncomplete, "an empty side must be refused")

	zero, err := decimal.NewFromString("0")
	require.NoError(t, err, "the fixture notional must parse")
	_, _, err = DeriveExecutablePrice(levels, zero)
	require.ErrorIs(t, err, ErrNonPositivePrice, "a zero notional must be refused")

	bad := []OrderBookLevel{{Price: "abc", Size: "1"}}
	_, _, err = DeriveExecutablePrice(bad, small)
	require.ErrorIs(t, err, ErrInvalidDecimal, "an unparsable level must be refused")

	nonPositive := []OrderBookLevel{{Price: "100", Size: "0"}}
	_, _, err = DeriveExecutablePrice(nonPositive, small)
	require.ErrorIs(t, err, ErrNonPositivePrice, "a zero size must be refused")
}

func TestExecutablePriceIsWorseThanTheMidpoint(t *testing.T) {
	t.Parallel()

	// The property the slippage check depends on: on a real book, filling an
	// order costs at least the midpoint, and the gap grows with size.
	levels := []OrderBookLevel{
		{Price: "100.0", Size: "0.01"},
		{Price: "100.5", Size: "1"},
		{Price: "101.0", Size: "5"},
	}
	mid, err := DeriveMidPrice("99.9", "100.1")
	require.NoError(t, err, "the fixture must derive a midpoint")

	var previous decimal.Decimal
	for i, raw := range []string{"1", "10", "100"} {
		notional, err := decimal.NewFromString(raw)
		require.NoError(t, err, "the fixture notional must parse")
		price, _, err := DeriveExecutablePrice(levels, notional)
		require.NoErrorf(t, err, "the book must absorb %s", raw)

		assert.GreaterOrEqualf(t, price.Compare(mid), 0, "size %s should not fill better than the midpoint", raw)
		if i > 0 {
			assert.Equal(t, 1, price.Compare(previous), "a larger order should not fill better than a smaller one")
		}
		previous = price
	}
}

func TestComputedValuesAreRoundedToOnePrecision(t *testing.T) {
	t.Parallel()

	// A float64's shortest plain rendering can need more fraction digits than
	// the contract's string rule allows — the MACD histogram really does produce
	// values like 0.0035044457052606504, which carries nineteen against a bound
	// of eighteen. Every computed value is therefore rounded to one documented
	// precision, and the precision is pinned here so a future change to it is a
	// deliberate one.
	assert.Equal(t, 12, MaxRenderedFractionDigits, "the rendering precision should not drift silently")

	for _, tc := range []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{100, "100"},
		{-2.5, "-2.5"},
		{0.0035044457052606504, "0.003504445705"},
		{1.0 / 3.0, "0.333333333333"},
		{2.0 / 3.0, "0.666666666667"},
		{1e-13, "0"},
	} {
		text, err := DecimalFromFloat(tc.value)
		require.NoErrorf(t, err, "%v must render", tc.value)
		assert.Equalf(t, tc.want, text, "%v should render at the documented precision", tc.value)
		assert.NoErrorf(t, ValidateSignedDecimal(text), "%v should satisfy the contract's rule", tc.value)
	}

	// Trailing zeros are dropped, so one value has exactly one spelling. That is
	// what lets a stored snapshot's hash be compared with a recomputed one.
	a, err := DecimalFromFloat(1.5)
	require.NoError(t, err, "1.5 must render")
	b, err := DecimalFromFloat(1.5000000000001)
	require.NoError(t, err, "the near value must render")
	assert.Equal(t, "1.5", a, "trailing zeros should be dropped")
	assert.Equal(t, "1.5", b, "a value inside the precision should collapse to the same spelling")

	// A summary over a window whose prices are tiny must still validate: this is
	// the shape that produced the nineteen-digit value in the first place.
	at := testTime()
	candles := []CandleFact{
		candle(at, "0.003", "0.004", "0.003", "0.0035044457052606", "1"),
		candle(at.Add(time.Hour), "0.0035", "0.004", "0.003", "0.0036", "1"),
	}
	summary, err := SummariseCandles(candles)
	require.NoError(t, err, "a tiny-price window must summarise")
	require.NoError(t, summary.validate(), "the computed summary must satisfy the contract's rule")
}
