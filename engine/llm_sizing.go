package engine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M4-12 数量重算（docs 07 §5.3，Q13 定案 = A）。
//
// 模型给的 proposedMaxNotionalUsd 只是上界建议，不是下单数量。本层把它与
// 审批上限、剩余敞口、剩余日损额度逐级取 min，用当前盘口的可成交价（不是
// 最新成交价）折算数量，**按 step size 向下取整后再减一个容差 step**
// （Q13：float64 表示 0.001 有微小误差，减一个 step 保证转换后一定 ≤ 目标），
// 再用实际名义额重跑一遍滑点检查。
//
// 本函数只处理**新增风险的开仓**：§5.3 的九步全是一根风险上限链。平仓的数量
// 由退出策略决定（07 §7），不走这条链。float64 转换与转换后重校验属于订单落地
// （Q13 的交付端），不在本层。

// SizingCaps records how each cap limited the size, for the audit trail.
type SizingCaps struct {
	ByCandidateBound     string `json:"byCandidateBound"`
	ByApprovedOrderCap   string `json:"byApprovedOrderCap"`
	ByRemainingExposure  string `json:"byRemainingExposure"`
	ByRemainingDailyLoss string `json:"byRemainingDailyLoss"`
}

// SizingInput is everything the recomputation needs. The exposure and daily-loss
// facts are the same decimals the risk engine consumed; the stop distance is
// what bounds this order's worst case (ATR- or percent-derived by the exit
// policy — sizing cannot bound the loss without it).
type SizingInput struct {
	Candidate              *candidate.Candidate
	Envelope               candidate.RiskEnvelope
	ExistingExposureUSD    string
	DailyLossUSD           string
	StopDistancePerUnitUSD string
	Contract               RiskContractSpec
	// Walk reports the average fillable price for a notional on the "ask" or
	// "bid" side. It is the order book's walk, injected so this module stays a
	// pure function of its inputs; production reuses the orderbook movement
	// logic, tests script it.
	Walk func(side, notionalUSD string) (string, error)
}

// SizingResult is the recomputed order. On refusal Verdict is RiskVerdictReject
// and Rejection says which limit refused, with actual vs allowed.
type SizingResult struct {
	Verdict        string         `json:"verdict"`
	Quantity       string         `json:"quantity,omitempty"`
	ReferencePrice string         `json:"referencePrice,omitempty"`
	NotionalUSD    string         `json:"notionalUsd,omitempty"`
	Side           string         `json:"side,omitempty"`
	Caps           SizingCaps     `json:"caps"`
	Rejection      *RiskRejection `json:"rejection,omitempty"`
}

func sizingReject(r *SizingResult, code, message, actual, allowed, unit, field string) SizingResult {
	r.Verdict = RiskVerdictReject
	r.Rejection = reject(code, message, actual, allowed, unit, field)
	return *r
}

