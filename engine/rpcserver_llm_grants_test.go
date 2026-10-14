package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestMarketGrantRPCs covers the W5-07 export: grant -> list -> cascading
// revoke on a real database.
func TestMarketGrantRPCs(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	server := &RPCServer{Engine: &Engine{}}

	_, err := server.LLMGrantMarket(ctx, &gctrpc.LLMGrantMarketRequest{
		AccountId: "acct-1", Network: "binance-testnet", MarketId: policyTestMarket,
	})
	require.ErrorIs(t, err, errLLMStrategyInput, "missing valid_days and granted_by must be refused before any write")

	granted, err := server.LLMGrantMarket(ctx, &gctrpc.LLMGrantMarketRequest{
		AccountId: "acct-1", Network: "binance-testnet", MarketId: policyTestMarket,
		ValidDays: 7, GrantedBy: "op-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, granted.GetGrant().GetId())
	assert.Equal(t, policyTestMarket, granted.GetGrant().GetMarketId())
	assert.NotEmpty(t, granted.GetGrant().GetValidUntil())

	// The 30-day ceiling is the service's, not the client's to raise.
	_, err = server.LLMGrantMarket(ctx, &gctrpc.LLMGrantMarketRequest{
		AccountId: "acct-1", Network: "binance-testnet", MarketId: policyTestMarket,
		ValidDays: 31, GrantedBy: "op-1",
	})
	require.ErrorIs(t, err, ErrGrantInputInvalid)

	list, err := server.LLMListMarketGrants(ctx, &gctrpc.LLMListMarketGrantsRequest{AccountId: "acct-1"})
	require.NoError(t, err)
	require.Len(t, list.GetGrants(), 1)

	_, err = server.LLMListMarketGrants(ctx, &gctrpc.LLMListMarketGrantsRequest{})
	require.ErrorIs(t, err, errLLMStrategyInput)

	// An active approval covering this market must fall with the grant.
	created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: "grant cascade", Actor: "op-1"})
	require.NoError(t, err)
	saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
		StrategyId: created.GetStrategy().GetId(), ProposalJson: completeProposalJSON(), Actor: "op-1",
	})
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-g1", StrategyVersionID: saved.GetVersion().GetId(), AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", ApprovedMarketIDs: `["` + policyTestMarket + `"]`,
		RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-1", PairEpoch: 1,
		ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
	}))

	revoked, err := server.LLMRevokeMarketGrant(ctx, &gctrpc.LLMRevokeMarketGrantRequest{
		Id: granted.GetGrant().GetId(), Actor: "op-2", Reason: "smoke",
	})
	require.NoError(t, err)
	assert.Equal(t, granted.GetGrant().GetId(), revoked.GetGrantId())
	assert.Contains(t, revoked.GetRevokedApprovalIds(), "appr-g1")

	// Rewriting a revoke is tampering: the second one is refused.
	_, err = server.LLMRevokeMarketGrant(ctx, &gctrpc.LLMRevokeMarketGrantRequest{
		Id: granted.GetGrant().GetId(), Actor: "op-2", Reason: "again",
	})
	require.ErrorIs(t, err, ErrGrantAlreadyRevoked)

	list, err = server.LLMListMarketGrants(ctx, &gctrpc.LLMListMarketGrantsRequest{AccountId: "acct-1"})
	require.NoError(t, err)
	require.Len(t, list.GetGrants(), 1)
	require.NotEmpty(t, list.GetGrants()[0].GetRevokedAt())
	assert.Equal(t, "op-2", list.GetGrants()[0].GetRevokedBy())

	// The cascade is visible on the approval read side too.
	approval, err := llmstore.GetStrategyApprovalByVersion(ctx, saved.GetVersion().GetId())
	require.NoError(t, err)
	require.NotNil(t, approval.RevokedAt)
}
