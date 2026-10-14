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

// M5-09 测试：基线一次一封、划转校正、日损=基线±划转−权益、缺失即拒、FULL halt。

type dayStartFakeStore struct {
	baseline map[string]*llmstore.DayStartInventory
	flows    map[string][]llmstore.ExternalFlow
	inserts  int
	err      error
}

func newDayStartFakeStore() *dayStartFakeStore {
	return &dayStartFakeStore{
		baseline: map[string]*llmstore.DayStartInventory{},
		flows:    map[string][]llmstore.ExternalFlow{},
	}
}

func dayKey(accountID, day string) string { return accountID + "|" + day }

func (f *dayStartFakeStore) InsertDayStartInventory(_ context.Context, d *llmstore.DayStartInventory) error {
	if f.err != nil {
		return f.err
	}
	f.inserts++
	f.baseline[dayKey(d.AccountID, d.UtcDay)] = d
	return nil
}

func (f *dayStartFakeStore) GetDayStartInventory(_ context.Context, accountID, utcDay string) (*llmstore.DayStartInventory, error) {
	if f.err != nil {
		return nil, f.err
	}
	row, ok := f.baseline[dayKey(accountID, utcDay)]
	if !ok {
		return nil, llmstore.ErrNotFound
	}
	return row, nil
}

func (f *dayStartFakeStore) InsertExternalFlow(_ context.Context, flow *llmstore.ExternalFlow) error {
	if f.err != nil {
		return f.err
	}
	f.flows[dayKey(flow.AccountID, flow.UtcDay)] = append(f.flows[dayKey(flow.AccountID, flow.UtcDay)], *flow)
	return nil
}

func (f *dayStartFakeStore) ListExternalFlows(_ context.Context, accountID, utcDay string) ([]llmstore.ExternalFlow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.flows[dayKey(accountID, utcDay)], nil
}

type dayStartHarness struct {
	service *DayStartService
	store   *dayStartFakeStore
	halts   *reconcileFakeHalt
	audit   *recordFakeAudit
	now     time.Time
}

func dayStartTestService() *dayStartHarness {
	store := newDayStartFakeStore()
	halts := &reconcileFakeHalt{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	counter := 0
	return &dayStartHarness{
		service: &DayStartService{
			Store: store, Halts: halts, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		store: store, halts: halts, audit: audit, now: now,
	}
}

func TestUtcDayKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2026-10-11", UtcDayKey(time.Date(2026, 10, 11, 23, 59, 59, 0, time.UTC)))
	// The key flips exactly at UTC midnight, not at any local midnight.
	assert.Equal(t, "2026-10-12", UtcDayKey(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)))
}

func TestSealIsOncePerDay(t *testing.T) {
	t.Parallel()

	h := dayStartTestService()
	row, err := h.service.Seal(context.Background(), &SealRequest{AccountID: "acct-1", EquityUSD: "1000"})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-11", row.UtcDay)
	assert.Equal(t, DayStartSourceMidnight, row.Source)
	assert.Equal(t, "[]", row.PositionsJSON)
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindDayStartSealed, h.audit.inserted[0].Kind)

	// A second seal the same day refuses: rewriting a baseline would move the
	// loss threshold's zero point.
	_, err = h.service.Seal(context.Background(), &SealRequest{AccountID: "acct-1", EquityUSD: "9999"})
	require.ErrorIs(t, err, ErrDayStartSealed)
	assert.Equal(t, 1, h.store.inserts)

	// Bad equity refuses before any write.
	_, err = h.service.Seal(context.Background(), &SealRequest{AccountID: "acct-1", EquityUSD: "abc"})
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
}

func TestRecordFlowValidates(t *testing.T) {
	t.Parallel()

	h := dayStartTestService()
	flow, err := h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "in", AmountUSD: "50", Source: "UAccountIncomeHistory"})
	require.NoError(t, err)
	assert.Equal(t, FlowDirectionIn, flow.Direction)
	assert.Equal(t, "2026-10-11", flow.UtcDay)

	_, err = h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "SIDEWAYS", AmountUSD: "50"})
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
	_, err = h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "IN", AmountUSD: "0"})
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
	_, err = h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "IN", AmountUSD: "-5"})
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
}

