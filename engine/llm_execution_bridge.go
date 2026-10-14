package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5 收官 · 决策→订单桥（session-53，2026-10-12/13）。
//
// 一次决策在 PAPER/TESTNET/LIVE 模式下跑完整链（风控 ALLOW + 重算 ALLOW）后，
// 本桥把「已落库的决策 + 候选 + 同一份执行材料」组装成意图并分流提交：
//
//	decision_run → BuildIntentPlan → IntentRecorder.Record → OrderDispatcher.Dispatch
//
// 结构性保证（两条都不靠约定）：
//   - **SHADOW/READ_ONLY 不提交**：模式白名单在桥头再核一次，`ShadowRunner`
//     自身没有任何提交接缝——桥挂在请求闭包上（`ShadowRunRequest.Execute`），
//     由触发器消费者在运行完成后调用。
//   - **只有全链 ALLOW 才出单**：风控或重算任一非 ALLOW 一律不出单（意图的
//     唯一产地是硬风控 `BuildIntent` 自身再验一遍）。
//
// 单型与数量（decisions/02 = B）：开仓 LIMIT（§5.4 取价）；主动平仓
// MARKET——数量取**风控阶段评估过的同一仓位事实**（全平，见
// `activeExitSizing`）；保护性止损另挂 STOP_MARKET 于父意图。

// approvedRunMaterial is everything the bridge needs to turn one completed run
// into an intent: the run's own outcomes plus the bindings the request held.
type approvedRunMaterial struct {
	Run *ShadowRunResult
	// MarketID is the three-segment run market id.
	MarketID          string
	ApprovalID        string
	StrategyVersionID string
	EnvelopeHash      string
	PairEpoch         int64
	Envelope          candidate.RiskEnvelope
	// Positions are the normalised position facts the risk stage evaluated
	// (the same material: the close prices exactly what was approved).
	Positions []RiskPositionFact
	// LIMIT price material from the same snapshot the decision ran on (§5.4).
	Contract RiskContractSpec
	BestBid  string
	BestAsk  string
	// Mode reads the effective run mode at submission time (fail-closed: a
	// downgrade between the run and here must stop the submission).
	Mode       func() string
	Recorder   *IntentRecorder
	Dispatcher *OrderDispatcher
}

// executeApprovedRun routes one fully-approved run to the order path. A nil
// or ineligible material returns (nil, nil): "nothing to execute" is the
// record-only outcome, not an error.
func executeApprovedRun(ctx context.Context, in *approvedRunMaterial) (*DispatchOutcome, error) {
	if in == nil || in.Run == nil || in.Recorder == nil || in.Dispatcher == nil {
		return nil, nil
	}
	mode := ""
	if in.Mode != nil {
		mode = in.Mode()
	}
	if !executionModePermits(mode) {
		// READ_ONLY/SHADOW: the decision is recorded and nothing is sent --
		// shadow mode's structural promise restated at the one place that
		// could break it.
		return nil, nil
	}
	if in.Run.Record == nil || strings.TrimSpace(in.Run.Record.RiskEvaluationID) == "" {
		return nil, nil
	}
	if in.Run.Risk == nil || in.Run.Risk.Verdict != RiskVerdictAllow {
		return nil, nil
	}
	if in.Run.Synthesis == nil || in.Run.Synthesis.Candidate == nil {
		return nil, nil
	}
	act := in.Run.Synthesis.Candidate
	orderType, ok := executionOrderTypeFor(act.Action)
	if !ok {
		return nil, nil // HOLD/REVIEW create no order
	}
	planInput := &IntentPlanInput{
		Record: in.Run.Record, Risk: in.Run.Risk, Synthesis: in.Run.Synthesis,
		ApprovalID: in.ApprovalID, StrategyVersionID: in.StrategyVersionID,
		EnvelopeHash: in.EnvelopeHash, PairEpoch: in.PairEpoch, MarketID: in.MarketID,
		OrderType:      orderType,
		MaxSlippageBPS: in.Envelope.MaxSlippageBPS,
	}
	switch act.Action {
	case candidate.ActionOpenLong, candidate.ActionOpenShort:
		if in.Run.Sizing == nil || in.Run.Sizing.Verdict != RiskVerdictAllow {
			return nil, nil
		}
		planInput.Sizing = in.Run.Sizing
		planInput.SuggestedEntry = act.SuggestedEntry
		planInput.TickSize = in.Contract.TickSize
		planInput.BestBid = in.BestBid
		planInput.BestAsk = in.BestAsk
	case candidate.ActionReduce:
		pos, ok := positionForMarket(in.Positions, in.MarketID)
		if !ok {
			// The risk stage's own shape checks refuse a reduce without a
			// position, so an ALLOW with no position is a broken material
			// rather than "nothing to do".
			return nil, fmt.Errorf("%w: the reduce has no evaluated position to close", ErrIntentInputInvalid)
		}
		sizing, err := activeExitSizing(&pos)
		if err != nil {
			return nil, err
		}
		planInput.Sizing = sizing
		planInput.ReduceSide = pos.Side
	default:
		return nil, nil
	}
	plan, err := BuildIntentPlan(planInput)
	if err != nil {
		return nil, err
	}
	intent, err := in.Recorder.Record(ctx, plan, auditActorSystem)
	if err != nil {
		return nil, err
	}
	return in.Dispatcher.Dispatch(ctx, intent, mode)
}

