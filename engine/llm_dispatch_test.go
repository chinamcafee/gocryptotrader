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

// M5-02 测试：白名单分流、PAPER 走 fake 且开校验、UNKNOWN 疑罪从有、回执缺详情=UNKNOWN。

type dispatchFakeSubmitter struct {
	calls      int
	lastSubmit *order.Submit
	response   *OrderSubmitResponse
	err        error
}

func (f *dispatchFakeSubmitter) Submit(_ context.Context, o *order.Submit) (*OrderSubmitResponse, error) {
	f.calls++
	f.lastSubmit = o
	return f.response, f.err
}

type dispatchFakeFake struct {
	calls       int
	checkLimits bool
	resulting   *order.SubmitResponse
	response    *OrderSubmitResponse
	err         error
}

func (f *dispatchFakeFake) SubmitFakeOrder(_ *order.Submit, resulting *order.SubmitResponse, checkExchangeLimits bool) (*OrderSubmitResponse, error) {
	f.calls++
	f.checkLimits = checkExchangeLimits
	f.resulting = resulting
	return f.response, f.err
}

type dispatchFakeLifecycle struct {
	updates []struct {
		id, status string
		internal   *string
		exchange   *string
		at         time.Time
	}
	err error
}

func (f *dispatchFakeLifecycle) UpdateOrderIntentLifecycle(_ context.Context, id, status string, internalOrderID, exchangeOrderID *string, at time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.updates = append(f.updates, struct {
		id, status string
		internal   *string
		exchange   *string
		at         time.Time
	}{id, status, internalOrderID, exchangeOrderID, at})
	return nil
}

type dispatchFakeClaimer struct {
	applied bool
	calls   int
	err     error
}

func (f *dispatchFakeClaimer) ClaimOrderIntent(_ context.Context, _ string, _ time.Time) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.applied, nil
}

// dispatchAccountEvent is one captured account-trigger raise.
type dispatchAccountEvent struct {
	market string
	detail string
	at     time.Time
}

type dispatchHarness struct {
	dispatcher *OrderDispatcher
	live       *dispatchFakeSubmitter
	fake       *dispatchFakeFake
	claimer    *dispatchFakeClaimer
	lifecycle  *dispatchFakeLifecycle
	audit      *recordFakeAudit
	events     []dispatchAccountEvent
	now        time.Time
}

func dispatchTestHarness() *dispatchHarness {
	live := &dispatchFakeSubmitter{response: &OrderSubmitResponse{InternalOrderID: "int-1", Detail: &order.Detail{OrderID: "exch-1"}}}
	fake := &dispatchFakeFake{response: &OrderSubmitResponse{InternalOrderID: "int-paper", Detail: &order.Detail{OrderID: "fake-1"}}}
	claimer := &dispatchFakeClaimer{applied: true}
	lifecycle := &dispatchFakeLifecycle{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	counter := 0
	h := &dispatchHarness{
		dispatcher: &OrderDispatcher{
			Orders: live, Fake: fake, Claim: claimer, Lifecycle: lifecycle, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		live: live, fake: fake, claimer: claimer, lifecycle: lifecycle, audit: audit, now: now,
	}
	h.dispatcher.Events = func(market, detail string, at time.Time) {
		h.events = append(h.events, dispatchAccountEvent{market: market, detail: detail, at: at})
	}
	return h
}

func dispatchTestIntent() *llmstore.OrderIntent {
	return mapTestIntent()
}

func TestDispatchShadowAndReadOnlyNeverSubmit(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{LLMRunModeReadOnly, LLMRunModeShadow} {
		h := dispatchTestHarness()
		outcome, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), mode)
		require.NoError(t, err)
		assert.Falsef(t, outcome.Executed, "%s should not execute", mode)
		assert.Zero(t, h.live.calls)
		assert.Zero(t, h.fake.calls)
		assert.Empty(t, h.lifecycle.updates, "the intent's lifecycle is untouched: nothing happened")
		assert.Empty(t, h.audit.inserted)
	}
}

