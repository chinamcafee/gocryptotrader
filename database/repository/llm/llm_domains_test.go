package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/database"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
)

var baseTime = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func atMinutes(m int) time.Time { return baseTime.Add(time.Duration(m) * time.Minute) }

func TestChatAppendAssignsSeqAndMaintainsCounts(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertConversation(ctx, &Conversation{
		ID: "conv-1", Title: "risk check", Kind: "chat",
		CreatedAt: baseTime, UpdatedAt: baseTime, MessageCount: 0,
	}))

	seq, err := AppendChatMessage(ctx, &ChatMessage{
		ID: "msg-1", ConversationID: "conv-1", Role: "user",
		ContentCipher: []byte{1, 2, 3}, ContentNonce: []byte{4, 5},
		ContentSha256: "aa", CharCount: 10, CreatedAt: atMinutes(1),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), seq)

	seq, err = AppendChatMessage(ctx, &ChatMessage{
		ID: "msg-2", ConversationID: "conv-1", Role: "assistant",
		ContentSha256: "bb", CharCount: 20, CreatedAt: atMinutes(2),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), seq)

	conv, err := GetConversation(ctx, "conv-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), conv.MessageCount)
	assert.True(t, atMinutes(2).Equal(conv.UpdatedAt))

	messages, err := ListChatMessages(ctx, "conv-1", 0, 0)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, "msg-1", messages[0].ID)
	assert.Equal(t, "msg-2", messages[1].ID)
	assert.Equal(t, []byte{1, 2, 3}, messages[0].ContentCipher)
	assert.Nil(t, messages[1].ContentCipher)
	assert.True(t, atMinutes(1).Equal(messages[0].CreatedAt))

	messages, err = ListChatMessages(ctx, "conv-1", 1, 1)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "msg-2", messages[0].ID)
}

func TestStrategyFreezeIsFirstWriteOnly(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertStrategy(ctx, &Strategy{ID: "start-1", Name: "trend", CreatedAt: baseTime, CreatedBy: "op"}))
	require.NoError(t, InsertStrategyVersion(ctx, &StrategyVersion{
		ID: "ver-1", StrategyID: "start-1", State: "PROPOSED",
		SourcePromptCipher: []byte{9}, SourcePromptSha256: "cc",
		CreatedAt: baseTime,
	}))

	require.NoError(t, FreezeStrategyVersion(ctx, "ver-1", `{"entry":"x"}`, "hash-1", "env-1", atMinutes(1)))
	version, err := GetStrategyVersion(ctx, "ver-1")
	require.NoError(t, err)
	require.NotNil(t, version.FrozenJSON)
	assert.JSONEq(t, `{"entry":"x"}`, *version.FrozenJSON)
	require.NotNil(t, version.FrozenAt)

	// The database trigger must reject a second freeze.
	err = FreezeStrategyVersion(ctx, "ver-1", `{"entry":"y"}`, "hash-2", "env-2", atMinutes(2))
	assert.Error(t, err, "second freeze should be rejected by the immutability trigger")

	// State machine columns remain writable after freeze.
	require.NoError(t, UpdateStrategyVersionState(ctx, "ver-1", "APPROVED"))
	require.NoError(t, InsertStrategyStateTransition(ctx, &StrategyStateTransition{
		ID: "trans-1", StrategyVersionID: "ver-1", FromState: "PROPOSED", ToState: "APPROVED",
		Reason: "operator approved", Actor: "op", CreatedAt: atMinutes(3),
	}))
	transitions, err := ListStrategyStateTransitions(ctx, "ver-1")
	require.NoError(t, err)
	require.Len(t, transitions, 1)
}

