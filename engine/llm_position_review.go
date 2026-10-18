package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M5-20 生产装配 · 持仓复核（docs/llm-dev/13 §3.3，需求③）。
//
// 对已持仓合约生成"继续持有 / 平仓"决策：同一决策链（分析→Jev→合成→
// 风控→落账），不同的用户消息框架——附**自开仓以来**的综合走势：开仓时刻
// （本地 intents 最近一笔 FILLED/PARTIALLY_FILLED）、窗口涨跌、最大不利偏移、
// 区间振幅；持仓事实本身随快照账户事实进入模型输入（权威来源）。
//
// 纪律：
//   - **无持仓不复核**：场地读不到持仓（或仓量为零）即拒——复核一个不存在的
//     仓位是伪造语境；
//   - 开仓时刻读不到时以 48h 窗口兜底并**如实标注**（fallback=true 进消息），
//     不静默冒充自开仓口径；
//   - 只记录不下单（与 trigger/preview 同级承诺）。

// PositionReviewContext is the since-entry material appended to the analyst
// message for a hold/close review.
type PositionReviewContext struct {
	Side                    string `json:"side"`
	Size                    string `json:"size"`
	EntryPrice              string `json:"entryPrice"`
	MarkPrice               string `json:"markPrice"`
	UnrealisedPNLUSD        string `json:"unrealisedPnlUsd,omitempty"`
	FundingFeeUSD           string `json:"fundingFeeUsd,omitempty"`
	FundingFeePayments      int    `json:"fundingFeePayments,omitempty"`
	FundingFeeNote          string `json:"fundingFeeNote,omitempty"`
	ProtectiveStopStatus    string `json:"protectiveStopStatus,omitempty"`
	ProtectiveStopTrigger   string `json:"protectiveStopTrigger,omitempty"`
	ProtectiveStopNote      string `json:"protectiveStopNote,omitempty"`
	OpenedAt                string `json:"openedAt,omitempty"`
	WindowHours             int    `json:"windowHours"`
	WindowFallback          bool   `json:"windowFallback"`
	ChangePercent           string `json:"changePercent"`
	AdverseExcursionPercent string `json:"adverseExcursionPercent"`
	RangePercent            string `json:"rangePercent"`
	Bars                    int    `json:"bars"`
}

