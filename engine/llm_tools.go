package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchange/accounts"
	exchange "github.com/thrasher-corp/gocryptotrader/exchanges"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/fundingrate"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// iToolDataSource is the narrow slice of an exchange the tool executors need;
// exchange.IBotExchange satisfies it by construction.
type iToolDataSource interface {
	GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
	GetCachedOrderbook(p currency.Pair, a asset.Item) (*orderbook.Book, error)
	GetCachedSubAccounts(ctx context.Context, a asset.Item) (accounts.SubAccounts, error)
	GetFuturesPositions(ctx context.Context, r *futures.PositionsRequest) ([]futures.PositionDetails, error)
	GetLatestFundingRates(ctx context.Context, r *fundingrate.LatestRateRequest) ([]fundingrate.LatestRateResponse, error)
	GetHistoricalFundingRates(ctx context.Context, r *fundingrate.HistoricalRatesRequest) (*fundingrate.HistoricalRates, error)
	GetEnabledPairs(a asset.Item) (currency.Pairs, error)
	GetAvailablePairs(a asset.Item) (currency.Pairs, error)
}

var _ iToolDataSource = exchange.IBotExchange(nil)

// Return bounds from docs 05 §4.1.
const (
	toolCandleReturnLimit     = 50
	toolIndicatorPointLimit   = 20
	toolOrderBookReturnLevels = 20
	toolFundingHistoryReturn  = 21
	toolCandleFetchMargin     = 30
)

// LLMToolRunner executes the eight read-only tools against the live engine and
// returns serialised candidate.ToolEvidence for the tool loop. Every result
// passes the four gates here: candidate.ToolEvidence.Validate (time / size /
// sensitive keys / depth) and Scope.CheckEvidence (market / account / strategy).
type LLMToolRunner struct {
	// Exchange is the configured exchange name, e.g. "Binance".
	Exchange string
	// Asset is the configured asset type.
	Asset asset.Item
	// AccountID is the account this run may read; getAccountState must match it.
	AccountID string
	// Scope is the run's authorization boundary; nil is closed by default.
	Scope *tools.Scope
	// Source is the exchange-backed data source.
	Source iToolDataSource
	// Cache, when set, serves repeated candle fetches from closed history
	// instead of the exchange (M2-02).
	Cache *KlineCache
	// Markets is the covered universe in run vocabulary; screenMarkets scans
	// it (docs 13 §3.1). Empty leaves the screener tool refuse-closed.
	Markets []string
	// Interval is the cached candle interval the screener reads.
	Interval kline.Interval
	// FlowScanLimit bounds the T2 (per-symbol taker flow) coverage; zero
	// resolves to defaultScreenerFlowScanLimit.
	FlowScanLimit int
	// FullUniverse scans every available in-scope perpetual (N10), not only the
	// covered/rotator set. Missing candles are fetched on demand (pace-limited)
	// and backfilled into the cache.
	FullUniverse bool
	// JevJudge/JevModel/JevRevisionID/JevBudget configure the advisory Jev calls
	// (assessOpeningOpportunities). Nil leaves the tool refuse-closed.
	JevJudge      iJevBoundedJudge
	JevModel      string
	JevRevisionID string
	JevBudget     iChatBudget
	// Now, when set, is the clock (tests); nil means time.Now.
	Now func() time.Time
}

// NewLLMToolRunner builds a runner, rejecting a missing source or exchange.
func NewLLMToolRunner(exchangeName string, a asset.Item, accountID string, scope *tools.Scope, source iToolDataSource) (*LLMToolRunner, error) {
	if strings.TrimSpace(exchangeName) == "" {
		return nil, errors.New("llm tools: exchange name is required")
	}
	if source == nil {
		return nil, errors.New("llm tools: data source is required")
	}
	return &LLMToolRunner{Exchange: exchangeName, Asset: a, AccountID: accountID, Scope: scope, Source: source}, nil
}

