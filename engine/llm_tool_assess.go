package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/screener"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// iJevBoundedJudge is the chat advisory seam; *jev.Client satisfies it.
type iJevBoundedJudge interface {
	Judge(ctx context.Context, req *jev.SystemOneRequest) (*jev.AnswerSet, error)
}

// 对话 · 开盘机会评估（用户 2026-10-17 要求）：对筛选结果逐市场问 Jev「这个
// 周期是否有可开仓机会」，对判为 open 的市场再问一次「做多还是做空」，最终只
// 呈现「适合开仓 + 方向」的结论。两段都是有界 Choice 问题；预算、模型漂移与
// 答案校验走与评审相同的闸门。

const (
	assessQuestionOpportunity = "open_opportunity"
	assessQuestionDirection   = "open_direction"
	// assessVerdictOpen is the choice key meaning "a position is defensible".
	assessVerdictOpen = "open"
	// assessSpacing paces the fact reads between markets.
	assessSpacing = 120 * time.Millisecond
)

// assessMetrics are the facts handed to the judge: ticker + cached/on-demand
// candle metrics, no venue flow reads (one batch keeps the advisory cheap).
var assessMetrics = []string{
	"lastprice", "change24h", "quotevolume24h", "change4h", "change48h",
	"atrpercent", "range24h", "macdstate", "macdhist", "macdcross",
}

type assessState struct {
	Market  string         `json:"market"`
	Horizon string         `json:"horizon"`
	AsOf    string         `json:"asOf"`
	Note    string         `json:"note,omitempty"`
	Facts   map[string]any `json:"facts"`
}

type assessRow struct {
	Market              string  `json:"market"`
	Opportunity         string  `json:"opportunity"`
	Direction           string  `json:"direction,omitempty"`
	Confidence          float64 `json:"confidence,omitempty"`
	DirectionConfidence float64 `json:"directionConfidence,omitempty"`
	Error               string  `json:"error,omitempty"`
}

func (r *LLMToolRunner) runAssessOpeningOpportunities(ctx context.Context, raw string) (any, time.Time, error) {
	args, err := tools.DecodeAssessOpening(raw)
	if err != nil {
		return nil, time.Time{}, err
	}
	if r.JevJudge == nil || r.JevBudget == nil || r.JevRevisionID == "" || r.JevModel == "" {
		return nil, time.Time{}, fmt.Errorf("%w: the Jev judge is not assembled for this bot", ErrChatNotConfigured)
	}
	if r.Scope == nil {
		return nil, time.Time{}, fmt.Errorf("%w: the assessment needs the run scope", tools.ErrArgumentsInvalid)
	}
	for _, market := range args.Markets {
		if _, ok := r.Scope.Markets[market]; !ok {
			return nil, time.Time{}, fmt.Errorf("%w: market %q is outside the run scope", tools.ErrArgumentsInvalid, market)
		}
	}
	now := r.now()
	universe, notes := r.assessUniverse(ctx, args.Markets, now)
	result, err := screener.Evaluate(&screener.Request{Include: assessMetrics, Limit: len(universe)}, universe)
	if err != nil {
		return nil, time.Time{}, err
	}
	facts := make(map[string]map[string]any, len(result.Rows))
	for i := range result.Rows {
		row := &result.Rows[i]
		fact := make(map[string]any, len(assessMetrics))
		for _, name := range assessMetrics {
			metric, _ := screener.Lookup(name)
			if metric.Kind == screener.KindCategory {
				if state, ok := row.States[name]; ok {
					fact[name] = state
				} else {
					fact[name] = nil
				}
				continue
			}
			if value, ok := row.Numbers[name]; ok && value != nil {
				fact[name] = *value
			} else {
				fact[name] = nil
			}
		}
		facts[row.MarketID] = fact
	}
	rows := make([]assessRow, 0, len(args.Markets))
	open := 0
	for index, market := range args.Markets {
		state := &assessState{
			Market: market, Horizon: args.Horizon, AsOf: now.UTC().Format(time.RFC3339), Note: args.Note,
			Facts: facts[market],
		}
		row := assessRow{Market: market}
		opportunity, judgeErr := r.judgeChoice(ctx, assessQuestionOpportunity, opportunityQuestion(), state, now)
		switch {
		case judgeErr != nil:
			row.Opportunity = "ERROR"
			row.Error = judgeErr.Error()
		case opportunity.Choice == assessVerdictOpen:
			row.Opportunity = "OPEN"
			row.Confidence = opportunity.Confidence
			open++
			direction, directionErr := r.judgeChoice(ctx, assessQuestionDirection, directionQuestion(), state, now)
			if directionErr != nil {
				row.Error = directionErr.Error()
			} else {
				row.Direction = directionChoice(direction.Choice)
				row.DirectionConfidence = direction.Confidence
			}
		default:
			row.Opportunity = "NO_TRADE"
			row.Confidence = opportunity.Confidence
		}
		rows = append(rows, row)
		reportToolProgress(ctx, ToolProgress{
			Tool: tools.ToolAssessOpening, Phase: "JEV", Done: index + 1, Total: len(args.Markets),
			Message: fmt.Sprintf("Jev 判断 %d/%d 个合约（适合开仓 %d）", index+1, len(args.Markets), open),
		})
	}
	out := map[string]any{"horizon": args.Horizon, "rows": rows}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	return out, now, nil
}

