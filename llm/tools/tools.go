// Package tools defines the read-only tool contract from docs 05 §4: the JSON
// schemas the model sees, the Go structs its arguments must decode into, the
// semantic rules those arguments must satisfy, and the run scope every piece
// of returned evidence is checked against.
//
// The executors (which touch candles, order books, funding and accounts) live
// in engine/: this package is pure so the model layer stays extractable, and
// so every rule here is testable without a bot.
//
// Decoding is deliberately strict twice over (docs 05 §4.6): encoding/json
// into named structs with DisallowUnknownFields forbids extra fields, and the
// handwritten Validate functions own ranges, enums and cross-field rules. A
// reflective schema library would hide the exact rejection reason; these rules
// name theirs.
package tools

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// Tool names. Stable strings: they appear in JSON schemas, transcripts and
// audit rows.
const (
	ToolListMarkets        = "listMarkets"
	ToolGetCandleWindow    = "getCandleWindow"
	ToolGetIndicators      = "getIndicators"
	ToolGetOrderBook       = "getOrderBookSummary"
	ToolGetFunding         = "getFunding"
	ToolGetAccountState    = "getAccountState"
	ToolGetStrategyHistory = "getStrategyHistory"
	ToolGetRiskBudget      = "getRiskBudget"
)

// Argument bounds from docs 05 §4.1, named so tests and executors share them.
const (
	MaxQueryChars      = 80
	MaxMarketItems     = 25
	MaxLookbackBars    = 200
	MinIndicatorBars   = 40
	MaxIndicatorBars   = 500
	MaxIndicatorSeries = 6
	MaxOrderBookDepth  = 100
	MaxFundingHistory  = 21
	MaxStrategyItems   = 20
	MaxMAPeriods       = 6
	MinMAPeriod        = 2
	MaxMAPeriod        = 500
)

// AllowedIntervals lists the candle and indicator intervals the tools accept.
var AllowedIntervals = []string{"1m", "5m", "15m", "30m", "1h", "4h", "1d"}

// AllowedIndicators lists the indicator names getIndicators accepts.
var AllowedIndicators = []string{"MACD", "SMA", "EMA", "RSI", "ATR", "BBANDS"}

var (
	// ErrArgumentsInvalid is the umbrella for every argument rejection; the
	// message names the exact rule that failed.
	ErrArgumentsInvalid = errors.New("llm tools: invalid arguments")
	// ErrUnknownTool is returned for names outside the catalogue.
	ErrUnknownTool = errors.New("llm tools: unknown tool")
	// ErrScopeDenied is returned when evidence points outside the run's
	// authorized scope.
	ErrScopeDenied = errors.New("llm tools: evidence outside the run scope")
)

