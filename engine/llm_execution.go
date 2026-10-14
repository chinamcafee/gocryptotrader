package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M5-01 OrderIntent 生成与落库（docs 07 §6.1）。
//
// 意图是**不可变的、经风控批准的下单凭据**：它绑定 decision_run、审批、
// envelope 哈希与 pair_epoch，数量取自 M4-12 的重算值，价格只从盘口推导。
// 生成前三重把关：风控 ALLOW、重算 ALLOW、动作只能是开/平——任何一重不是
// ALLOW 都拒绝生成（ALLOW 的唯一产地是硬风控，这里只是把它落成凭据）。
//
// 计划与落库分离：`BuildIntent` 是纯函数（可测、可重放），`IntentRecorder`
// 负责写 intent + 审计事件，任一步失败中止——"不能落库的意图不许流向执行"。

// Intent constants (07 §6.1).
const (
	// AuditKindIntentPrepared is recorded for every stored intent.
	AuditKindIntentPrepared = "ORDER_INTENT_PREPARED"
	// IntentStatusPrepared is the initial lifecycle status.
	IntentStatusPrepared = "PREPARED"
	// IntentClientOrderIDPrefix marks ids minted by this system; the prefix is
	// the namespace that makes the venue-side idempotency key recognisable.
	IntentClientOrderIDPrefix = "gct-llm-"
	// OrderTypeLimit and OrderTypeMarket are the entry types a plan may carry
	// (§5.4); OrderTypeStopMarket is the protective exit's own and is prepared
	// separately against its parent intent.
	OrderTypeLimit      = "LIMIT"
	OrderTypeMarket     = "MARKET"
	OrderTypeStopMarket = "STOP_MARKET"
	// PositionSideLong / PositionSideShort are the one spelling of an existing
	// position's side (reduce side, protective exit, reconcile snapshot).
	PositionSideLong  = "LONG"
	PositionSideShort = "SHORT"
	// IntentDefaultExpiry is the documented default validity of an intent.
	IntentDefaultExpiry = 10 * time.Second
	// IntentMaxExpiry bounds an operator-supplied validity.
	IntentMaxExpiry = 2 * time.Minute
)

var (
	// ErrIntentNotApproved marks a plan whose upstream verdict is not ALLOW.
	ErrIntentNotApproved = errors.New("llm intent: only an ALLOW verdict may become an intent")
	// ErrIntentInputInvalid marks a plan missing required bindings.
	ErrIntentInputInvalid = errors.New("llm intent: the plan is missing required fields")
	// ErrIntentPriceOutOfBounds marks a limit price outside the book plus slippage.
	ErrIntentPriceOutOfBounds = errors.New("llm intent: the limit price is outside the allowed book range")
	// ErrIntentExpired marks an authorisation that is too old to submit.
	ErrIntentExpired = errors.New("llm intent: the intent is stale for submission")
	// ErrIntentClaimLost marks a claim that another writer (or a crash scan)
	// already took.
	ErrIntentClaimLost = errors.New("llm intent: the intent claim was lost")
)

// IntentPlan is the reviewed material one intent is built from.
type IntentPlan struct {
	DecisionRunID     string
	RiskEvaluationID  string
	StrategyVersionID string
	ApprovalID        string
	MarketID          string
	Action            candidate.Action
	// ReduceSide names the existing position for a REDUCE intent ("LONG" or
	// "SHORT"); ignored for opens.
	ReduceSide string
	// OrderType is LIMIT or MARKET; STOP_MARKET is the protective exit and is
	// prepared separately against its parent intent.
	OrderType    string
	EnvelopeHash string
	PairEpoch    int64
	Risk         *RiskResult
	Sizing       *SizingResult

	// LIMIT price material (§5.4): the model's suggested entry, rounded to the
	// tick and bounded by the book plus the approved slippage.
	SuggestedEntry string
	TickSize       string
	BestBid        string
	BestAsk        string
	MaxSlippageBPS *int

	// ClientOrderID is an explicit idempotency key; empty mints a fresh one.
	ClientOrderID string
	// NewID mints the intent id; the recorder supplies its own when empty.
	NewID func() string
	// ExpiresIn overrides the 10s default; capped at IntentMaxExpiry.
	ExpiresIn time.Duration
}

// IntentRecorder writes prepared intents and their audit events.
type IntentRecorder struct {
	Intents iIntentStore
	Audit   iAuditEvents
	Now     func() time.Time
	NewID   func() string
}

// iIntentStore is the narrow write seam; LLMIntentStore adapts the repository.
type iIntentStore interface {
	InsertOrderIntent(ctx context.Context, o *llmstore.OrderIntent) error
}

