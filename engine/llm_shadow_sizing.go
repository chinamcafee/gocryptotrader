package engine

import (
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M4-12 装配件 · 影子运行的 SizingInput（`RiskFacts` 闭包返回元组第二件）。
//
// 只装配开仓——平仓数量归退出策略（07 §7），闭包对 REDUCE 传 nil 即可；
// 本件对非开仓动作直接拒绝，以免调用方把退出误送进 §5.3 的风险上限链。
// 三块材料各有单一口径，全部复用既有件、不写第二份拼写：
//
//   - `ExistingExposureUSD`：`existingExposure(positions)` 的保守名义额
//     （max(entry,mark)×qty）——与 C 组敞口检查同一句式，账实相加只有一份；
//     读不出即拒（空串是它的「不可读」形状）；
//   - `DailyLossUSD`：`DayStartService.DailyLoss` 的产物（调用方传入——
//     服务实例挂在 bot 上，本件不持有）；空串=不可读，拒绝；
//   - `StopDistancePerUnitUSD`：`ProtectiveStopPrice`（Q8b 公式 + 向远离市场
//     取整）算出的止损价再取 |entry − stop|——与真实挂出的保护性止损同一句
//     话；缺 ATR/锚价/tick 各自拒绝（没有距离就没有第 3 步反推的最坏情形上界）。
func buildShadowSizingInput(in *ShadowSizingMaterial) (*SizingInput, error) {
	if in == nil || in.Candidate == nil || in.Snapshot == nil {
		return nil, fmt.Errorf("%w: the sizing material is incomplete", ErrRiskFactsInput)
	}
	if in.Walk == nil {
		return nil, fmt.Errorf("%w: the book walk is required to size", ErrRiskFactsInput)
	}
	var positionSide string
	switch in.Candidate.Action {
	case candidate.ActionOpenLong:
		positionSide = PositionSideLong
	case candidate.ActionOpenShort:
		positionSide = PositionSideShort
	default:
		return nil, fmt.Errorf("%w: action %q is not sized by the allowed chain", ErrRiskFactsInput, in.Candidate.Action)
	}
	entry := strings.TrimSpace(in.Candidate.SuggestedEntry)
	if entry == "" {
		return nil, fmt.Errorf("%w: the candidate carries no entry to anchor the stop distance", ErrRiskFactsInput)
	}
	if in.Snapshot.Indicators.ATR == nil || strings.TrimSpace(in.Snapshot.Indicators.ATR.Latest) == "" {
		return nil, fmt.Errorf("%w: the ATR reading is required for the stop distance", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.DailyLossUSD) == "" {
		return nil, fmt.Errorf("%w: the daily loss reading is required", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.Contract.TickSize) == "" {
		return nil, fmt.Errorf("%w: the contract tick size is required to round the stop", ErrRiskFactsInput)
	}
	exposure := existingExposure(in.Positions)
	if exposure == "" {
		return nil, fmt.Errorf("%w: the position exposure is unreadable", ErrRiskFactsInput)
	}
	stop, err := ProtectiveStopPrice(positionSide, entry, in.Snapshot.Indicators.ATR.Latest, in.Contract.TickSize)
	if err != nil {
		return nil, fmt.Errorf("llm shadow sizing: stop distance: %w", err)
	}
	var distance decimal.Decimal
	if positionSide == PositionSideLong {
		distance, err = decSub(entry, stop)
	} else {
		distance, err = decSub(stop, entry)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: the stop distance is unreadable", ErrRiskFactsInput)
	}
	return &SizingInput{
		Candidate:              in.Candidate,
		Envelope:               in.Envelope,
		ExistingExposureUSD:    exposure,
		DailyLossUSD:           in.DailyLossUSD,
		StopDistancePerUnitUSD: distance.String(),
		Contract:               in.Contract,
		Walk:                   in.Walk,
	}, nil
}

// ShadowSizingMaterial is what the closure raises for the sizing input.
type ShadowSizingMaterial struct {
	// Envelope is the approval's frozen risk envelope (policy.Risk).
	Envelope candidate.RiskEnvelope
	// Candidate is the synthesised candidate under evaluation (opens only).
	Candidate *candidate.Candidate
	// Snapshot is the run's assembled snapshot; its ATR anchors the stop.
	Snapshot *snapshot.MarketSnapshot
	// Positions are the account facts' positions (the exposure basis the risk
	// engine consumed).
	Positions []RiskPositionFact
	// Contract is the venue contract spec (tick/step/minimums).
	Contract RiskContractSpec
	// Walk is the order book's walk (production: sizingWalkFromDepth).
	Walk func(side, notionalUSD string) (string, error)
	// DailyLossUSD is DayStartService.DailyLoss's reading (positive = loss).
	DailyLossUSD string
}