// Specs returns the read-only tool specifications in a stable order.
func Specs() []openai.ToolSpec {
	return []openai.ToolSpec{
		spec(ToolListMarkets, "List tradable perpetual markets on the configured exchange. Optionally filter by a substring of the market id.", `{
	"type": "object",
	"properties": {
		"query": { "type": "string", "maxLength": 80, "description": "Substring filter over market ids, e.g. BTC" },
		"maxItems": { "type": "integer", "minimum": 1, "maximum": 25 }
	},
	"additionalProperties": false
}`),
		spec(ToolGetCandleWindow, "Read closed candles for one enabled perpetual market. Returns at most 50 closed candles as decimal strings. Candles are evidence, never a trade instruction.", `{
	"type": "object",
	"properties": {
		"marketId": { "type": "string", "description": "Exact market id from listMarkets, e.g. Binance:usdtmarginedfutures:BTC_USDT" },
		"interval": { "type": "string", "enum": ["1m","5m","15m","30m","1h","4h","1d"] },
		"lookbackBars": { "type": "integer", "minimum": 1, "maximum": 200 }
	},
	"required": ["marketId","interval","lookbackBars"],
	"additionalProperties": false
}`),
		spec(ToolGetIndicators, "Compute technical indicators over closed candles for one enabled perpetual market. Returns bounded series plus deterministic derived facts. Values are decimal strings. Indicators are evidence, never a trade instruction.", `{
	"type": "object",
	"properties": {
		"marketId": { "type": "string", "description": "Exact market id from listMarkets, e.g. Binance:usdtmarginedfutures:BTC_USDT" },
		"interval": { "type": "string", "enum": ["1m","5m","15m","30m","1h","4h","1d"] },
		"indicators": {
			"type": "array", "minItems": 1, "maxItems": 6,
			"items": { "type": "string", "enum": ["MACD","SMA","EMA","RSI","ATR","BBANDS"] }
		},
		"macdParams": {
			"type": "object",
			"properties": {
				"fast": { "type": "integer", "minimum": 2, "maximum": 100 },
				"slow": { "type": "integer", "minimum": 3, "maximum": 500 },
				"signal": { "type": "integer", "minimum": 2, "maximum": 100 }
			},
			"additionalProperties": false
		},
		"maPeriods": { "type": "array", "minItems": 1, "maxItems": 6, "items": { "type": "integer", "minimum": 2, "maximum": 500 } },
		"lookbackBars": { "type": "integer", "minimum": 40, "maximum": 500 }
	},
	"required": ["marketId","interval","indicators"],
	"additionalProperties": false
}`),
		spec(ToolGetOrderBook, "Summarise the order book of one enabled perpetual market: bounded levels per side plus a slippage summary for an optional notional.", `{
	"type": "object",
	"properties": {
		"marketId": { "type": "string" },
		"depthLevels": { "type": "integer", "minimum": 1, "maximum": 100 },
		"nominalUsd": { "type": "string", "description": "Optional decimal string, e.g. \"5000\"" }
	},
	"required": ["marketId","depthLevels"],
	"additionalProperties": false
}`),
		spec(ToolGetFunding, "Read the latest funding rate for one enabled perpetual market, optionally with bounded history.", `{
	"type": "object",
	"properties": {
		"marketId": { "type": "string" },
		"includeHistory": { "type": "boolean" },
		"historyLimit": { "type": "integer", "minimum": 1, "maximum": 21 }
	},
	"required": ["marketId"],
	"additionalProperties": false
}`),
		spec(ToolGetAccountState, "Read the configured account's open futures positions and balances.", `{
	"type": "object",
	"properties": {
		"accountId": { "type": "string" }
	},
	"required": ["accountId"],
	"additionalProperties": false
}`),
		spec(ToolGetStrategyHistory, "Read the recent decision history of one strategy version.", `{
	"type": "object",
	"properties": {
		"strategyVersionId": { "type": "string" },
		"maxItems": { "type": "integer", "minimum": 1, "maximum": 20 }
	},
	"required": ["strategyVersionId"],
	"additionalProperties": false
}`),
		spec(ToolGetRiskBudget, "Read the approved risk envelope and remaining budget placeholders of one strategy version.", `{
	"type": "object",
	"properties": {
		"strategyVersionId": { "type": "string" }
	},
	"required": ["strategyVersionId"],
	"additionalProperties": false
}`),
		screenMarketsSpec(),
		proposeActionSpec(),
		assessOpeningSpec(),
	}
}

func spec(name, description, parameters string) openai.ToolSpec {
	return openai.ToolSpec{
		Type: "function",
		Function: openai.ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(parameters),
		},
	}
}

// Names returns the tool names in the same order as Specs.
func Names() []string {
	specs := Specs()
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Function.Name)
	}
	return names
}

// decodeStrict decodes raw into out, rejecting unknown fields and trailing
// data -- a model that adds an unspecified field must get a rejection, not a
// silently ignored one.
func decodeStrict(raw string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrArgumentsInvalid, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: trailing data after the arguments object", ErrArgumentsInvalid)
	}
	return nil
}

// ListMarketsArgs bounds a listMarkets call.
type ListMarketsArgs struct {
	Query    string `json:"query"`
	MaxItems int    `json:"maxItems"`
}

