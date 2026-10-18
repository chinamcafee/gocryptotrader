package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/screener"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// M2-17 · 全宇宙筛选的执行器（docs/llm-dev/13 §3.1）。
//
// 三层数据齐一人手：T0 缓存 ticker（24h 涨跌幅/成交额）、T1 K 线缓存的已收盘
// 窗口（零权重：全量重载已经在缓存里）、T2 净流入的逐符号场地读（只在
// criteria/sort/include 真的引用了场地档指标时、且只对成交额前 K 个市场做，
// 并发受限）。缺席一律 null：依赖缺席指标的 criteria 把市场排除并计数——
// 与全仓"缺席不伪装"同纪律。

// iTakerFlowSource is the venue seam for the T2 net-flow metric (binance's
// Exchange satisfies it via UTakerBuySellVol).
type iTakerFlowSource interface {
	GetName() string
	UTakerBuySellVol(ctx context.Context, symbol currency.Pair, period string, limit int64, startTime, endTime time.Time) ([]binance.UTakerVolumeData, error)
}

// iBatchTickerSource is the venue seam for one batched 24h ticker read across
// every symbol (N10: the full universe's T0 tier costs one request).
type iBatchTickerSource interface {
	U24HTickerPriceChangeStats(ctx context.Context, symbol currency.Pair) ([]binance.U24HrPriceChangeStats, error)
}

