package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-06 测试：止损价（较紧者 + tick 远离市场）、计划行、挂单、fail-safe 两连。

type exitFakeStore struct {
	inserted  int
	insertErr error
	updates   []struct {
		status   string
		failSafe bool
	}
	updateErr error
}

func (f *exitFakeStore) InsertProtectiveExit(_ context.Context, _ *llmstore.ProtectiveExit) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted++
	return nil
}

func (f *exitFakeStore) UpdateProtectiveExitStatus(_ context.Context, _, status string, failSafeTriggered bool, _, _ *string, _ time.Time) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, struct {
		status   string
		failSafe bool
	}{status, failSafeTriggered})
	return nil
}

type exitHarness struct {
	enforcer *ProtectiveExitEnforcer
	store    *exitFakeStore
	live     *dispatchFakeSubmitter
	audit    *recordFakeAudit
	now      time.Time
}

func exitTestHarness() *exitHarness {
	store := &exitFakeStore{}
	live := &dispatchFakeSubmitter{response: &OrderSubmitResponse{InternalOrderID: "int-stop", Detail: &order.Detail{OrderID: "exch-stop"}}}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC)
	counter := 0
	return &exitHarness{
		enforcer: &ProtectiveExitEnforcer{
			Exits: store, Orders: live, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		store: store, live: live, audit: audit, now: now,
	}
}

func exitTestParent() *llmstore.OrderIntent {
	return mapTestIntent()
}

func TestProtectiveStopPriceTighterOfATRAndPercent(t *testing.T) {
	t.Parallel()

	stop, err := ProtectiveStopPrice("LONG", "1000", "100", "0.1")
	require.NoError(t, err)
	assert.Equalf(t, "850", stop, "1.5*100 dominates 2%% of 1000")

	stop, err = ProtectiveStopPrice("LONG", "1000", "10", "0.1")
	require.NoError(t, err)
	assert.Equalf(t, "980", stop, "2%% of 1000 dominates 1.5*10")

	stop, err = ProtectiveStopPrice("SHORT", "1000", "100", "0.1")
	require.NoError(t, err)
	assert.Equal(t, "1150", stop)

	// Tick rounding moves the stop away from the market, never into it.
	stop, err = ProtectiveStopPrice("LONG", "1000.05", "10", "0.1")
	require.NoError(t, err)
	assert.Equal(t, "980", stop)

	_, err = ProtectiveStopPrice("SIDEWAYS", "1000", "10", "0.1")
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
	_, err = ProtectiveStopPrice("LONG", "0", "10", "0.1")
	assert.ErrorIs(t, err, ErrIntentInputInvalid)
	_, err = ProtectiveStopPrice("LONG", "1", "10", "0.1")
	assert.ErrorIs(t, err, ErrIntentInputInvalid, "a stop at or below zero is not a stop")
}

func TestBuildProtectiveExitPlan(t *testing.T) {
	t.Parallel()

	row, err := BuildProtectiveExit(&ProtectiveExitPlan{
		ParentIntent: *exitTestParent(),
		PositionSide: "LONG",
		FillPrice:    "1000",
		ATR:          "100",
		TickSize:     "0.1",
	}, time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC), nil)
	require.NoError(t, err)
	assert.Equal(t, ProtectiveExitKindStopMarket, row.ExitKind)
	assert.Equal(t, "850", row.TriggerPrice)
	assert.Equal(t, mapTestIntent().Quantity, row.Quantity, "the exit protects the position's quantity")
	assert.Equal(t, ProtectiveExitClientPrefix, row.ClientOrderID[:len(ProtectiveExitClientPrefix)])
	assert.Equal(t, mapTestIntent().ID, row.ParentIntentID)

	_, err = BuildProtectiveExit(&ProtectiveExitPlan{PositionSide: "LONG", FillPrice: "1", ATR: "1", TickSize: "0.1"}, time.Now(), nil)
	assert.ErrorIs(t, err, ErrIntentInputInvalid, "an exit without its parent is impossible")
}

