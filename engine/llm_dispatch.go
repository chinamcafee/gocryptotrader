package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-02 执行分流（docs 07 §6.2，Q1=E）。
//
// ```text
// READ_ONLY / SHADOW -> 不执行（只留 decision_run + 审计）
// PAPER              -> orderManager.SubmitFakeOrder(submit, nil, true)   // 完整校验+入库+跟踪，不打交易所
// TESTNET / LIVE     -> orderManager.Submit(ctx, submit)
// ```
//
// 分流是一张白名单：模式不认识就不执行。提交后立即落生命周期——
// 成功=ACKED（带内部/交易所订单号）；失败=UNKNOWN（**疑罪从有**：结果不明确的
// 提交绝不当作已拒绝，也绝不盲重试——完整状态机与对账标记归 M5-05）。
// 提交成功但回执缺详情同样算 UNKNOWN：没有订单号的成功不是成功。

// Intent lifecycle statuses this layer writes (07 §6.3 places the full machine in M5-05).
const (
	IntentStatusSubmitting      = "SUBMITTING"
	IntentStatusAcked           = "ACKED"
	IntentStatusResting         = "RESTING"
	IntentStatusPartiallyFilled = "PARTIALLY_FILLED"
	IntentStatusFilled          = "FILLED"
	IntentStatusCanceled        = "CANCELED"
	IntentStatusUnknown         = "UNKNOWN"
	IntentStatusRejected        = "REJECTED"
	IntentStatusExpired         = "EXPIRED"
)

// intentTransitions is the legal state machine (07 §6.3). Anything not listed
// is refused, which is what makes "UNKNOWN is never retried" structural rather
// than a convention: UNKNOWN has no outgoing edge back to SUBMITTING.
var intentTransitions = map[string][]string{
	IntentStatusPrepared:        {IntentStatusSubmitting, IntentStatusExpired, IntentStatusRejected},
	IntentStatusSubmitting:      {IntentStatusAcked, IntentStatusRejected, IntentStatusUnknown},
	IntentStatusAcked:           {IntentStatusResting, IntentStatusPartiallyFilled, IntentStatusFilled, IntentStatusCanceled, IntentStatusUnknown},
	IntentStatusResting:         {IntentStatusPartiallyFilled, IntentStatusFilled, IntentStatusCanceled, IntentStatusUnknown},
	IntentStatusPartiallyFilled: {IntentStatusFilled, IntentStatusCanceled, IntentStatusUnknown},
	IntentStatusCanceled:        {},
	IntentStatusFilled:          {},
	IntentStatusUnknown:         {},
	IntentStatusRejected:        {},
	IntentStatusExpired:         {},
}

// intentCanTransition reports whether the machine allows from -> to.
func intentCanTransition(from, to string) bool {
	return slices.Contains(intentTransitions[from], to)
}

// IntentSubmitMaxAge is the pre-submit freshness bar (07 §6.2, M5-04): an
// intent older than this may rest on prices that have moved and must be
// re-derived, not submitted.
const IntentSubmitMaxAge = 5 * time.Second

// AuditKindOrderSubmitted is recorded after the venue (or the fake path) accepts an order.
const AuditKindOrderSubmitted = "ORDER_SUBMITTED"

// iOrderSubmitter / iFakeOrderSubmitter / iIntentLifecycle are the dispatch seams.
type iOrderSubmitter interface {
	Submit(ctx context.Context, newOrder *order.Submit) (*OrderSubmitResponse, error)
}

type iFakeOrderSubmitter interface {
	SubmitFakeOrder(newOrder *order.Submit, resultingOrder *order.SubmitResponse, checkExchangeLimits bool) (*OrderSubmitResponse, error)
}

type iIntentLifecycle interface {
	UpdateOrderIntentLifecycle(ctx context.Context, id, status string, internalOrderID, exchangeOrderID *string, at time.Time) error
}

// iIntentClaimer is the atomic PREPARED -> SUBMITTING claim.
type iIntentClaimer interface {
	ClaimOrderIntent(ctx context.Context, id string, at time.Time) (bool, error)
}

// LLMOrderManager adapts the engine's order manager to both submit paths.
type LLMOrderManager struct {
	Manager *OrderManager
}

// Submit routes a live/testnet order.
func (a LLMOrderManager) Submit(ctx context.Context, newOrder *order.Submit) (*OrderSubmitResponse, error) {
	if a.Manager == nil {
		return nil, ErrChatNotConfigured
	}
	return a.Manager.Submit(ctx, newOrder)
}