// ComputeQuantity executes the nine steps of §5.3.
func ComputeQuantity(in *SizingInput) SizingResult {
	out := SizingResult{Verdict: RiskVerdictAllow}
	if in.Candidate == nil || in.Walk == nil {
		return sizingReject(&out, RiskInputInvalid, "a candidate and a book walk are required to size an order", "missing", "candidate and book walk", "", "candidate")
	}
	side, ok := sizingSide(in.Candidate.Action)
	if !ok {
		return sizingReject(&out, RiskInputInvalid, "only opening orders are sized by the allowed chain; exits are sized by the exit policy", string(in.Candidate.Action), "OPEN_LONG or OPEN_SHORT", "", "action")
	}
	out.Side = side

	// 1. notionalCap = min(candidate bound, approved cap).
	proposed := strings.TrimSpace(in.Candidate.ProposedMaxNotionalUSD)
	if proposed == "" || in.Envelope.MaxOrderNotionalUSD == nil {
		return sizingReject(&out, RiskOrderNotionalMissing, "the order cannot be sized: a notional bound is missing", "missing", "both the candidate bound and the approved allowed", "USD", "maxOrderNotionalUsd")
	}
	out.Caps.ByCandidateBound = proposed
	out.Caps.ByApprovedOrderCap = *in.Envelope.MaxOrderNotionalUSD
	allowed := minDecimal(proposed, *in.Envelope.MaxOrderNotionalUSD)

	// 2. Remaining exposure.
	if in.Envelope.MaxTotalExposureUSD == nil {
		return sizingReject(&out, RiskEnvelopeIncomplete, "the envelope carries no exposure allowed", "missing", "maxTotalExposureUsd", "USD", "maxTotalExposureUsd")
	}
	remainingExposure, err := decSub(*in.Envelope.MaxTotalExposureUSD, in.ExistingExposureUSD)
	if err != nil {
		return sizingReject(&out, RiskTotalExposureExceeded, "the exposure facts are unreadable", in.ExistingExposureUSD, *in.Envelope.MaxTotalExposureUSD, "USD", "maxTotalExposureUsd")
	}
	out.Caps.ByRemainingExposure = remainingExposure.String()
	if remainingExposure.LessThanOrEqual(decimal.Zero) {
		return sizingReject(&out, RiskTotalExposureExceeded, "no exposure headroom remains", in.ExistingExposureUSD, *in.Envelope.MaxTotalExposureUSD, "USD", "maxTotalExposureUsd")
	}
	allowed = minDecimal(allowed, remainingExposure.String())

	// 4 (first pass). Reference price: the fillable price for the cap so far.
	refPrice, rejection := sizingWalk(in, side, allowed, &out)
	if rejection != nil {
		return *rejection
	}

	// 3. Remaining daily-loss budget, reverse-derived through the stop distance.
	if in.Envelope.MaxDailyLossUSD == nil {
		return sizingReject(&out, RiskEnvelopeIncomplete, "the envelope carries no daily-loss allowed", "missing", "maxDailyLossUsd", "USD", "maxDailyLossUsd")
	}
	lossSoFar, err := decParse(in.DailyLossUSD)
	if err != nil {
		return sizingReject(&out, RiskDailyLossUnknown, "the current daily loss is unreadable", in.DailyLossUSD, "a parsable loss", "USD", "maxDailyLossUsd")
	}
	remainingLoss, err := decSub(*in.Envelope.MaxDailyLossUSD, lossSoFar.String())
	if err != nil {
		return sizingReject(&out, RiskDailyLossUnknown, "the daily-loss arithmetic is unreadable", in.DailyLossUSD, *in.Envelope.MaxDailyLossUSD, "USD", "maxDailyLossUsd")
	}
	if remainingLoss.LessThanOrEqual(decimal.Zero) {
		return sizingReject(&out, RiskDailyLossExceeded, "no daily-loss headroom remains", lossSoFar.String(), *in.Envelope.MaxDailyLossUSD, "USD", "maxDailyLossUsd")
	}
	stop, err := decParse(in.StopDistancePerUnitUSD)
	if err != nil || stop.IsZero() {
		return sizingReject(&out, RiskDailyLossUnknown, "the order's worst-case loss cannot be bounded: no stop distance", in.StopDistancePerUnitUSD, "a positive stop distance", "USD", "stopDistancePerUnitUsd")
	}
	priceDec, err := decParse(refPrice)
	if err != nil {
		return sizingReject(&out, RiskDailyLossUnknown, "the reference price is unreadable", refPrice, "a parsable price", "USD", "referencePrice")
	}
	notionalByLoss := remainingLoss.DivRound(stop, 18).Mul(priceDec)
	out.Caps.ByRemainingDailyLoss = notionalByLoss.String()
	allowed = minDecimal(allowed, notionalByLoss.String())

	// The loss cap may have shrunk the notional; refresh the reference price so
	// steps 5-8 use the price of the size that will actually be sent.
	refPrice, rejection = sizingWalk(in, side, allowed, &out)
	if rejection != nil {
		return *rejection
	}

	// 5-6. rawQty = cap / refPrice; floor to step, then subtract the tolerance
	// step (Q13 option A).
	step, err := decParse(in.Contract.StepSize)
	if err != nil || step.IsZero() {
		return sizingReject(&out, RiskPrecisionInvalid, "the contract step size is unreadable", in.Contract.StepSize, "a positive step size", "", "stepSize")
	}
	priceDec, err = decParse(refPrice)
	if err != nil {
		return sizingReject(&out, RiskPrecisionInvalid, "the reference price is unreadable", refPrice, "a parsable price", "USD", "referencePrice")
	}
	capDec, err := decimal.NewFromString(allowed)
	if err != nil {
		return sizingReject(&out, RiskOrderNotionalExceeded, "the capped notional is unreadable", allowed, "a parsable allowed", "USD", "notionalCap")
	}
	rawQty := capDec.Div(priceDec)
	aligned := rawQty.Div(step).Floor().Mul(step)
	qty := aligned.Sub(step)
	if qty.LessThanOrEqual(decimal.Zero) {
		return sizingReject(&out, RiskBelowMinNotional, "after alignment and the tolerance step no quantity remains", qty.String(), "a positive quantity", "", "quantity")
	}
	out.Quantity = qty.String()

	// 7. Minimums.
	if strings.TrimSpace(in.Contract.MinQuantity) != "" {
		if minQty, err := decParse(in.Contract.MinQuantity); err == nil && qty.LessThan(minQty) {
			return sizingReject(&out, RiskAmountBelowMin, "the aligned quantity is below the venue minimum", qty.String(), in.Contract.MinQuantity, "", "minQuantity")
		}
	}

	// 8. Actual notional.
	actual := qty.Mul(priceDec)
	out.NotionalUSD = actual.String()
	out.ReferencePrice = refPrice
	if strings.TrimSpace(in.Contract.MinNotionalUSD) != "" {
		if minNotional, err := decParse(in.Contract.MinNotionalUSD); err == nil && actual.LessThan(minNotional) {
			return sizingReject(&out, RiskBelowMinNotional, "the aligned notional is below the venue minimum", actual.String(), in.Contract.MinNotionalUSD, "USD", "minNotional")
		}
	}

	// 9. Re-run the slippage check with the actual notional.
	if in.Envelope.MaxSlippageBPS != nil {
		reWalkPrice, err := in.Walk(side, actual.String())
		if err != nil {
			return sizingReject(&out, RiskInsufficientDepth, "the actual notional cannot be walked on the book: "+err.Error(), actual.String(), "a fillable notional", "USD", "depth")
		}
		deviation, err := deviationBPS(reWalkPrice, refPrice)
		if err != nil {
			return sizingReject(&out, RiskSlippageExceeded, "the re-walked deviation is unreadable", reWalkPrice, strconv.Itoa(*in.Envelope.MaxSlippageBPS), "bps", "maxSlippageBps")
		}
		if deviation > float64(*in.Envelope.MaxSlippageBPS) {
			return sizingReject(&out, RiskSlippageExceeded, "the actual notional's fill price deviates too far from the reference", fmt.Sprintf("%.2f", deviation), strconv.Itoa(*in.Envelope.MaxSlippageBPS), "bps", "maxSlippageBps")
		}
	}
	return out
}

