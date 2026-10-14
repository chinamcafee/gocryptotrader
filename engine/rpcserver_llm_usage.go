package engine

import (
	"context"
	"errors"
	"sort"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W7-03 预算与用量读面（docs 05 §7、Q15）：把预算预约表里已经结算的
// token 记账汇总出来（按 provider kind × purpose 分列）。
//
// **自估成本刻意缺席**：库中没有任何价目源（provider 修订版没有价格列），
// 而一个猜出来的美元数字比一个缺席的字段更糟——「缺席字段不进消息、前端
// 渲染『—』」是项目级诚实纪律（session-40）；成本展示等有价目源的那天。
//
// 用量口径：窗口内 **state=USED** 的预约（只有跑完的调用有 token 与延迟；
// RESERVED 还在途、RELEASED 没跑起来——把后者计入用量就是把没花掉的算成
// 花了）。窗口缺省 24h、上限 720h。

// errLLMUsageUnavailable marks the surface before the LLM subsystem is
// assembled.
var errLLMUsageUnavailable = errors.New("llm usage accounting is not configured on this bot")

// maxUsageWindowHours bounds the aggregation window: the table grows without
// limit, and an unbounded scan is a latency bomb behind one RPC.
const maxUsageWindowHours = 720

// LLMGetUsage reports token and reservation usage per kind and purpose.
func (s *RPCServer) LLMGetUsage(ctx context.Context, r *gctrpc.LLMGetUsageRequest) (*gctrpc.LLMGetUsageResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil {
		return nil, errLLMUsageUnavailable
	}
	hours := r.GetWindowHours()
	if hours <= 0 {
		hours = 24
	}
	if hours > maxUsageWindowHours {
		hours = maxUsageWindowHours
	}
	now := time.Now().UTC()
	from := now.Add(-time.Duration(hours) * time.Hour)
	out := &gctrpc.LLMGetUsageResponse{
		WindowHours: hours,
		CheckedAt:   now.Format(time.RFC3339),
	}
	pair, err := llmstore.GetActiveModelPair(ctx)
	switch {
	case err == nil:
	case errors.Is(err, llmstore.ErrNotFound):
		// 从未激活过配对：空用量是状态不是错误（与 LLMGetStatus 同口径）。
		return out, nil
	default:
		return nil, err
	}
	var revisions []string
	if pair.LLMProfileRevisionID != nil {
		revisions = append(revisions, *pair.LLMProfileRevisionID)
	}
	if pair.JevProfileRevisionID != nil {
		revisions = append(revisions, *pair.JevProfileRevisionID)
	}

	type lineKey struct{ kind, purpose string }
	type aggregate struct {
		line       gctrpc.LLMUsageLine
		latencySum int64
		latencyN   int64
	}
	aggregates := make(map[lineKey]*aggregate)
	for _, revisionID := range revisions {
		rows, err := llmstore.ListReservationsInWindow(ctx, revisionID, from)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			row := &rows[i]
			if row.Status != reservationStatusUsed {
				continue
			}
			key := lineKey{kind: row.Kind, purpose: row.Purpose}
			entry := aggregates[key]
			if entry == nil {
				entry = &aggregate{line: gctrpc.LLMUsageLine{Kind: row.Kind, Purpose: row.Purpose}}
				aggregates[key] = entry
			}
			entry.line.Reservations++
			if row.InputTokens != nil {
				entry.line.InputTokens += *row.InputTokens
			}
			if row.OutputTokens != nil {
				entry.line.OutputTokens += *row.OutputTokens
			}
			if row.LatencyMS != nil {
				entry.latencySum += *row.LatencyMS
				entry.latencyN++
			}
		}
	}

	keys := make([]lineKey, 0, len(aggregates))
	for key := range aggregates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].purpose < keys[j].purpose
	})
	for _, key := range keys {
		entry := aggregates[key]
		if entry.latencyN > 0 {
			entry.line.AvgLatencyMs = entry.latencySum / entry.latencyN
		}
		out.Lines = append(out.Lines, &entry.line)
		out.TotalReservations += entry.line.Reservations
		out.TotalInputTokens += entry.line.InputTokens
		out.TotalOutputTokens += entry.line.OutputTokens
	}
	return out, nil
}