func TestApprovalRevokeAndColumnGuard(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertStrategyApproval(ctx, &StrategyApproval{
		ID: "appr-1", StrategyVersionID: "ver-1", AccountID: "acct", Network: "testnet",
		ExecutionMode: "paper", ApprovedMarketIDs: `["BTC_USDT"]`,
		RiskEnvelopeJSON: `{}`, RiskEnvelopeHash: "env",
		LLMProfileRevisionID: "rev-llm", JevProfileRevisionID: "rev-jev",
		ValidFrom: baseTime, ValidUntil: atMinutes(60), ApprovedBy: "op", ApprovedAt: baseTime,
	}))

	require.NoError(t, RevokeStrategyApproval(ctx, "appr-1", atMinutes(5), "op", "changed mind"))
	approval, err := GetStrategyApprovalByVersion(ctx, "ver-1")
	require.NoError(t, err)
	require.NotNil(t, approval.RevokedAt)
	assert.Equal(t, "op", *approval.RevokedBy)

	// Attempting to touch a protected column must be rejected by the trigger.
	_, err = modelSQLite.LLMStrategyApprovals(qm.Where("id = ?", "appr-1")).
		UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"execution_mode": "live"})
	assert.Error(t, err, "modifying execution_mode after insert should be rejected")
}

func TestDecisionRoundtrip(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertMarketSnapshot(ctx, &MarketSnapshot{
		ID: "snap-1", Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		SourceTime: baseTime, BuiltAt: atMinutes(1), FreshnessMS: 1500,
		QualityJSON: "{}", CandleWindowJSON: "[]", IndicatorJSON: "{}",
		TickerJSON: "{}", OrderbookJSON: "{}", FundingJSON: "{}", AccountJSON: "{}",
		TotalBytes: 4096, ContentSha256: "dd",
	}))
	snapshot, err := GetMarketSnapshot(ctx, "snap-1")
	require.NoError(t, err)
	assert.Equal(t, "BTC_USDT", snapshot.Pair)
	assert.True(t, baseTime.Equal(snapshot.SourceTime))
	assert.Equal(t, int64(4096), snapshot.TotalBytes)

	llmRev := "rev-llm"
	require.NoError(t, InsertDecisionRun(ctx, &DecisionRun{
		ID: "run-1", StrategyVersionID: new("ver-1"), SnapshotID: new("snap-1"),
		Trigger: "MANUAL", RunMode: "PAPER", LLMProfileRevisionID: llmRev,
		LLMModel: "model-a", PromptVersion: "p1", Decision: "HOLD", Reason: "nothing to do",
		InputHash: "ee", CreatedAt: atMinutes(2),
	}))
	run, err := GetDecisionRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "HOLD", run.Decision)
	assert.Nil(t, run.DeadlineAt)

	runs, err := ListDecisionRunsByStrategy(ctx, "ver-1", 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)

	require.NoError(t, InsertRiskEvaluation(ctx, &RiskEvaluation{
		ID: "risk-1", DecisionRunID: "run-1", CheckVersion: "v1", Verdict: "REJECT",
		ChecksJSON: "{}", EnvelopeHash: "env", EvaluatedAt: atMinutes(3),
	}))
	evals, err := ListRiskEvaluationsByRun(ctx, "run-1")
	require.NoError(t, err)
	require.Len(t, evals, 1)
	assert.Equal(t, "REJECT", evals[0].Verdict)
	assert.Nil(t, evals[0].RejectionsJSON)
}

