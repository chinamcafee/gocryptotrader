package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
)

// 生产装配件 · 数量重算的盘口走价（07 §5：滑点估算复用现有
// `orderbook.Movement`）。`SizingInput.Walk` 是数量链的第 9 步——用实际
// 名义额重跑一遍滑点；把盘口遍历留在 orderbook 包（与 RPC
// `GetOrderbookMovement` 同一条实现），本层只做三件事：名义额与边的解析、
// 调用、错误翻译。
//
// 语义（与 orderbook 的 quotation 遍历对齐）：
//   - "ask"（买入）：把报价额投到卖盘上 → `LiftTheAsksFromBest(notional, false)`；
//   - "bid"（卖出）：从买盘换回报价额 → `HitTheBidsFromBest(notional, true)`；
//   - 返回 `AverageOrderCost`（穿越档位后的平均成交价，报价/基础）。
//
// 纪律：**盘口填不满就拒绝**——`FullBookSideConsumed` 既表示"流动性被吃穿"
// 也表示"恰好停在最后一档"（同样无法定价），两者都不该拿一个残缺的平均价
// 冒充可成交价；数量链拿不到可读事实就走它的拒绝通道，而不是被喂假数。

var (
	// ErrSizingWalkInvalid marks a walk request without a usable side or a
	// positive notional.
	ErrSizingWalkInvalid = errors.New("llm sizing walk: side must be ask or bid and the notional must be positive")
	// ErrSizingWalkInsufficient marks a book side that cannot fill the notional.
	ErrSizingWalkInsufficient = errors.New("llm sizing walk: the orderbook side cannot fill the notional")
)

// Sizing-side vocabulary: the book side a fill touches ("ask" = buying lifts
// the asks, "bid" = selling hits the bids). One spelling for the walk, the
// slippage estimate and the order facts.
const (
	sizingSideAsk = "ask"
	sizingSideBid = "bid"
)

// sizingWalkFromDepth builds the SizingInput.Walk over a live depth: the
// average fill price for a USD notional on a side ("ask" = buying, "bid" =
// selling). A nil depth fails every call rather than panicking: the assembler
// must not build a walk it cannot back with a book.
func sizingWalkFromDepth(depth *orderbook.Depth) func(side, notionalUSD string) (string, error) {
	return func(side, notionalUSD string) (string, error) {
		if depth == nil {
			return "", ErrChatNotConfigured
		}
		notional, err := strconv.ParseFloat(strings.TrimSpace(notionalUSD), 64)
		if err != nil || notional <= 0 {
			return "", fmt.Errorf("%w: notional %q", ErrSizingWalkInvalid, notionalUSD)
		}
		var move *orderbook.Movement
		switch strings.ToLower(strings.TrimSpace(side)) {
		case sizingSideAsk:
			move, err = depth.LiftTheAsksFromBest(notional, false)
		case sizingSideBid:
			move, err = depth.HitTheBidsFromBest(notional, true)
		default:
			return "", fmt.Errorf("%w: side %q", ErrSizingWalkInvalid, side)
		}
		if err != nil {
			return "", fmt.Errorf("llm sizing walk: %w", err)
		}
		if move.FullBookSideConsumed {
			return "", fmt.Errorf("%w: %s side and %s", ErrSizingWalkInsufficient, side, notionalUSD)
		}
		return strconv.FormatFloat(move.AverageOrderCost, 'f', -1, 64), nil
	}
}
