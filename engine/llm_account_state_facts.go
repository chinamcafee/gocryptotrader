package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M4-11 装配件 · 仓储侧账户态 → `RiskAccountFactsInput` 材料（闭包装配的前件）。
//
// 能读的读、读不到的如实申报缺席——三件各有其语义：
//
//   - `DayStartEquityUSD`：当日（UTC 日界）已封的日界基线。**未封 ≠ 出错**：
//     基线缺席是法律状态（当天还没盖章），报 `nil` 交给检查按
//     `RISK_DAILY_LOSS_UNKNOWN` 拒——在本层发明一个 0 才是伪造；
//   - `UnresolvedUnknownOrders`：`UNKNOWN` 状态意图的未解决计数。intents
//     表没有账户列——与 E 组频率计数同一诚实口径：全表即单账户部署下的
//     账户级近似；
//   - `ExternalFlowsUSD` 不在此件：`ListExternalFlows` 的净额加减属日损
//     公式内部（`DayStartService.DailyLoss` 既已实现），装配侧若再算一遍
//     就是第二份拼写——保持显式输入。
func buildRiskAccountStateFacts(ctx context.Context, accountID string, now time.Time) (*riskAccountState, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("%w: the account is required for its state facts", ErrRiskFactsInput)
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: the account state needs an as-of instant", ErrRiskFactsInput)
	}
	state := &riskAccountState{}
	baseline, err := llmstore.GetDayStartInventory(ctx, accountID, UtcDayKey(now))
	switch {
	case errors.Is(err, llmstore.ErrNotFound):
		// Not sealed yet: absence is a state, not an error.
	case err != nil:
		return nil, fmt.Errorf("llm account state: reading the day-start baseline: %w", err)
	case baseline != nil:
		equity := baseline.EquityUsdt
		state.DayStartEquityUSD = &equity
	}
	unknowns, err := llmstore.CountOrderIntentsByStatus(ctx, IntentStatusUnknown)
	if err != nil {
		return nil, fmt.Errorf("llm account state: reading unresolved UNKNOWN intents: %w", err)
	}
	state.UnresolvedUnknownOrders = int(unknowns)
	partials, err := llmstore.CountOrderIntentsByStatus(ctx, IntentStatusPartiallyFilled)
	if err != nil {
		return nil, fmt.Errorf("llm account state: reading unreconciled partials: %w", err)
	}
	state.UnreconciledPartials = int(partials)
	return state, nil
}

// riskAccountState is the repo-backed slice of `RiskAccountFactsInput`; the
// closure merges it with the run's caller/venue material.
type riskAccountState struct {
	// DayStartEquityUSD is nil until the day's baseline is sealed.
	DayStartEquityUSD *string
	// UnresolvedUnknownOrders counts UNKNOWN intents (table-wide: the intents
	// table carries no account column).
	UnresolvedUnknownOrders int
	// UnreconciledPartials counts intents whose partial fill has not settled
	// (PARTIALLY_FILLED -- the state's own meaning; same table-wide scope).
	UnreconciledPartials int
}
