package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestMarketDelistWatch covers the M2-13 tail (and M6-02 #18) on a real
// database: a covered market disappearing from the enabled list revokes its
// grants, cascades the approvals, pauses the related frozen versions, audits
// and notifies -- exactly once, and only for the market that left.
func TestMarketDelistWatch(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	server := &RPCServer{Engine: &Engine{Config: &config.Config{}, llmManager: &LLMManager{}}}

	const (
		marketA = "BTC_USDT"
		marketB = "ETH_USDT"
		marketC = "SOL_USDT"
	)
	makeRunnable := func(name, market string) string {
		t.Helper()
		created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: name, Actor: "op-1"})
		require.NoError(t, err)
		saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
			StrategyId: created.GetStrategy().GetId(), ProposalJson: completeProposalJSON(), Actor: "op-1",
		})
		require.NoError(t, err)
		versionID := saved.GetVersion().GetId()
		require.NoError(t, llmstore.UpdateStrategyVersionState(ctx, versionID, strategyStateFrozen))
		now := time.Now()
		markets, err := EncodeApprovedMarkets([]string{market})
		require.NoError(t, err)
		require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
			ID: "appr-" + market, StrategyVersionID: versionID, AccountID: "acct-1", Network: "binance-testnet",
			ExecutionMode: "SHADOW", ApprovedMarketIDs: markets,
			RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-" + market, PairEpoch: 1,
			ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
		}))
		require.NoError(t, llmstore.InsertMarketTradeGrant(ctx, &llmstore.MarketTradeGrant{
			ID: "grant-" + market, AccountID: "acct-1", Network: "binance-testnet", MarketID: market,
			GrantedBy: "op-1", GrantedAt: now, ValidUntil: now.Add(24 * time.Hour),
		}))
		return versionID
	}
	versionA := makeRunnable("delist-A", marketA)
	versionB := makeRunnable("delist-B", marketB)

	pushed := make([]base.Event, 0, 2)
	notifier := &LLMNotifier{Push: func(evt base.Event) { pushed = append(pushed, evt) }}
	markets := []string{marketA, marketB}
	fixedNow := time.Now()
	watch := &MarketDelistWatcher{
		Markets: func() []string { return markets },
		Grants:  LLMMarketGrants{}, Versions: LLMPairSwitchStore{}, Audit: LLMDecisionStore{},
		Notify: notifier, AccountID: "acct-1", Network: "binance-testnet",
		MarketKey: testMarketKey,
		Now:       func() time.Time { return fixedNow },
	}

	// First check only primes the baseline: nothing "disappeared" yet.
	first, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, first.Checked)
	assert.Empty(t, first.Disappeared)
	grantA, err := llmstore.GetMarketTradeGrant(ctx, "grant-"+marketA)
	require.NoError(t, err)
	assert.Nil(t, grantA.RevokedAt, "priming should not revoke anything")
	assert.Empty(t, pushed)

	// Market A disappears: the full row-18 chain fires for A and nothing else.
	markets = []string{marketB}
	second, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{marketA}, second.Disappeared)
	assert.Equal(t, []string{"grant-" + marketA}, second.RevokedGrants)
	assert.Equal(t, []string{versionA}, second.PausedVersions)

	grantA, err = llmstore.GetMarketTradeGrant(ctx, "grant-"+marketA)
	require.NoError(t, err)
	require.NotNil(t, grantA.RevokedAt, "the delisted market's grant must be revoked")
	grantB, err := llmstore.GetMarketTradeGrant(ctx, "grant-"+marketB)
	require.NoError(t, err)
	assert.Nil(t, grantB.RevokedAt, "the surviving market's grant is untouched")

	activeApprovals, err := llmstore.ListActiveStrategyApprovals(ctx)
	require.NoError(t, err)
	require.Len(t, activeApprovals, 1, "only the surviving market's approval stays active")
	assert.Equal(t, "appr-"+marketB, activeApprovals[0].ID)

	versionAState, err := llmstore.GetStrategyVersion(ctx, versionA)
	require.NoError(t, err)
	assert.Equal(t, StrategyStatePaused, versionAState.State, "the related frozen version should be paused")
	versionBState, err := llmstore.GetStrategyVersion(ctx, versionB)
	require.NoError(t, err)
	assert.Equal(t, strategyStateFrozen, versionBState.State)

	events, err := llmstore.ListAuditEvents(ctx, AuditKindMarketDelisted, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, delistActor, events[0].Actor)
	assert.Contains(t, events[0].DetailJSON, marketA)

	require.Len(t, pushed, 1)
	assert.Equal(t, EventMarketDelisted, pushed[0].Type)
	assert.Contains(t, pushed[0].Message, marketA,
		"the notification should name the delisted market")

	// The response fires once: a follow-up check finds nothing new.
	third, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Empty(t, third.Disappeared)
	events, err = llmstore.ListAuditEvents(ctx, AuditKindMarketDelisted, 0)
	require.NoError(t, err)
	assert.Len(t, events, 1, "the response should fire exactly once per market")

	// A newly covered market joins the baseline: when it later disappears it
	// is caught with the same chain.
	markets = []string{marketB, marketC}
	fourth, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Empty(t, fourth.Disappeared, "a new market is an addition, not a delisting")
	markets = []string{marketB}
	fifth, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{marketC}, fifth.Disappeared)
}