// LLMIntentStore is the production adapter over database/repository/llm.
type LLMIntentStore struct{}

// InsertOrderIntent appends one intent row.
func (LLMIntentStore) InsertOrderIntent(ctx context.Context, o *llmstore.OrderIntent) error {
	return llmstore.InsertOrderIntent(ctx, o)
}

// BuildIntent turns a plan into the immutable intent row (pure function).
func BuildIntent(in *IntentPlan, now time.Time) (*llmstore.OrderIntent, error) {
	if in.Risk == nil || in.Risk.Verdict != RiskVerdictAllow {
		return nil, fmt.Errorf("%w: only an ALLOW risk verdict may become an intent", ErrIntentNotApproved)
	}
	if in.Sizing == nil || in.Sizing.Verdict != RiskVerdictAllow {
		return nil, fmt.Errorf("%w: only an ALLOW sizing result may become an intent", ErrIntentNotApproved)
	}
	if in.StrategyVersionID == "" || in.DecisionRunID == "" || in.ApprovalID == "" || in.EnvelopeHash == "" {
		return nil, fmt.Errorf("%w: decision, approval and envelope bindings are required", ErrIntentInputInvalid)
	}
	exchange, asset, pair, err := splitIntentMarketID(in.MarketID)
	if err != nil {
		return nil, err
	}
	side, reduceOnly, err := intentSide(in.Action, in.ReduceSide)
	if err != nil {
		return nil, err
	}
	orderType := strings.ToUpper(strings.TrimSpace(in.OrderType))
	if orderType != OrderTypeLimit && orderType != OrderTypeMarket {
		return nil, fmt.Errorf("%w: order type %q", ErrIntentInputInvalid, in.OrderType)
	}
	if strings.TrimSpace(in.Sizing.Quantity) == "" {
		return nil, fmt.Errorf("%w: a sized quantity is required", ErrIntentInputInvalid)
	}
	if in.RiskEvaluationID == "" {
		return nil, fmt.Errorf("%w: the risk evaluation row is required", ErrIntentInputInvalid)
	}

	intent := &llmstore.OrderIntent{
		ID:                in.newIntentID(),
		DecisionRunID:     in.DecisionRunID,
		RiskEvaluationID:  in.RiskEvaluationID,
		StrategyVersionID: in.StrategyVersionID,
		ApprovalID:        in.ApprovalID,
		Exchange:          exchange,
		Pair:              pair,
		Asset:             asset,
		Action:            string(in.Action),
		Side:              side,
		OrderType:         orderType,
		Quantity:          in.Sizing.Quantity,
		ReduceOnly:        reduceOnly,
		ClientOrderID:     in.clientOrderID(),
		NotionalUSD:       in.Sizing.NotionalUSD,
		EnvelopeHash:      in.EnvelopeHash,
		PairEpoch:         in.PairEpoch,
		Status:            IntentStatusPrepared,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if orderType == OrderTypeLimit {
		price, err := limitPrice(in, side)
		if err != nil {
			return nil, err
		}
		intent.Price = &price
	}
	expiry := in.ExpiresIn
	if expiry <= 0 {
		expiry = IntentDefaultExpiry
	}
	if expiry > IntentMaxExpiry {
		expiry = IntentMaxExpiry
	}
	intent.ExpiresAt = now.Add(expiry)
	return intent, nil
}

// Record stores the intent and its audit event, in that order; a failure aborts
// the chain so nothing unaudited can ever reach execution.
func (r *IntentRecorder) Record(ctx context.Context, plan *IntentPlan, actor string) (*llmstore.OrderIntent, error) {
	if r == nil || r.Intents == nil || r.Audit == nil || r.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	if plan.NewID == nil {
		plan.NewID = r.NewID
	}
	intent, err := BuildIntent(plan, now)
	if err != nil {
		return nil, err
	}
	if err := r.Intents.InsertOrderIntent(ctx, intent); err != nil {
		return nil, fmt.Errorf("llm intent: inserting: %w", err)
	}
	detail, err := json.Marshal(map[string]any{
		"intentId":      intent.ID,
		"decisionRunId": intent.DecisionRunID,
		"clientOrderId": intent.ClientOrderID,
		"marketId":      plan.MarketID,
		"action":        intent.Action,
		"side":          intent.Side,
		"orderType":     intent.OrderType,
		"quantity":      intent.Quantity,
		"notionalUsd":   intent.NotionalUSD,
		"expiresAt":     intent.ExpiresAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("llm intent: audit detail: %w", err)
	}
	subject := intent.ID
	if err := r.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID:         r.NewID(),
		Kind:       AuditKindIntentPrepared,
		Actor:      actor,
		Subject:    &subject,
		DetailJSON: string(detail),
		CreatedAt:  now,
	}); err != nil {
		return nil, fmt.Errorf("llm intent: audit event: %w", err)
	}
	return intent, nil
}

