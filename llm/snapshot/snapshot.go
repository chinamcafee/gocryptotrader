// Package snapshot defines the bounded fact set one decision runs on.
//
// A snapshot is built once, validated, then never modified
// (docs/llm-dev/04-数据层-K线与指标.md §3). It is the only thing the language
// model sees about the market, so its properties are the properties of the
// decision:
//
//   - Bounded. Every collection has a cap, so one market with an unusual amount
//     of history cannot produce a payload that costs more than a decision is
//     worth.
//   - Decimal strings. Prices and sizes are strings, never floats
//     (docs/CODING_GUIDELINES.md). A float that round-trips through JSON is a
//     number whose last digits changed, and an order sized from it is a
//     different order.
//   - Facts, not conclusions. The derived values are readings such as "the MACD
//     histogram changed sign three bars ago", never "buy". Deriving a
//     recommendation in Go would move the decision out of the audited path and
//     into code nobody reviews for that purpose.
//   - Labelled. Each fact category carries a quality tag that can only be
//     lowered, never raised.
//
// The types here are deliberately plain strings rather than the engine's own
// types. The snapshot is the boundary between the trading engine and the model
// layer, and llm/ is meant to be liftable on its own (docs/llm-dev/03-总体架构设计.md
// §7); importing the engine's candle types would drag the order manager in
// behind them.
package snapshot

import (
	"errors"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// Bounds. They are caps rather than targets: a builder that has more data than
// this keeps the newest and drops the rest, and a snapshot that exceeds one is
// invalid rather than merely large.
const (
	// MaxClosedCandles bounds the closed candles carried in full. The
	// indicators already compress the longer history, so more detail buys
	// nothing and costs tokens.
	MaxClosedCandles = 50
	// MaxIndicatorPoints bounds each indicator series.
	MaxIndicatorPoints = 20
	// MaxOrderBookLevels bounds each side of the book.
	MaxOrderBookLevels = 20
	// MaxFundingRates bounds the funding history, which covers about a week at
	// the eight-hour settlement interval.
	MaxFundingRates = 21
	// MaxPositions is a sanity bound, not a truncation rule: positions are never
	// dropped, because risk checks need all of them.
	MaxPositions = 64
	// MaxThesisBound and friends bound the free text a snapshot may carry.
	MaxNoteLength = 2000
)

// Fact category keys. They are the keys of MarketSnapshot.Quality, so a reader
// of a stored snapshot can find out which category was degraded.
const (
	CategoryCandles    = "candles"
	CategoryIndicators = "indicators"
	CategoryTicker     = "ticker"
	CategoryOrderBook  = "orderbook"
	CategoryFunding    = "funding"
	CategoryAccount    = "account"
)

// Categories returns every fact category, so a caller can iterate without
// repeating the list.
func Categories() []string {
	return []string{CategoryCandles, CategoryIndicators, CategoryTicker, CategoryOrderBook, CategoryFunding, CategoryAccount}
}

// Derived-fact vocabulary.
const (
	// CrossStateGolden marks a histogram that changed from negative to positive.
	CrossStateGolden = "GOLDEN"
	// CrossStateDeath marks a histogram that changed from positive to negative.
	CrossStateDeath = "DEATH"
	// CrossStateNone marks a histogram that did not change sign.
	CrossStateNone = "NONE"
)

// Histogram trends.
const (
	// TrendExpanding marks a histogram whose magnitude grew.
	TrendExpanding = "EXPANDING"
	// TrendContracting marks a histogram whose magnitude shrank.
	TrendContracting = "CONTRACTING"
	// TrendFlat marks a histogram whose magnitude did not change.
	TrendFlat = "FLAT"
)

// Moving-average position and slope.
const (
	// PriceAbove marks a close above its moving average.
	PriceAbove = "ABOVE"
	// PriceBelow marks a close below its moving average.
	PriceBelow = "BELOW"
	// SlopeUp marks a rising average.
	SlopeUp = "UP"
	// SlopeDown marks a falling average.
	SlopeDown = "DOWN"
	// SlopeFlat marks an average that moved less than the flat threshold.
	SlopeFlat = "FLAT"
)

// Moving-average kinds.
const (
	// MASimple is a simple moving average.
	MASimple = "SMA"
	// MAExponential is an exponential moving average.
	MAExponential = "EMA"
)

// Thresholds for the derived facts.
const (
	// histogramFlatEpsilon is the tolerance that makes two histogram values
	// count as unchanged. Exact equality is unusable on a computed series.
	histogramFlatEpsilon = 1e-9
	// slopeFlatFraction is the fraction of the price below which a moving
	// average counts as flat: 0.01%.
	slopeFlatFraction = 1e-4
	// bpsScale converts a ratio into basis points.
	bpsScale = 10000
)

// Sentinel errors.
var (
	// ErrSnapshotIncomplete marks a snapshot missing required facts.
	ErrSnapshotIncomplete = errors.New("snapshot is incomplete")
	// ErrOverBound marks a collection larger than its cap.
	ErrOverBound = errors.New("collection exceeds its bound")
	// ErrInvalidDecimal marks a value that is not a plain decimal string.
	ErrInvalidDecimal = errors.New("value must be a plain decimal string")
	// ErrInvalidInterval marks an interval outside the supported set.
	ErrInvalidInterval = errors.New("unsupported candle interval")
	// ErrInvalidDerived marks a derived-fact value outside its vocabulary.
	ErrInvalidDerived = errors.New("derived fact value is not in its vocabulary")
	// ErrSeriesMismatch marks indicator series of unequal length that must line
	// up, which would silently pair the wrong bars.
	ErrSeriesMismatch = errors.New("indicator series lengths do not match")
	// ErrTimeOrder marks a snapshot whose source time is after its build time,
	// which would mean the data came from the future.
	ErrTimeOrder = errors.New("source time must not be after the build time")
	// ErrOrderBookCrossed marks a book whose best ask is not above its best bid.
	ErrOrderBookCrossed = errors.New("order book is crossed or empty")
)

// decimalString matches a plain decimal. It accepts a leading zero and no
// exponent so a value cannot be written two ways, which would break the
// hash-based comparison between what the model saw and what was recorded.
var decimalString = regexp.MustCompile(`^\d{1,20}(\.\d{1,18})?$`)

// Interval is the candle interval. It is a string rather than a duration so a
// stored snapshot reads the same way a human wrote it, and so the contract does
// not depend on the engine's candle package.
type Interval string

// Supported intervals.
const (
	Interval1m  Interval = "1m"
	Interval5m  Interval = "5m"
	Interval15m Interval = "15m"
	Interval1h  Interval = "1h"
	Interval4h  Interval = "4h"
	Interval1d  Interval = "1d"
)

// SupportedIntervals returns every interval a decision may run on.
func SupportedIntervals() []Interval {
	return []Interval{Interval1m, Interval5m, Interval15m, Interval1h, Interval4h, Interval1d}
}

// Validate checks the interval against the supported set.
func (i Interval) Validate() error {
	if slices.Contains(SupportedIntervals(), i) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidInterval, string(i))
}