func TestEnforcePlacesTheStop(t *testing.T) {
	t.Parallel()

	h := exitTestHarness()
	parent := exitTestParent()
	row, err := BuildProtectiveExit(&ProtectiveExitPlan{ParentIntent: *parent, PositionSide: "LONG", FillPrice: "1000", ATR: "100", TickSize: "0.1"}, h.now, h.enforcer.NewID)
	require.NoError(t, err)
	outcome, err := h.enforcer.Enforce(context.Background(), row, parent)
	require.NoError(t, err)
	assert.False(t, outcome.FailSafe)
	assert.Equal(t, ProtectiveExitStatusPlaced, outcome.Status)
	assert.Equal(t, 1, h.store.inserted)
	require.Len(t, h.store.updates, 1)
	assert.Equal(t, ProtectiveExitStatusPlaced, h.store.updates[0].status)
	assert.False(t, h.store.updates[0].failSafe)
	require.NotNil(t, h.live.lastSubmit)
	assert.Equal(t, order.StopMarket, h.live.lastSubmit.Type)
	assert.True(t, h.live.lastSubmit.ReduceOnly)
	assert.Equal(t, 850.0, h.live.lastSubmit.TriggerPrice)
	assert.Equal(t, order.Sell, h.live.lastSubmit.Side, "protecting a long exits with a sell")
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindProtectiveExitPlaced, h.audit.inserted[0].Kind)
}

func TestEnforceFailsSafeWhenTheStopCannotBePlaced(t *testing.T) {
	t.Parallel()

	h := exitTestHarness()
	parent := exitTestParent()
	row, err := BuildProtectiveExit(&ProtectiveExitPlan{ParentIntent: *parent, PositionSide: "LONG", FillPrice: "1000", ATR: "100", TickSize: "0.1"}, h.now, h.enforcer.NewID)
	require.NoError(t, err)

	live := &flakySubmitter{failFirst: errors.New("venue rejected the stop"), response: &OrderSubmitResponse{InternalOrderID: "int-flat", Detail: &order.Detail{OrderID: "exch-flat"}}}
	h.enforcer.Orders = live
	outcome, err := h.enforcer.Enforce(context.Background(), row, parent)
	require.NoError(t, err)
	assert.True(t, outcome.FailSafe)
	assert.Equal(t, ProtectiveExitStatusFailSafe, outcome.Status)
	require.Len(t, h.store.updates, 1)
	assert.Equal(t, ProtectiveExitStatusFailSafe, h.store.updates[0].status)
	assert.True(t, h.store.updates[0].failSafe, "the flatten is recorded as the fail-safe")
	require.Len(t, live.submits, 2)
	assert.Equal(t, order.StopMarket, live.submits[0].Type)
	assert.Equal(t, order.Market, live.submits[1].Type)
	assert.True(t, live.submits[1].ReduceOnly)
	assert.Zero(t, live.submits[1].TriggerPrice, "the flatten is an unconditional market order")
	assert.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindProtectiveExitFailSafe, h.audit.inserted[0].Kind)
}

func TestEnforceFailSafeFailureIsUnknown(t *testing.T) {
	t.Parallel()

	h := exitTestHarness()
	parent := exitTestParent()
	row, err := BuildProtectiveExit(&ProtectiveExitPlan{ParentIntent: *parent, PositionSide: "LONG", FillPrice: "1000", ATR: "100", TickSize: "0.1"}, h.now, h.enforcer.NewID)
	require.NoError(t, err)
	h.enforcer.Orders = &flakySubmitter{failFirst: errors.New("stop refused"), failAll: errors.New("venue down")}
	_, err = h.enforcer.Enforce(context.Background(), row, parent)
	require.Error(t, err, "a fail-safe that cannot execute is the loudest state there is")
	require.Len(t, h.store.updates, 1)
	assert.Equal(t, ProtectiveExitStatusUnknown, h.store.updates[0].status)
	assert.True(t, h.store.updates[0].failSafe)
	assert.Empty(t, h.audit.inserted)

	var nilEnforcer *ProtectiveExitEnforcer
	_, err = nilEnforcer.Enforce(context.Background(), row, parent)
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

// flakySubmitter fails the first submit (with failFirst) and, when failAll is
// set, every submit.
type flakySubmitter struct {
	failFirst error
	failAll   error
	response  *OrderSubmitResponse
	submits   []*order.Submit
}

func (f *flakySubmitter) Submit(_ context.Context, o *order.Submit) (*OrderSubmitResponse, error) {
	f.submits = append(f.submits, o)
	if f.failAll != nil {
		return nil, f.failAll
	}
	if len(f.submits) == 1 && f.failFirst != nil {
		return nil, f.failFirst
	}
	return f.response, nil
}
