package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M6-01 生产事实源测试：把真实仓储里的配对/探针/停机/对账/日界/覆盖/审批读成
// 一份全 PASS 的准入报告；再把一条 FULL halt 塞进去，确认它按门失败。

func TestRuntimeFactsFeedAReadyReport(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	llmRevision, jevRevision := fixtureRevisions(t)
	probeFixture(t, llmRevision.ID, true, providerAdminNow)
	probeFixture(t, jevRevision.ID, true, providerAdminNow.Add(time.Minute))
	_, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID:  &llmRevision.ID,
		JevRevisionID:  &jevRevision.ID,
		ExpectedEpoch:  0,
		IdempotencyKey: "facts-ready",
		Actor:          "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	require.NoError(t, err)

	now := providerAdminNow.Add(3 * time.Minute)
	cfg := &config.LLMConfig{Network: "binance-testnet", AccountID: "acct-1", ProbeMaxAgeSeconds: 600}

	require.NoError(t, llmstore.InsertDayStartInventory(ctx, &llmstore.DayStartInventory{
		ID: "dsi-facts", AccountID: "acct-1", UtcDay: UtcDayKey(now),
		EquityUsdt: "1000", BalanceUsdt: "1000", UnrealisedPNL: "0",
		PositionsJSON: "[]", Source: "TESTNET", ObservedAt: now, SealedAt: now,
	}))
	require.NoError(t, llmstore.InsertReconciliation(ctx, &llmstore.Reconciliation{
		ID: "rec-facts", AccountID: "acct-1", StartedAt: now.Add(-time.Minute), Status: "CLEAN",
		LocalPositionsJSON: "[]", ExchangePositionsJSON: "[]",
		LocalOrdersJSON: "[]", ExchangeOrdersJSON: "[]",
	}))
	validUntil := now.Add(30 * time.Minute)
	require.NoError(t, llmstore.CompleteReconciliation(ctx, "rec-facts", "CLEAN", new("[]"), true, &validUntil, now))

	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{
		ID: "strategy-facts", Name: "facts", CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "ver-facts", StrategyID: "strategy-facts", State: "APPROVED", SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-facts", StrategyVersionID: "ver-facts", AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "testnet", ApprovedMarketIDs: `["BTC_USDT"]`, RiskEnvelopeJSON: `{"maxNotionalUsd":"1000"}`,
		RiskEnvelopeHash: "env", LLMProfileRevisionID: "rev-llm", JevProfileRevisionID: "rev-jev",
		ValidFrom: providerAdminNow, ValidUntil: providerAdminNow.Add(24 * time.Hour),
		ApprovedBy: "op", ApprovedAt: providerAdminNow,
	}))

	facts := &LLMRuntimeFacts{
		Config:   func() *config.LLMConfig { return cfg },
		Rotation: func() []string { return []string{"BTC_USDT", "ETH_USDT", "SOL_USDT"} },
		Switches: func() map[string]bool { return (&RiskSwitchService{}).CurrentState() },
		Now:      func() time.Time { return now },
	}
	service := &LLMReadinessService{Facts: facts, Now: func() time.Time { return now }}

	report, err := service.Evaluate(ctx, ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.NoError(t, err)
	assert.Truef(t, report.Ready, "gates: %+v", report.Gates)
	require.Len(t, report.Gates, 10)
	for _, gate := range report.Gates {
		assert.Equalf(t, ReadinessPass, gate.Status, "%s: %s", gate.Code, gate.Detail)
	}

	// A FULL halt must flip NO_FULL_HALT to FAIL on the next pass.
	require.NoError(t, llmstore.InsertHalt(ctx, &llmstore.Halt{
		ID: "halt-facts", Level: HaltLevelFull, Reason: "drill", SetBy: "op", SetAt: now,
	}))
	report, err = service.Evaluate(ctx, ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeTestnet})
	require.NoError(t, err)
	assert.False(t, report.Ready)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateNoFullHalt))
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGatePairReady))
}

// TestRuntimeFactsZeroStateFailsClosed pins the empty-bot behaviour: every
// unassembled fact source yields zero values, and the gates fail closed
// (except the two a non-trading mode is exempt from).
func TestRuntimeFactsZeroStateFailsClosed(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	now := providerAdminNow
	service := &LLMReadinessService{
		Facts: &LLMRuntimeFacts{Now: func() time.Time { return now }},
		Now:   func() time.Time { return now },
	}
	report, err := service.Evaluate(ctx, ReadinessRequest{Network: "binance-testnet", Mode: LLMRunModeShadow})
	require.NoError(t, err, "an empty bot is a fact set, not an error")
	assert.False(t, report.Ready)
	require.Len(t, report.Gates, 10)
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGatePairReady))
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateProbeFresh))
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateSwitchesIntact))
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateReconciled))
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateDaySealed))
	assert.Equal(t, ReadinessFail, readinessGateStatus(t, &report, ReadinessGateCoverage))
	// SHADOW is exempt from the venue gates: no network, no live approval.
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateNetwork))
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateApprovalLive))
	assert.Equal(t, ReadinessPass, readinessGateStatus(t, &report, ReadinessGateModePermitted))
}
