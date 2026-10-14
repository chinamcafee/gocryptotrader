package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-11/16 装配件 · 影子运行 RiskInput 组装（闭包元组第一件）。
//
// 十四个字段里十三个直接来自已落地的映射件——本函数是它们唯一汇合点，
// 并做两件组装期才有的决定：
//
//   - `CheckVersion`：由代码常量盖章（`RiskCheckVersion`），不由调用方
//     传入——检查表的版本号是编译期事实，不该有任何外部输入面；
//   - `Order`：**以参数进入**——`RiskOrderFacts` 的定型压在单型决策（02）
//     上（侦察第 6 条：不许编造），调用方拍板后构造、本件只负责合流。
//
// 其余字段（Authorization/Data/Account/Positions/Frequency/Contract/Market/
// System/Switches）要求齐备：任一为「没装」的零值都是半套放行——分别由各自
// 映射件的拒绝径保证非零，本件只守「材料要齐」这一道。
func buildShadowRiskInput(in *ShadowRiskInputMaterial) (*RiskInput, error) {
	if in == nil || in.Candidate == nil {
		return nil, fmt.Errorf("%w: the risk material needs the candidate", ErrRiskFactsInput)
	}
	if in.Now.IsZero() {
		return nil, fmt.Errorf("%w: the risk material needs an as-of instant", ErrRiskFactsInput)
	}
	if strings.TrimSpace(in.Contract.TickSize) == "" || strings.TrimSpace(in.Contract.StepSize) == "" {
		return nil, fmt.Errorf("%w: the contract spec is required", ErrRiskFactsInput)
	}
	return &RiskInput{
		CheckVersion:  RiskCheckVersion,
		Now:           in.Now,
		Candidate:     in.Candidate,
		Envelope:      in.Envelope,
		Authorization: in.Authorization,
		Data:          in.Data,
		Account:       in.Account,
		Positions:     in.Positions,
		Frequency:     in.Frequency,
		Contract:      in.Contract,
		Order:         in.Order,
		Market:        in.Market,
		System:        in.System,
		Switches:      in.Switches,
	}, nil
}

// ShadowRiskInputMaterial is the closure's raised material for the risk input.
type ShadowRiskInputMaterial struct {
	Now           time.Time
	Candidate     *candidate.Candidate
	Envelope      candidate.RiskEnvelope
	Authorization RiskAuthorization
	Data          RiskDataFacts
	Account       RiskAccountFacts
	Positions     []RiskPositionFact
	Frequency     RiskFrequencyFacts
	Contract      RiskContractSpec
	// Order is the 02-gated parameter: the prospective order's facts are
	// pinned by the execution-type decision (decisions/02).
	Order  RiskOrderFacts
	Market RiskMarketFacts
	System RiskSystemFacts
	// Switches come straight from RiskSwitchService.Current().
	Switches RiskSwitches
}