func TestDailyLossMathAndMissingIsRefused(t *testing.T) {
	t.Parallel()

	// No baseline yet: UNKNOWN, refuse.
	h := dayStartTestService()
	_, err := h.service.DailyLoss(context.Background(), "acct-1", "900")
	require.ErrorIs(t, err, ErrDayStartMissing)

	// Baseline 1000; +50 in, -20 out; equity 900 -> loss 130.
	_, err = h.service.Seal(context.Background(), &SealRequest{AccountID: "acct-1", EquityUSD: "1000"})
	require.NoError(t, err)
	_, err = h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "IN", AmountUSD: "50"})
	require.NoError(t, err)
	_, err = h.service.RecordFlow(context.Background(), &FlowRequest{AccountID: "acct-1", Direction: "OUT", AmountUSD: "20"})
	require.NoError(t, err)
	loss, err := h.service.DailyLoss(context.Background(), "acct-1", "900")
	require.NoError(t, err)
	assert.Equal(t, "130", loss)

	// Profit reads negative (net 1000+50-20=1030, equity 1200 -> -170).
	loss, err = h.service.DailyLoss(context.Background(), "acct-1", "1200")
	require.NoError(t, err)
	assert.Equal(t, "-170", loss)

	// An unreadable stored flow is UNKNOWN too, never a zero.
	h.store.flows[dayKey("acct-1", "2026-10-11")] = append(h.store.flows[dayKey("acct-1", "2026-10-11")], llmstore.ExternalFlow{
		ID: "flow-x", AccountID: "acct-1", UtcDay: "2026-10-11", Direction: "IN", AmountUsdt: "not-a-number",
	})
	_, err = h.service.DailyLoss(context.Background(), "acct-1", "900")
	require.ErrorIs(t, err, ErrDayStartMissing)
}

func TestTripDailyLossHalt(t *testing.T) {
	t.Parallel()

	h := dayStartTestService()
	_, err := h.service.Seal(context.Background(), &SealRequest{AccountID: "acct-1", EquityUSD: "1000"})
	require.NoError(t, err)

	// Loss 130 exceeds the 20 cap: FULL halt, manual-clear only.
	haltID, tripped, err := h.service.TripDailyLossHalt(context.Background(), "acct-1", "870", "20")
	require.NoError(t, err)
	assert.True(t, tripped)
	assert.NotEmpty(t, haltID)
	require.Len(t, h.halts.inserted, 1)
	halt := h.halts.inserted[0]
	assert.Equal(t, HaltLevelFull, halt.Level)
	assert.Equal(t, ReasonDailyLossExceeded, halt.Reason)
	require.Len(t, h.audit.inserted, 2, "the seal's event plus the halt's")
	assert.Equal(t, AuditKindHaltSet, h.audit.inserted[1].Kind)

	// Within the cap or in profit: no halt. Net is 1030, so equity 1015 -> loss 15.
	_, tripped, err = h.service.TripDailyLossHalt(context.Background(), "acct-1", "1015", "20")
	require.NoError(t, err)
	assert.False(t, tripped)
	_, tripped, err = h.service.TripDailyLossHalt(context.Background(), "acct-1", "1100", "20")
	require.NoError(t, err)
	assert.False(t, tripped, "a profitable day is not a loss")

	// Missing baseline: refuse, and never a halt on an unknown number.
	h2 := dayStartTestService()
	_, _, err = h2.service.TripDailyLossHalt(context.Background(), "acct-1", "500", "20")
	require.ErrorIs(t, err, ErrDayStartMissing)
	assert.Empty(t, h2.halts.inserted)

	var nilService *DayStartService
	_, _, err = nilService.TripDailyLossHalt(context.Background(), "a", "1", "1")
	assert.ErrorIs(t, err, ErrChatNotConfigured)
	_, err = nilService.Seal(context.Background(), &SealRequest{AccountID: "a"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
	_ = errors.Is
}
