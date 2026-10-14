package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// TestLLMSetRunModeRPC covers the 08 §3.6 run-control member added after the
// session-52 reconnaissance (同段尾九): re-authentication, the LIVE triple
// unlock read server-side, the live apply, and the refusals that leave the
// mode untouched.
func TestLLMSetRunModeRPC(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.RemoteControl.Password = "s3cret"
	cfg.LLM.AccountID = "acct-1"
	cfg.LLM.Network = "binance-testnet"
	manager := &LLMManager{}
	manager.SetRunMode(LLMRunModeShadow)
	server := &RPCServer{Engine: &Engine{Config: cfg, llmManager: manager}}

	// SHADOW -> PAPER: re-authentication passes; the live field and the config
	// struct both move, and the switch leaves an audit row.
	resp, err := server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: LLMRunModePaper, Password: "s3cret", Actor: "op-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.GetStatus())
	assert.Equal(t, LLMRunModePaper, manager.EffectiveRunMode())
	assert.Equal(t, LLMRunModePaper, cfg.LLM.RunMode)
	events, err := llmstore.ListAuditEvents(ctx, "MODE_SWITCHED", 0)
	require.NoError(t, err)
	require.Len(t, events, 1, "every switch leaves an audit row")
	assert.Equal(t, "op-1", events[0].Actor)

	// Wrong password: re-authentication refuses and nothing moves.
	_, err = server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: LLMRunModeShadow, Password: "wrong", Actor: "op-1",
	})
	require.ErrorIs(t, err, ErrModeUnauthenticated)
	assert.Equal(t, LLMRunModePaper, manager.EffectiveRunMode())

	// Unknown target: the switch command is refused.
	_, err = server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: "TURBO", Password: "s3cret", Actor: "op-1",
	})
	require.ErrorIs(t, err, ErrModeSwitchInvalid)

	// LIVE without a binding approval: the triple unlock fails closed.
	_, err = server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: LLMRunModeLive, Password: "s3cret", Actor: "op-1", Acknowledged: LiveAcknowledgment,
	})
	require.ErrorIs(t, err, ErrLiveNotUnlocked)
	assert.Equal(t, LLMRunModePaper, manager.EffectiveRunMode())

	// A LIVE approval for this account carries the unlock material.
	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{
		ID: "s-live", Name: "live", CreatedAt: providerAdminNow, CreatedBy: "op-1",
	}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "ver-live", StrategyID: "s-live", State: StrategyStateDraft, SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))
	envelope := candidate.RiskEnvelope{
		MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"), MaxDailyLossUSD: new("20"),
	}
	raw, err := json.Marshal(&envelope)
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-live", StrategyVersionID: "ver-live", AccountID: "acct-1", Network: "binance-mainnet",
		ExecutionMode: LLMRunModeLive, ApprovedMarketIDs: `[]`,
		RiskEnvelopeJSON: string(raw), RiskEnvelopeHash: "h", PairEpoch: 1,
		ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
	}))

	// The wrong phrase is refused: nothing moves.
	_, err = server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: LLMRunModeLive, Password: "s3cret", Actor: "op-1", Acknowledged: "sure",
	})
	require.ErrorIs(t, err, ErrLiveNotUnlocked)
	assert.Equal(t, LLMRunModePaper, manager.EffectiveRunMode())

	// The full material passes and the switch lands.
	resp, err = server.LLMSetRunMode(ctx, &gctrpc.LLMSetRunModeRequest{
		RunMode: LLMRunModeLive, Password: "s3cret", Actor: "op-1", Acknowledged: LiveAcknowledgment,
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.GetStatus())
	assert.Equal(t, LLMRunModeLive, manager.EffectiveRunMode())
	assert.Equal(t, LLMRunModeLive, cfg.LLM.RunMode)
}