func TestOrderIntentAndExitLifecycle(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertOrderIntent(ctx, &OrderIntent{
		ID: "intent-1", DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
		StrategyVersionID: "ver-1", ApprovalID: "appr-1",
		Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		Action: "OPEN", Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
		ClientOrderID: "gct-1", NotionalUSD: "500.00", EnvelopeHash: "env",
		Status: "PREPARED", ExpiresAt: atMinutes(30), CreatedAt: baseTime, UpdatedAt: baseTime,
	}))

	require.NoError(t, UpdateOrderIntentLifecycle(ctx, "intent-1", "SUBMITTED", new("int-1"), new("ext-1"), atMinutes(1)))
	intent, err := GetOrderIntent(ctx, "intent-1")
	require.NoError(t, err)
	assert.Equal(t, "SUBMITTED", intent.Status)
	assert.Equal(t, "int-1", *intent.InternalOrderID)
	assert.Equal(t, "ext-1", *intent.ExchangeOrderID)
	assert.True(t, atMinutes(1).Equal(intent.UpdatedAt))

	// The trigger must reject changes outside the lifecycle columns.
	_, err = modelSQLite.LLMOrderIntents(qm.Where("id = ?", "intent-1")).
		UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"quantity": "999"})
	assert.Error(t, err, "modifying quantity after insert should be rejected")

	require.NoError(t, InsertProtectiveExit(ctx, &ProtectiveExit{
		ID: "exit-1", ParentIntentID: "intent-1", ExitKind: "STOP_LOSS",
		ClientOrderID: "gct-sl-1", Quantity: "0.01", TriggerPrice: "48000",
		Status: "ARMED", CreatedAt: baseTime, UpdatedAt: baseTime,
	}))
	require.NoError(t, UpdateProtectiveExitStatus(ctx, "exit-1", "TRIGGERED_FAIL_SAFE", true, nil, nil, atMinutes(2)))
	exits, err := ListProtectiveExitsByParent(ctx, "intent-1")
	require.NoError(t, err)
	require.Len(t, exits, 1)
	assert.Equal(t, "TRIGGERED_FAIL_SAFE", exits[0].Status)
	assert.True(t, exits[0].FailSafeTriggered)
}

func TestBudgetWindowAndNewRiskCounters(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertBudgetReservation(ctx, &BudgetReservation{
		ID: "res-1", ProfileRevisionID: "rev-1", Kind: "llm", Purpose: "DECISION",
		Status: "RESERVED", ReservedAt: atMinutes(0), ExpiresAt: atMinutes(5),
	}))
	require.NoError(t, InsertBudgetReservation(ctx, &BudgetReservation{
		ID: "res-2", ProfileRevisionID: "rev-1", Kind: "llm", Purpose: "DECISION",
		Status: "RESERVED", ReservedAt: atMinutes(3), ExpiresAt: atMinutes(8),
	}))
	require.NoError(t, InsertBudgetReservation(ctx, &BudgetReservation{
		ID: "res-3", ProfileRevisionID: "rev-1", Kind: "llm", Purpose: "DECISION",
		Status: "RESERVED", ReservedAt: atMinutes(-30), ExpiresAt: atMinutes(-25),
	}))

	window, err := ListReservationsInWindow(ctx, "rev-1", atMinutes(-10))
	require.NoError(t, err)
	require.Len(t, window, 2, "the -30m reservation must fall outside the window")

	require.NoError(t, SettleBudgetReservation(ctx, "res-1", "COMPLETED", atMinutes(1), new(int64(120)), new(int64(60)), new(int64(900))))
	settled, err := GetBudgetReservation(ctx, "res-1")
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", settled.Status)
	require.NotNil(t, settled.InputTokens)
	assert.Equal(t, int64(120), *settled.InputTokens)

	require.NoError(t, InsertNewRiskCounter(ctx, &NewRiskCounter{
		ID: "nrc-1", AccountID: "acct", Scope: "MARKET", ScopeID: "BTC_USDT",
		IntentID: "intent-1", CountedAt: atMinutes(0), WindowMinutes: 60,
	}))
	require.NoError(t, InsertNewRiskCounter(ctx, &NewRiskCounter{
		ID: "nrc-2", AccountID: "acct", Scope: "MARKET", ScopeID: "ETH_USDT",
		IntentID: "intent-2", CountedAt: atMinutes(10), WindowMinutes: 60,
	}))
	count, err := CountNewRiskInWindow(ctx, "acct", atMinutes(-30))
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	require.NoError(t, ReleaseNewRiskCounter(ctx, "nrc-1", atMinutes(11)))
	count, err = CountNewRiskInWindow(ctx, "acct", atMinutes(-30))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "released counters should drop out of the window")
}