// DecodeListMarkets validates and decodes listMarkets arguments.
func DecodeListMarkets(raw string) (*ListMarketsArgs, error) {
	args := &ListMarketsArgs{}
	if raw != "" {
		if err := decodeStrict(raw, args); err != nil {
			return nil, err
		}
	}
	if len(args.Query) > MaxQueryChars {
		return nil, fmt.Errorf("%w: query longer than %d characters", ErrArgumentsInvalid, MaxQueryChars)
	}
	if args.MaxItems != 0 && (args.MaxItems < 1 || args.MaxItems > MaxMarketItems) {
		return nil, fmt.Errorf("%w: maxItems must be within 1..%d", ErrArgumentsInvalid, MaxMarketItems)
	}
	if args.MaxItems == 0 {
		args.MaxItems = MaxMarketItems
	}
	return args, nil
}

// CandleWindowArgs bounds a getCandleWindow call.
type CandleWindowArgs struct {
	MarketID     string `json:"marketId"`
	Interval     string `json:"interval"`
	LookbackBars int    `json:"lookbackBars"`
}

// DecodeCandleWindow validates and decodes getCandleWindow arguments.
func DecodeCandleWindow(raw string) (*CandleWindowArgs, error) {
	args := &CandleWindowArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if err := validateMarketAndInterval(args.MarketID, args.Interval); err != nil {
		return nil, err
	}
	if args.LookbackBars < 1 || args.LookbackBars > MaxLookbackBars {
		return nil, fmt.Errorf("%w: lookbackBars must be within 1..%d", ErrArgumentsInvalid, MaxLookbackBars)
	}
	return args, nil
}

// MACDParams bounds MACD parameters.
type MACDParams struct {
	Fast   int `json:"fast"`
	Slow   int `json:"slow"`
	Signal int `json:"signal"`
}

// IndicatorsArgs bounds a getIndicators call.
type IndicatorsArgs struct {
	MarketID     string      `json:"marketId"`
	Interval     string      `json:"interval"`
	Indicators   []string    `json:"indicators"`
	MACDParams   *MACDParams `json:"macdParams"`
	MAPeriods    []int       `json:"maPeriods"`
	LookbackBars int         `json:"lookbackBars"`
}

