package engine

import (
	"context"
	"fmt"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-15 崩溃恢复（docs 11 §7）。
//
// 重启扫描 `SUBMITTING` 中的意图 → `UNKNOWN` → 需对账。
//
// 为什么：`SUBMITTING` 只有一个写入者（M5-05 的原子认领），所以进程重启后
// 还停在 `SUBMITTING` 的意图**必然**是"认领了但没等到结果"——结果不明，
// 按 §6.3 的铁律：UNKNOWN，不盲重试，账户进对账。过期的 `PREPARED` 顺手
// 清成 `EXPIRED`（授权在宕机期间过时了）。M6-06 会用 kill -9 演练这条路径。

// AuditKindCrashRecovery is recorded once per recovery pass.
const AuditKindCrashRecovery = "CRASH_RECOVERY"

// iIntentStatusLister is the recovery read seam.
type iIntentStatusLister interface {
	ListOrderIntentsByStatus(ctx context.Context, status string) ([]llmstore.OrderIntent, error)
}

// LLMIntentStatusLister adapts the repository.
type LLMIntentStatusLister struct{}

// ListOrderIntentsByStatus lists intents in one status.
func (LLMIntentStatusLister) ListOrderIntentsByStatus(ctx context.Context, status string) ([]llmstore.OrderIntent, error) {
	return llmstore.ListOrderIntentsByStatus(ctx, status)
}

// CrashRecovery resolves intents a crash left in flight.
type CrashRecovery struct {
	Intents   iIntentStatusLister
	Lifecycle iIntentLifecycle
	Audit     iAuditEvents
	Metrics   *LLMMetrics
	Notifier  *LLMNotifier
	Now       func() time.Time
}

// RecoveryResult reports what the restart scan resolved.
type RecoveryResult struct {
	UnknownIntents         []string `json:"unknownIntents"`
	ExpiredIntents         []string `json:"expiredIntents"`
	ReconciliationRequired bool     `json:"reconciliationRequired"`
}

// Recover runs the restart scan. Every ambiguity it resolves is joined by a
// high-severity notification: results that went missing while the process was
// dead are exactly what a human must see.
func (r *CrashRecovery) Recover(ctx context.Context) (*RecoveryResult, error) {
	if r == nil || r.Intents == nil || r.Lifecycle == nil || r.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	result := &RecoveryResult{}

	submitting, err := r.Intents.ListOrderIntentsByStatus(ctx, IntentStatusSubmitting)
	if err != nil {
		return nil, fmt.Errorf("llm recovery: reading SUBMITTING intents: %w", err)
	}
	for i := range submitting {
		intent := &submitting[i]
		if err := r.Lifecycle.UpdateOrderIntentLifecycle(ctx, intent.ID, IntentStatusUnknown, nil, nil, now); err != nil {
			return nil, fmt.Errorf("llm recovery: marking %s UNKNOWN: %w", intent.ID, err)
		}
		result.UnknownIntents = append(result.UnknownIntents, intent.ID)
		if r.Notifier != nil {
			r.Notifier.OrderUnknown(intent.ID, "restart scan: the claim had no outcome before the process died")
		}
	}

	prepared, err := r.Intents.ListOrderIntentsByStatus(ctx, IntentStatusPrepared)
	if err != nil {
		return nil, fmt.Errorf("llm recovery: reading PREPARED intents: %w", err)
	}
	for i := range prepared {
		intent := &prepared[i]
		if intent.ExpiresAt.IsZero() || now.Before(intent.ExpiresAt) {
			continue
		}
		if err := r.Lifecycle.UpdateOrderIntentLifecycle(ctx, intent.ID, IntentStatusExpired, nil, nil, now); err != nil {
			return nil, fmt.Errorf("llm recovery: expiring %s: %w", intent.ID, err)
		}
		result.ExpiredIntents = append(result.ExpiredIntents, intent.ID)
	}

	if len(result.UnknownIntents) > 0 {
		result.ReconciliationRequired = true
		if r.Metrics != nil {
			r.Metrics.Set(MetricUnknownOrders, float64(len(result.UnknownIntents)), nil)
		}
	}
	detail, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("llm recovery: audit detail: %w", err)
	}
	id := newLLMIdentifier()
	subject := id
	if err := r.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: id, Kind: AuditKindCrashRecovery, Actor: auditActorSystem,
		Subject: &subject, DetailJSON: string(detail), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm recovery: audit event: %w", err)
	}
	return result, nil
}