func TestDispatchPaperGoesToTheFakePathWithLimitsChecked(t *testing.T) {
	t.Parallel()

	h := dispatchTestHarness()
	outcome, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModePaper)
	require.NoError(t, err)
	assert.True(t, outcome.Executed)
	assert.Equal(t, IntentStatusAcked, outcome.Status)
	assert.Zero(t, h.live.calls, "the paper path never touches the venue")
	assert.Equal(t, 1, h.fake.calls)
	assert.True(t, h.fake.checkLimits, "checkExchangeLimits=true, exactly like the backtester")
	require.Len(t, h.lifecycle.updates, 1)
	update := h.lifecycle.updates[0]
	assert.Equal(t, IntentStatusAcked, update.status)
	require.NotNil(t, update.internal)
	assert.Equal(t, "int-paper", *update.internal)
	require.Len(t, h.audit.inserted, 1)
	assert.Equal(t, AuditKindOrderSubmitted, h.audit.inserted[0].Kind)
	assert.Contains(t, h.audit.inserted[0].DetailJSON, "fake-1")
}

func TestDispatchLiveRoutesToTheVenue(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{LLMRunModeTestnet, LLMRunModeLive} {
		h := dispatchTestHarness()
		outcome, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), mode)
		require.NoError(t, err)
		assert.True(t, outcome.Executed)
		assert.Equalf(t, 1, h.live.calls, "%s submits to the venue", mode)
		assert.Zero(t, h.fake.calls)
		require.NotNil(t, h.live.lastSubmit)
		assert.Equal(t, 0.0, h.live.lastSubmit.Leverage)
		assert.Equal(t, IntentStatusAcked, h.lifecycle.updates[0].status)
	}
}

func TestDispatchUnknownModeRefuses(t *testing.T) {
	t.Parallel()

	h := dispatchTestHarness()
	_, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), "YOLO")
	assert.ErrorIs(t, err, ErrIntentInputInvalid, "an unrecognised mode executes nothing")
	assert.Zero(t, h.live.calls)
	assert.Zero(t, h.fake.calls)

	h = dispatchTestHarness()
	intent := dispatchTestIntent()
	intent.Status = IntentStatusAcked
	_, err = h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	assert.ErrorIs(t, err, ErrIntentInputInvalid, "only a PREPARED intent may be dispatched")
	assert.Zero(t, h.live.calls)
}

func TestDispatchFailureIsUnknownNeverRejected(t *testing.T) {
	t.Parallel()

	h := dispatchTestHarness()
	h.live.err = errors.New("connection reset")
	outcome, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.Error(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, IntentStatusUnknown, outcome.Status, "an unclear submission is UNKNOWN, never REJECTED")
	require.Len(t, h.lifecycle.updates, 1)
	assert.Equal(t, IntentStatusUnknown, h.lifecycle.updates[0].status)
	assert.Nil(t, h.lifecycle.updates[0].internal)
	assert.Empty(t, h.audit.inserted, "no ORDER_SUBMITTED event for an order that may not exist")

	// A response without order detail is just as unclear.
	h = dispatchTestHarness()
	h.live.response = &OrderSubmitResponse{}
	_, err = h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeTestnet)
	require.Error(t, err)
	require.Len(t, h.lifecycle.updates, 1)
	assert.Equal(t, IntentStatusUnknown, h.lifecycle.updates[0].status)

	// A lifecycle write failure surfaces rather than being swallowed.
	h = dispatchTestHarness()
	h.lifecycle.err = errors.New("db down")
	_, err = h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.Error(t, err)
}

