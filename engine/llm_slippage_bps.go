package engine

import (
	"fmt"
	"math"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M4-11 装配件 · §5.2 D 滑点估算（bps）。
//
// 检查 `RiskSlippageExceeded` 把 `EstimatedSlippageBPS` 与审批的
// `maxSlippageBps` 比大小——估算本体属装配（它持有盘口走价与名义额上界链，
// llm_risk_market_facts.go:111 原注）。口径：
//
//   - `side` 用走价侧词汇（"ask" 买入吃卖盘 / "bid" 卖出吃买盘），与
//     `sizingWalkFromDepth` 同一拼写；
//   - 按**审批名义额上界**走价（§5.3 第 4 步参考价的语义：买入 ask 侧按
//     notionalCap 的成交均价），与最优档比较：(成交均价 − 最优档) ÷ 最优档
//     × 10000；
//   - **向上取整**：检查是「超过阈值即拒」，高报是 fail-closed 方向；
//   - 走价落到最优档的**错误一侧**（ask 低于最优卖价 / bid 高于最优买价）是
//     盘口不一致而不是捡便宜：拒绝而不是报 0（0 会静默通过检查）；最优档
//     缺失/非正、走价不可读同样拒绝。
func estimatedSlippageBPS(walk func(side, notionalUSD string) (string, error), side, notionalUSD, bestQuote string) (int, error) {
	if walk == nil {
		return 0, fmt.Errorf("%w: the walk is required to estimate slippage", ErrRiskFactsInput)
	}
	avgText, err := walk(side, notionalUSD)
	if err != nil {
		return 0, fmt.Errorf("llm slippage: walking the book: %w", err)
	}
	if _, err := decParse(avgText); err != nil {
		return 0, fmt.Errorf("%w: the walk returned an unreadable price %q", ErrRiskFactsInput, avgText)
	}
	if _, err := decParse(bestQuote); err != nil {
		return 0, fmt.Errorf("%w: the best quote %q is unreadable", ErrRiskFactsInput, bestQuote)
	}
	nonPositive, err := decLE(bestQuote, "0")
	if err != nil {
		return 0, fmt.Errorf("%w: the best quote %q is unreadable", ErrRiskFactsInput, bestQuote)
	}
	if nonPositive {
		return 0, fmt.Errorf("%w: the best quote %q is not positive", ErrRiskFactsInput, bestQuote)
	}
	avg, _ := decParse(avgText)
	best, _ := decParse(bestQuote)
	var diff decimal.Decimal
	switch strings.ToLower(strings.TrimSpace(side)) {
	case sizingSideAsk:
		// Buying lifts the asks: the average fill can only sit at or above
		// the best ask.
		atLeastBest, err := decLE(bestQuote, avgText)
		if err != nil {
			return 0, fmt.Errorf("%w: the walk's price %q is unreadable", ErrRiskFactsInput, avgText)
		}
		if !atLeastBest {
			return 0, fmt.Errorf("%w: the walk priced %q better than the best ask %q", ErrRiskFactsInput, avgText, bestQuote)
		}
		diff = avg.Sub(best)
	case sizingSideBid:
		// Selling hits the bids: the average fill can only sit at or below
		// the best bid.
		atMostBest, err := decLE(avgText, bestQuote)
		if err != nil {
			return 0, fmt.Errorf("%w: the walk's price %q is unreadable", ErrRiskFactsInput, avgText)
		}
		if !atMostBest {
			return 0, fmt.Errorf("%w: the walk priced %q above the best bid %q", ErrRiskFactsInput, avgText, bestQuote)
		}
		diff = best.Sub(avg)
	default:
		return 0, fmt.Errorf("%w: side %q cannot estimate slippage", ErrRiskFactsInput, side)
	}
	ratio := diff.Div(best)
	bps := math.Ceil(ratio.Mul(decimal.NewFromInt(10000)).InexactFloat64())
	return int(bps), nil
}
