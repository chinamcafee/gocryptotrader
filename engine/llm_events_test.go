package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/communications/base"
)

// M5-11 测试：消息成文（含关键字段）、两处响铃的等级、全事件词汇可达、空推不炸。

type eventCollector struct {
	events []base.Event
}

func (c *eventCollector) push(evt base.Event) { c.events = append(c.events, evt) }

func TestEmitFormatsAndNeverPanics(t *testing.T) {
	t.Parallel()

	collector := &eventCollector{}
	notifier := &LLMNotifier{Push: collector.push}
	notifier.Emit(LLMEvent{Type: EventHaltSet, Severity: SeverityHigh, Summary: "HALT FULL: clock drift", Detail: `{"x":1}`})
	require.Len(t, collector.events, 1)
	assert.Equal(t, EventHaltSet, collector.events[0].Type)
	assert.Equal(t, "[high] HALT FULL: clock drift | {\"x\":1}", collector.events[0].Message)

	// No detail: no trailing separator.
	notifier.Emit(LLMEvent{Type: EventBudgetExhausted, Severity: SeverityInfo, Summary: "budget exhausted"})
	assert.Equal(t, "[info] budget exhausted", collector.events[1].Message)

	// A nil seam or nil notifier drops silently: a notification outage must not
	// be able to fail the path it reports on.
	var nilNotifier *LLMNotifier
	nilNotifier.Emit(LLMEvent{Type: EventHaltSet, Summary: "s"})
	nilNotifier.HaltSet("FULL", "r", "global")
	(&LLMNotifier{}).HaltSet("FULL", "r", "global")
}

func TestTheTwoLoudBells(t *testing.T) {
	t.Parallel()

	collector := &eventCollector{}
	notifier := &LLMNotifier{Push: collector.push}

	notifier.OrderUnknown("intent-1", "connection reset")
	require.Len(t, collector.events, 1)
	assert.Equal(t, EventOrderUnknown, collector.events[0].Type)
	assert.Contains(t, collector.events[0].Message, "UNKNOWN")
	assert.Contains(t, collector.events[0].Message, "reconciliation")

	notifier.ProtectiveExitFailed("intent-1", "venue rejected the stop")
	require.Len(t, collector.events, 2)
	assert.Equal(t, EventProtectiveExitFailed, collector.events[1].Type)
	assert.Contains(t, collector.events[1].Message, "[critical]")
	assert.Contains(t, collector.events[1].Message, "fail-safe flatten triggered")

	notifier.HaltSet(HaltLevelFull, "clock drift", "global")
	assert.Equal(t, EventHaltSet, collector.events[2].Type)
	assert.Contains(t, collector.events[2].Message, "[high]")
}

func TestDecisionRejectCarriesActualVsAllowed(t *testing.T) {
	t.Parallel()

	collector := &eventCollector{}
	notifier := &LLMNotifier{Push: collector.push}
	notifier.DecisionReject([]RiskRejection{
		{Code: RiskHalted, Actual: "halted", Allowed: "no halt", Unit: "", Field: "halt"},
		{Code: RiskMarketNotGranted},
	})
	require.Len(t, collector.events, 1)
	message := collector.events[0].Message
	assert.Contains(t, message, "RISK_HALTED")
	assert.Contains(t, message, "actual halted vs allowed no halt")
	assert.Contains(t, message, "RISK_MARKET_NOT_GRANTED", "the full rejection list travels in the detail")
}

func TestEveryEventTypeHasAPath(t *testing.T) {
	t.Parallel()

	collector := &eventCollector{}
	notifier := &LLMNotifier{Push: collector.push}
	notifier.DecisionAllow("v1", "m1", "OPEN_LONG", "0.0199", "19.9", "AWAITING_HARD_RISK_AND_APPROVAL")
	notifier.DecisionReject(nil)
	notifier.DecisionReview("v1", "m1", ReasonJevHold)
	notifier.OrderSubmitted("i1", "exch-1", "gct-llm-1")
	notifier.OrderFilled("i1", "0.0199", "1000", "0.01")
	notifier.OrderUnknown("i1", "timeout")
	notifier.PairChanged(4, 2)
	notifier.ProbeFailed("rev-1", "AUTH", "TIMEOUT")
	notifier.BudgetExhausted("rev-1")
	notifier.HaltSet(HaltLevelSoft, "pair stale", "global")
	notifier.HaltCleared(HaltLevelSoft, "fixed", "op-1")
	notifier.ReconcileMismatch("acct-1", []PositionDelta{{MarketID: "m1", Side: "LONG", Local: "1", Exchange: "2"}})
	notifier.StrategyExpiring("v1", 15)
	notifier.ProtectiveExitFailed("i1", "detail")
	notifier.DataStale("candles", 300)

	seen := map[string]bool{}
	for _, evt := range collector.events {
		seen[evt.Type] = true
	}
	require.Len(t, collector.events, 15)
	assert.Len(t, seen, 15, "every event type is reachable and distinct")
	for _, expected := range []string{
		EventDecisionAllow, EventDecisionReject, EventDecisionReview,
		EventOrderSubmitted, EventOrderFilled, EventOrderUnknown,
		EventPairChanged, EventProbeFailed, EventBudgetExhausted,
		EventHaltSet, EventHaltCleared, EventReconcileMismatch,
		EventStrategyExpiring, EventProtectiveExitFailed, EventDataStale,
	} {
		assert.Truef(t, seen[expected], "%s should be emitted", expected)
	}
	assert.True(t, strings.HasPrefix(collector.events[0].Message, "[info]"), "routine decisions are informational")
}
