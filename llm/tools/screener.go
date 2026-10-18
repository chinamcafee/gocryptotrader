package tools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/llm/screener"
)

// ToolScreenMarkets filters the covered perpetual universe by metric criteria
// and ranks the matches -- the "sub-agent collects every indicator" capability
// as one deterministic read-only tool (docs/llm-dev/13 §3.1).
const ToolScreenMarkets = "screenMarkets"

// JSON schema keys repeated across the spec builder.
const (
	schemaKeyType        = "type"
	schemaKeyDescription = "description"
)

// Screen bounds: one screen stays small and bounded.
const (
	// MaxScreenCriteria bounds the AND-joined criteria list.
	MaxScreenCriteria = 8
	// MaxScreenSort bounds the ranking keys.
	MaxScreenSort = 3
	// MaxScreenInclude bounds the extra reported metrics. Twelve is the whole
	// catalogue: "详细指标" requests naturally ask for all of them, and the
	// result still rides the evidence byte budget (tail trimming states itself).
	MaxScreenInclude = 12
)

// ScreenMarketsArgs is a decoded screenMarkets call. Metric names, operators
// and categories are validated by the screener's catalogue; this layer owns
// only the shape and the counts.
type ScreenMarketsArgs struct {
	Criteria []screener.Criteria `json:"criteria,omitempty"`
	Sort     []screener.SortKey  `json:"sort,omitempty"`
	Limit    int                 `json:"limit,omitempty"`
	Include  []string            `json:"include,omitempty"`
}

// DecodeScreenMarkets decodes and bounds one screenMarkets call.
func DecodeScreenMarkets(raw string) (*ScreenMarketsArgs, error) {
	args := &ScreenMarketsArgs{}
	if raw != "" {
		if err := decodeStrict(raw, args); err != nil {
			return nil, err
		}
	}
	if len(args.Criteria) > MaxScreenCriteria {
		return nil, fmt.Errorf("%w: at most %d criteria per screen", ErrArgumentsInvalid, MaxScreenCriteria)
	}
	if len(args.Sort) > MaxScreenSort {
		return nil, fmt.Errorf("%w: at most %d sort keys per screen", ErrArgumentsInvalid, MaxScreenSort)
	}
	if len(args.Include) > MaxScreenInclude {
		return nil, fmt.Errorf("%w: at most %d include metrics per screen", ErrArgumentsInvalid, MaxScreenInclude)
	}
	if args.Limit != 0 && (args.Limit < 1 || args.Limit > screener.MaxLimit) {
		return nil, fmt.Errorf("%w: limit must be within 1..%d", ErrArgumentsInvalid, screener.MaxLimit)
	}
	return args, nil
}

// screenMarketsSpec builds the tool specification from the screener's metric
// catalogue, so the prompt's metric list and the evaluator cannot drift apart.
func screenMarketsSpec() openai.ToolSpec {
	catalogue := screener.Catalogue()
	names := make([]string, 0, len(catalogue))
	categories := map[string][]string{}
	lines := make([]string, 0, len(catalogue))
	for i := range catalogue {
		info := catalogue[i]
		names = append(names, info.Name)
		if len(info.Categories) > 0 {
			categories[info.Name] = info.Categories
		}
		unit := info.Unit
		if unit == "" {
			unit = "enum"
		}
		lines = append(lines, fmt.Sprintf("%s (%s, %s, %s): %s", info.Name, strings.ToLower(string(info.Kind)), unit, strings.ToLower(string(info.Tier)), info.Description))
	}
	parameters, err := json.Marshal(map[string]any{
		schemaKeyType: "object",
		"properties": map[string]any{
			"criteria": map[string]any{
				"type":               "array",
				"maxItems":           MaxScreenCriteria,
				schemaKeyDescription: "AND-joined filters over the metric catalogue",
				"items": map[string]any{
					schemaKeyType: "object",
					"properties": map[string]any{
						"metric": map[string]any{schemaKeyType: "string", "enum": names},
						"op":     map[string]any{schemaKeyType: "string", "enum": []string{"gt", "gte", "lt", "lte", "between", "eq"}},
						"value":  map[string]any{schemaKeyType: "number"},
						"value2": map[string]any{schemaKeyType: "number", schemaKeyDescription: "upper bound for between"},
						"state":  map[string]any{schemaKeyType: "string", schemaKeyDescription: "category name for kind=category metrics; see the catalogue in the description"},
					},
					"required":             []string{"metric", "op"},
					"additionalProperties": false,
				},
			},
			"sort": map[string]any{
				schemaKeyType: "array", "maxItems": MaxScreenSort,
				schemaKeyDescription: "ranking keys over numeric metrics; the first key is primary",
				"items": map[string]any{
					schemaKeyType: "object",
					"properties": map[string]any{
						"metric": map[string]any{schemaKeyType: "string", "enum": names},
						"desc":   map[string]any{schemaKeyType: "boolean"},
					},
					"required":             []string{"metric"},
					"additionalProperties": false,
				},
			},
			"limit": map[string]any{schemaKeyType: "integer", "minimum": 1, "maximum": screener.MaxLimit, schemaKeyDescription: "row count, default 20"},
			"include": map[string]any{
				schemaKeyType: "array", "maxItems": MaxScreenInclude, "items": map[string]any{schemaKeyType: "string", "enum": names},
				schemaKeyDescription: "extra metrics reported on every row",
			},
		},
		"additionalProperties": false,
	})
	if err != nil {
		// The catalogue is program data; a marshal failure is a build defect.
		panic(fmt.Sprintf("tools: screenMarkets schema: %v", err))
	}
	description := "Screen the entire covered perpetual universe by metric criteria (AND-joined) and rank the matches. " +
		"Use when the operator asks to find, filter or scan markets by indicator conditions -- " +
		"for example top movers over 24h/48h/4h, net inflow or outflow, MACD momentum or crosses. " +
		"Metrics are computed deterministically over cached tickers and closed candles; " +
		"metrics whose data is missing are reported as absent, never zero-filled. Catalogue: " +
		strings.Join(lines, "; ") + ". Category metrics carry these states: " + categorySummary(categories) + "."
	return spec(ToolScreenMarkets, description, string(parameters))
}

// categorySummary renders the categorical enums for the tool description.
func categorySummary(categories map[string][]string) string {
	if len(categories) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(categories))
	for name, values := range categories {
		parts = append(parts, name+"="+strings.Join(values, "|"))
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}