// CandleFact is one candle. The numbers are strings for the reason in
// docs/CODING_GUIDELINES.md: money must not pass through a float.
type CandleFact struct {
	// Time is the candle's opening instant, in UTC.
	Time   time.Time `json:"time"`
	Open   string    `json:"open"`
	High   string    `json:"high"`
	Low    string    `json:"low"`
	Close  string    `json:"close"`
	Volume string    `json:"volume"`
	// Partial is true only on the still-forming candle: it may describe
	// "where price is now" but must never feed indicator maths (docs 04 §5).
	Partial bool `json:"partial,omitempty"`
}

// Validate checks the candle's numbers and its internal ordering.
func (c *CandleFact) Validate() error {
	for name, value := range map[string]string{
		"open": c.Open, "high": c.High, "low": c.Low, "close": c.Close, "volume": c.Volume,
	} {
		if err := ValidateDecimal(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	high, _ := new(big.Float).SetString(c.High)
	low, _ := new(big.Float).SetString(c.Low)
	if high.Cmp(low) < 0 {
		return fmt.Errorf("%w: high %s is below low %s", ErrInvalidDecimal, c.High, c.Low)
	}
	if c.Time.IsZero() {
		return fmt.Errorf("%w: candle has no time", ErrSnapshotIncomplete)
	}
	return nil
}

// CandleSummary is the bounded view of the window, so the model gets shape
// rather than fifty rows to average by eye.
type CandleSummary struct {
	FirstOpen      string `json:"firstOpen"`
	LastClose      string `json:"lastClose"`
	HighestHigh    string `json:"highestHigh"`
	LowestLow      string `json:"lowestLow"`
	TotalVolume    string `json:"totalVolume"`
	ChangePct      string `json:"changePct"`
	DirectionScore string `json:"directionScore"`
}

// CandleWindow is the closed candle history plus its summary.
type CandleWindow struct {
	Interval Interval      `json:"interval"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end"`
	Count    int           `json:"count"`
	Closed   []CandleFact  `json:"closed"`
	Summary  CandleSummary `json:"summary"`
}

// MACDFact is the MACD reading with its derived state.
type MACDFact struct {
	Fast           int64    `json:"fast"`
	Slow           int64    `json:"slow"`
	Signal         int64    `json:"signal"`
	DIF            []string `json:"dif"`
	DEA            []string `json:"dea"`
	Histogram      []string `json:"histogram"`
	CrossState     string   `json:"crossState"`
	BarsSinceCross int      `json:"barsSinceCross"`
	HistogramTrend string   `json:"histogramTrend"`
}

// MovingAverageFact is one moving average with its derived state.
type MovingAverageFact struct {
	Type      string   `json:"type"`
	Period    int64    `json:"period"`
	Values    []string `json:"values"`
	Latest    string   `json:"latest"`
	PriceVsMA string   `json:"priceVsMa"`
	Slope     string   `json:"slope"`
}

// SeriesFact is a bounded series with its latest value, used for the optional
// indicators.
type SeriesFact struct {
	Period int64    `json:"period"`
	Values []string `json:"values"`
	Latest string   `json:"latest"`
}

// BollingerFact is the Bollinger band reading.
type BollingerFact struct {
	Period                int64    `json:"period"`
	StandardDeviationUp   float64  `json:"standardDeviationUp"`
	StandardDeviationDown float64  `json:"standardDeviationDown"`
	Upper                 []string `json:"upper"`
	Middle                []string `json:"middle"`
	Lower                 []string `json:"lower"`
	LatestUpper           string   `json:"latestUpper"`
	LatestMiddle          string   `json:"latestMiddle"`
	LatestLower           string   `json:"latestLower"`
}

// IndicatorParams records the settings the indicators were computed with.
//
// It is part of the snapshot rather than a global setting because a decision
// must be replayable: without the parameters, the same candles would produce a
// different MACD and the recorded reading could not be checked.
type IndicatorParams struct {
	MACDFast   int64 `json:"macdFast"`
	MACDSlow   int64 `json:"macdSlow"`
	MACDSignal int64 `json:"macdSignal"`
	// SMAPeriods and EMAPeriods are separate because the same period means a
	// different line for each kind, and a reader checking a recorded decision
	// must be able to tell which one produced a value.
	SMAPeriods      []int64 `json:"smaPeriods,omitempty"`
	EMAPeriods      []int64 `json:"emaPeriods,omitempty"`
	RSIPeriod       int64   `json:"rsiPeriod,omitempty"`
	ATRPeriod       int64   `json:"atrPeriod,omitempty"`
	BollingerPeriod int64   `json:"bollingerPeriod,omitempty"`
}

// IndicatorSet is every indicator reading.
type IndicatorSet struct {
	MACD           *MACDFact           `json:"macd,omitempty"`
	MovingAverages []MovingAverageFact `json:"movingAverages,omitempty"`
	RSI            *SeriesFact         `json:"rsi,omitempty"`
	ATR            *SeriesFact         `json:"atr,omitempty"`
	Bollinger      *BollingerFact      `json:"bollinger,omitempty"`
	ComputedAt     time.Time           `json:"computedAt"`
	Params         IndicatorParams     `json:"params"`
}

// TickerFacts is the rolling 24 hour ticker reading.
type TickerFacts struct {
	Last        string    `json:"last"`
	Bid         string    `json:"bid"`
	Ask         string    `json:"ask"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Volume      string    `json:"volume"`
	QuoteVolume string    `json:"quoteVolume"`
	ChangePct   string    `json:"changePct"`
	ReadAt      time.Time `json:"readAt"`
}

// OrderBookLevel is one price level.
type OrderBookLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// OrderBookFacts is the bounded book plus its derived numbers.
type OrderBookFacts struct {
	Bids []OrderBookLevel `json:"bids"`
	Asks []OrderBookLevel `json:"asks"`
	// SpreadBPS, MidPrice and the executable prices are derived from the levels
	// above. ExecutablePriceFor is indexed by notional so a caller can ask what
	// a twenty dollar order and a two hundred dollar order would each pay.
	SpreadBPS string    `json:"spreadBps"`
	MidPrice  string    `json:"midPrice"`
	ReadAt    time.Time `json:"readAt"`
}

// FundingFacts is the funding reading, which is a cost the model may consider
// and not a risk the engine refuses on.
type FundingFacts struct {
	LatestRate        string    `json:"latestRate"`
	AnnualisedPct     string    `json:"annualisedPct"`
	History           []string  `json:"history"`
	NextFundingTime   time.Time `json:"nextFundingTime"`
	SettlementsPerDay int64     `json:"settlementsPerDay"`
	ReadAt            time.Time `json:"readAt"`
}

// PositionFact is one open position.
type PositionFact struct {
	Symbol           string `json:"symbol"`
	Side             string `json:"side"`
	Size             string `json:"size"`
	EntryPrice       string `json:"entryPrice"`
	MarkPrice        string `json:"markPrice"`
	LiquidationPrice string `json:"liquidationPrice"`
	UnrealisedPNLUSD string `json:"unrealisedPnlUsd"`
	Leverage         string `json:"leverage"`
}

// AccountFacts is the account state at read time.
type AccountFacts struct {
	TotalEquityUSD      string         `json:"totalEquityUsd"`
	AvailableBalanceUSD string         `json:"availableBalanceUsd"`
	UnrealisedPNLUSD    string         `json:"unrealisedPnlUsd"`
	Positions           []PositionFact `json:"positions"`
	ReadAt              time.Time      `json:"readAt"`
}

// MarketSnapshot is the bounded, immutable fact set one decision runs on.
type MarketSnapshot struct {
	// ID is the snapshot identifier and the first evidence reference every
	// candidate must cite.
	ID string `json:"id"`
	// Exchange, Pair and Asset name the market. Pair is in config format, such
	// as BTC_USDT.
	Exchange string `json:"exchange"`
	Pair     string `json:"pair"`
	Asset    string `json:"asset"`
	// SourceTime is the close of the newest closed candle, not the build time.
	// It is what freshness is measured against, because a snapshot built now
	// from stale candles is stale.
	SourceTime time.Time `json:"sourceTime"`
	// BuiltAt is when the snapshot was assembled.
	BuiltAt time.Time `json:"builtAt"`

	Candles    CandleWindow    `json:"candles"`
	Indicators IndicatorSet    `json:"indicators"`
	Ticker     *TickerFacts    `json:"ticker,omitempty"`
	OrderBook  *OrderBookFacts `json:"orderbook,omitempty"`
	Funding    *FundingFacts   `json:"funding,omitempty"`
	Account    *AccountFacts   `json:"account,omitempty"`
	// LiveCandle is the candle that has not closed yet. It may describe where
	// the price is, and it must never be used to compute an indicator, because
	// its values still change.
	LiveCandle *CandleFact `json:"liveCandle,omitempty"`
	// Gaps lists holes (and padded candles) found in the closed window; it is
	// non-empty exactly when the candle and indicator categories are marked
	// GAP (docs 04 §5).
	Gaps []GapRange `json:"gaps,omitempty"`

	// Quality labels each fact category. A category absent from the map is
	// UNKNOWN rather than trusted.
	Quality map[string]candidate.DataQuality `json:"quality"`
	// FreshnessMS is the age of the oldest required fact, so a single number
	// answers "is this snapshot fresh enough".
	FreshnessMS int64 `json:"freshnessMs"`
	// OnDemand marks a snapshot for a market that is not enabled for trading.
	// Such a snapshot may be discussed and must never enter the decision
	// pipeline (docs/llm-dev/04-数据层-K线与指标.md §7.3).
	OnDemand bool `json:"onDemand,omitempty"`
}

// Validate checks the snapshot's internal consistency and its bounds.
//
// It does not check freshness: freshness depends on the current time, and a
// snapshot that has aged is still a well-formed record of what was true.
func (s *MarketSnapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: nil snapshot", ErrSnapshotIncomplete)
	}
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("%w: id", ErrSnapshotIncomplete)
	}
	if strings.TrimSpace(s.Exchange) == "" {
		return fmt.Errorf("%w: exchange", ErrSnapshotIncomplete)
	}
	if strings.TrimSpace(s.Pair) == "" {
		return fmt.Errorf("%w: pair", ErrSnapshotIncomplete)
	}
	if strings.TrimSpace(s.Asset) == "" {
		return fmt.Errorf("%w: asset", ErrSnapshotIncomplete)
	}
	if s.SourceTime.IsZero() || s.BuiltAt.IsZero() {
		return fmt.Errorf("%w: source time and build time are required", ErrSnapshotIncomplete)
	}
	if s.SourceTime.After(s.BuiltAt) {
		return fmt.Errorf("%w: source %s after build %s", ErrTimeOrder, s.SourceTime, s.BuiltAt)
	}
	if err := s.Candles.validate(); err != nil {
		return err
	}
	if err := s.Indicators.validate(); err != nil {
		return err
	}
	if err := s.Ticker.validate(); err != nil {
		return err
	}
	if err := s.OrderBook.validate(); err != nil {
		return err
	}
	if err := s.Funding.validate(); err != nil {
		return err
	}
	if err := s.Account.validate(); err != nil {
		return err
	}
	if s.LiveCandle != nil {
		if err := s.LiveCandle.Validate(); err != nil {
			return fmt.Errorf("live candle: %w", err)
		}
	}
	return nil
}

func (c *CandleWindow) validate() error {
	if c == nil {
		return nil
	}
	if err := c.Interval.Validate(); err != nil {
		return err
	}
	if len(c.Closed) > MaxClosedCandles {
		return fmt.Errorf("%w: %d closed candles over %d", ErrOverBound, len(c.Closed), MaxClosedCandles)
	}
	if c.Count < len(c.Closed) {
		return fmt.Errorf("%w: count %d is below the %d candles carried", ErrSnapshotIncomplete, c.Count, len(c.Closed))
	}
	for i := range c.Closed {
		if err := c.Closed[i].Validate(); err != nil {
			return fmt.Errorf("closed[%d]: %w", i, err)
		}
		if i > 0 && !c.Closed[i].Time.After(c.Closed[i-1].Time) {
			return fmt.Errorf("%w: closed[%d] is not after closed[%d]", ErrSnapshotIncomplete, i, i-1)
		}
	}
	if len(c.Closed) > 0 {
		last := c.Closed[len(c.Closed)-1]
		if !c.End.IsZero() && c.End.Before(last.Time) {
			return fmt.Errorf("%w: window end %s precedes the newest candle %s", ErrSnapshotIncomplete, c.End, last.Time)
		}
	}
	return c.Summary.validate()
}

func (c *CandleSummary) validate() error {
	if c == nil {
		return nil
	}
	for name, value := range map[string]string{
		"firstOpen": c.FirstOpen, "lastClose": c.LastClose, "highestHigh": c.HighestHigh,
		"lowestLow": c.LowestLow, "totalVolume": c.TotalVolume,
	} {
		if value == "" {
			continue
		}
		if err := ValidateDecimal(value); err != nil {
			return fmt.Errorf("summary.%s: %w", name, err)
		}
	}
	// ChangePct and DirectionScore are signed, so they use the signed rule.
	for name, value := range map[string]string{"changePct": c.ChangePct, "directionScore": c.DirectionScore} {
		if value == "" {
			continue
		}
		if err := ValidateSignedDecimal(value); err != nil {
			return fmt.Errorf("summary.%s: %w", name, err)
		}
	}
	return nil
}

func (i *IndicatorSet) validate() error {
	if i == nil {
		return nil
	}
	if i.MACD != nil {
		if err := i.MACD.validate(); err != nil {
			return err
		}
	}
	for idx := range i.MovingAverages {
		if err := i.MovingAverages[idx].validate(); err != nil {
			return fmt.Errorf("movingAverages[%d]: %w", idx, err)
		}
	}
	for name, series := range map[string]*SeriesFact{"rsi": i.RSI, "atr": i.ATR} {
		if series == nil {
			continue
		}
		if err := series.validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return i.Bollinger.validate()
}

func (m *MACDFact) validate() error {
	if m == nil {
		return nil
	}
	if len(m.DIF) != len(m.DEA) || len(m.DIF) != len(m.Histogram) {
		return fmt.Errorf("%w: dif %d, dea %d, histogram %d",
			ErrSeriesMismatch, len(m.DIF), len(m.DEA), len(m.Histogram))
	}
	if len(m.Histogram) > MaxIndicatorPoints {
		return fmt.Errorf("%w: %d histogram points over %d", ErrOverBound, len(m.Histogram), MaxIndicatorPoints)
	}
	for i, series := range [][]string{m.DIF, m.DEA, m.Histogram} {
		for j, value := range series {
			if err := ValidateSignedDecimal(value); err != nil {
				return fmt.Errorf("series %d point %d: %w", i, j, err)
			}
		}
	}
	switch m.CrossState {
	case CrossStateGolden, CrossStateDeath, CrossStateNone:
	default:
		return fmt.Errorf("%w: crossState %q", ErrInvalidDerived, m.CrossState)
	}
	switch m.HistogramTrend {
	case TrendExpanding, TrendContracting, TrendFlat:
	default:
		return fmt.Errorf("%w: histogramTrend %q", ErrInvalidDerived, m.HistogramTrend)
	}
	if m.BarsSinceCross < 0 {
		return fmt.Errorf("%w: barsSinceCross %d", ErrInvalidDerived, m.BarsSinceCross)
	}
	return nil
}

func (m *MovingAverageFact) validate() error {
	if m == nil {
		return nil
	}
	switch m.Type {
	case MASimple, MAExponential:
	default:
		return fmt.Errorf("%w: type %q", ErrInvalidDerived, m.Type)
	}
	if m.Period <= 0 {
		return fmt.Errorf("%w: period %d", ErrInvalidDerived, m.Period)
	}
	if len(m.Values) > MaxIndicatorPoints {
		return fmt.Errorf("%w: %d points over %d", ErrOverBound, len(m.Values), MaxIndicatorPoints)
	}
	for i, value := range m.Values {
		if err := ValidateDecimal(value); err != nil {
			return fmt.Errorf("values[%d]: %w", i, err)
		}
	}
	switch m.PriceVsMA {
	case PriceAbove, PriceBelow:
	default:
		return fmt.Errorf("%w: priceVsMa %q", ErrInvalidDerived, m.PriceVsMA)
	}
	switch m.Slope {
	case SlopeUp, SlopeDown, SlopeFlat:
	default:
		return fmt.Errorf("%w: slope %q", ErrInvalidDerived, m.Slope)
	}
	return nil
}

func (s *SeriesFact) validate() error {
	if s == nil {
		return nil
	}
	if s.Period <= 0 {
		return fmt.Errorf("%w: period %d", ErrInvalidDerived, s.Period)
	}
	if len(s.Values) > MaxIndicatorPoints {
		return fmt.Errorf("%w: %d points over %d", ErrOverBound, len(s.Values), MaxIndicatorPoints)
	}
	for i, value := range s.Values {
		if err := ValidateSignedDecimal(value); err != nil {
			return fmt.Errorf("values[%d]: %w", i, err)
		}
	}
	return nil
}

func (b *BollingerFact) validate() error {
	if b == nil {
		return nil
	}
	if b.Period <= 0 {
		return fmt.Errorf("%w: period %d", ErrInvalidDerived, b.Period)
	}
	for _, series := range [][]string{b.Upper, b.Middle, b.Lower} {
		if len(series) > MaxIndicatorPoints {
			return fmt.Errorf("%w: %d band points over %d", ErrOverBound, len(series), MaxIndicatorPoints)
		}
		for _, value := range series {
			if err := ValidateDecimal(value); err != nil {
				return fmt.Errorf("band point: %w", err)
			}
		}
	}
	return nil
}

func (t *TickerFacts) validate() error {
	if t == nil {
		return nil
	}
	for name, value := range map[string]string{"last": t.Last, "bid": t.Bid, "ask": t.Ask} {
		if err := ValidateDecimal(value); err != nil {
			return fmt.Errorf("ticker.%s: %w", name, err)
		}
	}
	return nil
}

func (o *OrderBookFacts) validate() error {
	if o == nil {
		return nil
	}
	for name, side := range map[string][]OrderBookLevel{"bids": o.Bids, "asks": o.Asks} {
		if len(side) > MaxOrderBookLevels {
			return fmt.Errorf("%w: %s has %d levels over %d", ErrOverBound, name, len(side), MaxOrderBookLevels)
		}
		for i := range side {
			if err := ValidateDecimal(side[i].Price); err != nil {
				return fmt.Errorf("%s[%d].price: %w", name, i, err)
			}
			if err := ValidateDecimal(side[i].Size); err != nil {
				return fmt.Errorf("%s[%d].size: %w", name, i, err)
			}
		}
	}
	// An empty book is allowed while a snapshot is being assembled, but a
	// crossed one never is: it would let a slippage estimate come out negative.
	if len(o.Bids) > 0 && len(o.Asks) > 0 {
		bid, _ := new(big.Float).SetString(o.Bids[0].Price)
		ask, _ := new(big.Float).SetString(o.Asks[0].Price)
		if bid.Cmp(ask) >= 0 {
			return fmt.Errorf("%w: bid %s, ask %s", ErrOrderBookCrossed, o.Bids[0].Price, o.Asks[0].Price)
		}
	}
	return nil
}

func (f *FundingFacts) validate() error {
	if f == nil {
		return nil
	}
	if len(f.History) > MaxFundingRates {
		return fmt.Errorf("%w: %d funding points over %d", ErrOverBound, len(f.History), MaxFundingRates)
	}
	// Funding rates are signed: a negative rate means shorts pay longs.
	if f.LatestRate != "" {
		if err := ValidateSignedDecimal(f.LatestRate); err != nil {
			return fmt.Errorf("funding.latestRate: %w", err)
		}
	}
	for i, value := range f.History {
		if err := ValidateSignedDecimal(value); err != nil {
			return fmt.Errorf("history[%d]: %w", i, err)
		}
	}
	return nil
}

func (a *AccountFacts) validate() error {
	if a == nil {
		return nil
	}
	if len(a.Positions) > MaxPositions {
		return fmt.Errorf("%w: %d positions over %d", ErrOverBound, len(a.Positions), MaxPositions)
	}
	for i := range a.Positions {
		p := &a.Positions[i]
		if strings.TrimSpace(p.Symbol) == "" {
			return fmt.Errorf("%w: positions[%d].symbol", ErrSnapshotIncomplete, i)
		}
		for name, value := range map[string]string{
			"size": p.Size, "entryPrice": p.EntryPrice, "markPrice": p.MarkPrice,
		} {
			if value == "" {
				continue
			}
			if err := ValidateDecimal(value); err != nil {
				return fmt.Errorf("positions[%d].%s: %w", i, name, err)
			}
		}
	}
	return nil
}

// ValidateDecimal checks that a value is a plain non-negative decimal.
//
// It rejects empty strings, signs and exponents. An exponent would let the same
// number be written two ways, which would break the comparison between what the
// model was shown and what was recorded; a sign would let a negative price or
// size through.
func ValidateDecimal(value string) error {
	if !decimalString.MatchString(value) {
		return fmt.Errorf("%w: %q", ErrInvalidDecimal, value)
	}
	return nil
}

// ValidateSignedDecimal allows a leading minus. It is for values that are
// genuinely signed, such as a percentage change, a histogram value or a funding
// rate, so those do not have to be checked by a different and laxer rule.
func ValidateSignedDecimal(value string) error {
	trimmed := strings.TrimPrefix(value, "-")
	return ValidateDecimal(trimmed)
}

// QualityOf returns a category's label, defaulting to UNKNOWN.
//
// The default is deliberately untrusting: a category carrying data but no label
// is one nobody assessed, and treating an unassessed fact as trusted is the
// mistake the labelling scheme exists to prevent. Absence of the fact itself is
// a different matter, which DegradedCategories distinguishes.
func (s *MarketSnapshot) QualityOf(category string) candidate.DataQuality {
	if s == nil {
		return candidate.QualityUnknown
	}
	quality, ok := s.Quality[category]
	if !ok {
		return candidate.QualityUnknown
	}
	return quality
}

// SetQuality records a category's label, lowering it if the category already
// carries one.
//
// Lowering is the only permitted direction, so a writer cannot accidentally
// re-trust a fact a later stage has already discredited.
func (s *MarketSnapshot) SetQuality(category string, quality candidate.DataQuality) error {
	if s == nil {
		return fmt.Errorf("%w: nil snapshot", ErrSnapshotIncomplete)
	}
	if s.Quality == nil {
		s.Quality = make(map[string]candidate.DataQuality, len(Categories()))
	}
	existing, ok := s.Quality[category]
	if !ok {
		s.Quality[category] = quality
		return nil
	}
	lowered, err := existing.Downgrade(quality)
	if err != nil {
		return fmt.Errorf("quality %s: %w", category, err)
	}
	s.Quality[category] = lowered
	return nil
}

// DegradedCategories returns the categories whose quality blocks a decision.
//
// The pipeline refuses on a non-empty result rather than on a specific label,
// so adding a blocking label later does not mean hunting for every call site.
//
// A category counts when it carries data, or when it carries a label at all. The
// distinction matters in both directions:
//
//   - Data with no label is UNKNOWN and blocks, because nobody assessed it.
//   - A label that is itself degraded blocks even with no data, because a
//     builder that marked a category STALE after a failed fetch is asserting
//     that something it needed is missing.
//   - Neither data nor label is simply absent. A candle-only snapshot has no
//     ticker, and refusing it for that would make every narrower snapshot
//     unusable.
func (s *MarketSnapshot) DegradedCategories() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, category := range Categories() {
		_, labelled := s.Quality[category]
		if !labelled && !s.carriesData(category) {
			continue
		}
		if s.QualityOf(category).IsDegraded() {
			out = append(out, category)
		}
	}
	return out
}

// carriesData reports whether a category holds any fact at all.
func (s *MarketSnapshot) carriesData(category string) bool {
	switch category {
	case CategoryCandles:
		return s.Candles.Count > 0 || len(s.Candles.Closed) > 0 || s.LiveCandle != nil
	case CategoryIndicators:
		return s.Indicators.MACD != nil || len(s.Indicators.MovingAverages) > 0 ||
			s.Indicators.RSI != nil || s.Indicators.ATR != nil || s.Indicators.Bollinger != nil
	case CategoryTicker:
		return s.Ticker != nil
	case CategoryOrderBook:
		return s.OrderBook != nil
	case CategoryFunding:
		return s.Funding != nil
	case CategoryAccount:
		return s.Account != nil
	default:
		return false
	}
}

// Clone returns a deep copy, so a builder can hand out a snapshot that a caller
// cannot mutate into a different decision's input.
func (s *MarketSnapshot) Clone() *MarketSnapshot {
	if s == nil {
		return nil
	}
	out := *s
	out.Candles.Closed = append([]CandleFact(nil), s.Candles.Closed...)
	// Every nested slice is copied. A shallow copy would leave two snapshots
	// sharing a series, so editing one would silently change the other's facts.
	out.Indicators.MovingAverages = make([]MovingAverageFact, len(s.Indicators.MovingAverages))
	for i := range s.Indicators.MovingAverages {
		out.Indicators.MovingAverages[i] = s.Indicators.MovingAverages[i]
		out.Indicators.MovingAverages[i].Values = append([]string(nil), s.Indicators.MovingAverages[i].Values...)
	}
	for name, source := range map[string]**SeriesFact{"rsi": &s.Indicators.RSI, "atr": &s.Indicators.ATR} {
		if *source == nil {
			continue
		}
		series := **source
		series.Values = append([]string(nil), (*source).Values...)
		switch name {
		case "rsi":
			out.Indicators.RSI = &series
		case "atr":
			out.Indicators.ATR = &series
		}
	}
	if s.Indicators.Bollinger != nil {
		bands := *s.Indicators.Bollinger
		bands.Upper = append([]string(nil), s.Indicators.Bollinger.Upper...)
		bands.Middle = append([]string(nil), s.Indicators.Bollinger.Middle...)
		bands.Lower = append([]string(nil), s.Indicators.Bollinger.Lower...)
		out.Indicators.Bollinger = &bands
	}
	if s.Indicators.MACD != nil {
		macd := *s.Indicators.MACD
		macd.DIF = append([]string(nil), s.Indicators.MACD.DIF...)
		macd.DEA = append([]string(nil), s.Indicators.MACD.DEA...)
		macd.Histogram = append([]string(nil), s.Indicators.MACD.Histogram...)
		out.Indicators.MACD = &macd
	}
	if s.LiveCandle != nil {
		live := *s.LiveCandle
		out.LiveCandle = &live
	}
	if s.OrderBook != nil {
		book := *s.OrderBook
		book.Bids = append([]OrderBookLevel(nil), s.OrderBook.Bids...)
		book.Asks = append([]OrderBookLevel(nil), s.OrderBook.Asks...)
		out.OrderBook = &book
	}
	if s.Funding != nil {
		funding := *s.Funding
		funding.History = append([]string(nil), s.Funding.History...)
		out.Funding = &funding
	}
	if s.Account != nil {
		account := *s.Account
		account.Positions = append([]PositionFact(nil), s.Account.Positions...)
		out.Account = &account
	}
	if s.Ticker != nil {
		ticker := *s.Ticker
		out.Ticker = &ticker
	}
	out.Quality = make(map[string]candidate.DataQuality, len(s.Quality))
	maps.Copy(out.Quality, s.Quality)
	return &out
}
