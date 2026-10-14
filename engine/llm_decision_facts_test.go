package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// trigger/preview 的 per-run 事实闭包测试：请求从活状态组装（冻结策略文本、
// 授权窗口、配对世代、门禁事实），未装配的部件显式报错。

func seedFrozenVersion(t *testing.T, versionID string) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{
		ID: "strategy-live", Name: "trend", CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	frozen := candidate.ReviewablePolicy{
		SchemaVersion: "reviewable-v1", State: "READY_FOR_APPROVAL",
		AccountID: "acct-1", Network: "binance-testnet", ExecutionMode: LLMRunModeShadow,
		MarketScopes: []candidate.MarketScope{{
			MarketID: testMarketID("BTC"), MarketType: "PERP",
			Actions: []candidate.Action{candidate.ActionOpenLong, candidate.ActionReduce},
		}},
		ValidFrom:  providerAdminNow,
		ValidUntil: providerAdminNow.Add(24 * time.Hour),
		Risk: candidate.RiskEnvelope{
			MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
			MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"),
			MaxDailyLossUSD: new("20"), MaxSlippageBPS: new(20),
			MaxLeverage: new(1), MaxConcurrentPositions: 1, RequiresProtectiveExit: true,
		},
		LLMRevisionID: "rev-llm", JevRevisionID: "rev-jev", PairEpoch: 1,
	}
	require.NoError(t, frozen.Validate())
	frozenJSON, err := json.Marshal(&frozen)
	require.NoError(t, err)
	frozenText := string(frozenJSON)
	proposalJSON := `{"objective":"低频趋势跟随：回调做多","marketScopes":[{"marketId":"` + testMarketID("BTC") + `","marketType":"PERP","actions":["OPEN_LONG","REDUCE"]}],"risk":{"maxOrderNotionalUsd":"20","maxTotalExposureUsd":"100","maxDailyLossUsd":"20"}}`
	frozenAt := providerAdminNow
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: versionID, StrategyID: "strategy-live", State: strategyStateFrozen,
		SourcePromptSha256: "aa", ProposalJSON: &proposalJSON,
		FrozenJSON: &frozenText, FrozenAt: &frozenAt, CreatedAt: providerAdminNow,
	}))
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-trigger", StrategyVersionID: versionID, AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: LLMRunModeShadow, ApprovedMarketIDs: `["` + testMarketID("BTC") + `"]`,
		RiskEnvelopeJSON: `{"maxNotionalUsd":"20"}`, RiskEnvelopeHash: "env-hash",
		LLMProfileRevisionID: "rev-llm", JevProfileRevisionID: "rev-jev",
		ValidFrom: providerAdminNow, ValidUntil: providerAdminNow.Add(12 * time.Hour),
		ApprovedBy: "op", ApprovedAt: providerAdminNow,
	}))
}

func triggerTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Config{}
	cfg.LLM = config.LLMConfig{
		RunMode: LLMRunModeShadow, Network: "binance-testnet",
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
		ProbeMaxAgeSeconds: 600,
	}
	cfg.LLM.Indicators.DefaultInterval = "1h"
	cfg.LLM.Budget.LLMReservationCap24h = 5
	bot := &Engine{Config: &cfg, llmManager: &LLMManager{}}
	bot.llmReadiness = &LLMReadinessService{
		Facts: &LLMRuntimeFacts{
			Config:   func() *config.LLMConfig { return &bot.Config.LLM },
			Rotation: func() []string { return []string{testMarketID("BTC")} },
			Switches: func() map[string]bool { return (&RiskSwitchService{}).CurrentState() },
			Now:      func() time.Time { return providerAdminNow },
		},
		Now: func() time.Time { return providerAdminNow },
	}
	return bot
}

func TestBuildShadowRunRequestFromLiveState(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	_, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)
	probeFixture(t, "rev-llm", true, providerAdminNow)
	seedFrozenVersion(t, "version-live")

	bot := triggerTestEngine(t)
	request, err := bot.buildShadowRunRequest(ctx, "version-live", testMarketID("BTC"))
	require.NoError(t, err)
	assert.Equal(t, "低频趋势跟随：回调做多", request.StrategyInstruction, "the reviewed strategy text drives the analyst prompt")
	assert.Equal(t, "acct-1", request.AccountID)
	assert.Equal(t, "rev-llm", request.ProfileRevisionID)
	assert.Equal(t, "rev-jev", request.JevRevisionID)
	assert.Equal(t, "model-a", request.LLMModel)
	assert.Equal(t, "jev-1.0.0", request.JevModel)
	assert.Equal(t, int64(1), request.PairEpoch)
	assert.Equal(t, "env-hash", request.EnvelopeHash)
	assert.True(t, request.PreGates.BudgetAvailable, "no reservations against a cap of five")
	assert.True(t, request.PreGates.ProbePassed, "the seeded LLM probe passed")
	assert.False(t, request.PreGates.Halted)
	assert.Equal(t, strategyStateFrozen, request.PreGates.StrategyState,
		"production versions stay frozen: the approval row is the approved signal (M4-05)")

	// Assembly needs the venue; without an exchange manager it fails closed
	// instead of pretending the run happened.
	_, err = request.Assemble(ctx)
	require.Error(t, err, "no exchange manager must fail assembly")
	// The judge request needs the assembled snapshot first.
	_, err = request.BuildJev(ctx, &AnalysisOutcome{})
	require.Error(t, err)
	// The hard-risk closure and the PAPER/LIVE bridge are wired: the run
	// evaluates at the awaiting gate and (in executing modes) submits.
	assert.NotNil(t, request.RiskFacts, "the risk closure is the production composer")
	assert.NotNil(t, request.Execute, "the execution bridge belongs to the request, not the runner")
}

func TestBuildShadowRunRequestRefusesUnfrozenAndUnknown(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	_, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	bot := triggerTestEngine(t)
	_, err := bot.buildShadowRunRequest(ctx, "version-missing", testMarketID("BTC"))
	require.Error(t, err)

	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{ID: "s2", Name: "n", CreatedAt: providerAdminNow, CreatedBy: "op"}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "version-unfrozen", StrategyID: "s2", State: "DRAFT", SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))
	_, err = bot.buildShadowRunRequest(ctx, "version-unfrozen", testMarketID("BTC"))
	require.ErrorIs(t, err, ErrStrategyNotFrozen)

	_, err = bot.buildShadowRunRequest(ctx, "version-unfrozen", "Kraken:spot:BTC_USD")
	require.Error(t, err, "a market outside the configured venue must be refused")
}
