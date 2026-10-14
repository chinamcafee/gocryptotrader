package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/margin"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
	"github.com/thrasher-corp/gocryptotrader/exchanges/request"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// M4-11 收官 · `RiskFacts` 闭包的生产合成器（session-53，2026-10-12）。
//
// 本文件把 session-51 侦察第 11 条的接线清单一次装齐：授权（A）／数据（B）／
// 账户（C）／频率（E）／合约／订单／市场（D）／系统（G）七路映射 + 两组装件，
// 全部材料来自既有读面。三处装配期决定（各带记录理由）：
//
//   - **订单事实（decisions/02 = B）**：开仓 LIMIT（§5.4 取价：suggestedEntry
//     经 tick 取整 + 盘口区间校验，与 `BuildIntent` 的 `limitPrice` 同一实现
//     ——`limitPriceFor` 即抽出本体）；主动平仓 MARKET（不传价）。NotionalUSD
//     = min(候选上界, 审批上限)——数量链可能产出的上界，C 组杠杆/保证金检查
//     按最坏情形评估；`Quantity` 开仓留空（数量是重算的产物，桥落库时给），
//     平仓取该市场现存仓位数量（全平语义：REDUCE 即"减少或关闭现有敞口"）。
//   - **C 组保证金模式（decisions/03 = A）**：`llm.risk.expectedMarginType`
//     配置值（operator 词汇）经 `margin.StringToMarginType` 一次译为场地词汇
//     （`margin.Type.String()`：cross→"multi"、isolated→"isolated"）——检查
//     两侧裸字符串比较，词汇必须同拼写。
//   - **F 组仓位市场号归一**：仓位事实的行内拼写是 BASE_QUOTE（账户生产者
//     口径），而候选/映射全用运行三段式——经 `runMarketID` 归一，否则
//     反向持仓/并发计数永远看不见仓位（同五缝之一类）。
//
// 读不到即拒（fail-closed）：每个读面错误向上抛，run 记失败而不是"看起来
// 评估了"；唯一例外是日损（见下）。

// ShadowRiskMaterialInput is everything the closure raises for the hard-risk
// stage of one run. Every field is either a captured run fact or a live read
// taken inside the closure.
type ShadowRiskMaterialInput struct {
	Now      time.Time
	MarketID string
	Exchange string
	Item     asset.Item
	Pair     currency.Pair

	Candidate *candidate.Candidate
	Approval  *llmstore.StrategyApproval
	// FrozenJSON is the frozen policy text the run already decoded; used for
	// the envelope-hash recheck (A group).
	FrozenJSON string
	Envelope   candidate.RiskEnvelope
	PairEpoch  int64

	// Snapshot is the run's assembled snapshot (account, book, ticker, ATR).
	Snapshot *SnapshotResult
	// Premium is the run's premium-index reading (mark/index for D group).
	Premium *fundingMarkIndexFacts
	// AccountModes is the venue account-mode material (C group's MarginType
	// and HedgeMode).
	AccountModes *venueAccountModes

	PairReadiness string
	Reconciled    bool
	ManifestBound bool

	// Mode is the effective run mode at evaluation time (fail-closed: read
	// fresh, not at run build).
	Mode string
	// BudgetExhausted mirrors the pre-gate's budget verdict.
	BudgetExhausted bool
	// ExpectedMarginType is the operator's margin-mode assumption (config);
	// it is translated to the venue's spelling here.
	ExpectedMarginType string
}

