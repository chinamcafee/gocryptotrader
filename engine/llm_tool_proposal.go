package engine

import (
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// 生产装配 · 对话配置提案执行器（docs/llm-dev/16）：proposeAction 只做参数校验、
// 归一化与作用域核对，然后作为证据数据返回——它从不触碰任何写路径。真正的执行
// 发生在操作者确认之后，由前端调用与手动页面完全相同的治理 RPC。
func (r *LLMToolRunner) runProposeAction(raw string) (any, time.Time, error) {
	proposal, err := tools.DecodeProposeAction(raw)
	if err != nil {
		return nil, time.Time{}, err
	}
	if r.Scope == nil {
		return nil, time.Time{}, fmt.Errorf("%w: proposals need the run scope", tools.ErrArgumentsInvalid)
	}
	switch proposal.Action {
	case candidate.ActionStartOpenDecisionBatch:
		markets, marketsErr := r.proposalMarkets(proposal.MarketIDs)
		if marketsErr != nil {
			return nil, time.Time{}, marketsErr
		}
		proposal.MarketIDs = markets
		if versionErr := r.proposalStrategyVersion(proposal.StrategyVersionID); versionErr != nil {
			return nil, time.Time{}, versionErr
		}
	case candidate.ActionStartPositionReview:
		if len(proposal.MarketIDs) > 0 {
			markets, marketsErr := r.proposalMarkets(proposal.MarketIDs)
			if marketsErr != nil {
				return nil, time.Time{}, marketsErr
			}
			proposal.MarketIDs = markets
		}
		if versionErr := r.proposalStrategyVersion(proposal.StrategyVersionID); versionErr != nil {
			return nil, time.Time{}, versionErr
		}
	case candidate.ActionSetMarketTracked:
		// The target may be a market that is not tracked yet, so scope
		// membership cannot be required here; the execution endpoint checks
		// the market against the exchange's available pairs.
		normalized, normalizeErr := runMarketID(r.Exchange, r.Asset, proposal.MarketID)
		if normalizeErr != nil {
			return nil, time.Time{}, fmt.Errorf("%w: marketId: %v", tools.ErrArgumentsInvalid, normalizeErr)
		}
		proposal.MarketID = normalized
	case candidate.ActionCancelBatchJob:
		// The job id is bounded by the decoder; existence is checked by the
		// cancel endpoint at execution time.
	}
	if proposal.Actor == "" {
		proposal.Actor = "webui-operator"
	}
	return proposal, r.now().UTC(), nil
}

// proposalMarkets normalises the proposal's markets to the run vocabulary and
// requires each one to sit inside the run scope.
func (r *LLMToolRunner) proposalMarkets(markets []string) ([]string, error) {
	normalized := make([]string, 0, len(markets))
	for _, market := range markets {
		key, err := runMarketID(r.Exchange, r.Asset, market)
		if err != nil {
			return nil, fmt.Errorf("%w: market %q: %v", tools.ErrArgumentsInvalid, market, err)
		}
		if _, ok := r.Scope.Markets[key]; !ok {
			return nil, fmt.Errorf("%w: market %q is outside the run scope", tools.ErrArgumentsInvalid, key)
		}
		normalized = append(normalized, key)
	}
	return normalized, nil
}

// proposalStrategyVersion requires the version to be one the run may name.
func (r *LLMToolRunner) proposalStrategyVersion(strategyVersionID string) error {
	if _, ok := r.Scope.StrategyVersions[strategyVersionID]; !ok {
		return fmt.Errorf("%w: strategy version %q is outside the run scope", tools.ErrArgumentsInvalid, strategyVersionID)
	}
	return nil
}
