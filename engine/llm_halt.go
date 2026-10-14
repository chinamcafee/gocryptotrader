package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-10 HaltController（docs 07 §8）。
//
// 三级停机：SOFT（阻止新开仓；**允许 reduce-only 平仓与保护性止损**）、
// MARKET（某市场阻止新开仓，允许平仓）、FULL（阻止一切模型驱动动作；
// 确定性保护性止损仍可运行——它不依赖模型）。
//
// 触发序列：置 halt（落库即立即生效）→ 暂停运行中策略 → （审批撤销留给
// 调用方显式执行）→ **只撤机器人自己的挂单**（用户同时在币安 App 上手动
// 操作的订单不可被撤——用 `gct-llm-` 前缀区分）→ （FULL 且显式二次确认）
// reduce-only 平掉全部仓位 → 写审计。
//
// **un-halt 必须显式且需认证**：清除原因必须记录；清除后**策略不自动恢复**
// （逐个重新启用）、**已撤销的审批不自动恢复**（重新审批）。

const (
	// HaltLevelMarket blocks new risk for one market.
	HaltLevelMarket = "MARKET"
	// AuditKindHaltCleared labels the audit event; it is never a halt reason.
	AuditKindHaltCleared = "HALT_CLEARED"
)

var (
	// ErrHaltInputInvalid marks a malformed halt or clear command.
	ErrHaltInputInvalid = errors.New("llm halt: the command is missing required elements")
	// ErrHaltConfirmationRequired marks a FULL flatten without its explicit
	// second confirmation.
	ErrHaltConfirmationRequired = errors.New("llm halt: flattening positions requires explicit confirmation")
	// ErrHaltClearUnauthenticated marks a clear without re-authentication.
	ErrHaltClearUnauthenticated = errors.New("llm halt: clearing requires the operator to re-authenticate")
)

// iBotOrderCanceller cancels only the bot's own resting orders (the
// cancellation is filtered by the gct-llm- prefix at the runtime adapter).
type iBotOrderCanceller interface {
	CancelBotOrders(ctx context.Context, reason string) (int, error)
}

// iRunningStrategyPauser suspends every running strategy version.
type iRunningStrategyPauser interface {
	PauseRunningStrategies(ctx context.Context, reason, actor string) (int, error)
}

// iPositionFlattener reduce-only closes all positions (FULL, confirmed only).
type iPositionFlattener interface {
	FlattenAllPositions(ctx context.Context, reason string) (int, error)
}

// iHaltClearer / iActiveHaltReader are the repository clear/read seams.
type iHaltClearer interface {
	ClearHalt(ctx context.Context, id string, at time.Time, by, reason string) error
}

type iActiveHaltReader interface {
	ListActiveHalts(ctx context.Context) ([]llmstore.Halt, error)
}

// LLMHaltClearer / LLMActiveHalts adapt the repository.
type LLMHaltClearer struct{}

// ClearHalt marks one halt cleared.
func (LLMHaltClearer) ClearHalt(ctx context.Context, id string, at time.Time, by, reason string) error {
	return llmstore.ClearHalt(ctx, id, at, by, reason)
}

// LLMActiveHalts adapts the repository.
type LLMActiveHalts struct{}

// ListActiveHalts returns halts that are still in force.
func (LLMActiveHalts) ListActiveHalts(ctx context.Context) ([]llmstore.Halt, error) {
	return llmstore.ListActiveHalts(ctx)
}

// HaltController sets and clears halts under the §8 rules.
type HaltController struct {
	Halts          iHaltStore
	Clears         iHaltClearer
	Active         iActiveHaltReader
	Canceller      iBotOrderCanceller
	Strategies     iRunningStrategyPauser
	Flattener      iPositionFlattener
	Audit          iAuditEvents
	VerifyPassword func(ctx context.Context, password string) error
	Now            func() time.Time
	NewID          func() string
}

// HaltRequest is one operator or system halt command.
type HaltRequest struct {
	Level    string
	MarketID string
	Reason   string
	SetBy    string
	// Flatten asks for the FULL-level position flatten and requires
	// ConfirmFlatten to be set: the second confirmation is not a formality.
	Flatten        bool
	ConfirmFlatten bool
}

// HaltOutcome reports what the halt sequence did.
type HaltOutcome struct {
	HaltID              string `json:"haltId"`
	Level               string `json:"level"`
	SuspendedStrategies int    `json:"suspendedStrategies"`
	CanceledOrders      int    `json:"canceledOrders"`
	FlattenedPositions  int    `json:"flattenedPositions"`
}