// buildShadowRiskMaterial raises every group's material from live state and
// assembles the risk input plus (for opens) the sizing input.
func (bot *Engine) buildShadowRiskMaterial(ctx context.Context, in *ShadowRiskMaterialInput) (*RiskInput, *SizingInput, error) {
	if bot == nil || in == nil || bot.llmManager == nil {
		return nil, nil, ErrChatNotConfigured
	}
	if in.Candidate == nil || in.Approval == nil || in.Snapshot == nil || in.Snapshot.Snapshot == nil {
		return nil, nil, fmt.Errorf("%w: the candidate, approval and snapshot are required", ErrRiskFactsInput)
	}
	if in.Premium == nil || in.AccountModes == nil {
		return nil, nil, fmt.Errorf("%w: the premium-index and account-mode readings are required", ErrRiskFactsInput)
	}
	if in.Now.IsZero() {
		return nil, nil, ErrSnapshotClockMissing
	}
	snap := in.Snapshot.Snapshot
	if bot.ExchangeManager == nil {
		return nil, nil, fmt.Errorf("%w: the exchange manager is required for the venue reads", ErrRiskFactsInput)
	}
	source, err := bot.ExchangeManager.GetExchangeByName(in.Exchange)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: exchange %q: %w", in.Exchange, err)
	}

	// A. Authorization: the approval row the run already holds plus the live
	// grants; the envelope hash is re-derived from the frozen text.
	grants, err := (LLMApprovals{}).ListGrants(ctx, in.Approval.AccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: grants: %w", err)
	}
	auth, err := riskAuthorizationFromState(&RiskAuthorizationInput{
		Approval: in.Approval, FrozenJSON: in.FrozenJSON, Candidate: in.Candidate,
		PairEpoch: in.PairEpoch, Grants: grants, ManifestBound: in.ManifestBound, Now: in.Now,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: authorization: %w", err)
	}

	// B. Data facts from the run's snapshot.
	data, err := riskDataFactsFromSnapshot(&RiskSnapshotFactsInput{
		Result: in.Snapshot, Now: in.Now,
		MaxSnapshotAgeMs:         RiskMaxSnapshotAgeMs,
		MaxAccountInventoryAgeMs: RiskMaxAccountInventoryAgeMs,
		AccountReconciled:        in.Reconciled,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: data facts: %w", err)
	}

	// C. Account facts: the repo-backed state slice plus the venue-phase
	// material. The external-flows netting belongs to DayStartService (one
	// spelling); an unreadable flows read leaves the field nil so the engine's
	// own RISK_DAILY_LOSS_UNKNOWN check records the refusal.
	state, err := buildRiskAccountStateFacts(ctx, in.Approval.AccountID, in.Now)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: account state: %w", err)
	}
	var flowsPtr *string
	if bot.llmDayStart != nil {
		if flows, err := bot.llmDayStart.NetExternalFlowsUSD(ctx, in.Approval.AccountID); err == nil {
			flowsPtr = &flows
		}
	}
	expectedMargin, err := venueMarginType(in.ExpectedMarginType)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: %w", err)
	}
	account, positions, err := riskAccountFactsFromSnapshot(snap, &RiskAccountFactsInput{
		AccountID:               in.Approval.AccountID,
		TargetAccountID:         in.Approval.AccountID,
		MarginType:              in.AccountModes.MarginType,
		ExpectedMarginType:      expectedMargin,
		HedgeMode:               in.AccountModes.HedgeMode,
		DayStartEquityUSD:       state.DayStartEquityUSD,
		ExternalFlowsUSD:        flowsPtr,
		UnresolvedUnknownOrders: state.UnresolvedUnknownOrders,
		UnreconciledPartials:    state.UnreconciledPartials,
	}, in.Now)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: account facts: %w", err)
	}
	// F group's position-side key: the run vocabulary. Without this the
	// opposing-position and concurrent-position checks silently miss every
	// position (BASE_QUOTE never equals the three-segment candidate market).
	for i := range positions {
		normalised, err := runMarketID(in.Exchange, in.Item, positions[i].MarketID)
		if err != nil {
			return nil, nil, fmt.Errorf("llm risk material: position market %q: %w", positions[i].MarketID, err)
		}
		positions[i].MarketID = normalised
	}

	// E. Frequency facts (the counts are read inside).
	cooldownElapsed := true // no cooldown length is configured yet: 0-length means no gate (the 0=no-gate translation), so always elapsed
	freq, err := riskFrequencyFactsFromState(ctx, &RiskFrequencyFactsInput{
		AccountID: in.Approval.AccountID, StrategyVersionID: in.Approval.StrategyVersionID,
		Action: in.Candidate.Action, Now: in.Now, WindowMinutes: in.Envelope.RiskWindowMinutes,
		MarketCooldownElapsed: cooldownElapsed,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: frequency facts: %w", err)
	}

	// Contract spec from the venue's limits store.
	spec, err := buildContractSpec(in.Exchange, in.Item, in.Pair)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: contract spec: %w", err)
	}

	// D. Market facts: the book-derived touch and ATR ride the snapshot; the
	// venue-phase literals come from the premium read and the sizing-side
	// estimates below.
	bestBid, bestAsk := snapshotBestBidAsk(snap)
	depth, err := depthFromSnapshotBook(snap.OrderBook, in.Exchange, in.Pair, in.Item)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: book depth: %w", err)
	}
	order, err := shadowOrderFacts(&shadowOrderMaterial{
		Candidate: in.Candidate, Envelope: in.Envelope, Contract: *spec, Snapshot: snap,
		Positions: positions, BestBid: bestBid, BestAsk: bestAsk,
		Switches: bot.currentRiskSwitches(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: %w", err)
	}
	slippage := 0
	if bound := strings.TrimSpace(order.NotionalUSD); bound != "" {
		side := shadowSizingSide(in.Candidate, positions)
		best := bestBid
		if side == sizingSideAsk {
			best = bestAsk
		}
		if estimate, err := estimatedSlippageBPS(sizingWalkFromDepth(depth), side, bound, best); err == nil {
			slippage = estimate
		}
		// A walk that cannot price the bound leaves the estimate at zero: the
		// notional/thin-depth checks refuse on the same fact, and refusing the
		// whole run would lose the recorded refusal.
	}
	rank, err := bot.quoteVolumeRankFor(in.Exchange, in.Item, in.Pair)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: liquidity rank: %w", err)
	}
	market, err := riskMarketFactsFromSnapshot(snap, &RiskMarketFactsInput{
		Now: in.Now, MarkPrice: in.Premium.MarkPrice, IndexPrice: in.Premium.IndexPrice,
		EstimatedSlippageBPS: slippage, QuoteVolumeRank: rank,
		FundingRequired: false, // no face can declare "this strategy needs funding"; absent means undeclared (the check stays dormant)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: market facts: %w", err)
	}

	// G. System facts: halts and the clock drift are live reads; the rest is
	// the engine's own state.
	halts, err := (LLMActiveHalts{}).ListActiveHalts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: halts: %w", err)
	}
	timeSource, ok := source.(iServerTimeSource)
	if !ok {
		return nil, nil, fmt.Errorf("%w: exchange %q does not expose the server-time call", ErrRiskFactsInput, in.Exchange)
	}
	drift, err := readClockDriftMs(ctx, timeSource, in.Now)
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: clock drift: %w", err)
	}
	system, err := riskSystemFactsFromState(&RiskSystemFactsInput{
		Halts: halts, MarketID: in.MarketID, Mode: in.Mode,
		LiveAcknowledged:     bot.liveTradingAcknowledged(),
		ModelBudgetExhausted: in.BudgetExhausted,
		DatabaseHealthy:      databaseHealthy(ctx),
		ClockDriftMs:         drift, MaxClockDriftMs: RiskMaxClockDriftMs,
		RateLimitDegraded: rateLimitDegradedAt(in.Now), // the shared request layer's back-off horizon (a 429/Retry-After seen in this process), not a reading invented here
		PairReadiness:     in.PairReadiness,
		OrderManagerUp:    bot.OrderManager != nil,
		ExchangeManagerUp: bot.ExchangeManager != nil,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llm risk material: system facts: %w", err)
	}

	switches := bot.currentRiskSwitches()
	riskIn, err := buildShadowRiskInput(&ShadowRiskInputMaterial{
		Now: in.Now, Candidate: in.Candidate, Envelope: in.Envelope,
		Authorization: *auth, Data: *data, Account: account, Positions: positions,
		Frequency: *freq, Contract: *spec, Order: order, Market: *market, System: *system,
		Switches: switches,
	})
	if err != nil {
		return nil, nil, err
	}

	// The sizing chain covers opens only (exits are sized by the exit policy,
	// 07 §7). A daily-loss read that cannot be established skips the sizing
	// input: the risk engine's own RISK_DAILY_LOSS_UNKNOWN check already
	// records the refusal, and the run proceeds to a recorded REJECT rather
	// than an assembly failure.
	var sizing *SizingInput
	if in.Candidate.Action.CreatesRisk() {
		if bot.llmDayStart != nil {
			if loss, err := bot.llmDayStart.DailyLoss(ctx, in.Approval.AccountID, account.EquityUSD); err == nil {
				walk := sizingWalkFromDepth(depth)
				built, err := buildShadowSizingInput(&ShadowSizingMaterial{
					Envelope: in.Envelope, Candidate: in.Candidate, Snapshot: snap,
					Positions: positions, Contract: *spec, Walk: walk, DailyLossUSD: loss,
				})
				if err != nil {
					return nil, nil, fmt.Errorf("llm risk material: sizing: %w", err)
				}
				sizing = built
			}
		}
	}
	return riskIn, sizing, nil
}