func (r *LLMToolRunner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Supported reports whether the name is one of the contract tools.
func (r *LLMToolRunner) Supported(name string) bool {
	return slices.Contains(tools.Names(), name)
}

// Run decodes and executes one tool call, returning the serialised evidence
// the model will see.
func (r *LLMToolRunner) Run(ctx context.Context, callID, name, rawArgs string) (string, error) {
	var (
		payload                                any
		asOf                                   time.Time
		quality                                candidate.DataQuality
		marketID, accountID, strategyVersionID *string
		err                                    error
	)
	switch name {
	case tools.ToolListMarkets:
		payload, asOf, err = r.runListMarkets(rawArgs)
		quality = candidate.QualityLocalTracked
	case tools.ToolGetCandleWindow:
		payload, asOf, marketID, err = r.runCandleWindow(ctx, rawArgs)
		quality = candidate.QualityExchangeREST
	case tools.ToolGetIndicators:
		payload, asOf, marketID, err = r.runIndicators(ctx, rawArgs)
		quality = candidate.QualityExchangeREST
	case tools.ToolGetOrderBook:
		payload, asOf, marketID, err = r.runOrderBook(rawArgs)
		quality = candidate.QualityExchangeREST
	case tools.ToolGetFunding:
		payload, asOf, marketID, err = r.runFunding(ctx, rawArgs)
		quality = candidate.QualityExchangeREST
	case tools.ToolGetAccountState:
		payload, asOf, accountID, err = r.runAccountState(ctx, rawArgs)
		quality = candidate.QualityLocalTracked
	case tools.ToolGetStrategyHistory:
		payload, asOf, strategyVersionID, err = r.runStrategyHistory(ctx, rawArgs)
		quality = candidate.QualityDatabaseCached
	case tools.ToolGetRiskBudget:
		payload, asOf, strategyVersionID, err = r.runRiskBudget(ctx, rawArgs)
		quality = candidate.QualityDatabaseCached
	case tools.ToolScreenMarkets:
		payload, asOf, err = r.RunScreenMarkets(ctx, rawArgs)
		if err == nil {
			if out, isMap := payload.(map[string]any); isMap {
				// The chat tool path rides the evidence byte cap (which
				// rejects); the GUI RPC calls RunScreenMarkets directly and
				// is not trimmed.
				payload = fitScreenResultToEvidenceBudget(out)
			}
		}
		quality = candidate.QualityDatabaseCached
	case tools.ToolProposeAction:
		payload, asOf, err = r.runProposeAction(rawArgs)
		quality = candidate.QualityDatabaseCached
	case tools.ToolAssessOpening:
		payload, asOf, err = r.runAssessOpeningOpportunities(ctx, rawArgs)
		quality = candidate.QualityDatabaseCached
	default:
		return "", fmt.Errorf("%w: %s", tools.ErrUnknownTool, name)
	}
	if err != nil {
		if errors.Is(err, tools.ErrArgumentsInvalid) {
			return "", fmt.Errorf("%w: %v", ErrToolArgumentsInvalid, err)
		}
		return "", err
	}
	return r.evidence(name, callID, asOf, quality, marketID, accountID, strategyVersionID, payload)
}

// evidence builds the ToolEvidence, runs the four gates and serialises it.
func (r *LLMToolRunner) evidence(name, callID string, asOf time.Time, quality candidate.DataQuality, marketID, accountID, strategyVersionID *string, data any) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	evidence := candidate.ToolEvidence{
		Ref:               "tool:" + name + ":" + callID,
		AsOf:              asOf.UTC(),
		Quality:           quality,
		Exchange:          r.Exchange,
		MarketID:          marketID,
		AccountID:         accountID,
		StrategyVersionID: strategyVersionID,
		Data:              raw,
	}
	if err := evidence.Validate(r.now()); err != nil {
		return "", err
	}
	if err := r.Scope.CheckEvidence(&evidence); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (r *LLMToolRunner) parseMarketID(id string) (currency.Pair, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return currency.Pair{}, fmt.Errorf("%w: marketId %q is not <exchange>:<asset>:<BASE>_<QUOTE>", tools.ErrArgumentsInvalid, id)
	}
	if parts[0] != r.Exchange {
		return currency.Pair{}, fmt.Errorf("%w: market %q is not on the configured exchange %s", tools.ErrArgumentsInvalid, id, r.Exchange)
	}
	if parts[1] != r.Asset.String() {
		return currency.Pair{}, fmt.Errorf("%w: market %q is not on the configured asset %s", tools.ErrArgumentsInvalid, id, r.Asset)
	}
	pairParts := strings.Split(parts[2], "_")
	if len(pairParts) != 2 || pairParts[0] == "" || pairParts[1] == "" {
		return currency.Pair{}, fmt.Errorf("%w: marketId %q is not <exchange>:<asset>:<BASE>_<QUOTE>", tools.ErrArgumentsInvalid, id)
	}
	return currency.NewPairWithDelimiter(pairParts[0], pairParts[1], "_"), nil
}

