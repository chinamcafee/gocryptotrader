package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// W7-02 生产快照适配器（对账手动触发的取数侧）。
//
// Reconciler（M5-08）拥有比对语义与落账；本层只负责把两侧读数取回来：
//
//   - 交易所侧（真值）：对启用合约逐对摘 GetFuturesPositionSummary——
//     「成交与仓位以交易所查询为准」（07 §9）的字面落实；**任何一对读不到
//     就整体拒**——「读不到」不是「没有仓位」，把前者当后者是在编造对账；
//   - 本地侧：订单管理器的跟踪仓位（Latest→Opening 回退；非本所/本资产的
//     仓位不在 LLM 交易范围内，不是关于这个账户的分歧）。
//
// 两侧的 Side 统一 LONG/SHORT、MarketID 统一 `<exchange>:<asset>:<BASE>_<QUOTE>`
// （小写 exchange/asset）——同一拼写是比对成立的全部前提。

// iPositionSummarySource is the exchange seam network side（exchange.IBotExchange
// 满足它）。
type iPositionSummarySource interface {
	GetName() string
	GetEnabledPairs(asset.Item) (currency.Pairs, error)
	GetFuturesPositionSummary(context.Context, *futures.PositionSummaryRequest) (*futures.PositionSummary, error)
}

// iLocalPositionSource is the order-manager seam.
type iLocalPositionSource interface {
	GetAllOpenFuturesPositions() ([]futures.Position, error)
}

// llmMarketID formats the canonical market id both sides share.
func llmMarketID(exchangeName string, item asset.Item, pair currency.Pair) string {
	return fmt.Sprintf("%s:%s:%s", strings.ToLower(exchangeName), strings.ToLower(item.String()),
		pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true}))
}

// fetchExchangePositions reads every non-zero position on the exchange.
// A pair whose reading fails refuses the whole pass: half a reading is not a
// reconciliation.
func fetchExchangePositions(ctx context.Context, src iPositionSummarySource, item asset.Item) ([]ReconcilePosition, error) {
	pairs, err := src.GetEnabledPairs(item)
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: enabled pairs: %w", err)
	}
	rows := make([]ReconcilePosition, 0, len(pairs))
	for _, pair := range pairs {
		summary, err := src.GetFuturesPositionSummary(ctx, &futures.PositionSummaryRequest{Asset: item, Pair: pair})
		if err != nil {
			return nil, fmt.Errorf("llm reconcile: reading position %s: %w", pair, err)
		}
		if summary == nil || summary.CurrentSize.IsZero() {
			// A zero position is not a disagreement.
			continue
		}
		size, side := summary.CurrentSize, PositionSideLong
		if size.IsNegative() {
			size, side = size.Abs(), PositionSideShort
		}
		rows = append(rows, ReconcilePosition{
			MarketID: llmMarketID(src.GetName(), item, pair),
			Side:     side,
			Quantity: size.String(),
		})
	}
	return rows, nil
}

// fetchLocalPositions reads the tracked positions limited to the configured
// exchange and asset.
func fetchLocalPositions(src iLocalPositionSource, exchangeName string, item asset.Item) ([]ReconcilePosition, error) {
	positions, err := src.GetAllOpenFuturesPositions()
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: local positions: %w", err)
	}
	rows := make([]ReconcilePosition, 0, len(positions))
	for i := range positions {
		pos := &positions[i]
		if !strings.EqualFold(pos.Exchange, exchangeName) || pos.Asset != item {
			// Another venue's position is not a disagreement about this account.
			continue
		}
		size, direction := pos.LatestSize, pos.LatestDirection
		if size.IsZero() {
			// Older than its latest update: fall back to the opening facts
			// rather than skipping a real position.
			size, direction = pos.OpeningSize, pos.OpeningDirection
		}
		if size.IsZero() {
			continue
		}
		if size.IsNegative() {
			size = size.Abs()
		}
		var side string
		switch direction {
		case order.Long:
			side = PositionSideLong
		case order.Short:
			side = PositionSideShort
		default:
			return nil, fmt.Errorf("%w: local position %s %s has direction %q", ErrIntentInputInvalid, pos.Exchange, pos.Pair, direction)
		}
		rows = append(rows, ReconcilePosition{
			MarketID: llmMarketID(pos.Exchange, pos.Asset, pos.Pair),
			Side:     side,
			Quantity: size.String(),
		})
	}
	return rows, nil
}
