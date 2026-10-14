package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scheduleRequest(version, market string, at time.Time) EvaluationRequest {
	return EvaluationRequest{StrategyVersionID: version, MarketID: market, TriggerKind: "KLINE_CLOSE", At: at}
}

func TestEvaluationSchedulerDebouncesPerVersion(t *testing.T) {
	clock := &fixedClock{at: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	scheduler := NewEvaluationScheduler(clock.now)

	first, err := scheduler.Admit(scheduleRequest("ver-1", "Binance:usdtmarginedfutures:BTC_USDT", clock.at))
	require.NoError(t, err)
	assert.True(t, first.Allowed, "the first trigger runs")

	// A second trigger inside the minute is skipped and names its reason --
	// the caller records SKIPPED_RATE_LIMITED and spends no budget.
	clock.advance(20 * time.Second)
	second, err := scheduler.Admit(scheduleRequest("ver-1", "Binance:usdtmarginedfutures:ETH_USDT", clock.at))
	require.NoError(t, err)
	assert.False(t, second.Allowed)
	assert.Equal(t, ReasonSkippedRateLimited, second.ReasonCode)
	assert.Equal(t, 40*time.Second, second.RetryAfter)

	// The skip does not extend the window: a storm cannot postpone the
	// evaluation forever (starvation by politeness).
	clock.advance(10 * time.Second)
	third, err := scheduler.Admit(scheduleRequest("ver-1", "Binance:usdtmarginedfutures:BTC_USDT", clock.at))
	require.NoError(t, err)
	assert.False(t, third.Allowed)
	assert.Equal(t, 30*time.Second, third.RetryAfter, "the window still ends at first+1m")

	// A different version has its own window.
	other, err := scheduler.Admit(scheduleRequest("ver-2", "Binance:usdtmarginedfutures:BTC_USDT", clock.at))
	require.NoError(t, err)
	assert.True(t, other.Allowed, "versions should not share windows")

	// One minute after the first run, the version runs again.
	clock.at = clock.at.Add(30 * time.Second).Add(1 * time.Second) // first + 61s
	reopened, err := scheduler.Admit(scheduleRequest("ver-1", "Binance:usdtmarginedfutures:BTC_USDT", clock.at))
	require.NoError(t, err)
	assert.True(t, reopened.Allowed, "the window reopens after a minute")
}

func TestEvaluationSchedulerUsesItsClockWhenUnset(t *testing.T) {
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	scheduler := NewEvaluationScheduler(func() time.Time { return at })

	request := EvaluationRequest{StrategyVersionID: "ver-1", MarketID: "m", TriggerKind: "MANUAL"}
	first, err := scheduler.Admit(request)
	require.NoError(t, err)
	assert.True(t, first.Allowed)
	at = at.Add(30 * time.Second)
	second, err := scheduler.Admit(request)
	require.NoError(t, err)
	assert.False(t, second.Allowed)
}

func TestEvaluationSchedulerForgetAndPrune(t *testing.T) {
	clock := &fixedClock{at: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	scheduler := NewEvaluationScheduler(clock.now)

	_, err := scheduler.Admit(scheduleRequest("ver-1", "m", clock.at))
	require.NoError(t, err)
	_, err = scheduler.Admit(scheduleRequest("ver-2", "m", clock.at))
	require.NoError(t, err)
	assert.Equal(t, 2, scheduler.TrackedVersions())

	// Paused/revoked strategies must not carry a stale throttle.
	scheduler.Forget("ver-1")
	assert.Equal(t, 1, scheduler.TrackedVersions())
	admitted, err := scheduler.Admit(scheduleRequest("ver-1", "m", clock.at.Add(10*time.Second)))
	require.NoError(t, err)
	assert.True(t, admitted.Allowed, "a forgotten version starts fresh")

	// Prune drops windows that can no longer throttle anything.
	clock.advance(2 * time.Minute)
	assert.Equal(t, 2, scheduler.Prune(clock.at), "both stale windows are droppable")
	assert.Zero(t, scheduler.TrackedVersions())

	clock.advance(5 * time.Second)
	_, err = scheduler.Admit(scheduleRequest("ver-3", "m", clock.at))
	require.NoError(t, err)
	assert.Zero(t, scheduler.Prune(clock.at.Add(30*time.Second)), "a fresh window is not pruned early")
}

func TestEvaluationSchedulerValidationAndConcurrency(t *testing.T) {
	scheduler := NewEvaluationScheduler(nil)
	_, err := scheduler.Admit(EvaluationRequest{StrategyVersionID: " ", MarketID: "m"})
	assert.ErrorIs(t, err, ErrScheduleInputInvalid)
	_, err = scheduler.Admit(EvaluationRequest{StrategyVersionID: "v", MarketID: ""})
	assert.ErrorIs(t, err, ErrScheduleInputInvalid)

	var nilScheduler *EvaluationScheduler
	_, err = nilScheduler.Admit(scheduleRequest("v", "m", time.Now()))
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	// Simultaneous triggers of one version: exactly one wins.
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	concurrent := NewEvaluationScheduler(func() time.Time { return at })
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 32 {
		wg.Go(func() {
			admission, err := concurrent.Admit(scheduleRequest("ver-race", "m", at))
			if !assert.NoError(t, err) {
				return
			}
			if admission.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	assert.Equal(t, 1, allowed, "one version, one evaluation per minute -- even under a trigger storm")
}