// positionReviewBlock renders the review context for the analyst message.
func positionReviewBlock(rc *PositionReviewContext) string {
	if rc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Position review (since entry; the snapshot's account facts are the authoritative position record):\n")
	fmt.Fprintf(&b, "- side %s, size %s, entry %s, mark %s", rc.Side, rc.Size, rc.EntryPrice, rc.MarkPrice)
	if rc.UnrealisedPNLUSD != "" {
		fmt.Fprintf(&b, ", unrealised PnL %s USD", rc.UnrealisedPNLUSD)
	}
	b.WriteString("\n")
	if rc.OpenedAt != "" {
		fmt.Fprintf(&b, "- opened at %s (%d bars in the window)\n", rc.OpenedAt, rc.Bars)
	} else {
		fmt.Fprintf(&b, "- entry time unknown; using a %dh fallback window (%d bars) -- this is NOT the since-entry window\n", rc.WindowHours, rc.Bars)
	}
	switch {
	case rc.FundingFeeNote != "":
		fmt.Fprintf(&b, "- funding fees since entry: %s\n", rc.FundingFeeNote)
	case rc.FundingFeeUSD != "":
		fmt.Fprintf(&b, "- funding fees since entry: %s USD (%d payments)\n", rc.FundingFeeUSD, rc.FundingFeePayments)
	}
	switch {
	case rc.ProtectiveStopStatus != "":
		fmt.Fprintf(&b, "- protective stop: %s at %s\n", rc.ProtectiveStopStatus, rc.ProtectiveStopTrigger)
	case rc.ProtectiveStopNote != "":
		fmt.Fprintf(&b, "- protective stop: %s\n", rc.ProtectiveStopNote)
	}
	fmt.Fprintf(&b, "- change in window: %s%%\n", rc.ChangePercent)
	fmt.Fprintf(&b, "- maximum adverse excursion: %s%%\n", rc.AdverseExcursionPercent)
	fmt.Fprintf(&b, "- window range (high-low over last close): %s%%\n", rc.RangePercent)
	b.WriteString("Decide HOLD (keep the position) or REDUCE (close it); any other action is a contract violation.\n")
	return b.String()
}

// iFundingIncomeSource is the venue seam for the since-entry funding-fee sum
// (binance's Exchange satisfies it via UAccountIncomeHistory).
type iFundingIncomeSource interface {
	UAccountIncomeHistory(ctx context.Context, symbol currency.Pair, incomeType string, limit int64, startTime, endTime time.Time) ([]binance.UAccountIncomeHistory, error)
}

// fundingIncomeLimit bounds one income-history page (the venue maximum).
const fundingIncomeLimit = 1000

// fundingFeesSince sums FUNDING_FEE income over the window. A failed or
// unsupported read is reported as a note, never disguised as zero.
func fundingFeesSince(ctx context.Context, source any, pair currency.Pair, start, end time.Time) (sum string, payments int, note string) {
	income, ok := source.(iFundingIncomeSource)
	if !ok {
		return "", 0, "unavailable (venue exposes no income history)"
	}
	entries, err := income.UAccountIncomeHistory(ctx, pair, "FUNDING_FEE", fundingIncomeLimit, start, end)
	if err != nil {
		return "", 0, "unavailable (venue read failed)"
	}
	total := decimal.Zero
	for i := range entries {
		total = total.Add(decimal.MustFromFloat(entries[i].Income))
	}
	return total.String(), len(entries), ""
}

// latestProtectiveStop reports the newest protective-exit row recorded for the
// entry intent: its lifecycle status and the stop trigger price. No row means
// the entry has no protective exit on record -- stated, not improvised away.
func latestProtectiveStop(ctx context.Context, entryIntentID string) (status, trigger, note string) {
	exits, err := llmstore.ListProtectiveExitsByParent(ctx, entryIntentID)
	if err != nil {
		return "", "", "unavailable (store read failed)"
	}
	if len(exits) == 0 {
		return "", "", "none on record"
	}
	last := exits[len(exits)-1] // the store orders by created_at ascending
	return last.Status, last.TriggerPrice, ""
}

// buildPositionReviewRequest assembles one position-review run: the base
// decision request plus the since-entry context, under the review trigger.
func (bot *Engine) buildPositionReviewRequest(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error) {
	if bot == nil || bot.llmManager == nil {
		return nil, ErrChatNotConfigured
	}
	cfg := &bot.Config.LLM
	base, err := bot.buildShadowRunRequest(ctx, strategyVersionID, marketID)
	if err != nil {
		return nil, err
	}
	item, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, fmt.Errorf("llm position review: asset %q: %w", cfg.Asset, err)
	}
	pair, err := parseMarketPair(cfg.Exchange, item, marketID)
	if err != nil {
		return nil, err
	}
	if bot.ExchangeManager == nil {
		return nil, fmt.Errorf("%w: no exchange manager for the position read", ErrChatNotConfigured)
	}
	source, err := bot.ExchangeManager.GetExchangeByName(cfg.Exchange)
	if err != nil {
		return nil, fmt.Errorf("llm position review: exchange %q: %w", cfg.Exchange, err)
	}
	summarySource, ok := source.(iPositionSummarySource)
	if !ok {
		return nil, fmt.Errorf("%w: exchange %q does not expose the position summary call", ErrRiskFactsInput, cfg.Exchange)
	}
	summary, err := summarySource.GetFuturesPositionSummary(ctx, &futures.PositionSummaryRequest{Asset: item, Pair: pair})
	if err != nil {
		return nil, fmt.Errorf("llm position review: reading the position: %w", err)
	}
	if summary == nil || summary.CurrentSize.IsZero() {
		return nil, fmt.Errorf("llm position review: %s carries no open position to review", marketID)
	}
	side, size := PositionSideLong, summary.CurrentSize
	if summary.CurrentSize.IsNegative() {
		side, size = PositionSideShort, summary.CurrentSize.Abs()
	}
	now := time.Now()
	review := &PositionReviewContext{
		Side: side, Size: size.String(),
		EntryPrice: summary.AverageOpenPrice.String(),
		MarkPrice:  summary.MarkPrice.String(),
	}
	if !summary.UnrealisedPNL.IsZero() {
		review.UnrealisedPNLUSD = summary.UnrealisedPNL.String()
	}
	// The since-entry window: local intents say when the position was opened;
	// an unreadable entry time falls back to 48h, clearly flagged.
	entryIntentID, openedAt, hasOpenedAt, err := llmstore.LastEntryIntentAt(ctx, cfg.Exchange, pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String(), item.String())
	if err != nil {
		return nil, fmt.Errorf("llm position review: entry intent time: %w", err)
	}
	interval, err := intervalFromConfig(cfg.Indicators.DefaultInterval)
	if err != nil {
		return nil, fmt.Errorf("llm position review: interval: %w", err)
	}
	start := now.Add(-48 * time.Hour)
	if hasOpenedAt && openedAt.Before(now) {
		review.OpenedAt = openedAt.UTC().Format(time.RFC3339)
		if openedAt.After(start) {
			start = openedAt
		}
	}
	review.WindowHours = int(now.Sub(start).Hours())
	review.WindowFallback = !hasOpenedAt
	if hasOpenedAt {
		review.ProtectiveStopStatus, review.ProtectiveStopTrigger, review.ProtectiveStopNote = latestProtectiveStop(ctx, entryIntentID)
	} else {
		review.ProtectiveStopNote = "none on record (no entry intent)"
	}
	if sum, payments, note := fundingFeesSince(ctx, source, pair, start, now); note != "" {
		review.FundingFeeNote = note
	} else {
		review.FundingFeeUSD = sum
		review.FundingFeePayments = payments
	}
	var candles []kline.Candle
	if cache := bot.llmManager.Cache(); cache != nil {
		candles = cache.closedCandles(pair, item, interval, start, now)
	}
	stats, ok := positionReviewStats(candles, review.EntryPrice, side)
	if !ok {
		return nil, fmt.Errorf("llm position review: no cached candles cover the window for %s", marketID)
	}
	review.ChangePercent = stats.ChangePercent
	review.AdverseExcursionPercent = stats.AdverseExcursionPercent
	review.RangePercent = stats.RangePercent
	review.Bars = stats.Bars
	base.Trigger = DecisionTriggerPositionReview
	base.Review = review
	return base, nil
}

