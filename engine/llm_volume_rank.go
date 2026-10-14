package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
)

// M4-11 装配件 · 24h quoteVolume 排名（§5.6 liquidityTopNOnly 的取数件）。
//
// 检查 `RiskMarketLowLiquidity` 只在 `rank > 0 && rank <= 50` 时放行，
// 而 `liquidityTopNOnly` 开关**默认 ON**——排名缺席不是「没闸」而是闭闸。
// 本函数把「某交易所某资产下所有已缓存 ticker」折算成排名表：
//
//   - 排名键与全仓同拼写（`llmMarketID`：小写 exchange/asset + `BASE_QUOTE`）；
//   - 按 24h quote volume 降序；**同量按对名升序**去随机（同一批 ticker
//     两次调用必须给出同一张表——审计里的排名要可复现）；
//   - 零/负面量的市场**照排**（票数少于 5 的市场「进前 50」是文档语义，
//     不是伪造；量小该由检查的上限去说话，不是由排名去隐藏）；
//   - 该场所该资产**一个都没有 → 报错**：空宇宙算不出排名，报 0 会让
//     「没读到」与「排名很差」长得一样；
//   - 别家交易所、别种资产的 ticker 不是关于这个场所的事实，不入表。
func quoteVolumeRanks(exchangeName string, item asset.Item, tickers []*ticker.Price) (map[string]int, error) {
	type row struct {
		key    string
		volume float64
	}
	rows := make([]row, 0, len(tickers))
	for _, tick := range tickers {
		if tick == nil || !strings.EqualFold(tick.ExchangeName, exchangeName) || tick.AssetType != item {
			continue
		}
		rows = append(rows, row{key: llmMarketID(tick.ExchangeName, tick.AssetType, tick.Pair), volume: tick.QuoteVolume})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no cached tickers for %s %s to rank", ErrRiskFactsInput, exchangeName, item)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].volume != rows[j].volume {
			return rows[i].volume > rows[j].volume
		}
		return rows[i].key < rows[j].key
	})
	ranks := make(map[string]int, len(rows))
	for i := range rows {
		if _, dup := ranks[rows[i].key]; dup {
			continue
		}
		ranks[rows[i].key] = i + 1
	}
	return ranks, nil
}