// shadowOrderMaterial is the explicit material the order facts are derived
// from — shared by the risk stage and (through the same helpers) the bridge.
type shadowOrderMaterial struct {
	Candidate *candidate.Candidate
	Envelope  candidate.RiskEnvelope
	Contract  RiskContractSpec
	Snapshot  *snapshot.MarketSnapshot
	Positions []RiskPositionFact
	BestBid   string
	BestAsk   string
	Switches  RiskSwitches
}

// shadowOrderFacts derives the prospective order's facts (decisions/02 = B):
// opens rest at the suggested entry (LIMIT, §5.4 rounding and book bounds);
// active exits take the book (MARKET, no price).Quantity stays empty for
// opens — the sizing chain produces it — and carries the position size for a
// reduce (the action means reducing the existing exposure).
func shadowOrderFacts(in *shadowOrderMaterial) (RiskOrderFacts, error) {
	if in == nil || in.Candidate == nil {
		return RiskOrderFacts{}, fmt.Errorf("%w: the order material needs the candidate", ErrRiskFactsInput)
	}
	cand := in.Candidate
	switch cand.Action {
	case candidate.ActionOpenLong, candidate.ActionOpenShort:
		side := orderSideBuy
		if cand.Action == candidate.ActionOpenShort {
			side = orderSideSell
		}
		if strings.TrimSpace(cand.SuggestedEntry) == "" {
			// The synthesis layer refuses an open without an entry; reaching
			// this point means the material is broken, not "price optional".
			return RiskOrderFacts{}, fmt.Errorf("%w: the open candidate carries no suggested entry to price the LIMIT", ErrRiskFactsInput)
		}
		price, err := limitPriceFor(cand.SuggestedEntry, in.Contract.TickSize, side, in.Envelope.MaxSlippageBPS, in.BestBid, in.BestAsk)
		if err != nil {
			return RiskOrderFacts{}, err
		}
		notional := strings.TrimSpace(cand.ProposedMaxNotionalUSD)
		if notional != "" && in.Envelope.MaxOrderNotionalUSD != nil {
			notional = minDecimal(notional, *in.Envelope.MaxOrderNotionalUSD)
		}
		return RiskOrderFacts{
			OrderType: OrderTypeLimit,
			Price:     price,
			// The notional bound the sizing chain may not exceed: the risk
			// checks evaluate the worst case this order could take.
			NotionalUSD: notional,
			// The plan commits to its protective stop when the switch or the
			// envelope requires one; whether the stop is derivable is the
			// sizing chain's own refusal (stop distance).
			HasProtectiveExit: in.Switches.RequireProtectiveExit || in.Envelope.RequiresProtectiveExit,
			// No production path reaches RESTING yet (the M5-05 lifecycle
			// feeder is not wired), so no conflict can exist; re-pin the read
			// face when the lifecycle lands.
			ConflictsWithResting: false,
		}, nil
	case candidate.ActionReduce:
		quantity, notional := "", ""
		for i := range in.Positions {
			if in.Positions[i].MarketID != cand.MarketID {
				continue
			}
			quantity = in.Positions[i].Quantity
			if price, err := decParse(in.Positions[i].MarkPrice); err == nil {
				if entry, err := decParse(in.Positions[i].EntryPrice); err == nil && entry.GreaterThan(price) {
					price = entry
				}
				if qty, err := decParse(quantity); err == nil {
					notional = price.Mul(qty).String()
				}
			}
			break
		}
		// A reduce with no position (or an unreadable one) leaves the shape
		// empty: the engine's own min-quantity/notional checks record the
		// refusal as unreadable rather than passing a fabricated size.
		return RiskOrderFacts{OrderType: OrderTypeMarket, Quantity: quantity, NotionalUSD: notional}, nil
	default:
		return RiskOrderFacts{}, fmt.Errorf("%w: action %q creates no order to evaluate", ErrRiskFactsInput, cand.Action)
	}
}