func TestHaltsAndReconcile(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertHalt(ctx, &Halt{
		ID: "halt-1", Level: "GLOBAL", Reason: "manual stop", SetBy: "op",
		SetAt: baseTime, AffectedStrategies: 2, CanceledOrders: 3,
	}))
	require.NoError(t, InsertHalt(ctx, &Halt{
		ID: "halt-2", Level: "MARKET", MarketID: new("BTC_USDT"), Reason: "volatility", SetBy: "op",
		SetAt: atMinutes(1),
	}))

	halts, err := ListActiveHalts(ctx)
	require.NoError(t, err)
	require.Len(t, halts, 2)

	require.NoError(t, ClearHalt(ctx, "halt-1", atMinutes(5), "op", "resolved"))
	halts, err = ListActiveHalts(ctx)
	require.NoError(t, err)
	require.Len(t, halts, 1)
	assert.Equal(t, "halt-2", halts[0].ID)
	require.NotNil(t, halts[0].MarketID)
	assert.Equal(t, "BTC_USDT", *halts[0].MarketID)

	require.NoError(t, InsertDayStartInventory(ctx, &DayStartInventory{
		ID: "dsi-1", AccountID: "acct", UtcDay: "2026-10-10",
		EquityUsdt: "1000.00", BalanceUsdt: "900.00", UnrealisedPNL: "100.00",
		PositionsJSON: "[]", Source: "TESTNET", ObservedAt: baseTime, SealedAt: atMinutes(1),
	}))
	dsi, err := GetDayStartInventory(ctx, "acct", "2026-10-10")
	require.NoError(t, err)
	assert.Equal(t, "1000.00", dsi.EquityUsdt)

	require.NoError(t, InsertExternalFlow(ctx, &ExternalFlow{
		ID: "flow-1", AccountID: "acct", UtcDay: "2026-10-10", Direction: "IN",
		AmountUsdt: "50.00", Source: "TRANSFER", ObservedAt: atMinutes(2),
	}))
	flows, err := ListExternalFlows(ctx, "acct", "2026-10-10")
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.Equal(t, "50.00", flows[0].AmountUsdt)

	require.NoError(t, InsertReconciliation(ctx, &Reconciliation{
		ID: "rec-1", AccountID: "acct", StartedAt: baseTime, Status: "RUNNING",
		LocalPositionsJSON: "[]", ExchangePositionsJSON: "[]",
		LocalOrdersJSON: "[]", ExchangeOrdersJSON: "[]",
	}))
	require.NoError(t, CompleteReconciliation(ctx, "rec-1", "CLEAN", new("[]"), true, new(atMinutes(15)), atMinutes(3)))
	latest, err := LatestReconciliation(ctx)
	require.NoError(t, err)
	assert.Equal(t, "rec-1", latest.ID)
	assert.True(t, latest.Reconciled)
	require.NotNil(t, latest.CompletedAt)
	require.NotNil(t, latest.ValidUntil)
	assert.True(t, latest.ValidUntil.Equal(atMinutes(15)))
	require.NotNil(t, latest.DeltasJSON)
	assert.Equal(t, "[]", *latest.DeltasJSON)

	// A second, older run must not shadow the newest one.
	require.NoError(t, InsertReconciliation(ctx, &Reconciliation{
		ID: "rec-0", AccountID: "acct", StartedAt: baseTime.Add(-time.Hour), Status: "CLEAN",
		LocalPositionsJSON: "[]", ExchangePositionsJSON: "[]",
		LocalOrdersJSON: "[]", ExchangeOrdersJSON: "[]",
	}))
	latest, err = LatestReconciliation(ctx)
	require.NoError(t, err)
	assert.Equal(t, "rec-1", latest.ID)
}

