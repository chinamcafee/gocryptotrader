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

// M5-08 测试：数值等价比对、差异排序、清账/差异两分支、失败中止。

type reconcileFakeStore struct {
	inserted  []llmstore.Reconciliation
	completes []struct {
		status     string
		reconciled bool
		deltas     *string
		validUntil *time.Time
	}
	err error
}

func (f *reconcileFakeStore) InsertReconciliation(_ context.Context, r *llmstore.Reconciliation) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *r)
	return nil
}

func (f *reconcileFakeStore) CompleteReconciliation(_ context.Context, _, status string, deltasJSON *string, reconciled bool, validUntil *time.Time, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.completes = append(f.completes, struct {
		status     string
		reconciled bool
		deltas     *string
		validUntil *time.Time
	}{status, reconciled, deltasJSON, validUntil})
	return nil
}

type reconcileFakeHalt struct {
	inserted []llmstore.Halt
	err      error
}

func (f *reconcileFakeHalt) InsertHalt(_ context.Context, h *llmstore.Halt) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *h)
	return nil
}

type reconcileHarness struct {
	reconciler *Reconciler
	store      *reconcileFakeStore
	halts      *reconcileFakeHalt
	audit      *recordFakeAudit
	now        time.Time
}

func reconcileTestHarness() *reconcileHarness {
	store := &reconcileFakeStore{}
	halts := &reconcileFakeHalt{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)
	counter := 0
	return &reconcileHarness{
		reconciler: &Reconciler{
			Runs: store, Halts: halts, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		store: store, halts: halts, audit: audit, now: now,
	}
}

func TestDiffPositionsComparesNumerically(t *testing.T) {
	t.Parallel()

	// 0.1 and 0.10 are the same position; a missing side is a zero reading.
	deltas := DiffPositions(
		[]ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "0.1"}},
		[]ReconcilePosition{{MarketID: "m1", Side: "long", Quantity: "0.10"}},
	)
	assert.Empty(t, deltas)

	deltas = DiffPositions(
		[]ReconcilePosition{
			{MarketID: "m2", Side: "LONG", Quantity: "1"},
			{MarketID: "m1", Side: "SHORT", Quantity: "2"},
			{MarketID: "m1", Side: "LONG", Quantity: "0.5"},
		},
		[]ReconcilePosition{
			{MarketID: "m1", Side: "LONG", Quantity: "0.6"},
			{MarketID: "m3", Side: "LONG", Quantity: "9"},
		},
	)
	require.Len(t, deltas, 4)
	// Sorted by market then side for replay.
	assert.Equal(t, "m1", deltas[0].MarketID)
	assert.Equal(t, "LONG", deltas[0].Side)
	assert.Equal(t, "0.5", deltas[0].Local)
	assert.Equal(t, "0.6", deltas[0].Exchange)
	assert.Equal(t, "m2", deltas[2].MarketID)
	assert.Equal(t, "0", deltas[2].Exchange, "a position only we know about reads as exchange zero")
	assert.Equal(t, "m3", deltas[3].MarketID)

	// An unreadable number is not a match.
	deltas = DiffPositions(
		[]ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "abc"}},
		[]ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "abc"}},
	)
	assert.Empty(t, deltas, "identical raw strings still match")
	deltas = DiffPositions(
		[]ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "abc"}},
		[]ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "1"}},
	)
	require.Len(t, deltas, 1)
}

func TestReconcileCleanPass(t *testing.T) {
	t.Parallel()

	h := reconcileTestHarness()
	result, err := h.reconciler.Pass(context.Background(), &ReconcileRequest{
		AccountID:       "acct-1",
		Local:           []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "1"}},
		Exchange:        []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "1.0"}},
		LocalOrdersJSON: "[]", ExchangeOrdersJSON: "[]",
	})
	require.NoError(t, err)
	assert.True(t, result.Reconciled)
	require.Len(t, h.store.inserted, 1)
	assert.Equal(t, "RUNNING", h.store.inserted[0].Status)
	require.Len(t, h.store.completes, 1)
	assert.Equal(t, "CLEAN", h.store.completes[0].status)
	assert.True(t, h.store.completes[0].reconciled)
	require.NotNil(t, h.store.completes[0].validUntil)
	assert.Equal(t, h.now.Add(ReconcileInterval), *h.store.completes[0].validUntil,
		"a clean verdict is valid until the next pass")
	assert.Empty(t, h.halts.inserted, "a clean pass halts nothing")
	assert.Empty(t, h.audit.inserted)
}

func TestReconcileMismatchHalts(t *testing.T) {
	t.Parallel()

	h := reconcileTestHarness()
	result, err := h.reconciler.Pass(context.Background(), &ReconcileRequest{
		AccountID: "acct-1",
		Local:     []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "1"}},
		Exchange:  []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "2"}},
		SetBy:     "operator-1",
	})
	require.NoError(t, err)
	assert.False(t, result.Reconciled)
	require.Len(t, result.Deltas, 1)
	require.Len(t, h.store.completes, 1)
	assert.Equal(t, "MISMATCH", h.store.completes[0].status)
	assert.False(t, h.store.completes[0].reconciled)
	assert.Nil(t, h.store.completes[0].validUntil, "a mismatch is not valid until anything")
	require.NotNil(t, h.store.completes[0].deltas)
	assert.Contains(t, *h.store.completes[0].deltas, "\"local\":\"1\"")
	require.Len(t, h.halts.inserted, 1)
	halt := h.halts.inserted[0]
	assert.Equal(t, HaltLevelSoft, halt.Level)
	assert.Equal(t, ReasonReconcileMismatch, halt.Reason)
	assert.Equal(t, "operator-1", halt.SetBy)
	assert.Equal(t, h.now, halt.SetAt)
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindReconcileMismatch, h.audit.inserted[0].Kind)
}

func TestReconcileFailuresAbortAndMisuse(t *testing.T) {
	t.Parallel()

	h := reconcileTestHarness()
	h.store.err = errors.New("db down")
	_, err := h.reconciler.Pass(context.Background(), &ReconcileRequest{AccountID: "acct-1"})
	require.Error(t, err)
	assert.Empty(t, h.halts.inserted)

	h = reconcileTestHarness()
	h.halts.err = errors.New("db down")
	_, err = h.reconciler.Pass(context.Background(), &ReconcileRequest{
		AccountID: "acct-1",
		Local:     []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "1"}},
		Exchange:  []ReconcilePosition{{MarketID: "m1", Side: "LONG", Quantity: "2"}},
	})
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted, "no audit event may claim a halt that did not land")

	h = reconcileTestHarness()
	_, err = h.reconciler.Pass(context.Background(), &ReconcileRequest{})
	assert.ErrorIs(t, err, ErrIntentInputInvalid)

	var nilReconciler *Reconciler
	_, err = nilReconciler.Pass(context.Background(), &ReconcileRequest{AccountID: "a"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
