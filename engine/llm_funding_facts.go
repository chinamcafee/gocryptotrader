package engine

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// M4-11 装配件 · premiumIndex 一次读 → funding 事实 + mark/index 价（先决 ④）。
//
// 「快照已带 Funding」是误记：构建器（`engine/llm_snapshot.go`）零 funding
// 引用、无 `FundingFacts` 构造点。而 D 组要的 mark/index 与 funding 事实
// **恰好同源**——binance `/fapi/v1/premiumIndex`（`UGetMarkPrice`）一次
// 返回 markPrice + indexPrice + lastFundingRate + nextFundingTime，所以
// 合为一个件：一次读同时喂 ①快照的 Funding 事实（经 `SnapshotRequest.Funding`
// 像 Account 一样由调用方供给，但质量标 **EXCHANGE_REST**——这是场地直读，
// 不是本地缓存视图）②D 组 `MarkPrice`/`IndexPrice` 两个显式输入。
//
// 读不到即拒：空回包、mark/index 非正都是「读不可信」，不许零值冒充
// （mark=0 会让偏差检查把可读事实当异常行情拒掉——错误的方向上仍然在撒谎）。
func buildFundingMarkIndexFacts(ctx context.Context, src iPremiumIndexSource, pair currency.Pair, now time.Time) (*fundingMarkIndexFacts, error) {
	if src == nil {
		return nil, fmt.Errorf("%w: the premium-index source is required", ErrRiskFactsInput)
	}
	readings, err := src.UGetMarkPrice(ctx, pair)
	if err != nil {
		return nil, fmt.Errorf("llm funding: premium index for %s: %w", pair, err)
	}
	if len(readings) == 0 {
		return nil, fmt.Errorf("%w: the premium index returned no rows for %s", ErrRiskFactsInput, pair)
	}
	row := readings[0]
	mark, index := row.MarkPrice.Float64(), row.IndexPrice.Float64()
	if mark <= 0 || index <= 0 {
		return nil, fmt.Errorf("%w: the premium index for %s carries no positive mark/index price", ErrRiskFactsInput, pair)
	}
	return &fundingMarkIndexFacts{
		Funding: &snapshot.FundingFacts{
			LatestRate:      numberText(row.LastFundingRate.Float64()),
			NextFundingTime: row.NextFundingTime.Time(),
			ReadAt:          now,
		},
		MarkPrice:  numberText(mark),
		IndexPrice: numberText(index),
	}, nil
}

// fundingMarkIndexFacts is what one premium-index read yields for a market.
type fundingMarkIndexFacts struct {
	// Funding feeds the snapshot's funding facts.
	Funding *snapshot.FundingFacts
	// MarkPrice / IndexPrice feed the market-facts mapping.
	MarkPrice  string
	IndexPrice string
}

// iPremiumIndexSource is the venue seam (binance's Exchange satisfies it).
type iPremiumIndexSource interface {
	GetName() string
	UGetMarkPrice(ctx context.Context, symbol currency.Pair) ([]binance.UMarkPrice, error)
}

// numberText renders a venue float as the shortest decimal that round-trips:
// funding rates are signed, so no positivity is implied here.
func numberText(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// fundingFromRequest copies a caller-supplied funding reading onto the snapshot.
// The quality is EXCHANGE_REST: unlike the account block (a local cache view),
// this reading comes straight from the venue.
func applyFunding(built *snapshot.MarketSnapshot, req *SnapshotRequest) error {
	if req.Funding == nil {
		return nil
	}
	built.Funding = req.Funding
	return built.SetQuality(snapshot.CategoryFunding, candidate.QualityExchangeREST)
}