// iUnrestrictedKlineSource reads closed candles for a pair that is not in the
// enabled set: the screener must cover available markets the operator has not
// enabled (N10 on-demand read-through), and the ordinary candle path refuses
// those pairs by design.
type iUnrestrictedKlineSource interface {
	GetHistoricCandlesUnrestricted(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
}

const (
	// screenerCandleWindowBars bounds the cached window the T1 metrics read:
	// MACD needs ~35 bars and the 48h change 49; 300 is the reload depth.
	screenerCandleWindowBars = 300
	// screenerFlowWorkers bounds the T2 read concurrency.
	screenerFlowWorkers = 8
	// defaultScreenerFlowScanLimit is the T2 coverage when the config is unset.
	defaultScreenerFlowScanLimit = 100
	// screenerFlowPeriod is the taker-flow bucket width ("1d" = one 24h row).
	screenerFlowPeriod = "1d"
	// screenerScanSpacing paces on-demand candle reads so a full-universe scan
	// stays far under the venue's weight budget (weight 2 per read: ~16/s at
	// this spacing is ~960/min against Binance's 2400/min).
	screenerScanSpacing = 120 * time.Millisecond
	// screenerScanWorkers bounds concurrent on-demand candle reads. Six workers
	// at the dispatch spacing keep the scan near two minutes for ~590 markets.
	screenerScanWorkers = 6
)

// toScreenerCandles copies the closed window into the screener's value type.
func toScreenerCandles(candles []kline.Candle) []screener.Candle {
	out := make([]screener.Candle, 0, len(candles))
	for i := range candles {
		out = append(out, screener.Candle{
			Open: candles[i].Open, High: candles[i].High, Low: candles[i].Low,
			Close: candles[i].Close, Volume: candles[i].Volume,
		})
	}
	return out
}

// RunScreenMarkets executes one screenMarkets call: assemble the universe,
// evaluate the criteria, report honestly what was scanned.
// RunScreenMarkets executes one screenMarkets call (exported for the GUI
// RPC; the chat tool path calls it through Run).
func (r *LLMToolRunner) RunScreenMarkets(ctx context.Context, raw string) (payload any, asOf time.Time, err error) {
	args, err := tools.DecodeScreenMarkets(raw)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := r.now()
	if r.Interval.Duration() <= 0 {
		return nil, time.Time{}, errors.New("the screener is not assembled: the candle interval is missing (check the assembly logs)")
	}
	markets, notes := r.screenerMarketList()
	if len(markets) == 0 {
		// Environmental, not the model's fault: retrying the same call cannot
		// help, so this is deliberately NOT ErrArgumentsInvalid.
		return nil, time.Time{}, errors.New("the screener has no markets: enable llm.coverage.enabled or add at least one enabled pair")
	}
	reportToolProgress(ctx, ToolProgress{
		Tool: tools.ToolScreenMarkets, Phase: "UNIVERSE", Total: len(markets),
		Message: fmt.Sprintf("开始扫描 %d 个合约", len(markets)),
	})
	universe, buildNotes := r.buildScreenerUniverse(ctx, args, now, markets)
	notes = append(notes, buildNotes...)
	result, err := screener.Evaluate(&screener.Request{
		Criteria: args.Criteria, Sort: args.Sort, Limit: args.Limit, Include: args.Include,
	}, universe)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := map[string]any{
		"rows": result.Rows,
		"meta": result.Meta,
	}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	payload = out
	return payload, now, nil
}

// screenerMarketList resolves the scan universe. Full-universe mode (N10) takes
// every available in-scope perpetual -- the same scope rule the coverage plan
// uses -- and orders the operator's enabled markets first (N13). Without a
// full universe the covered/rotator list is used as before.
func (r *LLMToolRunner) screenerMarketList() (markets, notes []string) {
	markets = r.Markets
	if r.FullUniverse {
		if available, err := r.Source.GetAvailablePairs(r.Asset); err == nil && len(available) > 0 {
			symbols := make([]string, 0, len(available))
			for i := range available {
				symbols = append(symbols, available[i].Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String())
			}
			selected, summary, scopeErr := coverage.SelectMarkets(coverage.ScopeAllUSDTMPerpetual, symbols)
			if scopeErr == nil {
				// SelectMarkets speaks BASE_QUOTE; the screener evaluates run ids.
				markets = make([]string, 0, len(selected))
				for _, symbol := range selected {
					key, keyErr := runMarketID(r.Exchange, r.Asset, symbol)
					if keyErr != nil {
						continue
					}
					markets = append(markets, key)
				}
				notes = append(notes, fmt.Sprintf(
					"full universe: %d selected of %d available (excluded: dated futures %d, other quote %d, empty %d)",
					summary.Selected, summary.Received, summary.DatedFutures, summary.OtherQuote, summary.Empty))
			}
		}
	}
	return r.orderEnabledFirst(markets), notes
}

// orderEnabledFirst puts the enabled markets at the head of the scan (N13):
// when the answer is "top N", the markets the operator cares about are read
// first, so a slow full scan still surfaces them early.
func (r *LLMToolRunner) orderEnabledFirst(markets []string) []string {
	if len(markets) < 2 {
		return markets
	}
	enabled, err := r.Source.GetEnabledPairs(r.Asset)
	if err != nil {
		return markets
	}
	enabledSet := make(map[string]bool, len(enabled))
	for i := range enabled {
		key, keyErr := runMarketID(r.Exchange, r.Asset, enabled[i].Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String())
		if keyErr != nil {
			continue
		}
		enabledSet[key] = true
	}
	sort.SliceStable(markets, func(i, j int) bool {
		return enabledSet[markets[i]] && !enabledSet[markets[j]]
	})
	return markets
}

// toolEvidenceResultBudget keeps a serialised tool result under the evidence
// byte cap with envelope headroom. The cap rejects, it does not truncate --
// a legitimate screen at limit 50 with many metrics can serialise past it.
const toolEvidenceResultBudget = candidate.MaxToolResultBytes - 1024

// fitScreenResultToEvidenceBudget trims rows from the tail (the worst-ranked
// last) until the serialised map fits the budget, and states the trim in the
// notes: the model is never fed a silently shortened list, and a large but
// legitimate screen never fails the whole tool call.
func fitScreenResultToEvidenceBudget(out map[string]any) map[string]any {
	raw, err := json.Marshal(out)
	if err != nil || len(raw) <= toolEvidenceResultBudget {
		return out
	}
	rows, ok := out["rows"].([]screener.Row)
	if !ok || len(rows) == 0 {
		return out
	}
	omitted := 0
	for len(rows) > 0 && len(raw) > toolEvidenceResultBudget {
		omitted++
		rows = rows[:len(rows)-1]
		out["rows"] = rows
		if raw, err = json.Marshal(out); err != nil {
			break
		}
	}
	notes, _ := out["notes"].([]string)
	out["notes"] = append(notes, fmt.Sprintf(
		"result trimmed to %d rows to fit the tool-result byte budget (%d omitted; narrow criteria or lower limit for a complete view)",
		len(rows), omitted))
	return out
}

// buildScreenerUniverse assembles one Market per universe entry. T0 comes from
// one batched ticker read (falling back to the ticker cache); T1 comes from the
// candle cache, and a market the cache has never seen is fetched on demand and
// backfilled -- pace-limited so a 500+ market scan stays far under the venue's
// weight budget. Absent data is counted and named, never zero-filled.
func (r *LLMToolRunner) buildScreenerUniverse(ctx context.Context, args *tools.ScreenMarketsArgs, now time.Time, markets []string) (outMarkets []*screener.Market, outNotes []string) {
	notes := []string{}
	out := make([]*screener.Market, len(markets))
	stats := r.batchTickerStats(ctx)
	unrestricted, canFetch := r.Source.(iUnrestrictedKlineSource)
	missingTicker := 0
	start := now.Add(-time.Duration(screenerCandleWindowBars) * r.Interval.Duration())
	type pendingFetch struct {
		index    int
		marketID string
		pair     currency.Pair
	}
	needCandles := make([]pendingFetch, 0, len(markets))
	for index, marketID := range markets {
		pair, err := parseMarketPair(r.Exchange, r.Asset, marketID)
		if err != nil {
			// The universe is internal and normalised; an unparsable entry is a
			// wiring defect we report rather than screen over silently.
			notes = append(notes, fmt.Sprintf("market %q could not be parsed and was skipped", marketID))
			out[index] = &screener.Market{MarketID: marketID}
			continue
		}
		m := &screener.Market{MarketID: marketID}
		key := normaliseSymbol(pair.Format(currency.PairFormat{Delimiter: "", Uppercase: true}).String())
		if stat, ok := stats[key]; ok {
			last := stat.LastPrice.Float64()
			change := stat.PriceChangePercent.Float64()
			volume := stat.QuoteVolume.Float64()
			m.Last, m.HasLast = last, last != 0
			// Zero change is a fact here (a flat day), not an absence.
			m.Change24h, m.HasChange24h = change, true
			m.QuoteVolume24h, m.HasVolume24h = volume, true
		} else if price, tickerErr := ticker.GetTicker(r.Exchange, pair, r.Asset); tickerErr == nil && price != nil {
			m.Last, m.HasLast = price.Last, price.Last != 0
			m.Change24h, m.HasChange24h = price.PercentChange24Hour, true
			m.QuoteVolume24h, m.HasVolume24h = price.QuoteVolume, true
		} else {
			missingTicker++
		}
		if candles := r.cachedCandles(pair, start, now); len(candles) > 0 {
			m.Candles = toScreenerCandles(candles)
		} else if canFetch {
			needCandles = append(needCandles, pendingFetch{index: index, marketID: marketID, pair: pair})
		}
		out[index] = m
	}

	fetched, failed := 0, 0
	if len(needCandles) > 0 {
		// On-demand reads run in a small worker pool: the dispatch spacing and
		// the pool together keep the request rate near 8/s (weight 2 each,
		// ~960/min against Binance's 2400/min) while a 500+ market scan finishes
		// in a couple of minutes instead of ten.
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, screenerScanWorkers)
		for _, job := range needCandles {
			paceScan(ctx, screenerScanSpacing)
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(job pendingFetch) {
				defer wg.Done()
				defer func() { <-sem }()
				window, fetchErr := unrestricted.GetHistoricCandlesUnrestricted(ctx, job.pair, r.Asset, r.Interval, start, now)
				mu.Lock()
				defer mu.Unlock()
				if fetchErr != nil {
					failed++
				} else {
					fetched++
					if r.Cache != nil && window != nil {
						if _, mergeErr := r.Cache.merge(job.pair, r.Asset, r.Interval, window, now); mergeErr != nil {
							notes = append(notes, fmt.Sprintf("candles for %s were fetched but not cached: %v", job.marketID, mergeErr))
						}
					}
					candles := r.cachedCandles(job.pair, start, now)
					if len(candles) == 0 && window != nil {
						candles = window.Candles
					}
					if len(candles) > 0 {
						out[job.index].Candles = toScreenerCandles(candles)
					}
				}
				done := fetched + failed
				reportToolProgress(ctx, ToolProgress{
					Tool: tools.ToolScreenMarkets, Phase: "CANDLES", Done: done, Total: len(needCandles),
					Message: fmt.Sprintf("已扫描 %d/%d（按需取数 %d，失败 %d）", len(markets)-len(needCandles)+done, len(markets), fetched, failed),
				})
			}(job)
		}
		wg.Wait()
	}
	if missingTicker > 0 {
		notes = append(notes, fmt.Sprintf("%d markets had no ticker (24h metrics absent for them)", missingTicker))
	}
	if fetched > 0 || failed > 0 {
		notes = append(notes, fmt.Sprintf("on-demand candle reads: %d fetched, %d failed (the cache is warmed for repeat screens)", fetched, failed))
	}
	if r.FullUniverse && !canFetch {
		notes = append(notes, "this venue does not expose the unrestricted candle read: only cached markets carry candles")
	}
	notes = append(notes, fmt.Sprintf("candle window: up to %d closed %s bars per market (cache plus on-demand reads; staleness is bounded by one rotation cycle)", screenerCandleWindowBars, r.Interval))
	if screenerNeedsFlow(args) {
		reportToolProgress(ctx, ToolProgress{Tool: tools.ToolScreenMarkets, Phase: "FLOW", Message: "读取资金流（T2）…"})
		scanned, flowFailed, venueOK := r.attachFlowFacts(ctx, out)
		if !venueOK {
			notes = append(notes, "the venue does not expose the taker-flow read: flow metrics are absent for every market")
		} else {
			notes = append(notes, fmt.Sprintf("taker-flow read covered the top %d markets by 24h quote volume: %d succeeded, %d failed (their flow metrics are absent)", r.flowScanLimit(), scanned, flowFailed))
		}
	}
	return out, notes
}