func TestAuditEvents(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertAuditEvent(ctx, &AuditEvent{
		ID: "evt-1", Kind: "APPROVAL", Actor: "op", Subject: new("ver-1"),
		DetailJSON: `{"action":"approve"}`, CreatedAt: baseTime,
	}))
	require.NoError(t, InsertAuditEvent(ctx, &AuditEvent{
		ID: "evt-2", Kind: "HALT", Actor: "op",
		DetailJSON: `{"action":"halt"}`, CreatedAt: atMinutes(1),
	}))

	events, err := ListAuditEvents(ctx, "", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "evt-2", events[0].ID, "newest first")

	events, err = ListAuditEvents(ctx, "APPROVAL", 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "evt-1", events[0].ID)
	require.NotNil(t, events[0].Subject)
	assert.Equal(t, "ver-1", *events[0].Subject)
}

func TestMarketGrantAndActiveApprovalReads(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	require.NoError(t, InsertMarketTradeGrant(ctx, &MarketTradeGrant{
		ID: "grant-1", AccountID: "acct-1", Network: "binance-testnet",
		MarketID:  "Binance:usdtmarginedfutures:BTC_USDT",
		GrantedBy: "ops-1", GrantedAt: baseTime, ValidUntil: atMinutes(60 * 24 * 7),
	}))
	grant, err := GetMarketTradeGrant(ctx, "grant-1")
	require.NoError(t, err)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", grant.MarketID)
	assert.True(t, baseTime.Equal(grant.GrantedAt))
	assert.Nil(t, grant.RevokedAt)

	require.NoError(t, RevokeMarketTradeGrant(ctx, "grant-1", atMinutes(5), "ops-2", "granted by mistake"))
	grant, err = GetMarketTradeGrant(ctx, "grant-1")
	require.NoError(t, err)
	require.NotNil(t, grant.RevokedAt)
	assert.True(t, atMinutes(5).Equal(*grant.RevokedAt))
	require.NotNil(t, grant.RevokeReason)
	assert.Equal(t, "granted by mistake", *grant.RevokeReason)

	approval := StrategyApproval{
		StrategyVersionID: "ver-1", AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", ApprovedMarketIDs: `["Binance:usdtmarginedfutures:BTC_USDT"]`,
		RiskEnvelopeJSON: `{}`, RiskEnvelopeHash: "env",
		LLMProfileRevisionID: "rev-llm", JevProfileRevisionID: "rev-jev",
		ValidFrom: baseTime, ValidUntil: atMinutes(60), ApprovedBy: "ops-1", ApprovedAt: baseTime,
	}
	active := approval
	active.ID = "appr-active"
	require.NoError(t, InsertStrategyApproval(ctx, &active))

	// One version carries at most one approval (UNIQUE(strategy_version_id),
	// docs 07 §1), so each fixture approval names its own version.
	revoked := approval
	revoked.ID = "appr-revoked"
	revoked.StrategyVersionID = "ver-2"
	require.NoError(t, InsertStrategyApproval(ctx, &revoked))
	require.NoError(t, RevokeStrategyApproval(ctx, "appr-revoked", atMinutes(1), "ops-1", "revoked"))

	expired := approval
	expired.ID = "appr-expired"
	expired.StrategyVersionID = "ver-3"
	require.NoError(t, InsertStrategyApproval(ctx, &expired))
	require.NoError(t, ExpireStrategyApproval(ctx, "appr-expired", atMinutes(2)))

	list, err := ListActiveStrategyApprovals(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "revoked and expired approvals stay out of the cascade read")
	assert.Equal(t, "appr-active", list[0].ID)
	assert.Equal(t, `["Binance:usdtmarginedfutures:BTC_USDT"]`, list[0].ApprovedMarketIDs,
		"the market list round-trips in the canonical spelling")
	assert.True(t, baseTime.Equal(list[0].ApprovedAt))
}

