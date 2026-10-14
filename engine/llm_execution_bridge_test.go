package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5 桥测试：全链 ALLOW + 执行模式才出单（LIMIT 开仓价按 §5.4）、SHADOW 永不出单、
// 平仓在退出数量政策落地前跳过。

func bridgeTestMaterial(mode string) *approvedRunMaterial {
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	dh := dispatchTestHarness()
	dh.dispatcher.Now = func() time.Time { return at }
	ih := intentTestRecorder()
	ih.recorder.Now = func() time.Time { return at }
	cap20 := "20"
	bps20 := 20
	return &approvedRunMaterial{
		Run: &ShadowRunResult{
			Record: &DecisionRecordResult{DecisionRunID: "run-1", RiskEvaluationID: "risk-1"},
			Risk:   &RiskResult{Verdict: RiskVerdictAllow},
			Sizing: &SizingResult{Verdict: RiskVerdictAllow, Quantity: "0.0199", NotionalUSD: "19.9"},
			Synthesis: &SynthesisOutcome{Verdict: RiskVerdictAllow, Candidate: &candidate.Candidate{
				Action: candidate.ActionOpenLong, MarketID: "Binance:usdtmarginedfutures:BTC_USDT",
				SuggestedEntry: "50000.05", ProposedMaxNotionalUSD: "15",
			}},
		},
		MarketID: "Binance:usdtmarginedfutures:BTC_USDT", ApprovalID: "approval-1",
		StrategyVersionID: "ver-1", EnvelopeHash: "env-hash", PairEpoch: 7,
		Envelope: candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20, MaxSlippageBPS: &bps20},
		Positions: []RiskPositionFact{{
			MarketID: "Binance:usdtmarginedfutures:BTC_USDT", Side: PositionSideLong,
			Quantity: "0.5", EntryPrice: "49000", MarkPrice: "50000",
		}},
		Contract: materialTestContract(),
		BestBid:  "49990", BestAsk: "50010",
		Mode:       func() string { return mode },
		Recorder:   ih.recorder,
		Dispatcher: dh.dispatcher,
	}
}

// bridgeIntentStore returns the recorder's fake store with a checked assertion.
func bridgeIntentStore(t *testing.T, recorder *IntentRecorder) *intentFakeStore {
	t.Helper()
	store, ok := recorder.Intents.(*intentFakeStore)
	require.True(t, ok, "the recorder must hold the fake intent store")
	return store
}

// bridgePaperOrders / bridgeLiveOrders expose the dispatcher's fakes.
func bridgePaperOrders(t *testing.T, d *OrderDispatcher) *dispatchFakeFake {
	t.Helper()
	fake, ok := d.Fake.(*dispatchFakeFake)
	require.True(t, ok, "the dispatcher must hold the fake submitter")
	return fake
}

func bridgeLiveOrders(t *testing.T, d *OrderDispatcher) *dispatchFakeSubmitter {
	t.Helper()
	live, ok := d.Orders.(*dispatchFakeSubmitter)
	require.True(t, ok, "the dispatcher must hold the live submitter")
	return live
}

func TestExecuteApprovedRunSubmitsTheOpenInPaper(t *testing.T) {
	t.Parallel()
	in := bridgeTestMaterial(LLMRunModePaper)
	outcome, err := executeApprovedRun(t.Context(), in)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.True(t, outcome.Executed)
	assert.Equal(t, IntentStatusAcked, outcome.Status)

	store := bridgeIntentStore(t, in.Recorder)
	require.Len(t, store.inserted, 1)
	intent := store.inserted[0]
	assert.Equal(t, OrderTypeLimit, intent.OrderType, "opens rest at the suggested entry (decisions/02 B)")
	require.NotNil(t, intent.Price)
	assert.Equal(t, "50000", *intent.Price, "the LIMIT price is the tick-floored suggested entry (§5.4)")
	assert.Equal(t, "0.0199", intent.Quantity)
	assert.Equal(t, "19.9", intent.NotionalUSD)

	assert.Equal(t, 1, bridgePaperOrders(t, in.Dispatcher).calls, "PAPER routes through the fake submitter")
	assert.Zero(t, bridgeLiveOrders(t, in.Dispatcher).calls)
}