// batchTickerStats reads every symbol's 24h ticker in one request (T0 for the
// full universe). A venue without the batch seam or a failed read falls back to
// the ticker cache per market.
func (r *LLMToolRunner) batchTickerStats(ctx context.Context) map[string]binance.U24HrPriceChangeStats {
	out := make(map[string]binance.U24HrPriceChangeStats)
	source, ok := r.Source.(iBatchTickerSource)
	if !ok {
		return out
	}
	stats, err := source.U24HTickerPriceChangeStats(ctx, currency.EMPTYPAIR)
	if err != nil {
		return out
	}
	for i := range stats {
		out[normaliseSymbol(stats[i].Symbol)] = stats[i]
	}
	return out
}

// cachedCandles returns the closed window from the cache; a nil cache (tests,
// alternate assemblies) simply has nothing cached.
func (r *LLMToolRunner) cachedCandles(pair currency.Pair, start, now time.Time) []kline.Candle {
	if r.Cache == nil {
		return nil
	}
	return r.Cache.closedCandles(pair, r.Asset, r.Interval, start, now)
}

// normaliseSymbol folds venue and pair spellings to one alphanumeric key
// (BTC_USDT / BTCUSDT / btcusdt all collide on purpose).
func normaliseSymbol(value string) string {
	return strings.Map(func(char rune) rune {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			return char
		}
		return -1
	}, strings.ToUpper(value))
}