// positionForMarket finds the position a reduce closes (the run-vocabulary key
// the risk stage normalised positions to).
func positionForMarket(positions []RiskPositionFact, marketID string) (RiskPositionFact, bool) {
	for i := range positions {
		if positions[i].MarketID == marketID && !positions[i].RestingOrder {
			return positions[i], true
		}
	}
	return RiskPositionFact{}, false
}

// activeExitSizing is the minimal exit policy's product for one active close:
// the whole existing position, at the book (MARKET). The quantity and the
// conservative notional are the same facts the risk stage's order facts
// carried, so the intent closes exactly what the risk engine approved -- the
// ALLOW that gates the plan is that evaluation. (Partial exits land here when
// the candidate contract grows a fraction.) A position whose quantity or
// prices are unreadable refuses rather than closing "some" amount.
func activeExitSizing(pos *RiskPositionFact) (*SizingResult, error) {
	if pos == nil {
		return nil, fmt.Errorf("%w: the position to close is required", ErrIntentInputInvalid)
	}
	qty, err := decParse(pos.Quantity)
	if err != nil || qty.IsZero() {
		return nil, fmt.Errorf("%w: the position quantity %q is unreadable", ErrIntentInputInvalid, pos.Quantity)
	}
	price, err := decParse(pos.MarkPrice)
	if err != nil {
		return nil, fmt.Errorf("%w: the position mark price %q is unreadable", ErrIntentInputInvalid, pos.MarkPrice)
	}
	if entry, err := decParse(pos.EntryPrice); err == nil && entry.GreaterThan(price) {
		price = entry
	}
	side := sizingSideBid // closing a long sells into the bids
	if pos.Side == PositionSideShort {
		side = sizingSideAsk // closing a short buys from the asks
	}
	return &SizingResult{
		Verdict:        RiskVerdictAllow,
		Quantity:       pos.Quantity,
		NotionalUSD:    price.Mul(qty).String(),
		ReferencePrice: pos.MarkPrice,
		Side:           side,
	}, nil
}

// executionModePermits reports whether the mode may submit orders at all
// (07 §6.2's whitelist): READ_ONLY and SHADOW never do.
func executionModePermits(mode string) bool {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive:
		return true
	default:
		return false
	}
}

// executionOrderTypeFor is decisions/02's execution policy (2026-10-12):
// opens rest at the suggested entry (LIMIT, §5.4), active exits take the book
// (MARKET). The protective stop stays STOP_MARKET against its parent intent.
// The second return is false for the actions that create no order
// (HOLD/REVIEW).
func executionOrderTypeFor(action candidate.Action) (string, bool) {
	switch action {
	case candidate.ActionOpenLong, candidate.ActionOpenShort:
		return OrderTypeLimit, true
	case candidate.ActionReduce:
		return OrderTypeMarket, true
	default:
		return "", false
	}
}