// TestMarketDelistWatchUnifiesSpellings pins the seam normalisation: the
// rotator names the market in BASE_QUOTE while the stored grant and approval
// rows carry the three-segment form; comparing raw strings would leave the
// row-18 cascade silent.
func TestMarketDelistWatchUnifiesSpellings(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	server := &RPCServer{Engine: &Engine{Config: &config.Config{}, llmManager: &LLMManager{}}}

	const runMarket = "Binance:usdtmarginedfutures:BTC_USDT"
	created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: "delist-mixed", Actor: "op-1"})
	require.NoError(t, err)
	saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
		StrategyId: created.GetStrategy().GetId(), ProposalJson: completeProposalJSON(), Actor: "op-1",
	})
	require.NoError(t, err)
	versionID := saved.GetVersion().GetId()
	require.NoError(t, llmstore.UpdateStrategyVersionState(ctx, versionID, strategyStateFrozen))
	now := time.Now()
	markets, err := EncodeApprovedMarkets([]string{runMarket})
	require.NoError(t, err)
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-mixed", StrategyVersionID: versionID, AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", ApprovedMarketIDs: markets,
		RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-mixed", PairEpoch: 1,
		ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
	}))
	require.NoError(t, llmstore.InsertMarketTradeGrant(ctx, &llmstore.MarketTradeGrant{
		ID: "grant-mixed", AccountID: "acct-1", Network: "binance-testnet", MarketID: runMarket,
		GrantedBy: "op-1", GrantedAt: now, ValidUntil: now.Add(24 * time.Hour),
	}))

	listed := []string{"BTC_USDT"}
	watch := &MarketDelistWatcher{
		Markets: func() []string { return listed }, MarketKey: testMarketKey,
		Grants: LLMMarketGrants{}, Versions: LLMPairSwitchStore{}, Audit: LLMDecisionStore{},
		AccountID: "acct-1", Network: "binance-testnet", Now: func() time.Time { return now },
	}
	first, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Checked)

	listed = nil
	second, err := watch.Check(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"BTC_USDT"}, second.Disappeared, "the raw rotator spelling is what the operator sees")
	assert.Equal(t, []string{"grant-mixed"}, second.RevokedGrants)
	assert.Equal(t, []string{versionID}, second.PausedVersions)

	grant, err := llmstore.GetMarketTradeGrant(ctx, "grant-mixed")
	require.NoError(t, err)
	require.NotNil(t, grant.RevokedAt, "the cascade must match across the two spellings")
}

// TestSetupRotationAttachesTheDelistWatch covers the production wiring: a
// watcher pre-set on the manager (Engine.Start does this before Start) is
// attached by SetupRotation; without one the rotation carries no watch.
func TestSetupRotationAttachesTheDelistWatch(t *testing.T) {
	t.Parallel()

	cfg := &config.LLMConfig{Enabled: true, Exchange: "Binance", Asset: "usdtmarginedfutures"}
	cfg.Coverage.Enabled = true
	m, err := SetupLLMManager(llmExchangeManager{exch: &binance.Exchange{}}, cfg)
	require.NoError(t, err, "the manager must build")

	rotation, err := m.SetupRotation()
	require.NoError(t, err, "the rotator must build")
	assert.Nil(t, rotation.delist, "no pre-set watcher means no watch")

	watch := &MarketDelistWatcher{AccountID: "acct-1", Network: "net"}
	m.SetMarketWatch(watch)
	rotation, err = m.SetupRotation()
	require.NoError(t, err)
	require.NotNil(t, rotation.delist, "SetupRotation must attach the pre-set watcher")
	assert.NotNil(t, watch.Markets, "the watcher should read the rotator's own market list")

	// A nil watcher is a valid configuration and must not break the attach.
	rotation.attachDelistWatcher(nil)
	assert.NotNil(t, rotation.delist, "attaching nil leaves the existing watch alone")
}
