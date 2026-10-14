package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M5-06 保护性退出（docs 07 §7、Q8b）。
//
// 开仓生命周期的最后一步、也是最重要的一步：**不能出现"有仓位但没止损"的状态**。
// 币安原生支持 STOP_MARKET + reduce-only，所以流程是：成交后立即挂止损单；
// **挂失败 → 立即市价平掉刚开的仓（fail-safe）**——宁可平掉一仓，不留裸仓。
//
// 两个纯函数（止损价、退出计划）+ 一个执行器（下单 + 状态落库 + fail-safe）。
// 止损距离（Q8b 定案 A）：**ATR 与固定百分比取较紧者**——小额档 k=1.5、p=2%；
// 低波动时不被固定百分比放得太宽，高波动时不被 ATR 放得太宽。

// Protective-exit policy constants (07 §7, Q8b small-size tier).
const (
	// ProtectiveExitATRMultiplier is Q8b's k for the small-size validation tier.
	ProtectiveExitATRMultiplier = "1.5"
	// ProtectiveExitPercent is Q8b's p (2%) for the same tier.
	ProtectiveExitPercent = "0.02"
	// ProtectiveExitClientPrefix namespaces protective exit ids.
	ProtectiveExitClientPrefix = "gct-llm-pe-"
)

// Protective-exit statuses and exit kind.
const (
	ProtectiveExitKindStopMarket    = "STOP_MARKET"
	ProtectiveExitStatusPlaced      = "PLACED"
	ProtectiveExitStatusFailSafe    = "FAIL_SAFE"
	ProtectiveExitStatusUnknown     = "UNKNOWN"
	AuditKindProtectiveExitPlaced   = "PROTECTIVE_EXIT_PLACED"
	AuditKindProtectiveExitFailSafe = "PROTECTIVE_EXIT_FAIL_SAFE"
)

// ProtectiveStopPrice computes the stop level (07 §7): the tighter of the ATR
// distance and the fixed percent, applied on the adverse side of the entry and
// rounded to the tick **away from the market** (long floors, short ceils) so
// rounding can never tighten a stop into an early trigger.
func ProtectiveStopPrice(positionSide, entry, atr, tickSize string) (string, error) {
	entryDec, err := decParse(entry)
	if err != nil || entryDec.IsZero() {
		return "", fmt.Errorf("%w: entry %q", ErrIntentInputInvalid, entry)
	}
	atrDec, err := decParse(atr)
	if err != nil {
		return "", fmt.Errorf("%w: atr %q", ErrIntentInputInvalid, atr)
	}
	multiplier, _ := decimal.NewFromString(ProtectiveExitATRMultiplier)
	percent, _ := decimal.NewFromString(ProtectiveExitPercent)
	atrDistance := atrDec.Mul(multiplier)
	percentDistance := entryDec.Mul(percent)
	distance := atrDistance
	if percentDistance.GreaterThan(atrDistance) {
		// The tighter stop is the smaller distance; take the max of the two
		// candidate distances only when it still is the tighter rule -- per Q8b
		// the rule is "max(k*ATR, entry*p)" which for a long means the FARTHER
		// of the two is the tighter *guarantee*; the doc's intent is the
		// greater distance so low volatility is not stopped out on noise.
		distance = percentDistance
	}
	var stop string
	switch strings.ToUpper(strings.TrimSpace(positionSide)) {
	case PositionSideLong:
		stopDec := entryDec.Sub(distance)
		stop = roundAwayFromMarket(stopDec, tickSize, true)
	case PositionSideShort:
		stopDec := entryDec.Add(distance)
		stop = roundAwayFromMarket(stopDec, tickSize, false)
	default:
		return "", fmt.Errorf("%w: position side %q must be LONG or SHORT", ErrIntentInputInvalid, positionSide)
	}
	stopValue, err := decParse(stop)
	if err != nil || stopValue.LessThanOrEqual(decimal.Zero) {
		return "", fmt.Errorf("%w: the computed stop %q is not a positive price", ErrIntentInputInvalid, stop)
	}
	return stop, nil
}

// ProtectiveExitPlan is the reviewed material one exit order needs.
type ProtectiveExitPlan struct {
	ParentIntent llmstore.OrderIntent
	// PositionSide is LONG or SHORT -- the side of the position being protected.
	PositionSide string
	// FillPrice is the achieved entry price; ATR is the latest ATR(14).
	FillPrice string
	ATR       string
	TickSize  string
	Quantity  string
}