func TestExecuteApprovedRunNeverSubmitsOutsideExecutingModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{LLMRunModeReadOnly, LLMRunModeShadow, ""} {
		in := bridgeTestMaterial(mode)
		outcome, err := executeApprovedRun(t.Context(), in)
		require.NoError(t, err)
		assert.Nil(t, outcome)
		assert.Empty(t, bridgeIntentStore(t, in.Recorder).inserted)
		assert.Zero(t, bridgePaperOrders(t, in.Dispatcher).calls)
	}
}

func TestExecuteApprovedRunRequiresTheFullAllowChain(t *testing.T) {
	t.Parallel()

	rejected := bridgeTestMaterial(LLMRunModePaper)
	rejected.Run.Risk = &RiskResult{Verdict: RiskVerdictReject}
	outcome, err := executeApprovedRun(t.Context(), rejected)
	require.NoError(t, err)
	assert.Nil(t, outcome)
	assert.Empty(t, bridgeIntentStore(t, rejected.Recorder).inserted)

	unsized := bridgeTestMaterial(LLMRunModePaper)
	unsized.Run.Sizing = nil
	outcome, err = executeApprovedRun(t.Context(), unsized)
	require.NoError(t, err)
	assert.Nil(t, outcome)

	unrecorded := bridgeTestMaterial(LLMRunModePaper)
	unrecorded.Run.Record = nil
	outcome, err = executeApprovedRun(t.Context(), unrecorded)
	require.NoError(t, err)
	assert.Nil(t, outcome)
}

func TestExecuteApprovedRunClosesTheFullPositionOnReduce(t *testing.T) {
	t.Parallel()
	in := bridgeTestMaterial(LLMRunModePaper)
	in.Run.Synthesis.Candidate = &candidate.Candidate{Action: candidate.ActionReduce, MarketID: in.MarketID}
	in.Run.Sizing = nil // exits carry no open-sizing result: the plan is sized by the evaluated position

	outcome, err := executeApprovedRun(t.Context(), in)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.True(t, outcome.Executed)

	store := bridgeIntentStore(t, in.Recorder)
	require.Len(t, store.inserted, 1)
	intent := store.inserted[0]
	assert.Equal(t, OrderTypeMarket, intent.OrderType, "active closes take the book (decisions/02 B)")
	assert.Nil(t, intent.Price, "a MARKET order carries no price")
	assert.Equal(t, "SELL", intent.Side, "closing a long sells")
	assert.True(t, intent.ReduceOnly)
	assert.Equal(t, "0.5", intent.Quantity, "the whole evaluated position closes")
	assert.Equal(t, "25000", intent.NotionalUSD, "max(entry, mark) x quantity -- the conservative spelling")
	assert.Equal(t, 1, bridgePaperOrders(t, in.Dispatcher).calls)
}

func TestExecuteApprovedRunShortPositionClosesWithBuy(t *testing.T) {
	t.Parallel()
	in := bridgeTestMaterial(LLMRunModePaper)
	in.Run.Synthesis.Candidate = &candidate.Candidate{Action: candidate.ActionReduce, MarketID: in.MarketID}
	in.Run.Sizing = nil
	in.Positions[0].Side = PositionSideShort

	outcome, err := executeApprovedRun(t.Context(), in)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	intent := bridgeIntentStore(t, in.Recorder).inserted[0]
	assert.Equal(t, "BUY", intent.Side, "closing a short buys back")
	assert.True(t, intent.ReduceOnly)
}

func TestExecuteApprovedRunRefusesAReduceWithoutItsPosition(t *testing.T) {
	t.Parallel()
	in := bridgeTestMaterial(LLMRunModePaper)
	in.Run.Synthesis.Candidate = &candidate.Candidate{Action: candidate.ActionReduce, MarketID: in.MarketID}
	in.Run.Sizing = nil
	in.Positions = nil

	_, err := executeApprovedRun(t.Context(), in)
	require.ErrorIs(t, err, ErrIntentInputInvalid, "an ALLOWed reduce without its evaluated position is broken material")
	assert.Empty(t, bridgeIntentStore(t, in.Recorder).inserted)
}

func TestExecutionModePermits(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive, "paper"} {
		assert.Truef(t, executionModePermits(mode), "%q executes", mode)
	}
	for _, mode := range []string{LLMRunModeReadOnly, LLMRunModeShadow, ""} {
		assert.Falsef(t, executionModePermits(mode), "%q never executes", mode)
	}
}
