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

// M5-10 测试：三级 halt、只撤机器人挂单、FULL 平仓需二次确认、清除需认证且不自动恢复。

type haltFakeHalts struct {
	inserted []llmstore.Halt
	err      error
}

func (f *haltFakeHalts) InsertHalt(_ context.Context, h *llmstore.Halt) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *h)
	return nil
}

type haltFakeClearer struct {
	cleared []string
	err     error
}

func (f *haltFakeClearer) ClearHalt(_ context.Context, id string, _ time.Time, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.cleared = append(f.cleared, id)
	return nil
}

type haltFakeCanceller struct {
	calls int
	count int
	err   error
}

func (f *haltFakeCanceller) CancelBotOrders(context.Context, string) (int, error) {
	f.calls++
	return f.count, f.err
}

type haltFakePauser struct {
	calls   int
	paused  int
	reasons []string
	err     error
}

func (f *haltFakePauser) PauseRunningStrategies(_ context.Context, reason, _ string) (int, error) {
	f.calls++
	f.reasons = append(f.reasons, reason)
	return f.paused, f.err
}

type haltFakeFlattener struct {
	calls int
	count int
	err   error
}

func (f *haltFakeFlattener) FlattenAllPositions(context.Context, string) (int, error) {
	f.calls++
	return f.count, f.err
}

type haltHarness struct {
	controller *HaltController
	halts      *haltFakeHalts
	clearer    *haltFakeClearer
	canceller  *haltFakeCanceller
	pauser     *haltFakePauser
	flattener  *haltFakeFlattener
	audit      *recordFakeAudit
	now        time.Time
}

func haltTestController() *haltHarness {
	h := &haltHarness{
		halts: &haltFakeHalts{}, clearer: &haltFakeClearer{},
		canceller: &haltFakeCanceller{count: 3}, pauser: &haltFakePauser{paused: 2}, flattener: &haltFakeFlattener{count: 1},
		audit: &recordFakeAudit{}, now: time.Date(2026, 10, 11, 19, 0, 0, 0, time.UTC),
	}
	counter := 0
	h.controller = &HaltController{
		Halts: h.halts, Clears: h.clearer, Canceller: h.canceller, Strategies: h.pauser, Flattener: h.flattener, Audit: h.audit,
		VerifyPassword: func(_ context.Context, password string) error {
			if password == "s3cret" {
				return nil
			}
			return errors.New("rejected")
		},
		Now: func() time.Time { return h.now },
		NewID: func() string {
			counter++
			return "id-" + digitString(counter)
		},
	}
	return h
}

func TestHaltSetLevels(t *testing.T) {
	t.Parallel()

	// SOFT: the flag lands and strategies pause, but the bot's resting orders
	// survive -- protective stops must outlive a soft stop.
	h := haltTestController()
	outcome, err := h.controller.Set(context.Background(), HaltRequest{Level: "soft", Reason: "pair stale", SetBy: "system"})
	require.NoError(t, err)
	assert.Equal(t, HaltLevelSoft, outcome.Level)
	assert.Equal(t, 2, outcome.SuspendedStrategies)
	assert.Zero(t, h.canceller.calls, "SOFT keeps protective stops alive")
	require.Len(t, h.halts.inserted, 1)
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindHaltSet, h.audit.inserted[0].Kind)

	// MARKET: needs its market and withdraws the bot's orders.
	h = haltTestController()
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "MARKET", Reason: "funding extreme", SetBy: "system"})
	require.ErrorIs(t, err, ErrHaltInputInvalid, "a market halt without a market is malformed")
	outcome, err = h.controller.Set(context.Background(), HaltRequest{Level: "MARKET", MarketID: "Binance:usdtmarginedfutures:BTC_USDT", Reason: "funding extreme", SetBy: "system"})
	require.NoError(t, err)
	assert.Equal(t, 3, outcome.CanceledOrders)
	require.Len(t, h.halts.inserted, 1)
	require.NotNil(t, h.halts.inserted[0].MarketID)

	// FULL: blocks everything by the row alone; no flatten unless confirmed.
	h = haltTestController()
	outcome, err = h.controller.Set(context.Background(), HaltRequest{Level: "FULL", Reason: "clock drift", SetBy: "system"})
	require.NoError(t, err)
	assert.Zero(t, outcome.FlattenedPositions)
	assert.Zero(t, h.flattener.calls)

	// Garbage level and missing reason refuse.
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "PANIC", Reason: "r", SetBy: "s"})
	assert.ErrorIs(t, err, ErrHaltInputInvalid)
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "SOFT", SetBy: "s"})
	assert.ErrorIs(t, err, ErrHaltInputInvalid)
}