// positionReviewStats summarises one window against the entry price:
// change % (last close vs entry), maximum adverse excursion % (worst low for
// longs / worst high for shorts, reported as a positive adverse magnitude)
// and the window range % over the last close.
type positionReviewStatsResult struct {
	ChangePercent           string
	AdverseExcursionPercent string
	RangePercent            string
	Bars                    int
}

func positionReviewStats(candles []kline.Candle, entry, side string) (positionReviewStatsResult, bool) {
	var out positionReviewStatsResult
	if len(candles) == 0 {
		return out, false
	}
	entryValue, err := strconv.ParseFloat(strings.TrimSpace(entry), 64)
	if err != nil || entryValue == 0 {
		return out, false
	}
	last := candles[len(candles)-1].Close
	if last == 0 {
		return out, false
	}
	high, low := candles[0].High, candles[0].Low
	for i := range candles {
		if candles[i].High > high {
			high = candles[i].High
		}
		if candles[i].Low < low {
			low = candles[i].Low
		}
	}
	// These summary numbers are display context; the authoritative facts ride
	// the snapshot the model already receives.
	out.ChangePercent = strconv.FormatFloat((last-entryValue)/entryValue*100, 'f', 2, 64)
	adverse := (entryValue - low) / entryValue * 100
	if side != PositionSideLong {
		adverse = (high - entryValue) / entryValue * 100
	}
	out.AdverseExcursionPercent = strconv.FormatFloat(adverse, 'f', 2, 64)
	out.RangePercent = strconv.FormatFloat((high-low)/last*100, 'f', 2, 64)
	out.Bars = len(candles)
	return out, true
}