// Set executes the §8 trigger sequence.
func (c *HaltController) Set(ctx context.Context, req HaltRequest) (*HaltOutcome, error) {
	if c == nil || c.Halts == nil || c.Strategies == nil {
		return nil, ErrChatNotConfigured
	}
	level := strings.ToUpper(strings.TrimSpace(req.Level))
	switch level {
	case HaltLevelSoft, HaltLevelMarket, HaltLevelFull:
	default:
		return nil, fmt.Errorf("%w: level %q must be SOFT, MARKET or FULL", ErrHaltInputInvalid, req.Level)
	}
	if strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.SetBy) == "" {
		return nil, fmt.Errorf("%w: reason and actor are required", ErrHaltInputInvalid)
	}
	if level == HaltLevelMarket && strings.TrimSpace(req.MarketID) == "" {
		return nil, fmt.Errorf("%w: a MARKET halt names its market", ErrHaltInputInvalid)
	}
	if req.Flatten && !req.ConfirmFlatten {
		return nil, ErrHaltConfirmationRequired
	}
	if req.Flatten && level != HaltLevelFull {
		return nil, fmt.Errorf("%w: only a FULL halt may flatten positions", ErrHaltInputInvalid)
	}
	if level != HaltLevelSoft && c.Canceller == nil {
		return nil, ErrChatNotConfigured
	}
	if req.Flatten && c.Flattener == nil {
		return nil, ErrChatNotConfigured
	}
	now := c.now()
	newID := c.newID
	market := strings.TrimSpace(req.MarketID)
	var marketPtr *string
	if market != "" {
		marketPtr = &market
	}
	// 1. The halt row is the flag: persisted first, effective immediately.
	haltID := newID()
	if err := c.Halts.InsertHalt(ctx, &llmstore.Halt{
		ID: haltID, Level: level, MarketID: marketPtr,
		Reason: req.Reason, SetBy: req.SetBy, SetAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm halt: inserting: %w", err)
	}
	outcome := &HaltOutcome{HaltID: haltID, Level: level}

	// 2. Suspend every running strategy.
	suspended, err := c.Strategies.PauseRunningStrategies(ctx, req.Reason, req.SetBy)
	if err != nil {
		return nil, fmt.Errorf("llm halt: pausing strategies: %w", err)
	}
	outcome.SuspendedStrategies = suspended

	// 3. MARKET and FULL withdraw the bot's resting orders; SOFT keeps them,
	// precisely so protective stops survive a soft stop.
	if level != HaltLevelSoft {
		canceled, err := c.Canceller.CancelBotOrders(ctx, req.Reason)
		if err != nil {
			return nil, fmt.Errorf("llm halt: cancelling bot orders: %w", err)
		}
		outcome.CanceledOrders = canceled
	}

	// 4. FULL with explicit confirmation flattens.
	if req.Flatten {
		flattened, err := c.Flattener.FlattenAllPositions(ctx, req.Reason)
		if err != nil {
			return nil, fmt.Errorf("llm halt: flattening: %w", err)
		}
		outcome.FlattenedPositions = flattened
	}

	detail, err := json.Marshal(outcome)
	if err != nil {
		return nil, fmt.Errorf("llm halt: audit detail: %w", err)
	}
	subject := haltID
	if err := c.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: newID(), Kind: AuditKindHaltSet, Actor: req.SetBy,
		Subject: &subject, DetailJSON: string(detail), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm halt: audit event: %w", err)
	}
	return outcome, nil
}

// ClearRequest is one explicit un-halt.
type ClearRequest struct {
	HaltID   string
	By       string
	Reason   string
	Password string
}

// Clear lifts one halt under the §8 rules: re-authentication, a recorded
// reason, and no automatic recovery of strategies or approvals -- both must be
// re-enabled individually afterwards.
func (c *HaltController) Clear(ctx context.Context, req ClearRequest) error {
	if c == nil || c.Clears == nil || c.VerifyPassword == nil || c.Audit == nil {
		return ErrChatNotConfigured
	}
	if strings.TrimSpace(req.HaltID) == "" || strings.TrimSpace(req.By) == "" || strings.TrimSpace(req.Reason) == "" {
		return fmt.Errorf("%w: halt id, actor and reason are required", ErrHaltInputInvalid)
	}
	if err := c.VerifyPassword(ctx, req.Password); err != nil {
		return fmt.Errorf("%w: %v", ErrHaltClearUnauthenticated, err)
	}
	now := c.now()
	if err := c.Clears.ClearHalt(ctx, req.HaltID, now, req.By, req.Reason); err != nil {
		return fmt.Errorf("llm halt: clearing: %w", err)
	}
	detail := fmt.Sprintf(`{"haltId":%q,"clearedBy":%q,"reason":%q,"autoResume":"never; strategies and approvals need individual re-enablement"}`,
		req.HaltID, req.By, req.Reason)
	subject := req.HaltID
	if err := c.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: c.newID(), Kind: AuditKindHaltCleared, Actor: req.By,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("llm halt: clear audit event: %w", err)
	}
	return nil
}

// ActiveHalts lists halts still in force.
func (c *HaltController) ActiveHalts(ctx context.Context) ([]llmstore.Halt, error) {
	if c == nil || c.Active == nil {
		return nil, ErrChatNotConfigured
	}
	return c.Active.ListActiveHalts(ctx)
}

func (c *HaltController) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *HaltController) newID() string {
	if c.NewID != nil {
		return c.NewID()
	}
	return newLLMIdentifier()
}