// sizingSide maps an opening action to the book side it consumes.
func sizingSide(action candidate.Action) (string, bool) {
	switch action {
	case candidate.ActionOpenLong:
		return sizingSideAsk, true
	case candidate.ActionOpenShort:
		return sizingSideBid, true
	}
	return "", false
}

// sizingWalk resolves the average fillable price, refusing when the book cannot
// fill the notional at all.
func sizingWalk(in *SizingInput, side, notional string, out *SizingResult) (string, *SizingResult) {
	price, err := in.Walk(side, notional)
	if err != nil {
		r := sizingReject(out, RiskInsufficientDepth, "the notional cannot be filled within the book: "+err.Error(), notional, "a fillable notional", "USD", "depth")
		return "", &r
	}
	if _, err := decParse(price); err != nil {
		r := sizingReject(out, RiskSlippageExceeded, "the walked price is unreadable", price, "a parsable price", "USD", "referencePrice")
		return "", &r
	}
	return price, nil
}

// decSub returns a - b as a decimal string.
func decSub(a, b string) (decimal.Decimal, error) {
	da, err := decParse(a)
	if err != nil {
		return decimal.Zero, err
	}
	db, err := decParse(b)
	if err != nil {
		return decimal.Zero, err
	}
	return da.Sub(db), nil
}