// SubmitFakeOrder runs the paper path: full validation and tracking, no venue call.
func (a LLMOrderManager) SubmitFakeOrder(newOrder *order.Submit, resultingOrder *order.SubmitResponse, checkExchangeLimits bool) (*OrderSubmitResponse, error) {
	if a.Manager == nil {
		return nil, ErrChatNotConfigured
	}
	return a.Manager.SubmitFakeOrder(newOrder, resultingOrder, checkExchangeLimits)
}

// LLMIntentClaimer adapts the repository claim.
type LLMIntentClaimer struct{}

// ClaimOrderIntent claims one intent for submission.
func (LLMIntentClaimer) ClaimOrderIntent(ctx context.Context, id string, at time.Time) (bool, error) {
	return llmstore.ClaimOrderIntent(ctx, id, at)
}

// LLMIntentLifecycle adapts the repository lifecycle update.
type LLMIntentLifecycle struct{}

// UpdateOrderIntentLifecycle advances one intent's status.
func (LLMIntentLifecycle) UpdateOrderIntentLifecycle(ctx context.Context, id, status string, internalOrderID, exchangeOrderID *string, at time.Time) error {
	return llmstore.UpdateOrderIntentLifecycle(ctx, id, status, internalOrderID, exchangeOrderID, at)
}

// OrderDispatcher routes one intent by run mode.
type OrderDispatcher struct {
	Orders    iOrderSubmitter
	Fake      iFakeOrderSubmitter
	Claim     iIntentClaimer
	Lifecycle iIntentLifecycle
	Audit     iAuditEvents
	// Events raises an account-side trigger when an order is accepted: the
	// account changed the moment the venue (or the paper book) took the order,
	// and the trigger layer must hear about it so the next look does not wait
	// for the sweep. The execution bridge wires it to the scanner's
	// RaiseAccountEvent; nil drops silently.
	Events func(marketID, detail string, at time.Time)
	Now    func() time.Time
	NewID  func() string
}

// DispatchOutcome reports what the routing decided.
type DispatchOutcome struct {
	Executed        bool   `json:"executed"`
	Mode            string `json:"mode"`
	IntentID        string `json:"intentId"`
	Status          string `json:"status"`
	InternalOrderID string `json:"internalOrderId,omitempty"`
	ExchangeOrderID string `json:"exchangeOrderId,omitempty"`
}

