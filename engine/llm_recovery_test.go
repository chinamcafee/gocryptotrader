package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M5-15 测试：重启扫描把 SUBMITTING 清成 UNKNOWN+需对账、过期 PREPARED 清成 EXPIRED。

type recoveryFakeLister struct {
	byStatus map[string][]llmstore.OrderIntent
	err      error
}

func (f *recoveryFakeLister) ListOrderIntentsByStatus(_ context.Context, status string) ([]llmstore.OrderIntent, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byStatus[status], nil
}

type recoveryFakeLifecycle struct {
	updates []struct{ id, status string }
	err     error
}

func (f *recoveryFakeLifecycle) UpdateOrderIntentLifecycle(_ context.Context, id, status string, _, _ *string, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.updates = append(f.updates, struct{ id, status string }{id, status})
	return nil
}

type recoveryHarness struct {
	recovery  *CrashRecovery
	lister    *recoveryFakeLister
	lifecycle *recoveryFakeLifecycle
	metrics   *LLMMetrics
	notifier  *LLMNotifier
	collector *eventCollector
	audit     *recordFakeAudit
	now       time.Time
}

func recoveryTestHarness() *recoveryHarness {
	now := time.Date(2026, 10, 11, 21, 0, 0, 0, time.UTC)
	collector := &eventCollector{}
	h := &recoveryHarness{
		lister:    &recoveryFakeLister{byStatus: map[string][]llmstore.OrderIntent{}},
		lifecycle: &recoveryFakeLifecycle{},
		metrics:   NewLLMMetrics(),
		notifier:  &LLMNotifier{Push: collector.push},
		collector: collector,
		audit:     &recordFakeAudit{},
		now:       now,
	}
	h.recovery = &CrashRecovery{
		Intents: h.lister, Lifecycle: h.lifecycle, Audit: h.audit, Metrics: h.metrics, Notifier: h.notifier,
		Now: func() time.Time { return now },
	}
	return h
}

func TestRecoverMarksClaimsUnknown(t *testing.T) {
	t.Parallel()

	h := recoveryTestHarness()
	h.lister.byStatus[IntentStatusSubmitting] = []llmstore.OrderIntent{
		{ID: "intent-a", Status: IntentStatusSubmitting},
		{ID: "intent-b", Status: IntentStatusSubmitting},
	}
	h.lister.byStatus[IntentStatusPrepared] = []llmstore.OrderIntent{
		{ID: "intent-fresh", Status: IntentStatusPrepared, ExpiresAt: h.now.Add(time.Minute)},
		{ID: "intent-stale", Status: IntentStatusPrepared, ExpiresAt: h.now.Add(-time.Second)},
		{ID: "intent-no-expiry", Status: IntentStatusPrepared},
	}

	result, err := h.recovery.Recover(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"intent-a", "intent-b"}, result.UnknownIntents)
	assert.Equal(t, []string{"intent-stale"}, result.ExpiredIntents)
	assert.True(t, result.ReconciliationRequired, "a claim without an outcome means the account needs reconciliation")

	// Every resolved ambiguity moved the row through the lifecycle.
	require.Len(t, h.lifecycle.updates, 3)
	assert.Equal(t, IntentStatusUnknown, h.lifecycle.updates[0].status)
	assert.Equal(t, IntentStatusUnknown, h.lifecycle.updates[1].status)
	assert.Equal(t, IntentStatusExpired, h.lifecycle.updates[2].status)

	// The loud bell rang for each unknown, the gauge carries the count.
	require.Len(t, h.collector.events, 2)
	assert.Equal(t, EventOrderUnknown, h.collector.events[0].Type)
	assert.Equal(t, 2.0, h.metrics.Snapshot().Gauges[MetricUnknownOrders])

	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindCrashRecovery, h.audit.inserted[0].Kind)
	assert.Contains(t, h.audit.inserted[0].DetailJSON, "reconciliationRequired")
}

func TestRecoverCleanRestart(t *testing.T) {
	t.Parallel()

	h := recoveryTestHarness()
	h.lister.byStatus[IntentStatusPrepared] = []llmstore.OrderIntent{
		{ID: "intent-fresh", Status: IntentStatusPrepared, ExpiresAt: h.now.Add(time.Minute)},
	}
	result, err := h.recovery.Recover(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result.UnknownIntents)
	assert.Empty(t, result.ExpiredIntents)
	assert.False(t, result.ReconciliationRequired)
	assert.Empty(t, h.lifecycle.updates, "a clean restart moves nothing")
	assert.Empty(t, h.collector.events)
	require.Len(t, h.audit.inserted, 1, "even a clean pass leaves a trace")

	assert.Zero(t, h.metrics.Snapshot().Gauges[MetricUnknownOrders])
}

func TestRecoverFailuresAbort(t *testing.T) {
	t.Parallel()

	h := recoveryTestHarness()
	h.lister.err = errors.New("db down")
	_, err := h.recovery.Recover(context.Background())
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted)

	h = recoveryTestHarness()
	h.lister.byStatus[IntentStatusSubmitting] = []llmstore.OrderIntent{{ID: "intent-a", Status: IntentStatusSubmitting}}
	h.lifecycle.err = errors.New("db down")
	_, err = h.recovery.Recover(context.Background())
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted, "no audit may claim a recovery that did not land")

	var nilRecovery *CrashRecovery
	_, err = nilRecovery.Recover(context.Background())
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