// marketIDFor renders the tool-facing market id of a pair.
func marketIDFor(exchangeName string, a asset.Item, pair currency.Pair) string {
	return exchangeName + ":" + a.String() + ":" + pair.Base.String() + "_" + pair.Quote.String()
}

func toolIntervalDuration(interval string) (time.Duration, error) {
	switch interval {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "30m":
		return 30 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "4h":
		return 4 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("%w: interval %q", tools.ErrArgumentsInvalid, interval)
}

func toolKlineInterval(interval string) (kline.Interval, error) {
	switch interval {
	case "1m":
		return kline.OneMin, nil
	case "5m":
		return kline.FiveMin, nil
	case "15m":
		return kline.FifteenMin, nil
	case "30m":
		return kline.ThirtyMin, nil
	case "1h":
		return kline.OneHour, nil
	case "4h":
		return kline.FourHour, nil
	case "1d":
		return kline.OneDay, nil
	}
	return 0, fmt.Errorf("%w: interval %q", tools.ErrArgumentsInvalid, interval)
}

// fetchClosedWindow fetches candles and returns only the closed ones, newest
// last, capped to limit. The close test matches the assembler's: a candle is
// closed once its interval has elapsed.
func (r *LLMToolRunner) fetchClosedWindow(ctx context.Context, pair currency.Pair, interval string, bars, limit int) ([]kline.Candle, error) {
	duration, err := toolIntervalDuration(interval)
	if err != nil {
		return nil, err
	}
	kInterval, err := toolKlineInterval(interval)
	if err != nil {
		return nil, err
	}
	now := r.now()
	start := now.Add(-time.Duration(bars+toolCandleFetchMargin) * duration)
	window, err := fetchCandlesThroughCache(ctx, r.Source, r.Cache, pair, r.Asset, kInterval, start, now, now)
	if err != nil {
		// N10: the full-universe flow screens markets the operator has not
		// enabled; the ordinary candle path refuses those pairs by design, so
		// fall back to the venue's unrestricted read-only fetch.
		unrestricted, canRead := r.Source.(iUnrestrictedKlineSource)
		if !canRead || !errors.Is(err, currency.ErrPairNotEnabled) {
			return nil, err
		}
		window, err = unrestricted.GetHistoricCandlesUnrestricted(ctx, pair, r.Asset, kInterval, start, now)
		if err != nil {
			return nil, err
		}
	}
	if window == nil || len(window.Candles) == 0 {
		return nil, fmt.Errorf("no candles returned for %s", pair)
	}
	seconds := window.Interval.Duration()
	closed := make([]kline.Candle, 0, len(window.Candles))
	for i := range window.Candles {
		if window.Candles[i].Time.Add(seconds).After(now) {
			continue
		}
		closed = append(closed, window.Candles[i])
	}
	if len(closed) == 0 {
		return nil, fmt.Errorf("no closed candles for %s", pair)
	}
	if len(closed) > limit {
		closed = closed[len(closed)-limit:]
	}
	return closed, nil
}

func decimalOf(value float64) (string, error) {
	return snapshot.DecimalFromFloat(value)
}

type candlePayload struct {
	MarketID string              `json:"marketId"`
	Interval string              `json:"interval"`
	Candles  []map[string]string `json:"candles"`
}

func (r *LLMToolRunner) runCandleWindow(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeCandleWindow(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	pair, err := r.parseMarketID(args.MarketID)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	closed, err := r.fetchClosedWindow(ctx, pair, args.Interval, args.LookbackBars, toolCandleReturnLimit)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	candles := make([]map[string]string, 0, len(closed))
	for i := range closed {
		open, err := decimalOf(closed[i].Open)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		high, err := decimalOf(closed[i].High)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		low, err := decimalOf(closed[i].Low)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		closePrice, err := decimalOf(closed[i].Close)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		volume, err := decimalOf(closed[i].Volume)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		candles = append(candles, map[string]string{
			"time":   closed[i].Time.UTC().Format(time.RFC3339),
			"open":   open,
			"high":   high,
			"low":    low,
			"close":  closePrice,
			"volume": volume,
		})
	}
	asOf := closed[len(closed)-1].Time
	return candlePayload{MarketID: args.MarketID, Interval: args.Interval, Candles: candles}, asOf, new(args.MarketID), nil
}

type indicatorPayload struct {
	MarketID   string              `json:"marketId"`
	Interval   string              `json:"interval"`
	Indicators map[string][]string `json:"indicators"`
}

// runIndicators computes the requested series over the closed window. MACD,
// SMA and EMA reuse the exchange kline helpers the snapshot assembler uses;
// RSI, ATR and BBANDS are computed locally with the standard formulas
// (Wilder smoothing / SMA +/- 2 sigma) so the tool layer carries no unverified
// API surface. TODO: re-point these at the snapshot package's series once its
// extraction surface is reviewed, so tool and snapshot values cannot drift.
func (r *LLMToolRunner) runIndicators(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeIndicators(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	pair, err := r.parseMarketID(args.MarketID)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	closed, err := r.fetchClosedWindow(ctx, pair, args.Interval, args.LookbackBars, args.LookbackBars)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	window := &kline.Item{Pair: pair, Asset: r.Asset, Interval: mustKlineInterval(args.Interval), Candles: closed}
	closes := make([]float64, len(closed))
	for i := range closed {
		closes[i] = closed[i].Close
	}

	capped := func(values []float64) ([]string, error) {
		if len(values) > toolIndicatorPointLimit {
			values = values[len(values)-toolIndicatorPointLimit:]
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, err := decimalOf(value)
			if err != nil {
				return nil, err
			}
			out = append(out, text)
		}
		return out, nil
	}

	series := make(map[string][]string, len(args.Indicators))
	for _, name := range args.Indicators {
		switch name {
		case "MACD":
			fast, slow, signal := 12, 26, 9
			if args.MACDParams != nil {
				fast, slow, signal = args.MACDParams.Fast, args.MACDParams.Slow, args.MACDParams.Signal
			}
			macd, err := window.GetMovingAverageConvergenceDivergenceOnClose(int64(fast), int64(slow), int64(signal))
			if err != nil {
				return nil, time.Time{}, nil, fmt.Errorf("computing MACD: %w", err)
			}
			dif, err := capped(macd.Results)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			dea, err := capped(macd.SignalVals)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			histogram, err := capped(macd.Histogram)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			series["MACD.dif"] = dif
			series["MACD.dea"] = dea
			series["MACD.histogram"] = histogram
		case "SMA", "EMA":
			period := int64(20)
			if len(args.MAPeriods) > 0 {
				period = int64(args.MAPeriods[0])
			}
			var values []float64
			if name == "SMA" {
				values, err = window.GetSimpleMovingAverageOnClose(period)
			} else {
				values, err = window.GetExponentialMovingAverageOnClose(period)
			}
			if err != nil {
				return nil, time.Time{}, nil, fmt.Errorf("computing %s(%d): %w", name, period, err)
			}
			points, err := capped(values)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			series[name] = points
		case "RSI":
			points, err := capped(wilderRSI(closes, 14))
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			series["RSI"] = points
		case "ATR":
			points, err := capped(wilderATR(closed, 14))
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			series["ATR"] = points
		case "BBANDS":
			middle, err := capped(simpleMovingAverage(closes, 20))
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			upper, err := capped(bollingerBand(closes, 20, 2))
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			lower, err := capped(bollingerBand(closes, 20, -2))
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			series["BBANDS.middle"] = middle
			series["BBANDS.upper"] = upper
			series["BBANDS.lower"] = lower
		}
	}
	asOf := closed[len(closed)-1].Time
	return indicatorPayload{MarketID: args.MarketID, Interval: args.Interval, Indicators: series}, asOf, new(args.MarketID), nil
}

func mustKlineInterval(interval string) kline.Interval {
	kInterval, _ := toolKlineInterval(interval)
	return kInterval
}

func simpleMovingAverage(values []float64, period int) []float64 {
	if period <= 0 || len(values) < period {
		return nil
	}
	out := make([]float64, 0, len(values)-period+1)
	sum := 0.0
	for i := range values {
		sum += values[i]
		if i >= period {
			sum -= values[i-period]
		}
		if i >= period-1 {
			out = append(out, sum/float64(period))
		}
	}
	return out
}

func rollingStdDev(values []float64, period int) []float64 {
	if period <= 1 || len(values) < period {
		return nil
	}
	out := make([]float64, 0, len(values)-period+1)
	for i := period - 1; i < len(values); i++ {
		window := values[i-period+1 : i+1]
		mean := 0.0
		for _, v := range window {
			mean += v
		}
		mean /= float64(period)
		variance := 0.0
		for _, v := range window {
			d := v - mean
			variance += d * d
		}
		out = append(out, math.Sqrt(variance/float64(period)))
	}
	return out
}

func bollingerBand(values []float64, period int, multiplier float64) []float64 {
	middle := simpleMovingAverage(values, period)
	dev := rollingStdDev(values, period)
	if middle == nil || dev == nil || len(middle) != len(dev) {
		return nil
	}
	out := make([]float64, len(middle))
	for i := range middle {
		out[i] = middle[i] + multiplier*dev[i]
	}
	return out
}

func wilderRSI(closes []float64, period int) []float64 {
	if period <= 0 || len(closes) <= period {
		return nil
	}
	avgGain, avgLoss := 0.0, 0.0
	for i := 1; i <= period; i++ {
		change := closes[i] - closes[i-1]
		if change >= 0 {
			avgGain += change
		} else {
			avgLoss -= change
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)
	out := make([]float64, 0, len(closes)-period)
	out = append(out, rsiFromAverages(avgGain, avgLoss))
	for i := period + 1; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		gain, loss := 0.0, 0.0
		if change >= 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		out = append(out, rsiFromAverages(avgGain, avgLoss))
	}
	return out
}

func rsiFromAverages(avgGain, avgLoss float64) float64 {
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

func wilderATR(candles []kline.Candle, period int) []float64 {
	if period <= 0 || len(candles) <= period {
		return nil
	}
	trueRanges := make([]float64, len(candles))
	for i := 1; i < len(candles); i++ {
		highLow := candles[i].High - candles[i].Low
		highClose := absLocal(candles[i].High - candles[i-1].Close)
		lowClose := absLocal(candles[i].Low - candles[i-1].Close)
		tr := highLow
		if highClose > tr {
			tr = highClose
		}
		if lowClose > tr {
			tr = lowClose
		}
		trueRanges[i] = tr
	}
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trueRanges[i]
	}
	atr := sum / float64(period)
	out := []float64{atr}
	for i := period + 1; i < len(candles); i++ {
		atr = (atr*float64(period-1) + trueRanges[i]) / float64(period)
		out = append(out, atr)
	}
	return out
}

func absLocal(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

type orderBookPayload struct {
	MarketID  string              `json:"marketId"`
	Bids      []map[string]string `json:"bids"`
	Asks      []map[string]string `json:"asks"`
	MidPrice  string              `json:"midPrice,omitempty"`
	SpreadBPS string              `json:"spreadBps,omitempty"`
	Slippage  *slippagePayload    `json:"slippage,omitempty"`
}

type slippagePayload struct {
	NominalUSD   string `json:"nominalUsd"`
	FillableUSD  string `json:"fillableUsd"`
	AveragePrice string `json:"averagePrice"`
	SlippageBPS  string `json:"slippageBps"`
}

func (r *LLMToolRunner) runOrderBook(raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeOrderBook(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	pair, err := r.parseMarketID(args.MarketID)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	book, err := r.Source.GetCachedOrderbook(pair, r.Asset)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	if book == nil {
		return nil, time.Time{}, nil, errors.New("no cached order book")
	}
	take := min(args.DepthLevels, toolOrderBookReturnLevels)
	convert := func(levels orderbook.Levels) ([]map[string]string, error) {
		if len(levels) > take {
			levels = levels[:take]
		}
		out := make([]map[string]string, 0, len(levels))
		for i := range levels {
			price, err := decimalOf(levels[i].Price)
			if err != nil {
				return nil, err
			}
			amount, err := decimalOf(levels[i].Amount)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]string{"price": price, "amount": amount})
		}
		return out, nil
	}
	bids, err := convert(book.Bids)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	asks, err := convert(book.Asks)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	payload := orderBookPayload{MarketID: args.MarketID, Bids: bids, Asks: asks}
	if len(book.Bids) > 0 && len(book.Asks) > 0 {
		bestBid, err := decimalOf(book.Bids[0].Price)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		bestAsk, err := decimalOf(book.Asks[0].Price)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		spread, err := snapshot.DeriveSpreadBPS(bestBid, bestAsk)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		mid, err := snapshot.DeriveMidPrice(bestBid, bestAsk)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		payload.SpreadBPS = spread.String()
		payload.MidPrice = mid.String()
	}
	if args.NominalUSD != "" {
		target, err := strconv.ParseFloat(args.NominalUSD, 64)
		if err != nil {
			return nil, time.Time{}, nil, fmt.Errorf("%w: nominalUsd %q", tools.ErrArgumentsInvalid, args.NominalUSD)
		}
		quantity, spent, bestAskPrice := 0.0, 0.0, 0.0
		if len(book.Asks) > 0 {
			bestAskPrice = book.Asks[0].Price
		}
		for i := range book.Asks {
			levelUSD := book.Asks[i].Price * book.Asks[i].Amount
			if spent+levelUSD >= target {
				remaining := target - spent
				quantity += remaining / book.Asks[i].Price
				spent = target
				break
			}
			spent += levelUSD
			quantity += book.Asks[i].Amount
		}
		if quantity > 0 {
			averagePrice := spent / quantity
			slippageBPS := 0.0
			if bestAskPrice > 0 {
				slippageBPS = (averagePrice - bestAskPrice) / bestAskPrice * 10000
			}
			fillable, err := decimalOf(spent)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			avg, err := decimalOf(averagePrice)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			slip, err := decimalOf(slippageBPS)
			if err != nil {
				return nil, time.Time{}, nil, err
			}
			payload.Slippage = &slippagePayload{
				NominalUSD:   args.NominalUSD,
				FillableUSD:  fillable,
				AveragePrice: avg,
				SlippageBPS:  slip,
			}
		}
	}
	return payload, r.now(), new(args.MarketID), nil
}

type fundingPayload struct {
	MarketID     string              `json:"marketId"`
	LatestRate   map[string]string   `json:"latestRate"`
	NextRateTime string              `json:"nextRateTime"`
	History      []map[string]string `json:"history,omitempty"`
}

func (r *LLMToolRunner) runFunding(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeFunding(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	pair, err := r.parseMarketID(args.MarketID)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	// The predicted-upcoming rate is deliberately not requested: on Binance it
	// makes the endpoint answer 500 (same finding as the webUI proxy notes).
	latest, err := r.Source.GetLatestFundingRates(ctx, &fundingrate.LatestRateRequest{Asset: r.Asset, Pair: pair})
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	if len(latest) == 0 {
		return nil, time.Time{}, nil, errors.New("no funding rate returned")
	}
	payload := fundingPayload{
		MarketID: args.MarketID,
		LatestRate: map[string]string{
			"rate":    latest[0].LatestRate.Rate.String(),
			"payment": latest[0].LatestRate.Payment.String(),
			"time":    latest[0].LatestRate.Time.UTC().Format(time.RFC3339),
		},
		NextRateTime: latest[0].TimeOfNextRate.UTC().Format(time.RFC3339),
	}
	if args.IncludeHistory {
		now := r.now()
		history, err := r.Source.GetHistoricalFundingRates(ctx, &fundingrate.HistoricalRatesRequest{
			Asset:     r.Asset,
			Pair:      pair,
			StartDate: now.Add(-time.Duration(args.HistoryLimit) * 8 * time.Hour),
			EndDate:   now,
		})
		if err != nil {
			return nil, time.Time{}, nil, err
		}
		if history != nil {
			rates := history.FundingRates
			if len(rates) > toolFundingHistoryReturn {
				rates = rates[len(rates)-toolFundingHistoryReturn:]
			}
			for i := range rates {
				payload.History = append(payload.History, map[string]string{
					"time": rates[i].Time.UTC().Format(time.RFC3339),
					"rate": rates[i].Rate.String(),
				})
			}
		}
	}
	asOf := latest[0].TimeChecked
	if latest[0].LatestRate.Time.After(asOf) {
		asOf = latest[0].LatestRate.Time
	}
	return payload, asOf, new(args.MarketID), nil
}

type marketsPayload struct {
	Markets []marketEntry `json:"markets"`
	Count   int           `json:"count"`
}

type marketEntry struct {
	MarketID string `json:"marketId"`
	Status   string `json:"status"`
}

func (r *LLMToolRunner) runListMarkets(raw string) (any, time.Time, error) {
	args, err := tools.DecodeListMarkets(raw)
	if err != nil {
		return nil, time.Time{}, err
	}
	enabled, err := r.Source.GetEnabledPairs(r.Asset)
	if err != nil {
		return nil, time.Time{}, err
	}
	available, err := r.Source.GetAvailablePairs(r.Asset)
	if err != nil {
		return nil, time.Time{}, err
	}
	filter := strings.ToLower(args.Query)
	matches := func(id string) bool {
		return filter == "" || strings.Contains(strings.ToLower(id), filter)
	}
	payload := marketsPayload{Markets: make([]marketEntry, 0)}
	add := func(pair currency.Pair) {
		id := marketIDFor(r.Exchange, r.Asset, pair)
		if !matches(id) || len(payload.Markets) >= args.MaxItems {
			return
		}
		status := "available"
		if enabled.Contains(pair, true) {
			status = "enabled"
		}
		payload.Markets = append(payload.Markets, marketEntry{MarketID: id, Status: status})
	}
	for _, pair := range enabled {
		add(pair)
	}
	for _, pair := range available {
		if !enabled.Contains(pair, true) {
			add(pair)
		}
	}
	payload.Count = len(payload.Markets)
	return payload, r.now(), nil
}

type accountPayload struct {
	AccountID           string   `json:"accountId"`
	TotalEquityUSD      string   `json:"totalEquityUsd,omitempty"`
	AvailableBalanceUSD string   `json:"availableBalanceUsd,omitempty"`
	BalancesReadAt      string   `json:"balancesReadAt,omitempty"`
	PositionMarketIDs   []string `json:"positionMarketIds"`
}

func (r *LLMToolRunner) runAccountState(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeAccountState(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	if args.AccountID != r.AccountID {
		return nil, time.Time{}, nil, fmt.Errorf("%w: account %q is not this run's account", tools.ErrScopeDenied, args.AccountID)
	}
	now := r.now()
	payload := accountPayload{AccountID: args.AccountID, PositionMarketIDs: make([]string, 0)}
	balances, err := r.Source.GetCachedSubAccounts(ctx, r.Asset)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	facts, err := balanceFacts(balances, now)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	if facts != nil {
		payload.TotalEquityUSD = facts.TotalEquityUSD
		payload.AvailableBalanceUSD = facts.AvailableBalanceUSD
		payload.BalancesReadAt = facts.ReadAt.UTC().Format(time.RFC3339)
	}
	positions, err := r.Source.GetFuturesPositions(ctx, &futures.PositionsRequest{
		Asset:     r.Asset,
		StartDate: now.Add(-24 * time.Hour),
		EndDate:   now,
	})
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	for i := range positions {
		payload.PositionMarketIDs = append(payload.PositionMarketIDs, marketIDFor(r.Exchange, r.Asset, positions[i].Pair))
	}
	return payload, now, new(args.AccountID), nil
}

type strategyHistoryPayload struct {
	StrategyVersionID string                `json:"strategyVersionId"`
	Runs              []strategyHistoryItem `json:"runs"`
}

type strategyHistoryItem struct {
	DecisionRunID string `json:"decisionRunId"`
	Trigger       string `json:"trigger"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	CreatedAt     string `json:"createdAt"`
}

func (r *LLMToolRunner) runStrategyHistory(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeStrategyHistory(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	runs, err := llmstore.ListDecisionRunsByStrategy(ctx, args.StrategyVersionID, args.MaxItems)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	payload := strategyHistoryPayload{StrategyVersionID: args.StrategyVersionID, Runs: make([]strategyHistoryItem, 0, len(runs))}
	for i := range runs {
		payload.Runs = append(payload.Runs, strategyHistoryItem{
			DecisionRunID: runs[i].ID,
			Trigger:       runs[i].Trigger,
			Decision:      runs[i].Decision,
			Reason:        runs[i].Reason,
			CreatedAt:     runs[i].CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return payload, r.now(), new(args.StrategyVersionID), nil
}

type riskBudgetPayload struct {
	StrategyVersionID string `json:"strategyVersionId"`
	AccountID         string `json:"accountId"`
	Network           string `json:"network"`
	ExecutionMode     string `json:"executionMode"`
	RiskEnvelopeJSON  string `json:"riskEnvelopeJson"`
	ValidFrom         string `json:"validFrom"`
	ValidUntil        string `json:"validUntil"`
	Revoked           bool   `json:"revoked"`
	Expired           bool   `json:"expired"`
}

func (r *LLMToolRunner) runRiskBudget(ctx context.Context, raw string) (result any, asOfAt time.Time, refID *string, err error) {
	args, err := tools.DecodeRiskBudget(raw)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	approval, err := llmstore.GetStrategyApprovalByVersion(ctx, args.StrategyVersionID)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	payload := riskBudgetPayload{
		StrategyVersionID: args.StrategyVersionID,
		AccountID:         approval.AccountID,
		Network:           approval.Network,
		ExecutionMode:     approval.ExecutionMode,
		RiskEnvelopeJSON:  approval.RiskEnvelopeJSON,
		ValidFrom:         approval.ValidFrom.UTC().Format(time.RFC3339),
		ValidUntil:        approval.ValidUntil.UTC().Format(time.RFC3339),
		Revoked:           approval.RevokedAt != nil,
		Expired:           approval.ExpiredAt != nil,
	}
	return payload, r.now(), new(args.StrategyVersionID), nil
}