// TestCountOrderIntentsInWindow covers the frequency face's sliding-window
// count: rows from the since instant on, optionally per version and per action.
func TestCountOrderIntentsInWindow(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	seed := func(id, version, action string, at time.Time) {
		t.Helper()
		require.NoError(t, InsertOrderIntent(ctx, &OrderIntent{
			ID: id, DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
			StrategyVersionID: version, ApprovalID: "appr-1",
			Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
			Action: action, Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
			ClientOrderID: "gct-" + id, NotionalUSD: "500.00", EnvelopeHash: "env",
			Status: "PREPARED", ExpiresAt: atMinutes(60), CreatedAt: at, UpdatedAt: at,
		}))
	}
	seed("i1", "ver-1", "OPEN_LONG", baseTime)
	seed("i2", "ver-1", "REDUCE", atMinutes(10))
	seed("i3", "ver-2", "OPEN_LONG", atMinutes(20))
	seed("i4", "ver-1", "OPEN_LONG", baseTime.Add(-time.Minute)) // before the window

	count, err := CountOrderIntentsInWindow(ctx, "", "", baseTime)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count, "a row created before the window does not count")

	count, err = CountOrderIntentsInWindow(ctx, "ver-1", "", baseTime)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = CountOrderIntentsInWindow(ctx, "ver-1", "OPEN_LONG", baseTime)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = CountOrderIntentsInWindow(ctx, "", "REDUCE", baseTime)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestLastEntryIntentAt covers the market-cooldown data read: the latest
// ENTRY intent (opens only) for one market, and the "never entered" answer.
func TestLastEntryIntentAt(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()
	seed := func(id, exchange, pair, asset, action string, at time.Time) {
		t.Helper()
		require.NoError(t, InsertOrderIntent(ctx, &OrderIntent{
			ID: id, DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
			StrategyVersionID: "ver-1", ApprovalID: "appr-1",
			Exchange: exchange, Pair: pair, Asset: asset,
			Action: action, Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
			ClientOrderID: "gct-" + id, NotionalUSD: "500.00", EnvelopeHash: "env",
			Status: "PREPARED", ExpiresAt: atMinutes(60), CreatedAt: at, UpdatedAt: at,
		}))
	}
	seed("i1", "Binance", "BTC_USDT", "usdtmarginedfutures", "OPEN_LONG", baseTime.Add(-10*time.Minute))
	seed("i2", "Binance", "BTC_USDT", "usdtmarginedfutures", "REDUCE", atMinutes(-1)) // reduces are not entries
	seed("i3", "Binance", "BTC_USDT", "usdtmarginedfutures", "OPEN_SHORT", atMinutes(-5))
	seed("i4", "Kraken", "BTC_USDT", "usdtmarginedfutures", "OPEN_LONG", atMinutes(0))  // another venue
	seed("i5", "Binance", "ETH_USDT", "usdtmarginedfutures", "OPEN_LONG", atMinutes(0)) // another market

	at, ok, err := LastEntryIntentAt(ctx, "Binance", "BTC_USDT", "usdtmarginedfutures")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, baseTime.Add(-5*time.Minute).Equal(at), "the latest open for the market, not the reduce")

	_, ok, err = LastEntryIntentAt(ctx, "Binance", "SOL_USDT", "usdtmarginedfutures")
	require.NoError(t, err)
	assert.False(t, ok, "a market never entered is past any cooldown")
}

// TestCountOrderIntentsByStatus covers the lifecycle-state count behind the
// risk engine's unresolved-count facts.
func TestCountOrderIntentsByStatus(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()
	seed := func(id, status string) {
		t.Helper()
		require.NoError(t, InsertOrderIntent(ctx, &OrderIntent{
			ID: id, DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
			StrategyVersionID: "ver-1", ApprovalID: "appr-1",
			Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
			Action: "OPEN_LONG", Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
			ClientOrderID: "gct-" + id, NotionalUSD: "500.00", EnvelopeHash: "env",
			Status: status, ExpiresAt: atMinutes(60), CreatedAt: baseTime, UpdatedAt: baseTime,
		}))
	}
	seed("i1", "UNKNOWN")
	seed("i2", "UNKNOWN")
	seed("i3", "PREPARED")

	count, err := CountOrderIntentsByStatus(ctx, "UNKNOWN")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = CountOrderIntentsByStatus(ctx, "PARTIALLY_FILLED")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "an absent status counts zero, not everything")
}
