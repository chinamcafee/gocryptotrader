package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5-01 生产装配 · IntentPlan 生产者（决策→订单桥的第一件）。
//
// 一次决策链路跑到"该下单了"时（风控与重算双 ALLOW），需要把**已落库的
// 决策记录 + 候选 + 行情材料 + 审批绑定**组装成 `IntentPlan`，交给
// `IntentRecorder.Record`（`BuildIntent` 纯函数 + 落库 + 审计）。本文件只做
// 组装与**急切校验**，让坏材料在离库最远的地方就被拒：
//
//   - **三重 ALLOW 前置在生成期再核**：风控 ALLOW、重算 ALLOW、动作可出单
//     ——ALLOW 的唯一产地是硬风控，生产者不解释、不修补、不降级；
//   - **OrderType 由执行策略（调用方）给定**：LIMIT 要求 §5.4 材料齐全
//     （suggestedEntry / tick / bestBid / bestAsk——`limitPrice` 在落库期
//     用它做 tick 取整与盘口区间校验）；MARKET 不要求价材；STOP_MARKET
//     属保护性止损、针对父意图另行准备——这里拒；
//   - **MarketID 必须是三段**（`exchange:asset:BASE_QUOTE`）：生成期就用
//     `splitIntentMarketID` 校验，不让坏市场号走到落库才炸；
//   - 平仓（`ActionReduce`）必须点名现有仓位方向（LONG/SHORT）并统一大写。
//
// 生产者**不决定**订单类型与价格材料：它们是执行策略与同一张快照的产物；
// 生产者只保证"送上来的材料是完整且自洽的"。

// IntentPlanInput is the reviewed material one plan is assembled from.
type IntentPlanInput struct {
	// Record is the persisted decision and its risk evaluation row.
	Record *DecisionRecordResult
	// Risk and Sizing are the live verdicts; both must be ALLOW.
	Risk   *RiskResult
	Sizing *SizingResult
	// Synthesis carries the candidate the plan acts on.
	Synthesis *SynthesisOutcome
	// ApprovalID / StrategyVersionID / EnvelopeHash / PairEpoch bind the
	// intent to its authorisation.
	ApprovalID        string
	StrategyVersionID string
	EnvelopeHash      string
	PairEpoch         int64
	// MarketID is the three-segment identifier (exchange:asset:BASE_QUOTE).
	MarketID string
	// OrderType is the execution policy's choice: LIMIT or MARKET.
	OrderType string
	// LIMIT price material, from the same snapshot the decision ran on (§5.4).
	SuggestedEntry string
	TickSize       string
	BestBid        string
	BestAsk        string
	MaxSlippageBPS *int
	// ReduceSide names the existing position for a reduce plan (LONG/SHORT);
	// ignored for opens.
	ReduceSide string
	// ClientOrderID is an explicit idempotency key; empty mints one at record
	// time. ExpiresIn overrides the default validity.
	ClientOrderID string
	ExpiresIn     time.Duration
	NewID         func() string
}

// BuildIntentPlan assembles the reviewed material into an IntentPlan.
func BuildIntentPlan(in *IntentPlanInput) (*IntentPlan, error) {
	if in == nil {
		return nil, ErrIntentInputInvalid
	}
	if in.Record == nil || strings.TrimSpace(in.Record.DecisionRunID) == "" ||
		strings.TrimSpace(in.Record.RiskEvaluationID) == "" {
		return nil, fmt.Errorf("%w: the decision and its risk evaluation row are required", ErrIntentInputInvalid)
	}
	riskVerdict := ""
	if in.Risk != nil {
		riskVerdict = in.Risk.Verdict
	}
	if riskVerdict != RiskVerdictAllow {
		return nil, fmt.Errorf("%w: risk verdict %q", ErrIntentNotApproved, riskVerdict)
	}
	sizingVerdict := ""
	if in.Sizing != nil {
		sizingVerdict = in.Sizing.Verdict
	}
	if sizingVerdict != RiskVerdictAllow {
		return nil, fmt.Errorf("%w: sizing verdict %q", ErrIntentNotApproved, sizingVerdict)
	}
	if in.Synthesis == nil || in.Synthesis.Candidate == nil {
		return nil, fmt.Errorf("%w: no candidate to act on", ErrIntentInputInvalid)
	}
	action := in.Synthesis.Candidate.Action
	switch action {
	case candidate.ActionOpenLong, candidate.ActionOpenShort, candidate.ActionReduce:
	default:
		return nil, fmt.Errorf("%w: action %q creates no order", ErrIntentInputInvalid, action)
	}
	if strings.TrimSpace(in.StrategyVersionID) == "" || strings.TrimSpace(in.ApprovalID) == "" ||
		strings.TrimSpace(in.EnvelopeHash) == "" {
		return nil, fmt.Errorf("%w: the strategy version, approval and envelope hash are required", ErrIntentInputInvalid)
	}
	if _, _, _, err := splitIntentMarketID(in.MarketID); err != nil {
		return nil, err
	}
	orderType := strings.ToUpper(strings.TrimSpace(in.OrderType))
	switch orderType {
	case OrderTypeLimit:
		for name, value := range map[string]string{
			"suggestedEntry": in.SuggestedEntry,
			"tickSize":       in.TickSize,
			"bestBid":        in.BestBid,
			"bestAsk":        in.BestAsk,
		} {
			if strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%w: a LIMIT plan needs its %s", ErrIntentInputInvalid, name)
			}
		}
	case OrderTypeMarket:
	case OrderTypeStopMarket:
		return nil, fmt.Errorf("%w: %s is the protective exit and is prepared against its parent intent", ErrIntentInputInvalid, OrderTypeStopMarket)
	default:
		return nil, fmt.Errorf("%w: order type %q", ErrIntentInputInvalid, in.OrderType)
	}
	reduceSide := ""
	if action == candidate.ActionReduce {
		reduceSide = strings.ToUpper(strings.TrimSpace(in.ReduceSide))
		if reduceSide != PositionSideLong && reduceSide != PositionSideShort {
			return nil, fmt.Errorf("%w: a reduce plan needs the existing position side", ErrIntentInputInvalid)
		}
	}
	return &IntentPlan{
		DecisionRunID:     in.Record.DecisionRunID,
		RiskEvaluationID:  in.Record.RiskEvaluationID,
		StrategyVersionID: in.StrategyVersionID,
		ApprovalID:        in.ApprovalID,
		MarketID:          in.MarketID,
		Action:            action,
		ReduceSide:        reduceSide,
		OrderType:         orderType,
		EnvelopeHash:      in.EnvelopeHash,
		PairEpoch:         in.PairEpoch,
		Risk:              in.Risk,
		Sizing:            in.Sizing,
		SuggestedEntry:    in.SuggestedEntry,
		TickSize:          in.TickSize,
		BestBid:           in.BestBid,
		BestAsk:           in.BestAsk,
		MaxSlippageBPS:    in.MaxSlippageBPS,
		ClientOrderID:     in.ClientOrderID,
		NewID:             in.NewID,
		ExpiresIn:         in.ExpiresIn,
	}, nil
}
