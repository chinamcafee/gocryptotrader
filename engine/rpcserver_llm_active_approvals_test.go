package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// A5：活跃审批列表 RPC（GUI 批量工作台的默认版本来源）。

func TestLLMListActiveApprovalsRPC(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-live", StrategyVersionID: "ver-live", AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: LLMRunModeShadow, ApprovedMarketIDs: `["BTC_USDT"]`, RiskEnvelopeHash: "env-hash",
		PairEpoch: 3, ValidFrom: providerAdminNow, ValidUntil: providerAdminNow.Add(24 * 24 * 3600 * 1e9),
	}))
	// A revoked row must not appear in the active list (the revoke path is the
	// one that actually writes revoked_at).
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-dead", StrategyVersionID: "ver-dead", AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: LLMRunModeShadow, ApprovedMarketIDs: `["BTC_USDT"]`, RiskEnvelopeHash: "env-hash",
		PairEpoch: 3, ValidFrom: providerAdminNow, ValidUntil: providerAdminNow.Add(24 * 24 * 3600 * 1e9),
	}))
	require.NoError(t, llmstore.RevokeStrategyApproval(ctx, "appr-dead", providerAdminNow.Add(3600*1e9), "op", "test"))

	srv := &RPCServer{Engine: &Engine{}}
	out, err := srv.LLMListActiveApprovals(ctx, &gctrpc.LLMListActiveApprovalsRequest{})
	require.NoError(t, err)
	require.Len(t, out.GetApprovals(), 1, "only the live approval lists")
	assert.Equal(t, "ver-live", out.GetApprovals()[0].GetStrategyVersionId())

	// Filters apply when provided.
	filtered, err := srv.LLMListActiveApprovals(ctx, &gctrpc.LLMListActiveApprovalsRequest{Network: "binance-mainnet"})
	require.NoError(t, err)
	assert.Empty(t, filtered.GetApprovals(), "a mismatched network filters the row out")
	filtered, err = srv.LLMListActiveApprovals(ctx, &gctrpc.LLMListActiveApprovalsRequest{AccountId: "acct-1", Network: "binance-testnet"})
	require.NoError(t, err)
	assert.Len(t, filtered.GetApprovals(), 1)
}