// shadowSizingSide maps the prospective order to the book side its fill would
// touch: a long entry lifts the asks, a short entry hits the bids, and a
// reduce mirrors the position it closes.
func shadowSizingSide(cand *candidate.Candidate, positions []RiskPositionFact) string {
	switch cand.Action {
	case candidate.ActionOpenLong:
		return sizingSideAsk
	case candidate.ActionOpenShort:
		return sizingSideBid
	case candidate.ActionReduce:
		for i := range positions {
			if positions[i].MarketID != cand.MarketID {
				continue
			}
			if positions[i].Side == PositionSideLong {
				return sizingSideBid // selling a long back hits the bids
			}
			return sizingSideAsk
		}
	}
	return sizingSideAsk
}

// venueMarginType translates the operator's margin-mode vocabulary into the
// venue's own spelling (margin.Type.String()): the C-group check compares the
// two strings raw, so "CROSS" from the config must become the venue's "multi".
func venueMarginType(operatorValue string) (string, error) {
	parsed, err := margin.StringToMarginType(operatorValue)
	if err != nil {
		return "", fmt.Errorf("%w: expectedMarginType %q", ErrRiskFactsInput, operatorValue)
	}
	if parsed == margin.Unset {
		return "", fmt.Errorf("%w: the expected margin type must be declared", ErrRiskFactsInput)
	}
	return parsed.String(), nil
}