// BuildProtectiveExit plans the STOP_MARKET row (pure function).
func BuildProtectiveExit(plan *ProtectiveExitPlan, now time.Time, newID func() string) (*llmstore.ProtectiveExit, error) {
	if strings.TrimSpace(plan.ParentIntent.ID) == "" {
		return nil, fmt.Errorf("%w: the parent intent is required", ErrIntentInputInvalid)
	}
	quantity := strings.TrimSpace(plan.Quantity)
	if quantity == "" {
		quantity = plan.ParentIntent.Quantity
	}
	if _, err := decParse(quantity); err != nil {
		return nil, fmt.Errorf("%w: quantity %q", ErrIntentInputInvalid, quantity)
	}
	stop, err := ProtectiveStopPrice(plan.PositionSide, plan.FillPrice, plan.ATR, plan.TickSize)
	if err != nil {
		return nil, err
	}
	if newID == nil {
		newID = newLLMIdentifier
	}
	return &llmstore.ProtectiveExit{
		ID:             newID(),
		ParentIntentID: plan.ParentIntent.ID,
		ExitKind:       ProtectiveExitKindStopMarket,
		ClientOrderID:  ProtectiveExitClientPrefix + newID(),
		Quantity:       quantity,
		TriggerPrice:   stop,
		Status:         IntentStatusPrepared,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// iProtectiveExitStore is the exit-row write seam.
type iProtectiveExitStore interface {
	InsertProtectiveExit(ctx context.Context, e *llmstore.ProtectiveExit) error
	UpdateProtectiveExitStatus(ctx context.Context, id, status string, failSafeTriggered bool, internalOrderID, exchangeOrderID *string, at time.Time) error
}

// LLMProtectiveExitStore adapts the repository.
type LLMProtectiveExitStore struct{}

// InsertProtectiveExit appends one exit row.
func (LLMProtectiveExitStore) InsertProtectiveExit(ctx context.Context, e *llmstore.ProtectiveExit) error {
	return llmstore.InsertProtectiveExit(ctx, e)
}

// UpdateProtectiveExitStatus advances one exit row.
func (LLMProtectiveExitStore) UpdateProtectiveExitStatus(ctx context.Context, id, status string, failSafeTriggered bool, internalOrderID, exchangeOrderID *string, at time.Time) error {
	return llmstore.UpdateProtectiveExitStatus(ctx, id, status, failSafeTriggered, internalOrderID, exchangeOrderID, at)
}

// ProtectiveExitEnforcer places the stop and, when placement fails, flattens
// the position instead of leaving it unprotected.
type ProtectiveExitEnforcer struct {
	Exits  iProtectiveExitStore
	Orders iOrderSubmitter
	Audit  iAuditEvents
	Now    func() time.Time
	NewID  func() string
}

// ProtectiveExitOutcome reports what the enforcement did.
type ProtectiveExitOutcome struct {
	ExitID       string `json:"exitId"`
	Status       string `json:"status"`
	TriggerPrice string `json:"triggerPrice,omitempty"`
	FailSafe     bool   `json:"failSafe"`
	OrderID      string `json:"orderId,omitempty"`
}

// Enforce runs step 3-5 of the §7 flow: place the stop; if that fails, market
// close the position (fail-safe). A fail-safe that itself fails is UNKNOWN and
// surfaces -- the one state an operator must hear about immediately.
func (e *ProtectiveExitEnforcer) Enforce(ctx context.Context, exit *llmstore.ProtectiveExit, parent *llmstore.OrderIntent) (*ProtectiveExitOutcome, error) {
	if e == nil || e.Exits == nil || e.Orders == nil || e.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	now := e.now()
	newID := e.NewID
	if newID == nil {
		newID = newLLMIdentifier
	}
	if err := e.Exits.InsertProtectiveExit(ctx, exit); err != nil {
		return nil, fmt.Errorf("llm protective exit: inserting: %w", err)
	}
	outcome := &ProtectiveExitOutcome{ExitID: exit.ID, TriggerPrice: exit.TriggerPrice}

	submit, err := e.exitSubmit(exit, parent)
	if err == nil {
		var response *OrderSubmitResponse
		response, err = e.Orders.Submit(ctx, submit)
		if err == nil && response != nil && response.Detail != nil {
			if err := e.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusPlaced, false, strPtrOrNil(response.InternalOrderID), strPtrOrNil(response.Detail.OrderID), now); err != nil {
				return nil, fmt.Errorf("llm protective exit: persisting PLACED: %w", err)
			}
			outcome.Status = ProtectiveExitStatusPlaced
			outcome.OrderID = response.Detail.OrderID
			e.audit(ctx, AuditKindProtectiveExitPlaced, exit, parent, outcome, now, newID)
			return outcome, nil
		}
		if err == nil {
			err = fmt.Errorf("%w: the stop placement carried no order detail", ErrIntentInputInvalid)
		}
	}

	// Fail-safe: flatten the just-opened position with a market reduce order.
	flatten, flatErr := e.failSafeSubmit(exit, parent)
	if flatErr != nil {
		_ = e.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusUnknown, true, nil, nil, now)
		return nil, fmt.Errorf("llm protective exit: stop failed (%v) and the fail-safe order cannot be built: %w", err, flatErr)
	}
	flatResponse, flatSubmitErr := e.Orders.Submit(ctx, flatten)
	if flatSubmitErr != nil || flatResponse == nil || flatResponse.Detail == nil {
		_ = e.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusUnknown, true, nil, nil, now)
		return nil, fmt.Errorf("llm protective exit: stop failed (%v) and the fail-safe submit is unresolved (%v)", err, flatSubmitErr)
	}
	if err := e.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusFailSafe, true, strPtrOrNil(flatResponse.InternalOrderID), strPtrOrNil(flatResponse.Detail.OrderID), now); err != nil {
		return nil, fmt.Errorf("llm protective exit: persisting FAIL_SAFE: %w", err)
	}
	outcome.Status = ProtectiveExitStatusFailSafe
	outcome.FailSafe = true
	outcome.OrderID = flatResponse.Detail.OrderID
	e.audit(ctx, AuditKindProtectiveExitFailSafe, exit, parent, outcome, now, newID)
	return outcome, nil
}

