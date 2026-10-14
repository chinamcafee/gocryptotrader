package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M4-11 装配件 · 快照 → 硬风控 D 组（市场上下文）映射。
//
// 能派生的派生、能读的读，**读不到/派生不了的字段显式输入，禁发明**：
//
//   - `BestBid`/`BestAsk`：优先盘口最优档（spread/滑点都从盘口长出来），
//     盘口缺失时回落到行情 Ticker 的 bid/ask——两者都是市场自身读数；
//   - `LastPrice`：Ticker.Last；
//   - `MarkPrice`/`IndexPrice`：**库中与快照中都没有来源**——显式输入
//     （交易所侧 mark/index 读取属装配，绝不在这里编数）；
//   - `EstimatedSlippageBPS`：按名义额的盘口走价估算属装配（依赖 walk 与
//     名义额上界链）——显式输入，防两个地方各算一份；
//   - `QuoteVolumeRank`：全宇宙成交量排名的计算面未有——显式输入（≤0 时
//     风控检查自己按"unknown"拒，见 RiskMarketLowLiquidity）；
//   - `FundingRequired`：策略/审批侧的意图——显式输入；`FundingRate`/
//     `MinutesToFundingSettlement` 来自 Funding 事实；要求资金费却没读数时
//     留 nil（检查自己按 unreadable 拒——`RiskFundingExtreme`）；
//   - `ATRPercentOfPrice`：从指标派生（ATR.Latest ÷ LastPrice）——**读不到
//     ATR 或锚价即报错**：波动率闸开着时 0 会静默放行，那是 fail-open。
func riskMarketFactsFromSnapshot(snap *snapshot.MarketSnapshot, in *RiskMarketFactsInput) (*RiskMarketFacts, error) {
	if snap == nil {
		return nil, fmt.Errorf("%w: the snapshot is required", ErrRiskFactsInput)
	}
	if in == nil {
		return nil, fmt.Errorf("%w: the venue-side market material is required", ErrRiskFactsInput)
	}
	last := ""
	if snap.Ticker != nil {
		last = snap.Ticker.Last
	}
	atrPercent, err := atrPercentOfPrice(snap, last)
	if err != nil {
		return nil, err
	}
	bestBid, bestAsk := "", ""
	if snap.OrderBook != nil && len(snap.OrderBook.Bids) > 0 && len(snap.OrderBook.Asks) > 0 {
		// The book's top levels are the authoritative executable prices.
		bestBid, bestAsk = snap.OrderBook.Bids[0].Price, snap.OrderBook.Asks[0].Price
	} else if snap.Ticker != nil {
		// A narrower snapshot has no book; the ticker's touch is the next-best
		// market reading. Neither present leaves the fields empty and the
		// spread/slippage checks fail closed on their own.
		bestBid, bestAsk = snap.Ticker.Bid, snap.Ticker.Ask
	}
	facts := &RiskMarketFacts{
		BestBid:                    bestBid,
		BestAsk:                    bestAsk,
		LastPrice:                  last,
		MarkPrice:                  in.MarkPrice,
		IndexPrice:                 in.IndexPrice,
		EstimatedSlippageBPS:       in.EstimatedSlippageBPS,
		DepthAvailable:             snap.OrderBook != nil,
		FundingRequired:            in.FundingRequired,
		QuoteVolumeRank:            in.QuoteVolumeRank,
		ATRPercentOfPrice:          atrPercent,
		FundingRate:                nil,
		MinutesToFundingSettlement: 0,
	}
	if snap.Funding != nil {
		rate := snap.Funding.LatestRate
		facts.FundingRate = &rate
		if !snap.Funding.NextFundingTime.IsZero() && !in.Now.IsZero() {
			facts.MinutesToFundingSettlement = max(int(snap.Funding.NextFundingTime.Sub(in.Now).Minutes()), 0)
		}
	}
	return facts, nil
}

// atrPercentOfPrice derives ATR ÷ price. Both readings are required: a
// volatility gate evaluating zero would silently pass an unmeasured market.
func atrPercentOfPrice(snap *snapshot.MarketSnapshot, last string) (float64, error) {
	if snap.Indicators.ATR == nil || strings.TrimSpace(snap.Indicators.ATR.Latest) == "" {
		return 0, fmt.Errorf("%w: the ATR reading is required to judge volatility", ErrRiskFactsInput)
	}
	if strings.TrimSpace(last) == "" {
		return 0, fmt.Errorf("%w: a last price is required to turn the ATR into a ratio", ErrRiskFactsInput)
	}
	atr, err := decimal.NewFromString(strings.TrimSpace(snap.Indicators.ATR.Latest))
	if err != nil {
		return 0, fmt.Errorf("%w: ATR %q", ErrRiskFactsInput, snap.Indicators.ATR.Latest)
	}
	price, err := decimal.NewFromString(strings.TrimSpace(last))
	if err != nil {
		return 0, fmt.Errorf("%w: last price %q", ErrRiskFactsInput, last)
	}
	if price.IsZero() {
		return 0, fmt.Errorf("%w: a zero last price cannot anchor the ATR", ErrRiskFactsInput)
	}
	ratio, _ := atr.Div(price).Float64()
	return ratio, nil
}

// RiskMarketFactsInput is the venue-phase material D-group facts carry.
type RiskMarketFactsInput struct {
	// Now anchors the funding countdown.
	Now time.Time
	// MarkPrice / IndexPrice have no source in the snapshot or the database:
	// the venue writes them, and the assembler supplies them.
	MarkPrice  string
	IndexPrice string
	// EstimatedSlippageBPS is estimated in the assembler (it owns the walk and
	// the notional ceiling chain); computing it here would create a second
	// spelling of the estimate.
	EstimatedSlippageBPS int
	// QuoteVolumeRank comes from the universe-wide ranking face.
	QuoteVolumeRank int
	// FundingRequired is the policy's intent; the rate itself is snapshot data.
	FundingRequired bool
}
