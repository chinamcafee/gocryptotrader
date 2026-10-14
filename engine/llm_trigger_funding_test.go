package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// M2-13 残余①（快照侧）与残余②（账户事件入口）。

func TestFactFromSnapshotCarriesNextFunding(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 11, 12, 30, 0, 0, time.UTC)
	built := &snapshot.MarketSnapshot{
		Pair:    "BTC_USDT",
		Candles: snapshot.CandleWindow{Interval: snapshot.Interval1h},
		Funding: &snapshot.FundingFacts{NextFundingTime: at},
	}
	fact, err := FactFromSnapshot(built, nil)
	require.NoError(t, err)
	assert.True(t, fact.NextFundingAt.Equal(at), "the snapshot's settlement time should reach the fact")

	built.Funding = nil
	fact, err = FactFromSnapshot(built, nil)
	require.NoError(t, err)
	assert.True(t, fact.NextFundingAt.IsZero(), "an absent funding block leaves the time unknown")
}

func TestRaiseAccountEventThrottlesAndEnqueues(t *testing.T) {
	t.Parallel()
	clock := &fixedClock{at: testNow}
	s := newScanner(t, trigger.DefaultPolicy(), clock)

	raised, err := s.RaiseAccountEvent("BTC_USDT", "position liquidated", time.Time{})
	require.NoError(t, err)
	require.Len(t, raised, 1, "the first account event passes")
	assert.Equal(t, trigger.KindAccountEvent, raised[0].Kind)
	assert.False(t, raised[0].NeedsConfirmation, "a fact that happened needs no confirming pass")
	assert.Equal(t, testNow, raised[0].At, "an empty instant falls back to the scanner clock")

	raised, err = s.RaiseAccountEvent("BTC_USDT", "fill", clock.now())
	require.NoError(t, err)
	assert.Empty(t, raised, "a burst of account changes describes one moment and is throttled")

	clock.advance(2 * time.Minute)
	raised, err = s.RaiseAccountEvent("BTC_USDT", "fill", time.Time{})
	require.NoError(t, err)
	require.Len(t, raised, 1, "after the throttle window a new event passes")

	drained := s.Drain(10)
	require.Len(t, drained, 2, "both admitted events were queued")

	_, err = s.RaiseAccountEvent("  ", "fill", time.Time{})
	assert.ErrorIs(t, err, trigger.ErrNoMarket)

	var nilScanner *TriggerScanner
	_, err = nilScanner.RaiseAccountEvent("BTC_USDT", "fill", time.Time{})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
