package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-10 生产适配器（W7-01 写侧装配）。
//
// HaltController 的三个动作接缝此前只有测试侧实现；本文件把它们接到真实
// 子系统：
//   - LLMRunningStrategies：暂停每个「可运行」策略版本（state=FROZEN 且持有
//     活审批——被暂停过的版本不再动，没审批的版本本来就不会被调度）；
//   - LLMBotOrders：只撤机器人自己的挂单（`gct-llm-` 前缀过滤——用户同时
//     在币安 App 上手动操作的订单不可被撤）；
//   - LLMPositionFlattener：reduce-only 市价平掉全部合约仓位（FULL+显式
//     二次确认才会被控制器调到）。
//
// 三个适配器都是「失败即中止」：半撤的挂单或半平的仓位不是停机该有的样子，
// 错误带着已完成的计数上抛，由控制器中止整条序列（§8 的任一步失败中止）。

// strategyStateFrozen is the state a runnable version holds (freeze 写入的
// 生命周期值；resume 也回到它）。
const strategyStateFrozen = "FROZEN"

// LLMRunningStrategies pauses runnable strategy versions through the
// repository. Now/NewID are injectable for tests.
type LLMRunningStrategies struct {
	Now   func() time.Time
	NewID func() string
}

// PauseRunningStrategies suspends every version that is frozen and backed by
// a live approval, recording one transition per version.
func (s LLMRunningStrategies) PauseRunningStrategies(ctx context.Context, reason, actor string) (int, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	newID := s.NewID
	if newID == nil {
		newID = newLLMIdentifier
	}
	approvals, err := llmstore.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return 0, fmt.Errorf("llm halt: listing live approvals: %w", err)
	}
	paused := 0
	for i := range approvals {
		version, err := llmstore.GetStrategyVersion(ctx, approvals[i].StrategyVersionID)
		if err != nil {
			return paused, fmt.Errorf("llm halt: reading version %q: %w", approvals[i].StrategyVersionID, err)
		}
		if version.State != strategyStateFrozen {
			// Already paused, or never frozen: nothing to suspend.
			continue
		}
		if err := llmstore.UpdateStrategyVersionState(ctx, version.ID, StrategyStatePaused); err != nil {
			return paused, fmt.Errorf("llm halt: pausing version %q: %w", version.ID, err)
		}
		if err := llmstore.InsertStrategyStateTransition(ctx, &llmstore.StrategyStateTransition{
			ID:                newID(),
			StrategyVersionID: version.ID,
			FromState:         version.State,
			ToState:           StrategyStatePaused,
			Reason:            reason,
			Actor:             actor,
			CreatedAt:         now(),
		}); err != nil {
			return paused, fmt.Errorf("llm halt: recording the pause of %q: %w", version.ID, err)
		}
		paused++
	}
	return paused, nil
}

// LLMBotOrders cancels the bot's own resting orders through the engine's
// order manager.
type LLMBotOrders struct {
	Manager *OrderManager
	// Prefix defaults to IntentClientOrderIDPrefix; tests may narrow it.
	Prefix string
}

// CancelBotOrders cancels every active order whose client order id carries the
// robot prefix. Orders the operator placed elsewhere are left alone.
func (a LLMBotOrders) CancelBotOrders(ctx context.Context, reason string) (int, error) {
	if a.Manager == nil {
		return 0, ErrChatNotConfigured
	}
	prefix := a.Prefix
	if prefix == "" {
		prefix = IntentClientOrderIDPrefix
	}
	active, err := a.Manager.GetOrdersActive(&order.Filter{})
	if err != nil {
		return 0, fmt.Errorf("llm halt: listing active orders: %w", err)
	}
	canceled := 0
	for i := range active {
		detail := &active[i]
		if !strings.HasPrefix(detail.ClientOrderID, prefix) {
			continue
		}
		cancel, err := detail.DeriveCancel()
		if err != nil {
			return canceled, fmt.Errorf("llm halt: deriving cancel for %q: %w", detail.OrderID, err)
		}
		if err := a.Manager.Cancel(ctx, cancel); err != nil {
			return canceled, fmt.Errorf("llm halt: cancelling %q (reason %q): %w", detail.OrderID, reason, err)
		}
		canceled++
	}
	return canceled, nil
}

// LLMPositionFlattener reduce-only closes every open futures position.
type LLMPositionFlattener struct {
	Manager *OrderManager
	NewID   func() string
}

// FlattenAllPositions submits one reduce-only market order per open position,
// in the opposite direction of the position.
func (f LLMPositionFlattener) FlattenAllPositions(ctx context.Context, reason string) (int, error) {
	if f.Manager == nil {
		return 0, ErrChatNotConfigured
	}
	newID := f.NewID
	if newID == nil {
		newID = newLLMIdentifier
	}
	positions, err := f.Manager.GetAllOpenFuturesPositions()
	if err != nil {
		return 0, fmt.Errorf("llm halt: listing futures positions: %w", err)
	}
	flattened := 0
	for i := range positions {
		pos := &positions[i]
		size, direction := pos.LatestSize, pos.LatestDirection
		if size.IsZero() {
			// A position older than its latest update: fall back to the
			// opening facts rather than skipping a real position.
			size, direction = pos.OpeningSize, pos.OpeningDirection
		}
		if size.IsZero() {
			continue
		}
		if size.IsNegative() {
			size = size.Abs()
		}
		var side order.Side
		switch direction {
		case order.Long:
			side = order.Sell
		case order.Short:
			side = order.Buy
		default:
			return flattened, fmt.Errorf("%w: position %s %s has direction %q", ErrHaltInputInvalid, pos.Exchange, pos.Pair, direction)
		}
		amount, _ := size.Float64()
		submit := &order.Submit{
			Exchange:   pos.Exchange,
			Type:       order.Market,
			Side:       side,
			Pair:       pos.Pair,
			AssetType:  pos.Asset,
			ReduceOnly: true,
			Leverage:   0,
			Amount:     amount,
			// Every flatten order is the bot's own, so the prefix filter on a
			// later cancel recognises it.
			ClientOrderID:    newID(),
			RetrieveFees:     true,
			RetrieveFeeDelay: OrderSubmitFeeDelay,
		}
		if _, err := f.Manager.Submit(ctx, submit); err != nil {
			return flattened, fmt.Errorf("llm halt: flattening %s %s (reason %q): %w", pos.Exchange, pos.Pair, reason, err)
		}
		flattened++
	}
	return flattened, nil
}
