package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M5-08 装配件 · 账户事实的生产者（快照的 `Account` 字段——`llm_snapshot.go`
// 107-110 行所指的"另一件"）。
//
// `SnapshotRequest.Account` 由调用方供给而非快照自取：引擎的 futures 仓位
// 类型没有 position side，装配器不得替它发明一个方向（"a side invented here
// would be a fabrication the risk engine then trusts"）。本文件从**交易所
// 真值**生产这份事实：逐启用合约摘 `GetFuturesPositionSummary`——单次读同时
// 给出账户级权益（EquityUSD/AvailableEquity）与逐仓明细（AverageOpenPrice/
// MarkPrice/UnrealisedPNL/Leverage/EstimatedLiquidationPrice），因此账户与
// 仓位来自**同一次**读数，不存在两个来源对不齐的窗口。
//
// 纪律：
//   - **读不到就整体拒**：任何一对失败或返回空摘要都拒绝整次装配——「读不到」
//     不是「没有仓位」（对账侧先例，同一纪律）；
//   - 零仓位不是仓位：`CurrentSize` 为零跳过，不产生 0 数量的 PositionFact；
//   - 方向从 `CurrentSize` 符号推（正 LONG / 负 SHORT）、数量取绝对值——符号
//     与方向来自同一个读数，禁"补侧"；
//   - 市场拼写与快照/审批同口径：`BASE_QUOTE`（`snapshotPairDelimiter`，下划线
//     大写）；
//   - 一个启用合约都没有 = 账户读不出来，不是「账户为空」——拒绝而非给零值。

// buildSnapshotAccountFacts reads the exchange's futures account truth into
// the snapshot's account facts. The clock is injected so the fact set is
// reproducible.
//
// The same position-summary read also carries the venue's margin-type reading
// (account-level in practice; the first successful read pays for it, like the
// equity), which the risk engine's C group consumes -- it is returned
// alongside rather than dropped. HedgeMode is deliberately NOT claimed here:
// no read surface for it exists yet (workLog session-51 侦察第 11 条 ③ --
// the assembly session picks between adding a dual-side read and declaring
// one-way mode an operational precondition).
func buildSnapshotAccountFacts(ctx context.Context, src iPositionSummarySource, item asset.Item, now time.Time) (*snapshot.AccountFacts, *venueAccountModes, error) {
	if src == nil {
		return nil, nil, ErrChatNotConfigured
	}
	if now.IsZero() {
		return nil, nil, ErrSnapshotClockMissing
	}
	pairs, err := src.GetEnabledPairs(item)
	if err != nil {
		return nil, nil, fmt.Errorf("llm account facts: enabled pairs: %w", err)
	}
	facts := &snapshot.AccountFacts{ReadAt: now.UTC(), Positions: make([]snapshot.PositionFact, 0, len(pairs))}
	modes := &venueAccountModes{}
	equityRead := false
	unrealisedTotal := decimal.Zero
	for _, pair := range pairs {
		summary, err := src.GetFuturesPositionSummary(ctx, &futures.PositionSummaryRequest{Asset: item, Pair: pair})
		if err != nil {
			return nil, nil, fmt.Errorf("llm account facts: reading position %s: %w", pair, err)
		}
		if summary == nil {
			return nil, nil, fmt.Errorf("llm account facts: reading %s returned no summary", pair)
		}
		if !equityRead {
			// Equity is account-level, so the first successful read pays for it.
			facts.TotalEquityUSD = summary.EquityUSD.String()
			facts.AvailableBalanceUSD = summary.AvailableEquity.String()
			modes.MarginType = summary.MarginType.String()
			equityRead = true
		}
		if summary.CurrentSize.IsZero() {
			// A zero position is not a position.
			continue
		}
		size, side := summary.CurrentSize, PositionSideLong
		if size.IsNegative() {
			size, side = size.Abs(), PositionSideShort
		}
		unrealisedTotal = unrealisedTotal.Add(summary.UnrealisedPNL)
		facts.Positions = append(facts.Positions, snapshot.PositionFact{
			Symbol:           pair.Format(currency.PairFormat{Delimiter: snapshotPairDelimiter, Uppercase: true}).String(),
			Side:             side,
			Size:             size.String(),
			EntryPrice:       summary.AverageOpenPrice.String(),
			MarkPrice:        summary.MarkPrice.String(),
			LiquidationPrice: summary.EstimatedLiquidationPrice.String(),
			UnrealisedPNLUSD: summary.UnrealisedPNL.String(),
			Leverage:         summary.Leverage.String(),
		})
	}
	if !equityRead {
		return nil, nil, fmt.Errorf("llm account facts: no enabled %s pairs to read the account from", item)
	}
	facts.UnrealisedPNLUSD = unrealisedTotal.String()
	return facts, modes, nil
}

// venueAccountModes is the venue-side account-mode material for the C group.
type venueAccountModes struct {
	// MarginType is the venue's margin-mode string (from the position-summary
	// read above, account-level in practice).
	MarginType string
	// HedgeMode is the account's position mode, filled by the separate
	// dual-side read (`readVenueHedgeMode` in the Assemble closure): a real
	// reading beats a silent one-way assumption.
	HedgeMode bool
}

// iDualSideSource is the venue seam for the account's position-mode reading
// (binance's Exchange satisfies it via UGetDualSidePosition).
type iDualSideSource interface {
	GetName() string
	UGetDualSidePosition(ctx context.Context) (bool, error)
}

// readVenueHedgeMode reads the account's position mode. Fail-closed: an
// unreadable mode refuses rather than reporting one-way.
func readVenueHedgeMode(ctx context.Context, src iDualSideSource) (bool, error) {
	if src == nil {
		return false, fmt.Errorf("%w: the dual-side source is required", ErrRiskFactsInput)
	}
	hedge, err := src.UGetDualSidePosition(ctx)
	if err != nil {
		return false, fmt.Errorf("llm account modes: dual-side position: %w", err)
	}
	return hedge, nil
}