// DecodeIndicators validates and decodes getIndicators arguments, including
// the cross-field rule that the lookback must cover the longest period asked
// for (docs 05 §4.5): a request for MA(200) with 40 bars is rejected so the
// model retries instead of silently receiving a shorter series.
func DecodeIndicators(raw string) (*IndicatorsArgs, error) {
	args := &IndicatorsArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if err := validateMarketAndInterval(args.MarketID, args.Interval); err != nil {
		return nil, err
	}
	if len(args.Indicators) < 1 || len(args.Indicators) > MaxIndicatorSeries {
		return nil, fmt.Errorf("%w: indicators must hold 1..%d entries", ErrArgumentsInvalid, MaxIndicatorSeries)
	}
	seen := make(map[string]struct{}, len(args.Indicators))
	for _, name := range args.Indicators {
		if !slices.Contains(AllowedIndicators, name) {
			return nil, fmt.Errorf("%w: unknown indicator %q", ErrArgumentsInvalid, name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: duplicate indicator %q", ErrArgumentsInvalid, name)
		}
		seen[name] = struct{}{}
	}
	requiredBars := 0
	if args.MACDParams != nil {
		p := args.MACDParams
		if p.Fast < 2 || p.Fast > 100 {
			return nil, fmt.Errorf("%w: macd fast must be within 2..100", ErrArgumentsInvalid)
		}
		if p.Slow < 3 || p.Slow > 500 {
			return nil, fmt.Errorf("%w: macd slow must be within 3..500", ErrArgumentsInvalid)
		}
		if p.Signal < 2 || p.Signal > 100 {
			return nil, fmt.Errorf("%w: macd signal must be within 2..100", ErrArgumentsInvalid)
		}
		if p.Fast >= p.Slow {
			return nil, fmt.Errorf("%w: macd fast (%d) must be smaller than slow (%d)", ErrArgumentsInvalid, p.Fast, p.Slow)
		}
		requiredBars = p.Slow + p.Signal - 2
	} else if _, wantsMACD := seen["MACD"]; wantsMACD {
		requiredBars = 26 + 9 - 2 // default 12/26/9
	}
	if len(args.MAPeriods) > MaxMAPeriods {
		return nil, fmt.Errorf("%w: maPeriods holds at most %d entries", ErrArgumentsInvalid, MaxMAPeriods)
	}
	for _, period := range args.MAPeriods {
		if period < MinMAPeriod || period > MaxMAPeriod {
			return nil, fmt.Errorf("%w: ma period %d outside %d..%d", ErrArgumentsInvalid, period, MinMAPeriod, MaxMAPeriod)
		}
		if period > requiredBars {
			requiredBars = period
		}
	}
	if args.LookbackBars < MinIndicatorBars || args.LookbackBars > MaxIndicatorBars {
		return nil, fmt.Errorf("%w: lookbackBars must be within %d..%d", ErrArgumentsInvalid, MinIndicatorBars, MaxIndicatorBars)
	}
	if requiredBars > args.LookbackBars {
		return nil, fmt.Errorf("%w: lookbackBars %d does not cover the requested periods (need >= %d)", ErrArgumentsInvalid, args.LookbackBars, requiredBars)
	}
	return args, nil
}

// OrderBookArgs bounds a getOrderBookSummary call.
type OrderBookArgs struct {
	MarketID    string `json:"marketId"`
	DepthLevels int    `json:"depthLevels"`
	NominalUSD  string `json:"nominalUsd"`
}

// DecodeOrderBook validates and decodes getOrderBookSummary arguments.
func DecodeOrderBook(raw string) (*OrderBookArgs, error) {
	args := &OrderBookArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if err := validateMarketAndInterval(args.MarketID, ""); err != nil {
		return nil, err
	}
	if args.DepthLevels < 1 || args.DepthLevels > MaxOrderBookDepth {
		return nil, fmt.Errorf("%w: depthLevels must be within 1..%d", ErrArgumentsInvalid, MaxOrderBookDepth)
	}
	if args.NominalUSD != "" && !isPositiveDecimal(args.NominalUSD) {
		return nil, fmt.Errorf("%w: nominalUsd must be a positive decimal string", ErrArgumentsInvalid)
	}
	return args, nil
}

// FundingArgs bounds a getFunding call.
type FundingArgs struct {
	MarketID       string `json:"marketId"`
	IncludeHistory bool   `json:"includeHistory"`
	HistoryLimit   int    `json:"historyLimit"`
}

// DecodeFunding validates and decodes getFunding arguments.
func DecodeFunding(raw string) (*FundingArgs, error) {
	args := &FundingArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if err := validateMarketAndInterval(args.MarketID, ""); err != nil {
		return nil, err
	}
	if args.IncludeHistory {
		if args.HistoryLimit < 1 || args.HistoryLimit > MaxFundingHistory {
			return nil, fmt.Errorf("%w: historyLimit must be within 1..%d when history is requested", ErrArgumentsInvalid, MaxFundingHistory)
		}
	} else if args.HistoryLimit != 0 {
		return nil, fmt.Errorf("%w: historyLimit given without includeHistory", ErrArgumentsInvalid)
	}
	return args, nil
}

// AccountStateArgs bounds a getAccountState call.
type AccountStateArgs struct {
	AccountID string `json:"accountId"`
}

// DecodeAccountState validates and decodes getAccountState arguments.
func DecodeAccountState(raw string) (*AccountStateArgs, error) {
	args := &AccountStateArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.AccountID) == "" {
		return nil, fmt.Errorf("%w: accountId is required", ErrArgumentsInvalid)
	}
	return args, nil
}

// StrategyHistoryArgs bounds a getStrategyHistory call.
type StrategyHistoryArgs struct {
	StrategyVersionID string `json:"strategyVersionId"`
	MaxItems          int    `json:"maxItems"`
}

// DecodeStrategyHistory validates and decodes getStrategyHistory arguments.
func DecodeStrategyHistory(raw string) (*StrategyHistoryArgs, error) {
	args := &StrategyHistoryArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.StrategyVersionID) == "" {
		return nil, fmt.Errorf("%w: strategyVersionId is required", ErrArgumentsInvalid)
	}
	if args.MaxItems != 0 && (args.MaxItems < 1 || args.MaxItems > MaxStrategyItems) {
		return nil, fmt.Errorf("%w: maxItems must be within 1..%d", ErrArgumentsInvalid, MaxStrategyItems)
	}
	if args.MaxItems == 0 {
		args.MaxItems = MaxStrategyItems
	}
	return args, nil
}