// newIntentID mints the intent id from the plan's generator.
func (in *IntentPlan) newIntentID() string {
	if in.NewID != nil {
		return in.NewID()
	}
	return newLLMIdentifier()
}

func (in *IntentPlan) clientOrderID() string {
	if in.ClientOrderID != "" {
		if strings.HasPrefix(in.ClientOrderID, IntentClientOrderIDPrefix) {
			return in.ClientOrderID
		}
		return IntentClientOrderIDPrefix + in.ClientOrderID
	}
	return IntentClientOrderIDPrefix + newLLMIdentifier()
}

// splitIntentMarketID parses "Exchange:asset:PAIR" into its three parts.
func splitIntentMarketID(marketID string) (exchangeName, assetType, pairText string, err error) {
	parts := strings.Split(marketID, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("%w: market id %q must be exchange:asset:pair", ErrIntentInputInvalid, marketID)
	}
	return parts[0], parts[1], parts[2], nil
}

// intentSide maps the action to the venue side (Buy/Sell only) and marks
// reduce-only.
func intentSide(action candidate.Action, reduceSide string) (side string, reduceOnly bool, err error) {
	switch action {
	case candidate.ActionOpenLong:
		return "BUY", false, nil
	case candidate.ActionOpenShort:
		return orderSideSell, false, nil
	case candidate.ActionReduce:
		switch strings.ToUpper(strings.TrimSpace(reduceSide)) {
		case PositionSideLong:
			return orderSideSell, true, nil
		case PositionSideShort:
			return "BUY", true, nil
		}
		return "", false, fmt.Errorf("%w: a REDUCE intent needs the existing position side", ErrIntentInputInvalid)
	}
	return "", false, fmt.Errorf("%w: action %q creates no order", ErrIntentInputInvalid, action)
}

// limitPrice derives the executable price (§5.4): the suggested entry rounded
// conservatively to the tick (buys floor, sells ceil) and bounded by the book
// plus the approved slippage.
func limitPrice(in *IntentPlan, side string) (string, error) {
	return limitPriceFor(in.SuggestedEntry, in.TickSize, side, in.MaxSlippageBPS, in.BestBid, in.BestAsk)
}

// limitPriceFor is the §5.4 computation on explicit material — shared by the
// intent builder and the risk stage's order facts so both evaluate exactly the
// same price.
func limitPriceFor(suggestedEntry, tickSize, side string, maxSlippageBPS *int, bestBid, bestAsk string) (string, error) {
	suggested, err := decParse(suggestedEntry)
	if err != nil {
		return "", fmt.Errorf("%w: suggested entry %q", ErrIntentInputInvalid, suggestedEntry)
	}
	price := suggested
	if tick, err := decParse(tickSize); err == nil && !tick.IsZero() {
		quotient := suggested.Div(tick)
		if side == orderSideSell {
			price = quotient.Ceil().Mul(tick)
		} else {
			price = quotient.Floor().Mul(tick)
		}
	}
	if maxSlippageBPS != nil {
		slipFactor := decimal.NewFromInt(int64(*maxSlippageBPS)).Div(decimal.NewFromInt(10000))
		if side == orderSideSell {
			bestBidValue, err := decParse(bestBid)
			if err != nil {
				return "", fmt.Errorf("%w: best bid %q", ErrIntentInputInvalid, bestBid)
			}
			floor := bestBidValue.Mul(decimal.NewFromInt(1).Sub(slipFactor))
			if price.LessThan(floor) {
				return "", fmt.Errorf("%w: sell price %s below best bid minus slippage %s", ErrIntentPriceOutOfBounds, price, floor)
			}
		} else {
			bestAskValue, err := decParse(bestAsk)
			if err != nil {
				return "", fmt.Errorf("%w: best ask %q", ErrIntentInputInvalid, bestAsk)
			}
			ceil := bestAskValue.Mul(decimal.NewFromInt(1).Add(slipFactor))
			if price.GreaterThan(ceil) {
				return "", fmt.Errorf("%w: buy price %s above best ask plus slippage %s", ErrIntentPriceOutOfBounds, price, ceil)
			}
		}
	}
	return price.String(), nil
}