func TestDispatchMisuse(t *testing.T) {
	t.Parallel()

	var nilDispatcher *OrderDispatcher
	_, err := nilDispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	h := dispatchTestHarness()
	h.dispatcher.Lifecycle = nil
	_, err = h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

func TestDispatchPreSubmitFreshnessGate(t *testing.T) {
	t.Parallel()

	// Fresh authorisation passes.
	h := dispatchTestHarness()
	intent := dispatchTestIntent()
	intent.CreatedAt = h.now.Add(-time.Second)
	intent.ExpiresAt = h.now.Add(9 * time.Second)
	outcome, err := h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	require.NoError(t, err)
	assert.True(t, outcome.Executed)
	assert.Equal(t, 1, h.live.calls)

	// Exactly at the 5s bar still passes: the bar is a ceiling, not a margin.
	h = dispatchTestHarness()
	intent = dispatchTestIntent()
	intent.CreatedAt = h.now.Add(-IntentSubmitMaxAge)
	intent.ExpiresAt = h.now.Add(5 * time.Second)
	_, err = h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	require.NoError(t, err)
	assert.Equal(t, 1, h.live.calls)

	// Older than the bar: EXPIRED, nothing sent.
	h = dispatchTestHarness()
	intent = dispatchTestIntent()
	intent.CreatedAt = h.now.Add(-IntentSubmitMaxAge - time.Nanosecond)
	_, err = h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	require.ErrorIs(t, err, ErrIntentExpired)
	assert.Zero(t, h.live.calls, "a stale authorisation should not reach the venue")
	require.Len(t, h.lifecycle.updates, 1)
	assert.Equal(t, IntentStatusExpired, h.lifecycle.updates[0].status)
	assert.Empty(t, h.audit.inserted)

	// Past its own expiry: EXPIRED too.
	h = dispatchTestHarness()
	intent = dispatchTestIntent()
	intent.CreatedAt = h.now.Add(-time.Second)
	intent.ExpiresAt = h.now.Add(-time.Millisecond)
	_, err = h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	require.ErrorIs(t, err, ErrIntentExpired)
	assert.Zero(t, h.live.calls)
	assert.Equal(t, IntentStatusExpired, h.lifecycle.updates[0].status)

	// Shadow mode short-circuits before the freshness gate: it never executes,
	// so an old intent stays PREPARED rather than becoming EXPIRED.
	h = dispatchTestHarness()
	intent = dispatchTestIntent()
	intent.CreatedAt = h.now.Add(-time.Hour)
	_, err = h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeShadow)
	require.NoError(t, err)
	assert.Empty(t, h.lifecycle.updates)
}

func TestDispatchClaimProtectsAgainstDoubleSubmit(t *testing.T) {
	t.Parallel()

	// The claim is lost (another writer won it): refuse before the venue call.
	h := dispatchTestHarness()
	h.claimer.applied = false
	_, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.ErrorIs(t, err, ErrIntentClaimLost)
	assert.Zero(t, h.live.calls, "a lost claim should never reach the venue")
	assert.Empty(t, h.lifecycle.updates, "the winner owns the lifecycle from here")
	assert.Empty(t, h.audit.inserted)

	// A claim infrastructure failure surfaces.
	h = dispatchTestHarness()
	h.claimer.err = errors.New("db down")
	_, err = h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.Error(t, err)
	assert.Zero(t, h.live.calls)
}

func TestUnknownIsNeverResubmitted(t *testing.T) {
	t.Parallel()

	h := dispatchTestHarness()
	intent := dispatchTestIntent()
	intent.Status = IntentStatusUnknown
	_, err := h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
	require.ErrorIs(t, err, ErrIntentInputInvalid)
	assert.Zero(t, h.live.calls, "an ambiguous submission is never blindly retried")
	assert.Zero(t, h.claimer.calls, "the state machine refuses before any claim")

	for _, status := range []string{IntentStatusExpired, IntentStatusRejected, IntentStatusFilled, IntentStatusCanceled} {
		h := dispatchTestHarness()
		intent := dispatchTestIntent()
		intent.Status = status
		_, err := h.dispatcher.Dispatch(context.Background(), intent, LLMRunModeLive)
		assert.ErrorIsf(t, err, ErrIntentInputInvalid, "%s is terminal or inactive", status)
		assert.Zero(t, h.live.calls)
	}
}

