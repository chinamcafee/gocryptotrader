// Package screener evaluates deterministic universe-wide market screens: an
// operator (or the chat model) states criteria over a metric catalogue, and
// the evaluator filters, ranks and truncates the universe without any model
// involvement -- the model only picks the criteria (docs/llm-dev/13 §3.1).
//
// Nothing here touches the network: the caller assembles one Market per
// covered symbol from its caches (and, for T2 metrics, from the venue), and
// this package computes, filters and ranks. That split keeps the evaluation
// pure and testable, and keeps the weight budget in the caller's hands.
package screener

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Tier reports which data source a metric reads: cached ticker, cached klines
// or a venue read taken for this scan. The tier travels with every result row
// so a reader can judge freshness and cost.
type Tier string

// Metric tiers.
const (
	// TierTicker reads the cached 24h ticker (zero exchange weight).
	TierTicker Tier = "TICKER_CACHED"
	// TierCache reads the cached closed candles (zero exchange weight).
	TierCache Tier = "KLINES_CACHED"
	// TierVenue needs a per-symbol venue read taken for this scan.
	TierVenue Tier = "VENUE_READ"
)

// MetricKind is how a metric compares: numeric thresholds or a category.
type MetricKind string

// Metric kinds.
const (
	// KindNumeric compares with gt/gte/lt/lte/between.
	KindNumeric MetricKind = "NUMERIC"
	// KindCategory compares with eq against one of Categories.
	KindCategory MetricKind = "CATEGORY"
)

// MaxLimit bounds one screen's row count.
const MaxLimit = 50

// DefaultLimit is the row count when the caller did not choose one.
const DefaultLimit = 20

// FlowFacts is the venue taker-flow reading for one market (T2). Volumes are
// reported as the venue returns them; the ratio forms are the unit-safe ones.
type FlowFacts struct {
	BuyVol  float64
	SellVol float64
}

// Market is one market's screenable facts, assembled by the caller.
type Market struct {
	// MarketID is the run vocabulary (<exchange>:<asset>:<BASE>_<QUOTE>).
	MarketID string
	// Last / Change24h / QuoteVolume24h ride the cached ticker.
	Last           float64
	HasLast        bool
	Change24h      float64
	HasChange24h   bool
	QuoteVolume24h float64
	HasVolume24h   bool
	// Candles are the closed candles for the run interval, oldest first.
	Candles []Candle
	// Flow is present only when the caller read the venue for this market.
	Flow *FlowFacts
}

// Candle is the minimal closed-candle shape the screener computes over.
type Candle struct {
	Open, High, Low, Close, Volume float64
}

// Criteria is one filter clause: metric + operator + value(s).
type Criteria struct {
	Metric string   `json:"metric"`
	Op     string   `json:"op"`
	Value  *float64 `json:"value,omitempty"`
	Value2 *float64 `json:"value2,omitempty"`
	// State is the category name for KindCategory metrics.
	State string `json:"state,omitempty"`
}

// SortKey orders the matched rows; the first key is the primary order.
type SortKey struct {
	Metric string `json:"metric"`
	Desc   bool   `json:"desc"`
}

// Request is one screen.
type Request struct {
	Criteria []Criteria `json:"criteria,omitempty"`
	Sort     []SortKey  `json:"sort,omitempty"`
	Limit    int        `json:"limit,omitempty"`
	// Include lists extra numeric/category metrics reported on every row
	// regardless of criteria/sort use.
	Include []string `json:"include,omitempty"`
}

// Row is one matched market's reported values.
type Row struct {
	MarketID string              `json:"marketId"`
	Numbers  map[string]*float64 `json:"numbers"`
	States   map[string]string   `json:"states,omitempty"`
}

// Meta reports what the scan saw, including the honest exclusion counts: a
// market can fall out because a metric was absent (missing data) or because
// it simply did not pass a criterion. The two are counted apart, never
// conflated.
type Meta struct {
	UniverseSize       int      `json:"universeSize"`
	Matched            int      `json:"matched"`
	FilteredOut        int      `json:"filteredOut"`
	ExcludedForMissing int      `json:"excludedForMissing"`
	Notes              []string `json:"notes,omitempty"`
}

// Result is a screen's answer.
type Result struct {
	Rows []Row `json:"rows"`
	Meta Meta  `json:"meta"`
}