// RiskBudgetArgs bounds a getRiskBudget call.
type RiskBudgetArgs struct {
	StrategyVersionID string `json:"strategyVersionId"`
}

// DecodeRiskBudget validates and decodes getRiskBudget arguments.
func DecodeRiskBudget(raw string) (*RiskBudgetArgs, error) {
	args := &RiskBudgetArgs{}
	if err := decodeStrict(raw, args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.StrategyVersionID) == "" {
		return nil, fmt.Errorf("%w: strategyVersionId is required", ErrArgumentsInvalid)
	}
	return args, nil
}

func validateMarketAndInterval(marketID, interval string) error {
	if strings.TrimSpace(marketID) == "" {
		return fmt.Errorf("%w: marketId is required", ErrArgumentsInvalid)
	}
	if interval != "" && !slices.Contains(AllowedIntervals, interval) {
		return fmt.Errorf("%w: interval %q outside the allowed set", ErrArgumentsInvalid, interval)
	}
	return nil
}

func isPositiveDecimal(s string) bool {
	integer, fraction, hasFraction := strings.Cut(s, ".")
	if integer == "" || (integer != "0" && strings.HasPrefix(integer, "0")) {
		return false
	}
	nonzero := false
	for _, r := range integer {
		if r < '0' || r > '9' {
			return false
		}
		if r != '0' {
			nonzero = true
		}
	}
	if hasFraction {
		if fraction == "" {
			return false
		}
		for _, r := range fraction {
			if r < '0' || r > '9' {
				return false
			}
			if r != '0' {
				nonzero = true
			}
		}
	}
	return nonzero
}

// Scope is the run's authorization boundary. Closed by default: an id is
// allowed only when it is listed, so evidence built for another market,
// account or strategy cannot leak into this run (docs 05 §4.3 作用域闸).
type Scope struct {
	Markets          map[string]struct{}
	Accounts         map[string]struct{}
	StrategyVersions map[string]struct{}
}

// NewScope builds a scope from the run's authorized ids.
func NewScope(markets, accounts, strategyVersions []string) *Scope {
	s := &Scope{
		Markets:          make(map[string]struct{}, len(markets)),
		Accounts:         make(map[string]struct{}, len(accounts)),
		StrategyVersions: make(map[string]struct{}, len(strategyVersions)),
	}
	for _, id := range markets {
		s.Markets[id] = struct{}{}
	}
	for _, id := range accounts {
		s.Accounts[id] = struct{}{}
	}
	for _, id := range strategyVersions {
		s.StrategyVersions[id] = struct{}{}
	}
	return s
}

// Allows reports whether evidence naming these ids lies inside the scope. A
// nil id is fine only when the run never authorized that dimension: evidence
// that names nothing is allowed; evidence that names an unauthorized id is not.
func (s *Scope) Allows(marketID, accountID, strategyVersionID *string) error {
	if s == nil {
		return fmt.Errorf("%w: no scope", ErrScopeDenied)
	}
	if marketID != nil {
		if _, ok := s.Markets[*marketID]; !ok {
			return fmt.Errorf("%w: market %q", ErrScopeDenied, *marketID)
		}
	}
	if accountID != nil {
		if _, ok := s.Accounts[*accountID]; !ok {
			return fmt.Errorf("%w: account %q", ErrScopeDenied, *accountID)
		}
	}
	if strategyVersionID != nil {
		if _, ok := s.StrategyVersions[*strategyVersionID]; !ok {
			return fmt.Errorf("%w: strategy version %q", ErrScopeDenied, *strategyVersionID)
		}
	}
	return nil
}

// CheckEvidence applies the scope to a piece of tool evidence.
func (s *Scope) CheckEvidence(ev *candidate.ToolEvidence) error {
	if ev == nil {
		return fmt.Errorf("%w: nil evidence", ErrScopeDenied)
	}
	return s.Allows(ev.MarketID, ev.AccountID, ev.StrategyVersionID)
}