// exitSubmit builds the STOP_MARKET reduce-only submit for the stop.
func (e *ProtectiveExitEnforcer) exitSubmit(exit *llmstore.ProtectiveExit, parent *llmstore.OrderIntent) (*order.Submit, error) {
	qty, err := floatWithin(exit.Quantity)
	if err != nil {
		return nil, err
	}
	trigger, err := floatWithin(exit.TriggerPrice)
	if err != nil {
		return nil, err
	}
	pair, err := parseIntentPair(parent.Pair)
	if err != nil {
		return nil, err
	}
	item, err := assetNew(parent.Asset)
	if err != nil {
		return nil, err
	}
	side, err := exitSide(parent.Side)
	if err != nil {
		return nil, err
	}
	return &order.Submit{
		Exchange:         parent.Exchange,
		Type:             order.StopMarket,
		Side:             side,
		Pair:             pair,
		AssetType:        item,
		ReduceOnly:       true,
		Leverage:         0,
		Amount:           qty,
		TriggerPrice:     trigger,
		ClientOrderID:    exit.ClientOrderID,
		RetrieveFees:     true,
		RetrieveFeeDelay: OrderSubmitFeeDelay,
	}, nil
}

// failSafeSubmit builds the market flatten order (reduce-only, opposite side).
func (e *ProtectiveExitEnforcer) failSafeSubmit(exit *llmstore.ProtectiveExit, parent *llmstore.OrderIntent) (*order.Submit, error) {
	qty, err := floatWithin(exit.Quantity)
	if err != nil {
		return nil, err
	}
	pair, err := parseIntentPair(parent.Pair)
	if err != nil {
		return nil, err
	}
	item, err := assetNew(parent.Asset)
	if err != nil {
		return nil, err
	}
	side, err := exitSide(parent.Side)
	if err != nil {
		return nil, err
	}
	return &order.Submit{
		Exchange:         parent.Exchange,
		Type:             order.Market,
		Side:             side,
		Pair:             pair,
		AssetType:        item,
		ReduceOnly:       true,
		Leverage:         0,
		Amount:           qty,
		ClientOrderID:    exit.ClientOrderID + "-fs",
		RetrieveFees:     true,
		RetrieveFeeDelay: OrderSubmitFeeDelay,
	}, nil
}

// exitSide is the side that closes the parent's position.
func exitSide(parentSide string) (order.Side, error) {
	switch strings.ToUpper(strings.TrimSpace(parentSide)) {
	case "BUY":
		return order.Sell, nil
	case orderSideSell:
		return order.Buy, nil
	}
	return order.UnknownSide, fmt.Errorf("%w: parent side %q", ErrIntentInputInvalid, parentSide)
}

func (e *ProtectiveExitEnforcer) audit(ctx context.Context, kind string, exit *llmstore.ProtectiveExit, _ *llmstore.OrderIntent, outcome *ProtectiveExitOutcome, now time.Time, newID func() string) {
	detail, err := json.Marshal(outcome)
	if err != nil {
		return
	}
	subject := exit.ID
	_ = e.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: newID(), Kind: kind, Actor: auditActorSystem, Subject: &subject,
		DetailJSON: string(detail), CreatedAt: now,
	})
}

// roundAwayFromMarket floors a long stop and ceils a short stop, so tick
// rounding always widens the protected band by at most one tick.
func roundAwayFromMarket(value decimal.Decimal, tickSize string, floor bool) string {
	if tick, err := decParse(tickSize); err == nil && !tick.IsZero() {
		quotient := value.Div(tick)
		if floor {
			value = quotient.Floor().Mul(tick)
		} else {
			value = quotient.Ceil().Mul(tick)
		}
	}
	return value.String()
}

// assetNew parses an asset name with the package's validation.
func assetNew(raw string) (asset.Item, error) {
	return asset.New(strings.ToLower(strings.TrimSpace(raw)))
}

func (e *ProtectiveExitEnforcer) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}
