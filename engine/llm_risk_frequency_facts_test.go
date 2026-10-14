package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// TestRiskFrequencyFactsCountsWindow covers the E-group assembly: persisted
// sliding-window counts read from the store, the prospective order folded in,
// and the explicitly supplied policy fields passed through untouched.
func TestRiskFrequencyFactsCountsWindow(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	now := providerAdminNow

	seedIntent := func(id, version, action string, at time.Time) {
		t.Helper()
		require.NoError(t, llmstore.InsertOrderIntent(ctx, &llmstore.OrderIntent{
			ID: id, DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
			StrategyVersionID: version, ApprovalID: "appr-1",
			Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
			Action: action, Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
			ClientOrderID: "gct-" + id, NotionalUSD: "500.00", EnvelopeHash: "env",
			Status: "PREPARED", ExpiresAt: now.Add(time.Hour), CreatedAt: at, UpdatedAt: at,
		}))
	}
	seedIntent("i1", "ver-a", "OPEN_LONG", now.Add(-10*time.Minute))
	seedIntent("i2", "ver-b", "REDUCE", now.Add(-20*time.Minute))
	seedIntent("i3", "ver-a", "OPEN_LONG", now.Add(-400*time.Minute)) // outside the 300m window

	// One unreleased new-risk counter still occupies the window; a released
	// one (cancelled intent) does not.
	require.NoError(t, llmstore.InsertNewRiskCounter(ctx, &llmstore.NewRiskCounter{
		ID: "nrc-1", AccountID: "acct", Scope: "ACCOUNT", ScopeID: "acct",
		IntentID: "i1", CountedAt: now.Add(-5 * time.Minute), WindowMinutes: 300,
	}))
	require.NoError(t, llmstore.InsertNewRiskCounter(ctx, &llmstore.NewRiskCounter{
		ID: "nrc-2", AccountID: "acct", Scope: "ACCOUNT", ScopeID: "acct",
		IntentID: "i2", CountedAt: now.Add(-6 * time.Minute), WindowMinutes: 300,
	}))
	require.NoError(t, llmstore.ReleaseNewRiskCounter(ctx, "nrc-2", now.Add(-time.Minute)))

	in := &RiskFrequencyFactsInput{
		AccountID: "acct", StrategyVersionID: "ver-a", Action: candidate.ActionOpenLong,
		Now: now, WindowMinutes: 300,
		MaxVersionWindow: 5, MaxOrdersPerWindow: 6, MaxReduceOrdersPerWindow: 7,
		MarketCooldownElapsed: true, ConsecutiveLosses: 2, MaxConsecutiveLosses: 3,
	}
	facts, err := riskFrequencyFactsFromState(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, 2, facts.NewRiskIntentsInWindow, "one unreleased counter plus the prospective open")
	assert.Equal(t, 2, facts.VersionWindowCount, "one in-window version-a intent plus the prospective order")
	assert.Equal(t, 3, facts.AllOrdersInWindow, "two in-window intents plus the prospective order")
	assert.Equal(t, 1, facts.ReduceOrdersInWindow, "the existing reduce row counts; the prospective open does not fold here")
	assert.Equal(t, 5, facts.MaxVersionWindow)
	assert.Equal(t, 6, facts.MaxOrdersPerWindow)
	assert.Equal(t, 7, facts.MaxReduceOrdersPerWindow)
	assert.True(t, facts.MarketCooldownElapsed)
	assert.Equal(t, 2, facts.ConsecutiveLosses)
	assert.Equal(t, 3, facts.MaxConsecutiveLosses)

	// A REDUCE candidate folds into the reduce channel only: closing adds no
	// new risk (07 §5.2), but it is still an order of the version.
	in.Action = candidate.ActionReduce
	facts, err = riskFrequencyFactsFromState(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, 1, facts.NewRiskIntentsInWindow, "a reduce does not add new risk")
	assert.Equal(t, 2, facts.VersionWindowCount)
	assert.Equal(t, 3, facts.AllOrdersInWindow)
	assert.Equal(t, 2, facts.ReduceOrdersInWindow, "the existing reduce row plus the prospective reduce")
}

// TestRiskFrequencyFactsRefusals pins the boundary: material that cannot
// support an honest window count is refused, not defaulted.
func TestRiskFrequencyFactsRefusals(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	valid := func() *RiskFrequencyFactsInput {
		return &RiskFrequencyFactsInput{
			AccountID: "acct", StrategyVersionID: "ver-a", Action: candidate.ActionOpenLong,
			Now: providerAdminNow, WindowMinutes: 300, MarketCooldownElapsed: true,
		}
	}

	_, err := riskFrequencyFactsFromState(ctx, nil)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	cases := map[string]func(*RiskFrequencyFactsInput){
		"account missing":        func(in *RiskFrequencyFactsInput) { in.AccountID = "  " },
		"version missing":        func(in *RiskFrequencyFactsInput) { in.StrategyVersionID = "" },
		"as-of missing":          func(in *RiskFrequencyFactsInput) { in.Now = time.Time{} },
		"window missing":         func(in *RiskFrequencyFactsInput) { in.WindowMinutes = 0 },
		"hold has no semantics":  func(in *RiskFrequencyFactsInput) { in.Action = candidate.ActionHold },
		"negative version limit": func(in *RiskFrequencyFactsInput) { in.MaxVersionWindow = -1 },
		"negative orders limit":  func(in *RiskFrequencyFactsInput) { in.MaxOrdersPerWindow = -1 },
		"negative reduce limit":  func(in *RiskFrequencyFactsInput) { in.MaxReduceOrdersPerWindow = -1 },
		"negative losses":        func(in *RiskFrequencyFactsInput) { in.ConsecutiveLosses = -1 },
		"negative loss limit":    func(in *RiskFrequencyFactsInput) { in.MaxConsecutiveLosses = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := valid()
			mutate(in)
			_, err := riskFrequencyFactsFromState(ctx, in)
			require.ErrorIs(t, err, ErrRiskFactsInput)
		})
	}
}
