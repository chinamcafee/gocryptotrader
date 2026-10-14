package engine

import (
	"fmt"
	"strconv"

	"github.com/thrasher-corp/gocryptotrader/common/key"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchange/order/limits"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
)

// M4-11 装配件 · 交易所执行限额 → 硬风控 §5.4 合约规格（tick/step/最小量/最小名义额）。
//
// 来源 = 进程内执行限额仓库（`exchange/order/limits`，各交易所在
// `UpdateOrderExecutionLimits` 时灌入），按 (exchange, asset, pair) 精确取档——
// **不是 `GetFuturesContractDetails`**：`futures.Contract` 不携带 tick/step/
// 最小名义额（只有乘数与结算信息），限额仓库才是这三件的事实源。
// §5.4 的 tick 取整与数量链的 step 对齐都吃这份规格，所以：
//
//   - tick/step 非正 → **拒**：没有 tick 取整、没有 step 对齐，这两步会静默
//     变成 no-op——那是 fail-open；
//   - 最小量/最小名义额为 0 → 如实申报**缺席**（空串）：限额仓库把「场地未声明」
//     折叠成 0，检查对空串有公开语义（该条不过闸）；报 "0" 反而会被读成
//     「要求大于零」，是另一种伪造；
//   - 规格值以十进制字符串出门（float64 → 最短往返表示）：风控与数量链全程走
//     十进制字符串，二进制浮点只允许在源侧出现这一次。
func buildContractSpec(exchangeName string, item asset.Item, pair currency.Pair) (*RiskContractSpec, error) {
	level, err := limits.GetOrderExecutionLimits(key.NewExchangeAssetPair(exchangeName, item, pair))
	if err != nil {
		return nil, fmt.Errorf("llm contract spec: %w", err)
	}
	if level.PriceStepIncrementSize <= 0 {
		return nil, fmt.Errorf("%w: %s %s has no readable tick size", ErrRiskFactsInput, exchangeName, pair)
	}
	if level.AmountStepIncrementSize <= 0 {
		return nil, fmt.Errorf("%w: %s %s has no readable step size", ErrRiskFactsInput, exchangeName, pair)
	}
	return &RiskContractSpec{
		TickSize:       strconv.FormatFloat(level.PriceStepIncrementSize, 'f', -1, 64),
		StepSize:       strconv.FormatFloat(level.AmountStepIncrementSize, 'f', -1, 64),
		MinQuantity:    formatVenueLimit(level.MinimumBaseAmount),
		MinNotionalUSD: formatVenueLimit(level.MinNotional),
	}, nil
}

// formatVenueLimit renders a stored limit as a decimal string, or the empty
// string when the venue never stated one (the store collapses unstated to 0).
func formatVenueLimit(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