func TestHaltFlattenNeedsExplicitConfirmation(t *testing.T) {
	t.Parallel()

	h := haltTestController()
	_, err := h.controller.Set(context.Background(), HaltRequest{Level: "FULL", Reason: "manual", SetBy: "op", Flatten: true})
	assert.ErrorIs(t, err, ErrHaltConfirmationRequired, "the second confirmation is not a formality")
	assert.Empty(t, h.halts.inserted, "nothing lands before the confirmation")

	// A non-FULL level may never flatten, confirmed or not.
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "SOFT", Reason: "manual", SetBy: "op", Flatten: true, ConfirmFlatten: true})
	assert.ErrorIs(t, err, ErrHaltInputInvalid)

	h = haltTestController()
	outcome, err := h.controller.Set(context.Background(), HaltRequest{Level: "FULL", Reason: "manual emergency", SetBy: "op", Flatten: true, ConfirmFlatten: true})
	require.NoError(t, err)
	assert.Equal(t, 1, outcome.FlattenedPositions)
	assert.Equal(t, 1, h.flattener.calls)
	assert.Contains(t, h.audit.inserted[0].DetailJSON, `"flattenedPositions":1`)
}

func TestHaltSequenceAbortsOnFailure(t *testing.T) {
	t.Parallel()

	h := haltTestController()
	h.pauser.err = errors.New("db down")
	_, err := h.controller.Set(context.Background(), HaltRequest{Level: "FULL", Reason: "r", SetBy: "s"})
	require.Error(t, err)
	assert.Zero(t, h.canceller.calls, "the sequence stops where it failed")
	assert.Empty(t, h.audit.inserted)

	h = haltTestController()
	h.canceller.err = errors.New("venue down")
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "MARKET", MarketID: "m", Reason: "r", SetBy: "s"})
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted)
}

func TestHaltClearRequiresAuthenticationAndNeverAutoResumes(t *testing.T) {
	t.Parallel()

	h := haltTestController()
	// Missing reason refuses; wrong password refuses.
	err := h.controller.Clear(context.Background(), ClearRequest{HaltID: "h1", By: "op", Password: "s3cret"})
	assert.ErrorIs(t, err, ErrHaltInputInvalid)
	err = h.controller.Clear(context.Background(), ClearRequest{HaltID: "h1", By: "op", Reason: "resolved", Password: "wrong"})
	assert.ErrorIs(t, err, ErrHaltClearUnauthenticated)
	assert.Empty(t, h.clearer.cleared)

	// Authenticated clear lands, is audited, and resumes nothing: strategies
	// and approvals need individual re-enablement.
	err = h.controller.Clear(context.Background(), ClearRequest{HaltID: "h1", By: "op", Reason: "root cause fixed", Password: "s3cret"})
	require.NoError(t, err)
	assert.Equal(t, []string{"h1"}, h.clearer.cleared)
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindHaltCleared, h.audit.inserted[0].Kind)
	assert.Contains(t, h.audit.inserted[0].DetailJSON, "never; strategies and approvals need individual re-enablement")
	assert.Zero(t, h.pauser.calls, "clearing never touches strategies")
	assert.Zero(t, h.canceller.calls, "clearing never touches orders")
}

func TestHaltMisuse(t *testing.T) {
	t.Parallel()

	var nilController *HaltController
	_, err := nilController.Set(context.Background(), HaltRequest{Level: "SOFT", Reason: "r", SetBy: "s"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
	err = nilController.Clear(context.Background(), ClearRequest{HaltID: "h", By: "o", Reason: "r"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	// MARKET/FULL without a canceller is a misconfigured controller.
	h := haltTestController()
	h.controller.Canceller = nil
	_, err = h.controller.Set(context.Background(), HaltRequest{Level: "FULL", Reason: "r", SetBy: "s"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
