package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestLLMHaltRPCs covers the W7-01 write side on a real database: SOFT
// suspends the runnable version, MARKET without a canceller fails closed, and
// the clear demands re-authentication and resumes nothing.
func TestLLMHaltRPCs(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.RemoteControl.Password = "s3cret"
	server := &RPCServer{Engine: &Engine{Config: cfg, llmManager: &LLMManager{}}}

	// Seed a runnable version: FROZEN with a live approval.
	created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: "halt target", Actor: "op-1"})
	require.NoError(t, err)
	saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
		StrategyId: created.GetStrategy().GetId(), ProposalJson: completeProposalJSON(), Actor: "op-1",
	})
	require.NoError(t, err)
	versionID := saved.GetVersion().GetId()
	require.NoError(t, llmstore.UpdateStrategyVersionState(ctx, versionID, strategyStateFrozen))
	now := time.Now()
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-h1", StrategyVersionID: versionID, AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", ApprovedMarketIDs: `["` + policyTestMarket + `"]`,
		RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-1", PairEpoch: 1,
		ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
	}))

	// Input validation happens before anything is written.
	_, err = server.LLMSetHalt(ctx, &gctrpc.LLMSetHaltRequest{Level: "SOFT"})
	require.ErrorIs(t, err, ErrHaltInputInvalid)
	_, err = server.LLMSetHalt(ctx, &gctrpc.LLMSetHaltRequest{Level: "MARKET", Reason: "no market", Actor: "op-1"})
	require.ErrorIs(t, err, ErrHaltInputInvalid, "a MARKET halt names its market")

	// SOFT suspends the runnable version and needs no order manager.
	set, err := server.LLMSetHalt(ctx, &gctrpc.LLMSetHaltRequest{
		Level: HaltLevelSoft, Reason: "operator soft stop", Actor: "op-1",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), set.GetSuspendedStrategies(), "the frozen+approved version should be suspended")
	version, err := llmstore.GetStrategyVersion(ctx, versionID)
	require.NoError(t, err)
	assert.Equal(t, StrategyStatePaused, version.State, "the halt should move the version to PAUSED")

	// The halt is visible through the list.
	list, err := server.LLMListHalts(ctx, &gctrpc.LLMListHaltsRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetHalts(), 1)
	haltID := list.GetHalts()[0].GetId()
	assert.Equal(t, HaltLevelSoft, list.GetHalts()[0].GetLevel())
	assert.Equal(t, "operator soft stop", list.GetHalts()[0].GetReason())

	// MARKET needs a canceller; without an order manager the sequence aborts
	// with an error. The halt row stays in force: 落库即生效，中止不回滚——
	// 「已停机但撤单未完成」是安全方向的半成品，错误负责指出哪一步没做完。
	_, err = server.LLMSetHalt(ctx, &gctrpc.LLMSetHaltRequest{
		Level: HaltLevelMarket, MarketId: policyTestMarket, Reason: "market stop", Actor: "op-1",
	})
	require.ErrorIs(t, err, ErrChatNotConfigured)
	list, err = server.LLMListHalts(ctx, &gctrpc.LLMListHaltsRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetHalts(), 2, "the aborted sequence must leave the halt in force")
	assert.Equal(t, HaltLevelMarket, list.GetHalts()[1].GetLevel())

	// Clearing demands re-authentication: the wrong password never clears.
	_, err = server.LLMClearHalt(ctx, &gctrpc.LLMClearHaltRequest{
		Id: haltID, Actor: "op-1", Reason: "resume trading", OperatorPassword: "wrong",
	})
	require.ErrorIs(t, err, ErrHaltClearUnauthenticated)
	list, err = server.LLMListHalts(ctx, &gctrpc.LLMListHaltsRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetHalts(), 2, "a refused clear must leave the halts in force")

	// The right password lifts one halt; strategies and approvals stay put.
	_, err = server.LLMClearHalt(ctx, &gctrpc.LLMClearHaltRequest{
		Id: haltID, Actor: "op-1", Reason: "resume trading", OperatorPassword: "s3cret",
	})
	require.NoError(t, err)
	list, err = server.LLMListHalts(ctx, &gctrpc.LLMListHaltsRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetHalts(), 1, "clearing one halt leaves the other in force")
	_, err = server.LLMClearHalt(ctx, &gctrpc.LLMClearHaltRequest{
		Id: list.GetHalts()[0].GetId(), Actor: "op-1", Reason: "resume trading", OperatorPassword: "s3cret",
	})
	require.NoError(t, err)
	list, err = server.LLMListHalts(ctx, &gctrpc.LLMListHaltsRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.GetHalts(), "a cleared halt is no longer in force")
	version, err = llmstore.GetStrategyVersion(ctx, versionID)
	require.NoError(t, err)
	assert.Equal(t, StrategyStatePaused, version.State,
		"nothing auto-resumes: the strategy stays paused until individually re-enabled")
	approval, err := llmstore.GetStrategyApprovalByVersion(ctx, versionID)
	require.NoError(t, err)
	assert.Nil(t, approval.RevokedAt, "a halt clear does not touch approvals either")

	cleared, err := llmstore.ListAuditEvents(ctx, AuditKindHaltCleared, 10)
	require.NoError(t, err)
	require.Len(t, cleared, 2, "every clear is audited")
	subjects := []string{*cleared[0].Subject, *cleared[1].Subject}
	assert.Contains(t, subjects, haltID, "the clear is audited by halt id")
}

// TestLLMHaltRPCsFailClosed covers the unassembled surface and the FULL
// flatten confirmation gate.
func TestLLMHaltRPCsFailClosed(t *testing.T) {
	var empty *RPCServer
	_, err := empty.LLMSetHalt(context.Background(), &gctrpc.LLMSetHaltRequest{Level: HaltLevelSoft, Reason: "r", Actor: "a"})
	require.ErrorIs(t, err, errLLMHaltUnavailable)
	_, err = empty.LLMListHalts(context.Background(), &gctrpc.LLMListHaltsRequest{})
	require.ErrorIs(t, err, errLLMHaltUnavailable)
	_, err = empty.LLMClearHalt(context.Background(), &gctrpc.LLMClearHaltRequest{Id: "x"})
	require.ErrorIs(t, err, errLLMHaltUnavailable)

	// FULL flatten requires the explicit second confirmation, checked by the
	// controller before anything is written.
	server := &RPCServer{Engine: &Engine{llmManager: &LLMManager{}}}
	_, err = server.LLMSetHalt(context.Background(), &gctrpc.LLMSetHaltRequest{
		Level: HaltLevelFull, Reason: "flatten please", Actor: "op-1", Flatten: true,
	})
	require.ErrorIs(t, err, ErrHaltConfirmationRequired)
}
