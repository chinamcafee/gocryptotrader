package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/screener"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// LLMGetScreenerMetrics exposes the metric catalogue the GUI builds its
// criteria form from (docs 13 §3.4): one source of truth for what the
// evaluator can compute -- the form can never offer a metric the backend
// does not implement.
func (s *RPCServer) LLMGetScreenerMetrics(_ context.Context, _ *gctrpc.LLMGetScreenerMetricsRequest) (*gctrpc.LLMGetScreenerMetricsResponse, error) {
	out := &gctrpc.LLMGetScreenerMetricsResponse{
		MaxLimit:     int32(screener.MaxLimit),
		DefaultLimit: int32(screener.DefaultLimit),
	}
	for _, info := range screener.Catalogue() {
		out.Metrics = append(out.Metrics, &gctrpc.LLMScreenerMetric{
			Name:        info.Name,
			Kind:        string(info.Kind),
			Tier:        string(info.Tier),
			Unit:        info.Unit,
			Description: info.Description,
			Categories:  info.Categories,
		})
	}
	return out, nil
}

// LLMRunScreen runs one GUI screen: the same evaluator the chat tool drives,
// exposed for the workbench page (docs 13 §3.4). Read-only; the criteria are
// validated by the screener's own catalogue.
func (s *RPCServer) LLMRunScreen(ctx context.Context, r *gctrpc.LLMRunScreenRequest) (*gctrpc.LLMRunScreenResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMDecisionRunner
	}
	runner := s.Engine.currentDecisionRunner()
	if runner == nil || runner.Analysis == nil {
		return nil, errLLMDecisionRunner
	}
	// The pipeline's Tools seam is an interface; the screener rides the
	// concrete production runner (tests and alternate runners simply fail
	// the assertion and the RPC refuses closed).
	screenerRunner, ok := runner.Analysis.Tools.(*LLMToolRunner)
	if !ok || screenerRunner == nil {
		return nil, errLLMDecisionRunner
	}
	args := &tools.ScreenMarketsArgs{Limit: int(r.GetLimit()), Include: append([]string(nil), r.GetInclude()...)}
	for _, c := range r.GetCriteria() {
		criteria := screener.Criteria{Metric: c.GetMetric(), Op: c.GetOp(), State: c.GetState()}
		if op := strings.ToLower(strings.TrimSpace(criteria.Op)); op == "gt" || op == "gte" || op == "lt" || op == "lte" || op == "between" {
			value := c.GetValue()
			criteria.Value = &value
		}
		if op := strings.ToLower(strings.TrimSpace(criteria.Op)); op == "between" {
			value2 := c.GetValue2()
			criteria.Value2 = &value2
		}
		args.Criteria = append(args.Criteria, criteria)
	}
	for _, k := range r.GetSort() {
		args.Sort = append(args.Sort, screener.SortKey{Metric: k.GetMetric(), Desc: k.GetDesc()})
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("llm screener: arguments: %w", err)
	}
	payload, _, err := screenerRunner.RunScreenMarkets(ctx, string(raw))
	if err != nil {
		return nil, err
	}
	mapped, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("llm screener: unexpected payload shape")
	}
	out := &gctrpc.LLMRunScreenResponse{}
	rows, _ := mapped["rows"].([]screener.Row)
	for i := range rows {
		numbersJSON, err := json.Marshal(rows[i].Numbers)
		if err != nil {
			return nil, fmt.Errorf("llm screener: row numbers: %w", err)
		}
		statesJSON, err := json.Marshal(rows[i].States)
		if err != nil {
			return nil, fmt.Errorf("llm screener: row states: %w", err)
		}
		out.Rows = append(out.Rows, &gctrpc.LLMScreenRow{
			MarketId: rows[i].MarketID, NumbersJson: string(numbersJSON), StatesJson: string(statesJSON),
		})
	}
	if meta, ok := mapped["meta"].(screener.Meta); ok {
		out.UniverseSize = int32(meta.UniverseSize)             //nolint:gosec // bounded by the covered universe
		out.Matched = int32(meta.Matched)                       //nolint:gosec // bounded by the row limit
		out.FilteredOut = int32(meta.FilteredOut)               //nolint:gosec // bounded by the covered universe
		out.ExcludedForMissing = int32(meta.ExcludedForMissing) //nolint:gosec // bounded by the covered universe
	}
	if notes, ok := mapped["notes"].([]string); ok {
		out.Notes = notes
	}
	return out, nil
}
