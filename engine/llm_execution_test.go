package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5-01 测试：三重把关、动作到方向的映射、限价的 tick/盘口界、落库中止与审计。

type intentFakeStore struct {
	inserted []llmstore.OrderIntent
	err      error
}

func (f *intentFakeStore) InsertOrderIntent(_ context.Context, o *llmstore.OrderIntent) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, *o)
	return nil
}

type intentHarness struct {
	recorder *IntentRecorder
	store    *intentFakeStore
	audit    *recordFakeAudit
	now      time.Time
}

func intentTestRecorder() *intentHarness {
	store := &intentFakeStore{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)
	counter := 0
	return &intentHarness{
		recorder: &IntentRecorder{
			Intents: store, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		store: store, audit: audit, now: now,
	}
}

func intentTestPlan() *IntentPlan {
	return &IntentPlan{
		DecisionRunID:     "run-1",
		RiskEvaluationID:  "risk-1",
		StrategyVersionID: "4b1d2e71-2b3c-4d4e-9f50-6b7c8d9e0f10",
		ApprovalID:        "approval-1",
		MarketID:          "Binance:usdtmarginedfutures:BTC_USDT",
		Action:            candidate.ActionOpenLong,
		OrderType:         "LIMIT",
		EnvelopeHash:      "env-hash",
		PairEpoch:         7,
		Risk:              &RiskResult{Verdict: RiskVerdictAllow},
		Sizing:            &SizingResult{Verdict: RiskVerdictAllow, Quantity: "0.0199", NotionalUSD: "19.9", ReferencePrice: "1000"},
		SuggestedEntry:    "1000.05",
		TickSize:          "0.1",
		BestBid:           "999.9",
		BestAsk:           "1000.1",
		MaxSlippageBPS:    new(20),
	}
}

func TestBuildIntentHappyLimitOpen(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID
	intent, err := BuildIntent(plan, h.now)
	require.NoError(t, err)

	assert.Equal(t, "Binance", intent.Exchange)
	assert.Equal(t, "usdtmarginedfutures", intent.Asset)
	assert.Equal(t, "BTC_USDT", intent.Pair)
	assert.Equal(t, "BUY", intent.Side)
	assert.False(t, intent.ReduceOnly)
	assert.Equal(t, "LIMIT", intent.OrderType)
	assert.Equal(t, "0.0199", intent.Quantity)
	require.NotNil(t, intent.Price)
	assert.Equal(t, "1000", *intent.Price, "a buy price floors to the tick so it never pays above the reviewed level")
	assert.Equal(t, IntentStatusPrepared, intent.Status)
	assert.Equal(t, h.now.Add(IntentDefaultExpiry), intent.ExpiresAt, "the documented 10s default")
	assert.Equal(t, h.now, intent.CreatedAt)
	assert.Equal(t, "run-1", intent.DecisionRunID)
	assert.Equal(t, int64(7), intent.PairEpoch)
	assert.Greater(t, len(intent.ClientOrderID), len(IntentClientOrderIDPrefix))
	assert.Equal(t, IntentClientOrderIDPrefix, intent.ClientOrderID[:len(IntentClientOrderIDPrefix)])
}

func TestBuildIntentReduceSides(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID
	plan.Action = candidate.ActionReduce
	plan.ReduceSide = "LONG"
	intent, err := BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, "SELL", intent.Side)
	assert.True(t, intent.ReduceOnly, "a reduce is always reduce-only")

	plan.ReduceSide = "SHORT"
	intent, err = BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, "BUY", intent.Side)
	assert.True(t, intent.ReduceOnly)

	plan.ReduceSide = ""
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentInputInvalid)

	plan.Action = candidate.ActionHold
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentInputInvalid, "hold and review create no order")
}

func TestBuildIntentRequiresAllowEverywhere(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID

	plan.Risk = &RiskResult{Verdict: RiskVerdictReject}
	_, err := BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentNotApproved)
	plan.Risk = &RiskResult{Verdict: RiskVerdictAllow}

	plan.Sizing = &SizingResult{Verdict: RiskVerdictReject}
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentNotApproved)
	plan.Sizing = &SizingResult{Verdict: RiskVerdictAllow, Quantity: "0.0199", NotionalUSD: "19.9"}

	plan.Risk = nil
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentNotApproved)
}