func TestIntentStateMachineTransitions(t *testing.T) {
	t.Parallel()

	allowed := []struct{ from, to string }{
		{IntentStatusPrepared, IntentStatusSubmitting},
		{IntentStatusPrepared, IntentStatusExpired},
		{IntentStatusSubmitting, IntentStatusAcked},
		{IntentStatusSubmitting, IntentStatusRejected},
		{IntentStatusSubmitting, IntentStatusUnknown},
		{IntentStatusAcked, IntentStatusResting},
		{IntentStatusAcked, IntentStatusPartiallyFilled},
		{IntentStatusAcked, IntentStatusFilled},
		{IntentStatusAcked, IntentStatusCanceled},
		{IntentStatusAcked, IntentStatusUnknown},
		{IntentStatusResting, IntentStatusFilled},
		{IntentStatusResting, IntentStatusCanceled},
		{IntentStatusPartiallyFilled, IntentStatusFilled},
		{IntentStatusPartiallyFilled, IntentStatusCanceled},
	}
	for _, tc := range allowed {
		assert.Truef(t, intentCanTransition(tc.from, tc.to), "%s -> %s should be allowed", tc.from, tc.to)
	}
	refused := []struct{ from, to string }{
		{IntentStatusUnknown, IntentStatusSubmitting}, // the never-retry edge that must not exist
		{IntentStatusUnknown, IntentStatusPrepared},
		{IntentStatusExpired, IntentStatusSubmitting},
		{IntentStatusRejected, IntentStatusSubmitting},
		{IntentStatusFilled, IntentStatusCanceled}, // a filled order is final
		{IntentStatusCanceled, IntentStatusFilled}, // so is a cancelled one
		{IntentStatusPrepared, IntentStatusAcked},  // no skipping the claim
		{IntentStatusPrepared, IntentStatusFilled},
		{IntentStatusSubmitting, IntentStatusExpired}, // an in-flight order is no longer "stale"
	}
	for _, tc := range refused {
		assert.Falsef(t, intentCanTransition(tc.from, tc.to), "%s -> %s should be refused", tc.from, tc.to)
	}
}

// TestDispatchRaisesAccountEventOnAck covers the account-event producer: the
// moment the venue (or the paper book) accepts an order the trigger layer
// hears about it, so the next look does not wait for the sweep. The ambiguous
// paths raise nothing -- their response is the reconciliation flag.
func TestDispatchRaisesAccountEventOnAck(t *testing.T) {
	t.Parallel()

	h := dispatchTestHarness()
	outcome, err := h.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.NoError(t, err)
	require.True(t, outcome.Executed)
	require.Len(t, h.events, 1, "an accepted order is an account change")
	assert.Equal(t, dispatchTestIntent().Pair, h.events[0].market,
		"the trigger names the market in the scanner's own spelling")
	assert.Contains(t, h.events[0].detail, "exch-1")
	assert.Equal(t, h.now, h.events[0].at)

	// A failed submission is UNKNOWN, not an account change.
	failing := dispatchTestHarness()
	failing.live.err = errors.New("venue down")
	_, err = failing.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.Error(t, err)
	assert.Empty(t, failing.events, "an ambiguous submission should not raise a trigger")

	// Shadow executes nothing, so nothing is raised.
	shadow := dispatchTestHarness()
	_, err = shadow.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeShadow)
	require.NoError(t, err)
	assert.Empty(t, shadow.events)

	// The paper path accepted an order, so it raises like the live one.
	paper := dispatchTestHarness()
	_, err = paper.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModePaper)
	require.NoError(t, err)
	require.Len(t, paper.events, 1)
	assert.Contains(t, paper.events[0].detail, "fake-1")

	// A nil seam drops silently rather than failing the dispatch.
	silent := dispatchTestHarness()
	silent.dispatcher.Events = nil
	_, err = silent.dispatcher.Dispatch(context.Background(), dispatchTestIntent(), LLMRunModeLive)
	require.NoError(t, err)
}
