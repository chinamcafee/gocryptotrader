package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// intentPlanInputFixture is a fully ALLOW, LIMIT-ready plan input.
func intentPlanInputFixture() *IntentPlanInput {
	return &IntentPlanInput{
		Record:            &DecisionRecordResult{DecisionRunID: "run-1", RiskEvaluationID: "risk-1", Decision: "ALLOW"},
		Risk:              &RiskResult{Verdict: RiskVerdictAllow},
		Sizing:            &SizingResult{Verdict: RiskVerdictAllow, Quantity: "0.01", NotionalUSD: "1000"},
		Synthesis:         &SynthesisOutcome{Candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, MarketID: "BTC_USDT"}},
		ApprovalID:        "appr-1",
		StrategyVersionID: "ver-1",
		EnvelopeHash:      "hash-1",
		PairEpoch:         3,
		MarketID:          "binance:usdtmarginedfutures:BTC_USDT",
		OrderType:         OrderTypeLimit,
		SuggestedEntry:    "50000",
		TickSize:          "0.1",
		BestBid:           "49999",
		BestAsk:           "50001",
		MaxSlippageBPS:    new(5),
		NewID:             func() string { return "intent-1" },
	}
}

// TestBuildIntentPlanLimitOpen covers the happy path end to end: the plan
// carries the reviewed material and feeds BuildIntent directly, with the
// LIMIT price derived from the carried book material.
func TestBuildIntentPlanLimitOpen(t *testing.T) {
	t.Parallel()

	in := intentPlanInputFixture()
	plan, err := BuildIntentPlan(in)
	require.NoError(t, err)
	assert.Equal(t, "run-1", plan.DecisionRunID)
	assert.Equal(t, "risk-1", plan.RiskEvaluationID)
	assert.Equal(t, "appr-1", plan.ApprovalID)
	assert.Equal(t, OrderTypeLimit, plan.OrderType)
	assert.Equal(t, candidate.ActionOpenLong, plan.Action)
	assert.Empty(t, plan.ReduceSide, "an open plan carries no reduce side")
	assert.Equal(t, "binance:usdtmarginedfutures:BTC_USDT", plan.MarketID)
	assert.Equal(t, int64(3), plan.PairEpoch)
	assert.Equal(t, in.Risk, plan.Risk)
	assert.Equal(t, in.Sizing, plan.Sizing)

	intent, err := BuildIntent(plan, time.Now())
	require.NoError(t, err)
	assert.Equal(t, OrderTypeLimit, intent.OrderType)
	require.NotNil(t, intent.Price, "a limit intent carries its price")
	assert.Equal(t, "50000", *intent.Price)
	assert.Equal(t, "BUY", intent.Side)
	assert.False(t, intent.ReduceOnly)
	assert.Equal(t, "0.01", intent.Quantity)
	assert.Equal(t, "1000", intent.NotionalUSD)
	assert.Equal(t, "binance", intent.Exchange)
}

// TestBuildIntentPlanMarketAndReduce covers the other two shapes: MARKET
// needs no price material, and a reduce normalizes and honours its side.
func TestBuildIntentPlanMarketAndReduce(t *testing.T) {
	t.Parallel()

	market := intentPlanInputFixture()
	market.OrderType = OrderTypeMarket
	market.SuggestedEntry = ""
	market.TickSize = ""
	market.BestBid = ""
	market.BestAsk = ""
	market.MaxSlippageBPS = nil
	plan, err := BuildIntentPlan(market)
	require.NoError(t, err)
	assert.Equal(t, OrderTypeMarket, plan.OrderType)
	intent, err := BuildIntent(plan, time.Now())
	require.NoError(t, err)
	assert.Nil(t, intent.Price, "a market order carries no price")

	reduce := intentPlanInputFixture()
	reduce.Synthesis = &SynthesisOutcome{Candidate: &candidate.Candidate{Action: candidate.ActionReduce, MarketID: "BTC_USDT"}}
	reduce.ReduceSide = " short "
	plan, err = BuildIntentPlan(reduce)
	require.NoError(t, err)
	assert.Equal(t, "SHORT", plan.ReduceSide, "the position side is normalized")
	intent, err = BuildIntent(plan, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "BUY", intent.Side, "closing a short buys back")
	assert.True(t, intent.ReduceOnly)
}

// TestBuildIntentPlanRefusals pins the eager validation: bad material is
// refused at the plan, not at the database.
func TestBuildIntentPlanRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*IntentPlanInput)
		wantErr error
	}{
		{"risk not allow", func(in *IntentPlanInput) { in.Risk = &RiskResult{Verdict: RiskVerdictReject} }, ErrIntentNotApproved},
		{"risk missing", func(in *IntentPlanInput) { in.Risk = nil }, ErrIntentNotApproved},
		{"sizing not allow", func(in *IntentPlanInput) { in.Sizing = &SizingResult{Verdict: RiskVerdictReject} }, ErrIntentNotApproved},
		{"sizing missing", func(in *IntentPlanInput) { in.Sizing = nil }, ErrIntentNotApproved},
		{"missing record", func(in *IntentPlanInput) { in.Record = nil }, ErrIntentInputInvalid},
		{"missing risk row id", func(in *IntentPlanInput) { in.Record.RiskEvaluationID = "" }, ErrIntentInputInvalid},
		{"no candidate", func(in *IntentPlanInput) { in.Synthesis = &SynthesisOutcome{} }, ErrIntentInputInvalid},
		{"hold action", func(in *IntentPlanInput) { in.Synthesis.Candidate.Action = candidate.ActionHold }, ErrIntentInputInvalid},
		{"stop market type", func(in *IntentPlanInput) { in.OrderType = OrderTypeStopMarket }, ErrIntentInputInvalid},
		{"unknown type", func(in *IntentPlanInput) { in.OrderType = "ICEBERG" }, ErrIntentInputInvalid},
		{"limit without entry", func(in *IntentPlanInput) { in.SuggestedEntry = "" }, ErrIntentInputInvalid},
		{"limit without tick", func(in *IntentPlanInput) { in.TickSize = "" }, ErrIntentInputInvalid},
		{"limit without bid", func(in *IntentPlanInput) { in.BestBid = "" }, ErrIntentInputInvalid},
		{"limit without ask", func(in *IntentPlanInput) { in.BestAsk = "" }, ErrIntentInputInvalid},
		{"two-segment market", func(in *IntentPlanInput) { in.MarketID = "BTC_USDT" }, ErrIntentInputInvalid},
		{"reduce without side", func(in *IntentPlanInput) {
			in.Synthesis.Candidate.Action = candidate.ActionReduce
		}, ErrIntentInputInvalid},
		{"missing approval", func(in *IntentPlanInput) { in.ApprovalID = "" }, ErrIntentInputInvalid},
		{"missing version", func(in *IntentPlanInput) { in.StrategyVersionID = "" }, ErrIntentInputInvalid},
		{"missing envelope hash", func(in *IntentPlanInput) { in.EnvelopeHash = "" }, ErrIntentInputInvalid},
		{"nil input", nil, ErrIntentInputInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var in *IntentPlanInput
			if tc.mutate != nil {
				in = intentPlanInputFixture()
				tc.mutate(in)
			}
			_, err := BuildIntentPlan(in)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}