func TestBuildIntentValidatesBindings(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*IntentPlan){
		"decision run":     func(p *IntentPlan) { p.DecisionRunID = "" },
		"approval":         func(p *IntentPlan) { p.ApprovalID = "" },
		"envelope":         func(p *IntentPlan) { p.EnvelopeHash = "" },
		"risk evaluation":  func(p *IntentPlan) { p.RiskEvaluationID = "" },
		"market":           func(p *IntentPlan) { p.MarketID = "Binance:BTC_USDT" },
		"order type":       func(p *IntentPlan) { p.OrderType = "STOP_MARKET" },
		"quantity missing": func(p *IntentPlan) { p.Sizing.Quantity = "" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := intentTestRecorder()
			plan := intentTestPlan()
			plan.NewID = h.recorder.NewID
			planCopy := *plan
			sizingCopy := *plan.Sizing
			planCopy.Sizing = &sizingCopy
			mutate(&planCopy)
			_, err := BuildIntent(&planCopy, h.now)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrIntentInputInvalid)
		})
	}
}

func TestLimitPriceBoundsAndRounding(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID

	// Sell ceils to the tick and must stay at or above bid minus slippage.
	plan.Action = candidate.ActionOpenShort
	plan.SuggestedEntry = "999.94"
	plan.BestBid = "1000"
	plan.BestAsk = "1001"
	intent, err := BuildIntent(plan, h.now)
	require.NoError(t, err)
	require.NotNil(t, intent.Price)
	assert.Equal(t, "1000", *intent.Price)

	plan.SuggestedEntry = "990" // below bid*(1-20bps)=999 - 2 = 998
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentPriceOutOfBounds)

	// A buy above ask plus slippage refuses.
	plan = intentTestPlan()
	plan.NewID = h.recorder.NewID
	plan.SuggestedEntry = "1005" // above 1000.1 * 1.002
	_, err = BuildIntent(plan, h.now)
	assert.ErrorIs(t, err, ErrIntentPriceOutOfBounds)
}

func TestBuildIntentMarketAndExpiry(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID
	plan.OrderType = "MARKET"
	plan.SuggestedEntry = ""
	plan.TickSize = ""
	intent, err := BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Nil(t, intent.Price, "a market order carries no price (the venue rejects one)")

	plan.OrderType = "LIMIT"
	plan.SuggestedEntry = "1000.05"
	plan.TickSize = "0.1"
	plan.ExpiresIn = 10 * time.Minute
	intent, err = BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, h.now.Add(IntentMaxExpiry), intent.ExpiresAt, "an operator-supplied expiry is capped")

	plan.ExpiresIn = 30 * time.Second
	intent, err = BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, h.now.Add(30*time.Second), intent.ExpiresAt)
}

func TestClientOrderIDPrefixing(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	plan.NewID = h.recorder.NewID

	plan.ClientOrderID = "abc-123"
	intent, err := BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, "gct-llm-abc-123", intent.ClientOrderID, "a bare key gains the namespace prefix")

	plan.ClientOrderID = "gct-llm-abc-123"
	intent, err = BuildIntent(plan, h.now)
	require.NoError(t, err)
	assert.Equal(t, "gct-llm-abc-123", intent.ClientOrderID, "an operator retry with the full key is idempotent")
}

func TestIntentRecorderWritesIntentAndAudit(t *testing.T) {
	t.Parallel()

	h := intentTestRecorder()
	plan := intentTestPlan()
	intent, err := h.recorder.Record(context.Background(), plan, "operator-1")
	require.NoError(t, err)
	require.Len(t, h.store.inserted, 1)
	assert.Equal(t, intent.ID, h.store.inserted[0].ID)
	require.Len(t, h.audit.inserted, 1)
	event := h.audit.inserted[0]
	assert.Equal(t, AuditKindIntentPrepared, event.Kind)
	assert.Equal(t, "operator-1", event.Actor)
	require.NotNil(t, event.Subject)
	assert.Equal(t, intent.ID, *event.Subject)
	assert.Contains(t, event.DetailJSON, intent.ClientOrderID)

	// A failed insert must not leave an audit event claiming the intent exists.
	h = intentTestRecorder()
	h.store.err = errors.New("db down")
	_, err = h.recorder.Record(context.Background(), plan, "operator-1")
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted)

	h = intentTestRecorder()
	h.audit.err = errors.New("db down")
	_, err = h.recorder.Record(context.Background(), plan, "operator-1")
	require.Error(t, err, "an intent that cannot be audited must surface, not proceed silently")

	var nilRecorder *IntentRecorder
	_, err = nilRecorder.Record(context.Background(), plan, "operator-1")
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