// Evaluate filters, ranks and truncates the universe per the request.
//
// Missing values never silently pass or silently zero-fill: a market whose
// criterion metric cannot be computed is excluded and counted; a market whose
// sort metric is missing is excluded and counted too. Absence is reported,
// never disguised.
func Evaluate(req *Request, markets []*Market) (*Result, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: a screen request is required", ErrInvalid)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	referenced := map[string]bool{}
	for i := range req.Criteria {
		c := &req.Criteria[i]
		metric, ok := Lookup(c.Metric)
		if !ok {
			return nil, fmt.Errorf("%w: unknown metric %q", ErrInvalid, c.Metric)
		}
		if err := validateCriteria(metric, c); err != nil {
			return nil, err
		}
		referenced[metric.Name] = true
	}
	for i := range req.Sort {
		metric, ok := Lookup(req.Sort[i].Metric)
		if !ok {
			return nil, fmt.Errorf("%w: unknown sort metric %q", ErrInvalid, req.Sort[i].Metric)
		}
		if metric.Kind != KindNumeric {
			return nil, fmt.Errorf("%w: sort metric %q is not numeric", ErrInvalid, metric.Name)
		}
		referenced[metric.Name] = true
	}
	for _, name := range req.Include {
		metric, ok := Lookup(name)
		if !ok {
			return nil, fmt.Errorf("%w: unknown include metric %q", ErrInvalid, name)
		}
		referenced[metric.Name] = true
	}
	// Identity context travels on every row.
	referenced["change24h"] = true
	referenced["quoteVolume24h"] = true

	computed := make(map[string]map[string]computedValue, len(markets))
	matched := make([]*Market, 0, len(markets))
	filteredOut, missingOut := 0, 0
	for _, market := range markets {
		if market == nil {
			continue
		}
		values := make(map[string]computedValue, len(referenced))
		for name := range referenced {
			metric, _ := Lookup(name)
			values[name] = metric.compute(market)
		}
		computed[market.MarketID] = values
		pass, missing := evaluateCriteria(req.Criteria, values)
		if missing {
			missingOut++
			continue
		}
		if !pass {
			filteredOut++
			continue
		}
		if !hasAllSortKeys(req.Sort, values) {
			missingOut++
			continue
		}
		matched = append(matched, market)
	}
	sortRows(req.Sort, matched, computed)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	rows := make([]Row, 0, len(matched))
	for _, market := range matched {
		row := Row{MarketID: market.MarketID, Numbers: make(map[string]*float64, len(referenced))}
		for name := range referenced {
			value := computed[market.MarketID][name]
			metric, _ := Lookup(name)
			if metric.Kind == KindCategory {
				if value.ok {
					if row.States == nil {
						row.States = map[string]string{}
					}
					row.States[name] = value.state
				}
				continue
			}
			if !value.ok {
				row.Numbers[name] = nil // reported as absent, never zero-filled
				continue
			}
			number := value.number
			row.Numbers[name] = &number
		}
		rows = append(rows, row)
	}
	meta := Meta{
		UniverseSize:       len(markets),
		Matched:            len(rows),
		FilteredOut:        filteredOut,
		ExcludedForMissing: missingOut,
	}
	return &Result{Rows: rows, Meta: meta}, nil
}

// validateCriteria checks one clause against its metric's kind.
func validateCriteria(metric *Metric, c *Criteria) error {
	op := strings.ToLower(strings.TrimSpace(c.Op))
	switch metric.Kind {
	case KindCategory:
		if op != "eq" {
			return fmt.Errorf("%w: metric %q is categorical; use eq", ErrInvalid, metric.Name)
		}
		if !slices.Contains(metric.Categories, strings.ToUpper(strings.TrimSpace(c.State))) {
			return fmt.Errorf("%w: %q is not a category of %q (one of %s)", ErrInvalid, c.State, metric.Name, strings.Join(metric.Categories, ", "))
		}
		return nil
	case KindNumeric:
		if c.Value == nil {
			return fmt.Errorf("%w: metric %q needs a value", ErrInvalid, metric.Name)
		}
		switch op {
		case "gt", "gte", "lt", "lte":
			return nil
		case "between":
			if c.Value2 == nil {
				return fmt.Errorf("%w: between on %q needs value2", ErrInvalid, metric.Name)
			}
			return nil
		default:
			return fmt.Errorf("%w: operator %q is not one of gt, gte, lt, lte, between", ErrInvalid, c.Op)
		}
	}
	return fmt.Errorf("%w: unknown metric kind %q", ErrInvalid, metric.Kind)
}

// evaluateCriteria checks every clause for one market's computed values: pass
// says all clauses hold, missing says at least one referenced metric was
// absent (which excludes the market from passing, honestly counted apart).
func evaluateCriteria(criteria []Criteria, values map[string]computedValue) (pass, missing bool) {
	for i := range criteria {
		c := &criteria[i]
		metric, _ := Lookup(c.Metric)
		value := values[metric.Name]
		if !value.ok {
			return false, true
		}
		if metric.Kind == KindCategory {
			if value.state != strings.ToUpper(strings.TrimSpace(c.State)) {
				return false, false
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(c.Op)) {
		case "gt":
			if !(value.number > *c.Value) {
				return false, false
			}
		case "gte":
			if value.number < *c.Value {
				return false, false
			}
		case "lt":
			if !(value.number < *c.Value) {
				return false, false
			}
		case "lte":
			if value.number > *c.Value {
				return false, false
			}
		case "between":
			low, high := *c.Value, *c.Value2
			if low > high {
				low, high = high, low
			}
			if value.number < low || value.number > high {
				return false, false
			}
		}
	}
	return true, false
}

// hasAllSortKeys reports whether every sort metric is present for the market.
func hasAllSortKeys(keys []SortKey, values map[string]computedValue) bool {
	for i := range keys {
		metric, _ := Lookup(keys[i].Metric)
		if !values[metric.Name].ok {
			return false
		}
	}
	return true
}

// sortRows ranks the matched markets by the sort keys (stable for ties).
func sortRows(keys []SortKey, markets []*Market, computed map[string]map[string]computedValue) {
	if len(keys) == 0 {
		return
	}
	sort.SliceStable(markets, func(i, j int) bool {
		a, b := markets[i], markets[j]
		for k := range keys {
			key := keys[k]
			metric, _ := Lookup(key.Metric)
			left := computed[a.MarketID][metric.Name].number
			right := computed[b.MarketID][metric.Name].number
			if left == right {
				continue
			}
			if key.Desc {
				return left > right
			}
			return left < right
		}
		return a.MarketID < b.MarketID
	})
}