// snapshotBestBidAsk picks the market's touch from the snapshot: the book's
// top levels when present, the ticker's quotes otherwise (the precedence the
// market-facts mapping documents).
func snapshotBestBidAsk(snap *snapshot.MarketSnapshot) (bid, ask string) {
	if snap == nil {
		return "", ""
	}
	if snap.OrderBook != nil && len(snap.OrderBook.Bids) > 0 && len(snap.OrderBook.Asks) > 0 {
		return snap.OrderBook.Bids[0].Price, snap.OrderBook.Asks[0].Price
	}
	if snap.Ticker != nil {
		return snap.Ticker.Bid, snap.Ticker.Ask
	}
	return "", ""
}

// depthFromSnapshotBook converts the snapshot's book facts into the orderbook
// package's Depth so the sizing walk runs on its one implementation.
func depthFromSnapshotBook(book *snapshot.OrderBookFacts, exchangeName string, pair currency.Pair, item asset.Item) (*orderbook.Depth, error) {
	if book == nil || len(book.Bids) == 0 || len(book.Asks) == 0 {
		return nil, fmt.Errorf("%w: the snapshot carries no two-sided book to walk", ErrRiskFactsInput)
	}
	parse := func(levels []snapshot.OrderBookLevel) (orderbook.Levels, error) {
		out := make(orderbook.Levels, 0, len(levels))
		for i := range levels {
			price, err := strconv.ParseFloat(levels[i].Price, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: book price %q", ErrRiskFactsInput, levels[i].Price)
			}
			amount, err := strconv.ParseFloat(levels[i].Size, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: book amount %q", ErrRiskFactsInput, levels[i].Size)
			}
			out = append(out, orderbook.Level{Price: price, Amount: amount})
		}
		return out, nil
	}
	bids, err := parse(book.Bids)
	if err != nil {
		return nil, err
	}
	asks, err := parse(book.Asks)
	if err != nil {
		return nil, err
	}
	depth := orderbook.NewDepth(uuid.NewV4())
	if err := depth.LoadSnapshot(&orderbook.Book{
		Exchange: exchangeName, Pair: pair, Asset: item,
		Bids: bids, Asks: asks, LastUpdated: time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("llm risk material: loading the book snapshot: %w", err)
	}
	return depth, nil
}

// quoteVolumeRankFor ranks the market against the venue's cached tickers
// (the rotator fills the cache each cycle). The lookup key is llmMarketID --
// the function both the cache and this reader speak.
func (bot *Engine) quoteVolumeRankFor(exchangeName string, item asset.Item, pair currency.Pair) (int, error) {
	if bot == nil {
		return 0, ErrChatNotConfigured
	}
	var tickers []*ticker.Price
	for _, entry := range bot.GetAllActiveTickers() {
		if !strings.EqualFold(entry.ExchangeName, exchangeName) {
			continue
		}
		tickers = append(tickers, entry.ExchangeValues...)
	}
	ranks, err := quoteVolumeRanks(exchangeName, item, tickers)
	if err != nil {
		return 0, err
	}
	return ranks[llmMarketID(exchangeName, item, pair)], nil
}

// currentRiskSwitches reads the §5.6 catalogue (defaults applied); a missing
// service fails closed to the defaults rather than to all-off.
func (bot *Engine) currentRiskSwitches() RiskSwitches {
	if bot == nil || bot.llmRiskSwitches == nil {
		return DefaultRiskSwitches()
	}
	return bot.llmRiskSwitches.Current()
}

// liveTradingAcknowledged mirrors the manager's LIVE unlock bar.
func (bot *Engine) liveTradingAcknowledged() bool {
	if bot == nil {
		return false
	}
	return bot.Config.LLM.AcknowledgeLiveTrading == LiveAcknowledgment
}

// databaseHealthy probes the audit database once for the G group.
func databaseHealthy(ctx context.Context) bool {
	if database.DB == nil || database.DB.SQL == nil {
		return false
	}
	return database.DB.SQL.PingContext(ctx) == nil
}

// rateLimitDegradedAt reports whether the venue's last rate-limit back-off is
// still in force at now (the shared request layer records a 429/Retry-After as
// a horizon; no window is invented here).
func rateLimitDegradedAt(now time.Time) bool {
	return request.RateLimitDegradedUntil().After(now)
}