// paceScan waits between on-demand reads, bounded by the call's context.
func paceScan(ctx context.Context, pause time.Duration) {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// attachFlowFacts reads the T2 taker flow for the top-volume markets, bounded
// by the configured scan limit and a small worker pool. Per-market failures
// leave that market's flow absent (counted), never zero.
func (r *LLMToolRunner) attachFlowFacts(ctx context.Context, markets []*screener.Market) (okCount, failed int, venueOK bool) {
	source, isFlow := r.Source.(iTakerFlowSource)
	if !isFlow {
		return 0, 0, false
	}
	candidates := make([]*screener.Market, 0, len(markets))
	for _, m := range markets {
		if m.HasVolume24h && m.QuoteVolume24h > 0 {
			candidates = append(candidates, m)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].QuoteVolume24h > candidates[j].QuoteVolume24h })
	if limit := r.flowScanLimit(); len(candidates) > limit {
		candidates = candidates[:limit]
	}
	sem := make(chan struct{}, screenerFlowWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, market := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(m *screener.Market) {
			defer wg.Done()
			defer func() { <-sem }()
			pair, err := parseMarketPair(r.Exchange, r.Asset, m.MarketID)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			records, err := source.UTakerBuySellVol(ctx, pair, screenerFlowPeriod, 1, time.Time{}, time.Time{})
			if err != nil || len(records) == 0 {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			last := records[len(records)-1]
			mu.Lock()
			m.Flow = &screener.FlowFacts{BuyVol: last.BuyVol, SellVol: last.SellVol}
			okCount++
			mu.Unlock()
		}(market)
	}
	wg.Wait()
	return okCount, failed, true
}

// flowScanLimit resolves the T2 coverage bound.
func (r *LLMToolRunner) flowScanLimit() int {
	if r.FlowScanLimit > 0 {
		return r.FlowScanLimit
	}
	return defaultScreenerFlowScanLimit
}

// screenerNeedsFlow reports whether any referenced metric needs the T2 read.
func screenerNeedsFlow(args *tools.ScreenMarketsArgs) bool {
	if args == nil {
		return false
	}
	names := make([]string, 0, len(args.Criteria)+len(args.Sort)+len(args.Include))
	for i := range args.Criteria {
		names = append(names, args.Criteria[i].Metric)
	}
	for i := range args.Sort {
		names = append(names, args.Sort[i].Metric)
	}
	names = append(names, args.Include...)
	for _, name := range names {
		if metric, ok := screener.Lookup(name); ok && metric.Tier == screener.TierVenue {
			return true
		}
	}
	return false
}
