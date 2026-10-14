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

// M5-18 测试：12h 线、宽限窗幂等、EXPIRED 不执行、推送与审计。

type reviewFakeLister struct {
	runs      []llmstore.DecisionRun
	gotCutoff time.Time
	gotSince  time.Time
	err       error
}

func (f *reviewFakeLister) ListReviewRunsBefore(_ context.Context, cutoff, since time.Time, _ int) ([]llmstore.DecisionRun, error) {
	f.gotCutoff = cutoff
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	return f.runs, nil
}

type reviewHarness struct {
	sweeper   *ReviewExpirySweeper
	lister    *reviewFakeLister
	audit     *recordFakeAudit
	collector *eventCollector
	now       time.Time
}

func reviewTestSweeper() *reviewHarness {
	h := &reviewHarness{
		lister:    &reviewFakeLister{},
		audit:     &recordFakeAudit{},
		collector: &eventCollector{},
		now:       time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC),
	}
	h.sweeper = &ReviewExpirySweeper{
		Runs: h.lister, Audit: h.audit, Notifier: &LLMNotifier{Push: h.collector.push},
		Now: func() time.Time { return h.now },
	}
	return h
}

func TestReviewExpirySweep(t *testing.T) {
	t.Parallel()

	h := reviewTestSweeper()
	h.lister.runs = []llmstore.DecisionRun{
		{ID: "run-old", Decision: "REVIEW", CreatedAt: h.now.Add(-13 * time.Hour)},
	}
	result, err := h.sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"run-old"}, result.ExpiredRunIDs)

	// The 12h line and the grace window: cutoff is now-12h, since is cutoff-24h.
	assert.Equal(t, h.now.Add(-ReviewExpireAfter), h.lister.gotCutoff)
	assert.Equal(t, h.lister.gotCutoff.Add(-ReviewExpiryGrace), h.lister.gotSince,
		"the grace window keeps repeated sweeps idempotent")

	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindReviewExpired, h.audit.inserted[0].Kind)
	assert.Contains(t, h.audit.inserted[0].DetailJSON, "EXPIRED, not executed")

	require.Len(t, h.collector.events, 1)
	assert.Equal(t, EventReviewExpired, h.collector.events[0].Type)
	assert.Contains(t, h.collector.events[0].Message, "not executed")
}

func TestReviewExpiryCustomWindowAndFailures(t *testing.T) {
	t.Parallel()

	// A tighter operator window: 2h.
	h := reviewTestSweeper()
	h.sweeper.ExpireAfter = 2 * time.Hour
	_, err := h.sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, h.now.Add(-2*time.Hour), h.lister.gotCutoff)

	h = reviewTestSweeper()
	h.lister.err = errors.New("db down")
	_, err = h.sweeper.Sweep(context.Background())
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted)

	h = reviewTestSweeper()
	h.lister.runs = []llmstore.DecisionRun{{ID: "run-old", Decision: "REVIEW", CreatedAt: h.now.Add(-13 * time.Hour)}}
	h.audit.err = errors.New("db down")
	_, err = h.sweeper.Sweep(context.Background())
	require.Error(t, err)
	assert.Empty(t, h.collector.events, "no push may claim an expiry that was not audited")

	var nilSweeper *ReviewExpirySweeper
	_, err = nilSweeper.Sweep(context.Background())
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
