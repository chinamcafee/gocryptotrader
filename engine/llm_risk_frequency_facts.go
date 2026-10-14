package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-11 装配件 · 引擎态 → 硬风控 E 组（频率）映射。
//
// E 组的检查全部建立在**持久化的 UTC 滑动窗口计数**上（07 §5.2 E：
// 「所有窗口用 UTC 滑动窗口，计数持久化到数据库（不是内存，重启不能丢）」），
// 所以这一组不像 A/B/C/D/G 那样是纯映射——四个计数在这里**就地读库**
// （仓库层聚合查询），其余显式输入、禁发明：
//
//   - `NewRiskIntentsInWindow`：`llm_new_risk_counters` 的**未释放**计数
//     （M1-11 预算闸落的账；撤单释放后不再占窗口）；
//   - `VersionWindowCount`/`AllOrdersInWindow`/`ReduceOrdersInWindow`：
//     `llm_order_intents` 按窗口计数。intents 表没有账户列，所以「全账户」
//     口径取**全表**——单账户部署下二者同集，这是诚实口径里最大的一档；
//   - `MaxVersionWindow`/`MaxOrdersPerWindow`/`MaxReduceOrdersPerWindow`：
//     没有配置源——显式输入。检查里 `<=0` 就是「无闸」，0 照传（不是发明，
//     是既有语义）；负数读不可信，拒；
//   - `MarketCooldownElapsed`：冷却期的「上次开仓 + 冷却时长」两个来源
//     都不存在——显式输入；
//   - `ConsecutiveLosses`/`MaxConsecutiveLosses`：成交结果账没有读面——
//     显式输入（检查对 `<=0` 的上限回退 `RiskMaxConsecutiveLosses`）。
//
// **本条候选订单必被计入**（除非检查在装订处被跳过——REDUCE 不跑
// newRiskOnly 行）：检查是 `count <= max` 判过，而候选行尚未落库；若计数
// 不含候选，放行的将是第 max+1 条。所以映射把「候选若成单」折进计数：
// 任何订单都进全部计数与版本计数；新风险订单再进新风险计数；REDUCE
// 再进平仓通道计数（07 §5.2：「平仓不消耗新增风险预算，但有独立的
// 防风暴限额」）。
func riskFrequencyFactsFromState(ctx context.Context, in *RiskFrequencyFactsInput) (*RiskFrequencyFacts, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: the frequency material is required", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.AccountID) == "" {
		return nil, fmt.Errorf("%w: the account is required for the new-risk window", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.StrategyVersionID) == "" {
		return nil, fmt.Errorf("%w: the strategy version is required for the version window", ErrRiskFactsInput)
	}
	if in.Now.IsZero() {
		return nil, fmt.Errorf("%w: the rolling window needs an as-of instant", ErrRiskFactsInput)
	}
	if in.WindowMinutes <= 0 {
		return nil, fmt.Errorf("%w: the window length must be positive", ErrRiskFactsInput)
	}
	newRiskOrder := false
	switch in.Action {
	case candidate.ActionOpenLong, candidate.ActionOpenShort:
		newRiskOrder = true
	case candidate.ActionReduce:
	default:
		return nil, fmt.Errorf("%w: action %q has no frequency semantics", ErrRiskFactsInput, in.Action)
	}
	if in.MaxVersionWindow < 0 || in.MaxOrdersPerWindow < 0 || in.MaxReduceOrdersPerWindow < 0 {
		return nil, fmt.Errorf("%w: a negative window limit is unreadable", ErrRiskFactsInput)
	}
	if in.ConsecutiveLosses < 0 || in.MaxConsecutiveLosses < 0 {
		return nil, fmt.Errorf("%w: a negative loss count is unreadable", ErrRiskFactsInput)
	}

	since := in.Now.Add(-time.Duration(in.WindowMinutes) * time.Minute)
	newRiskCount, err := llmstore.CountNewRiskInWindow(ctx, in.AccountID, since)
	if err != nil {
		return nil, fmt.Errorf("llm frequency facts: counting new-risk intents: %w", err)
	}
	versionCount, err := llmstore.CountOrderIntentsInWindow(ctx, in.StrategyVersionID, "", since)
	if err != nil {
		return nil, fmt.Errorf("llm frequency facts: counting the version's intents: %w", err)
	}
	allCount, err := llmstore.CountOrderIntentsInWindow(ctx, "", "", since)
	if err != nil {
		return nil, fmt.Errorf("llm frequency facts: counting all intents: %w", err)
	}
	reduceCount, err := llmstore.CountOrderIntentsInWindow(ctx, "", string(candidate.ActionReduce), since)
	if err != nil {
		return nil, fmt.Errorf("llm frequency facts: counting reduce orders: %w", err)
	}

	facts := &RiskFrequencyFacts{
		NewRiskIntentsInWindow:   int(newRiskCount),
		VersionWindowCount:       int(versionCount) + 1,
		MaxVersionWindow:         in.MaxVersionWindow,
		MarketCooldownElapsed:    in.MarketCooldownElapsed,
		AllOrdersInWindow:        int(allCount) + 1,
		MaxOrdersPerWindow:       in.MaxOrdersPerWindow,
		ReduceOrdersInWindow:     int(reduceCount),
		MaxReduceOrdersPerWindow: in.MaxReduceOrdersPerWindow,
		ConsecutiveLosses:        in.ConsecutiveLosses,
		MaxConsecutiveLosses:     in.MaxConsecutiveLosses,
	}
	if newRiskOrder {
		facts.NewRiskIntentsInWindow++
	} else {
		facts.ReduceOrdersInWindow++
	}
	return facts, nil
}

// RiskFrequencyFactsInput is the window material the frequency facts carry.
type RiskFrequencyFactsInput struct {
	// AccountID scopes the new-risk counter window; it is required.
	AccountID string
	// StrategyVersionID scopes the version window; it is required.
	StrategyVersionID string
	// Action is the candidate action under evaluation; it decides which
	// window the prospective order is folded into.
	Action candidate.Action
	// Now is the window's as-of instant; the window starts Now minus
	// WindowMinutes.
	Now time.Time
	// WindowMinutes is the rolling window length (the envelope's
	// riskWindowMinutes); it must be positive.
	WindowMinutes int
	// MaxVersionWindow is the per-version intent cap; 0 means the check
	// treats the gate as not configured.
	MaxVersionWindow int
	// MaxOrdersPerWindow is the account-wide order cap; 0 means no gate.
	MaxOrdersPerWindow int
	// MaxReduceOrdersPerWindow is the reduce-channel storm cap; 0 means no
	// gate.
	MaxReduceOrdersPerWindow int
	// MarketCooldownElapsed reports the market is past its post-entry
	// cooldown. The "last entry" half is now readable (`LastEntryIntentAt`,
	// session-51 tail 27); the cooldown length has no policy source yet, so
	// the caller supplies the judgement until one exists.
	MarketCooldownElapsed bool
	// ConsecutiveLosses is the closing trades' losing streak; there is no
	// outcome ledger read for it yet.
	ConsecutiveLosses int
	// MaxConsecutiveLosses is the circuit breaker's bar; the check falls
	// back to RiskMaxConsecutiveLosses when it is not positive.
	MaxConsecutiveLosses int
}