// Dispatch routes one prepared intent. An unknown mode refuses; a failed or
// ambiguous submission persists UNKNOWN before the error is returned.
func (d *OrderDispatcher) Dispatch(ctx context.Context, intent *llmstore.OrderIntent, mode string) (*DispatchOutcome, error) {
	if d == nil || d.Orders == nil || d.Fake == nil || d.Claim == nil || d.Lifecycle == nil || d.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	normalised := strings.ToUpper(strings.TrimSpace(mode))
	outcome := &DispatchOutcome{Mode: normalised, IntentID: intent.ID, Status: intent.Status}

	switch normalised {
	case LLMRunModeReadOnly, LLMRunModeShadow:
		// Decision recorded; nothing is sent. This is the structural promise of
		// shadow mode restated at the one place that could break it.
		return outcome, nil
	case LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive:
	default:
		return nil, fmt.Errorf("%w: run mode %q", ErrIntentInputInvalid, mode)
	}
	if !intentCanTransition(intent.Status, IntentStatusSubmitting) {
		return nil, fmt.Errorf("%w: intent %s is %s, which cannot become %s", ErrIntentInputInvalid, intent.ID, intent.Status, IntentStatusSubmitting)
	}
	// Final pre-submit checks (M5-04): the authorisation must still be young and
	// unexpired. A stale intent is marked EXPIRED -- it is not a venue failure
	// and must never look like one.
	if !intent.ExpiresAt.IsZero() && !now.Before(intent.ExpiresAt) {
		return d.markExpired(ctx, outcome, now, fmt.Errorf("%w: intent %s expired at %s", ErrIntentExpired, intent.ID, intent.ExpiresAt.UTC()))
	}
	if !intent.CreatedAt.IsZero() && now.Sub(intent.CreatedAt) > IntentSubmitMaxAge {
		return d.markExpired(ctx, outcome, now, fmt.Errorf("%w: intent %s is %s old, over the %s bar", ErrIntentExpired, intent.ID, now.Sub(intent.CreatedAt), IntentSubmitMaxAge))
	}
	submit, err := BuildOrderSubmit(intent)
	if err != nil {
		return nil, err
	}
	// Claim before anything touches the venue: exactly one caller can move
	// PREPARED -> SUBMITTING, so a retry after an ambiguous result cannot mint a
	// second order (this is the never-retry rule made structural).
	claimed, err := d.Claim.ClaimOrderIntent(ctx, intent.ID, now)
	if err != nil {
		return nil, fmt.Errorf("llm dispatch: claiming: %w", err)
	}
	if !claimed {
		return nil, fmt.Errorf("%w: intent %s was already claimed or is no longer PREPARED", ErrIntentClaimLost, intent.ID)
	}
	outcome.Status = IntentStatusSubmitting

	var response *OrderSubmitResponse
	if normalised == LLMRunModePaper {
		response, err = d.Fake.SubmitFakeOrder(submit, nil, true)
	} else {
		response, err = d.Orders.Submit(ctx, submit)
	}
	if err != nil {
		return d.markUnknown(ctx, outcome, now, fmt.Errorf("llm dispatch: submitting: %w", err))
	}
	if response == nil || response.Detail == nil {
		return d.markUnknown(ctx, outcome, now, fmt.Errorf("%w: the submit response carried no order detail", ErrIntentInputInvalid))
	}
	internalID := response.InternalOrderID
	exchangeID := response.Detail.OrderID
	if err := d.Lifecycle.UpdateOrderIntentLifecycle(ctx, intent.ID, IntentStatusAcked, strPtrOrNil(internalID), strPtrOrNil(exchangeID), now); err != nil {
		return nil, fmt.Errorf("llm dispatch: persisting ACKED: %w", err)
	}
	outcome.Executed = true
	outcome.Status = IntentStatusAcked
	outcome.InternalOrderID = internalID
	outcome.ExchangeOrderID = exchangeID
	detail, err := json.Marshal(map[string]any{
		"intentId":        intent.ID,
		"mode":            normalised,
		"internalOrderId": internalID,
		"exchangeOrderId": exchangeID,
		"clientOrderId":   intent.ClientOrderID,
		"quantity":        intent.Quantity,
	})
	if err != nil {
		return nil, fmt.Errorf("llm dispatch: audit detail: %w", err)
	}
	subject := intent.ID
	if err := d.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: d.newID(), Kind: AuditKindOrderSubmitted, Actor: auditActorSystem,
		Subject: &subject, DetailJSON: string(detail), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm dispatch: audit event: %w", err)
	}
	// Only the accepted paths raise the account trigger. A failed or ambiguous
	// submission was NOT confirmed to change the account -- it gets the
	// reconciliation flag instead, not a look-again signal.
	if d.Events != nil {
		if market := strings.TrimSpace(intent.Pair); market != "" {
			d.Events(market, "order acked: "+exchangeID, now)
		}
	}
	return outcome, nil
}

// markExpired persists the stale-intent state: nothing was sent, and the run
// must be re-derived rather than retried against a moved market.
func (d *OrderDispatcher) markExpired(ctx context.Context, outcome *DispatchOutcome, now time.Time, cause error) (*DispatchOutcome, error) {
	if err := d.Lifecycle.UpdateOrderIntentLifecycle(ctx, outcome.IntentID, IntentStatusExpired, nil, nil, now); err != nil {
		return nil, fmt.Errorf("llm dispatch: persisting EXPIRED: %w", err)
	}
	outcome.Status = IntentStatusExpired
	return outcome, cause
}

// markUnknown persists the ambiguous-failure state after surfacing the error:
// a submission whose result is unclear must never look like a rejection.
func (d *OrderDispatcher) markUnknown(ctx context.Context, outcome *DispatchOutcome, now time.Time, cause error) (*DispatchOutcome, error) {
	if err := d.Lifecycle.UpdateOrderIntentLifecycle(ctx, outcome.IntentID, IntentStatusUnknown, nil, nil, now); err != nil {
		return nil, fmt.Errorf("llm dispatch: persisting UNKNOWN: %w", err)
	}
	outcome.Status = IntentStatusUnknown
	return outcome, cause
}

func (d *OrderDispatcher) newID() string {
	if d.NewID != nil {
		return d.NewID()
	}
	return newLLMIdentifier()
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