// assessUniverse builds the per-market facts the judge sees: the same market
// shape the screener uses (ticker + closed candles, cache first, on-demand reads
// paced).
func (r *LLMToolRunner) assessUniverse(ctx context.Context, markets []string, now time.Time) (universe []*screener.Market, notesOut []string) {
	notes := []string{}
	out := make([]*screener.Market, 0, len(markets))
	stats := r.batchTickerStats(ctx)
	unrestricted, canFetch := r.Source.(iUnrestrictedKlineSource)
	start := now.Add(-time.Duration(screenerCandleWindowBars) * r.Interval.Duration())
	fetched := 0
	for _, marketID := range markets {
		pair, err := parseMarketPair(r.Exchange, r.Asset, marketID)
		if err != nil {
			notes = append(notes, fmt.Sprintf("market %q could not be parsed and was skipped", marketID))
			continue
		}
		m := &screener.Market{MarketID: marketID}
		if stat, ok := stats[normaliseSymbol(pair.Format(currencyPairFormatNoDelimiter()).String())]; ok {
			last := stat.LastPrice.Float64()
			m.Last, m.HasLast = last, last != 0
			m.Change24h, m.HasChange24h = stat.PriceChangePercent.Float64(), true
			m.QuoteVolume24h, m.HasVolume24h = stat.QuoteVolume.Float64(), true
		} else if price, tickerErr := ticker.GetTicker(r.Exchange, pair, r.Asset); tickerErr == nil && price != nil {
			m.Last, m.HasLast = price.Last, price.Last != 0
			m.Change24h, m.HasChange24h = price.PercentChange24Hour, true
			m.QuoteVolume24h, m.HasVolume24h = price.QuoteVolume, true
		}
		candles := r.cachedCandles(pair, start, now)
		if len(candles) == 0 && canFetch {
			if fetched > 0 {
				paceScan(ctx, assessSpacing)
			}
			window, fetchErr := unrestricted.GetHistoricCandlesUnrestricted(ctx, pair, r.Asset, r.Interval, start, now)
			if fetchErr != nil {
				notes = append(notes, fmt.Sprintf("candles for %s are unavailable: %v", marketID, fetchErr))
			} else {
				fetched++
				if r.Cache != nil && window != nil {
					_, _ = r.Cache.merge(pair, r.Asset, r.Interval, window, now)
				}
				candles = r.cachedCandles(pair, start, now)
				if len(candles) == 0 && window != nil {
					candles = window.Candles
				}
			}
		}
		if len(candles) > 0 {
			m.Candles = toScreenerCandles(candles)
		}
		out = append(out, m)
	}
	return out, notes
}

// judgeChoice runs one bounded choice question through the judge, wrapped in a
// budget reservation.
func (r *LLMToolRunner) judgeChoice(ctx context.Context, key string, question jev.ChoiceQuestion, state any, now time.Time) (*jev.ChoiceAnswer, error) {
	raw, err := json.Marshal(question)
	if err != nil {
		return nil, err
	}
	request := &jev.SystemOneRequest{
		Model:     r.JevModel,
		State:     state,
		Questions: map[string]json.RawMessage{key: raw},
	}
	reservation, err := r.JevBudget.Reserve(ctx, BudgetReservationRequest{
		ProfileRevisionID: r.JevRevisionID, ProviderKind: ProviderKindJev, Purpose: "CHAT",
	}, now)
	if err != nil {
		return nil, fmt.Errorf("jev budget: %w", err)
	}
	started := time.Now()
	answers, err := r.JevJudge.Judge(ctx, request)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		if releaseErr := r.JevBudget.Release(ctx, reservation.ID, r.now()); releaseErr != nil {
			log.Warnf(log.LLMMgr, "jev advisory reservation %s could not be released: %v", reservation.ID, releaseErr)
		}
		return nil, err
	}
	if err := r.JevBudget.MarkUsed(ctx, reservation.ID, int64(answers.Usage.InputTokens), int64(answers.Usage.OutputTokens), latency, r.now()); err != nil {
		return nil, err
	}
	answer := answers.Choices[key]
	if answer == nil {
		return nil, fmt.Errorf("jev advisory: no choice answer for %s", key)
	}
	return answer, nil
}

func opportunityQuestion() jev.ChoiceQuestion {
	return jev.ChoiceQuestion{
		Type:         jev.QuestionTypeChoice,
		Instructions: "Using only the supplied state (market facts and the operator's stated holding horizon), decide whether opening a NEW position is defensible for that horizon. Choose 'open' only when the evidence supports a tradable opportunity; choose 'no_trade' otherwise. Do not assume facts that are absent.",
		Criteria: map[string]string{
			"open":     "a new position is defensible for the stated horizon",
			"no_trade": "no defensible new position for the stated horizon",
		},
	}
}

func directionQuestion() jev.ChoiceQuestion {
	return jev.ChoiceQuestion{
		Type:         jev.QuestionTypeChoice,
		Instructions: "An opening opportunity was already judged positive for this market and horizon. Using only the supplied state, choose the direction for a new position: 'long' or 'short'.",
		Criteria: map[string]string{
			"long":  "a long position is the better direction",
			"short": "a short position is the better direction",
		},
	}
}

func directionChoice(choice string) string {
	switch choice {
	case "long":
		return "LONG"
	case "short":
		return "SHORT"
	default:
		return ""
	}
}

// currencyPairFormatNoDelimiter is the key spelling the batch ticker uses
// (BTCUSDT), fed through normaliseSymbol.
func currencyPairFormatNoDelimiter() currency.PairFormat {
	return currency.PairFormat{Delimiter: "", Uppercase: true}
}
