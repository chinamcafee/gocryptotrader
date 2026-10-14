package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// setupStrategyAdminDB connects a fresh migrated sqlite database with its own
// file so each test starts from an empty schema (same reason as the provider
// admin fixture: the shared temp dir must outlive individual tests).
func setupStrategyAdminDB(t *testing.T) {
	t.Helper()
	testhelpers.MigrationDir = filepath.Join("..", "database", "migrations")
	if testhelpers.TempDir == "" {
		dir, err := os.MkdirTemp("", "gct-engine-llm-strategy-admin") //nolint:usetesting // the sqlite fixture must outlive the first test (deliberate).
		if err != nil {
			t.Fatalf("creating temp dir: %v", err)
		}
		testhelpers.TempDir = dir
	}
	cfg := &database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: fmt.Sprintf("llm-strategy-admin-%d.db", time.Now().UnixNano()),
	}
	conn, err := testhelpers.ConnectToDatabase(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testhelpers.CloseDatabase(conn))
	})
}

// incompleteProposalJSON is an extraction draft with an unanswered clarifying
// question and missing risk limits.
func incompleteProposalJSON() string {
	return `{"objective":"ride momentum","marketScopes":[{"marketId":"` + policyTestMarket + `","marketType":"PERP","actions":["OPEN_LONG"]}],` +
		`"validMinutes":null,` +
		`"risk":{"maxNewRiskIntents":2,"riskWindowMinutes":300,"maxOrderNotionalUsd":null,"maxTotalExposureUsd":null,"maxDailyLossUsd":null,"maxSlippageBps":null,"maxLeverage":1,"maxConcurrentPositions":1,"requiresProtectiveExit":true},` +
		`"exitPolicy":null,"clarifyingQuestions":["What daily loss ceiling should apply?"]}`
}

// completeProposalJSON is an extraction draft where every hard boundary is set.
func completeProposalJSON() string {
	return `{"objective":"ride momentum","marketScopes":[{"marketId":"` + policyTestMarket + `","marketType":"PERP","actions":["OPEN_LONG"]}],` +
		`"validMinutes":240,` +
		`"risk":{"maxNewRiskIntents":2,"riskWindowMinutes":300,"maxOrderNotionalUsd":"20","maxTotalExposureUsd":"100","maxDailyLossUsd":"20","maxSlippageBps":20,"maxLeverage":1,"maxConcurrentPositions":1,"requiresProtectiveExit":true},` +
		`"exitPolicy":"close on MACD cross back","clarifyingQuestions":[]}`
}

func strategyAdminService() *LLMStrategyAdmin {
	// The schema pins UNIQUE(strategy_id, created_at), so the frozen test clock
	// must step between saves.
	now := providerAdminNow
	return &LLMStrategyAdmin{
		Store: LLMStrategyAdminStore{},
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
		NewID: newLLMIdentifier,
	}
}

// TestStrategyAdminDraftLifecycle proves the W5 bootstrap end to end on a real
// database: create -> draft -> freeze, with the unanswered-clarification gate
// refusing the freeze in between.
func TestStrategyAdminDraftLifecycle(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	admin := strategyAdminService()

	strategy, err := admin.CreateStrategy(ctx, "  momentum v1 ", "op-1")
	require.NoError(t, err)
	assert.Equal(t, "momentum v1", strategy.Name)
	assert.Equal(t, "op-1", strategy.CreatedBy)
	assert.NotEmpty(t, strategy.ID)

	draft, err := admin.SavePolicyDraft(ctx, strategy.ID, "", incompleteProposalJSON(), "op-1")
	require.NoError(t, err)
	assert.Equal(t, StrategyStateDraft, draft.State)
	require.NotNil(t, draft.ProposalState)
	assert.Equal(t, string(candidate.StateNeedsClarification), *draft.ProposalState)
	assert.Nil(t, draft.ParentVersionID)
	require.NotNil(t, draft.ClarifyingQuestions)
	var questions []string
	require.NoError(t, json.Unmarshal([]byte(*draft.ClarifyingQuestions), &questions))
	require.Len(t, questions, 1)
	assert.Equal(t, sha256Hex([]byte(incompleteProposalJSON())), draft.SourcePromptSha256)

	versions, err := llmstore.ListStrategyVersions(ctx, strategy.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	// A version whose questions are unanswered must not freeze: the gate reads
	// the stored proposal state, not the caller's word.
	freezer := &StrategyFreezer{Versions: LLMStrategyVersions{}, Now: func() time.Time { return providerAdminNow }, NewID: newLLMIdentifier}
	_, err = freezer.Freeze(ctx, draft.ID, freezeTestPolicy(t), "op-1")
	require.ErrorIs(t, err, ErrProposalNotReady)

	// The next draft answers the questions and becomes reviewable.
	complete, err := admin.SavePolicyDraft(ctx, strategy.ID, draft.ID, completeProposalJSON(), "op-1")
	require.NoError(t, err)
	require.NotNil(t, complete.ProposalState)
	assert.Equal(t, string(candidate.StateReadyForReview), *complete.ProposalState)
	require.NotNil(t, complete.ParentVersionID)
	assert.Equal(t, draft.ID, *complete.ParentVersionID)

	frozen, err := freezer.Freeze(ctx, complete.ID, freezeTestPolicy(t), "op-1")
	require.NoError(t, err)
	assert.NotEmpty(t, frozen.StrategyHash)
	assert.NotEmpty(t, frozen.RiskEnvelopeHash)

	// Both operations are audited.
	created, err := llmstore.ListAuditEvents(ctx, AuditKindStrategyCreated, 10)
	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, strategy.ID, *created[0].Subject)
	saved, err := llmstore.ListAuditEvents(ctx, AuditKindStrategyDraftSaved, 10)
	require.NoError(t, err)
	require.Len(t, saved, 2)

	// The frozen version carries the policy document through the read path.
	stored, err := llmstore.GetStrategyVersion(ctx, complete.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.FrozenJSON)
}

// TestStrategyAdminValidation covers the refuse paths of the admin surface.
func TestStrategyAdminValidation(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	admin := strategyAdminService()

	_, err := admin.CreateStrategy(ctx, "  ", "op-1")
	require.ErrorIs(t, err, ErrStrategyAdminInput)
	_, err = admin.CreateStrategy(ctx, "ok name", " ")
	require.ErrorIs(t, err, ErrStrategyAdminInput)

	first, err := admin.CreateStrategy(ctx, "first", "op-1")
	require.NoError(t, err)
	second, err := admin.CreateStrategy(ctx, "second", "op-1")
	require.NoError(t, err)

	_, err = admin.SavePolicyDraft(ctx, first.ID, "", "{not json", "op-1")
	require.Error(t, err)
	assert.ErrorContainsf(t, err, "proposal json", "error should name the malformed document: %v", err)

	_, err = admin.SavePolicyDraft(ctx, "missing-strategy", "", completeProposalJSON(), "op-1")
	require.Error(t, err)

	firstDraft, err := admin.SavePolicyDraft(ctx, first.ID, "", completeProposalJSON(), "op-1")
	require.NoError(t, err)

	// A parent version from another strategy is not a version of this strategy.
	_, err = admin.SavePolicyDraft(ctx, second.ID, firstDraft.ID, completeProposalJSON(), "op-1")
	require.ErrorIs(t, err, ErrStrategyAdminInput)

	// Drafts under an archived strategy are dead work.
	require.NoError(t, llmstore.ArchiveStrategy(ctx, first.ID, providerAdminNow))
	_, err = admin.SavePolicyDraft(ctx, first.ID, "", completeProposalJSON(), "op-1")
	require.ErrorIs(t, err, ErrStrategyArchived)
}

// TestStrategyAdminHandlers covers the four RPC conversions.
func TestStrategyAdminHandlers(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	server := &RPCServer{Engine: &Engine{}}

	created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: "grid-v1", Actor: "op-2"})
	require.NoError(t, err)
	require.NotEmpty(t, created.GetStrategy().GetId())
	assert.Equal(t, "grid-v1", created.GetStrategy().GetName())

	_, err = server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: "grid-v1"})
	require.ErrorIs(t, err, ErrStrategyAdminInput)

	saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
		StrategyId:   created.GetStrategy().GetId(),
		ProposalJson: completeProposalJSON(),
		Actor:        "op-2",
	})
	require.NoError(t, err)
	assert.Equal(t, StrategyStateDraft, saved.GetVersion().GetState())
	assert.Equal(t, string(candidate.StateReadyForReview), saved.GetVersion().GetProposalState())

	list, err := server.LLMListStrategyVersions(ctx, &gctrpc.LLMListStrategyVersionsRequest{StrategyId: created.GetStrategy().GetId()})
	require.NoError(t, err)
	require.Len(t, list.GetVersions(), 1)
	assert.Equal(t, saved.GetVersion().GetId(), list.GetVersions()[0].GetId())

	_, err = server.LLMListStrategyVersions(ctx, &gctrpc.LLMListStrategyVersionsRequest{})
	require.ErrorIs(t, err, errLLMStrategyInput)

	// No approval row yet: found=false is a state, not an error.
	none, err := server.LLMGetStrategyApproval(ctx, &gctrpc.LLMGetStrategyApprovalRequest{StrategyVersionId: saved.GetVersion().GetId()})
	require.NoError(t, err)
	assert.False(t, none.GetFound())

	versionID := saved.GetVersion().GetId()
	now := providerAdminNow
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-1", StrategyVersionID: versionID, AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", ApprovedMarketIDs: `["` + policyTestMarket + `"]`,
		RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-1", PairEpoch: 3,
		ValidFrom: now, ValidUntil: now.Add(4 * time.Hour), ApprovedBy: "op-3", ApprovedAt: now,
	}))
	found, err := server.LLMGetStrategyApproval(ctx, &gctrpc.LLMGetStrategyApprovalRequest{StrategyVersionId: versionID})
	require.NoError(t, err)
	require.True(t, found.GetFound())
	assert.Equal(t, []string{policyTestMarket}, found.GetApproval().GetApprovedMarketIds())
	assert.Equal(t, "SHADOW", found.GetApproval().GetExecutionMode())
	assert.Equal(t, int64(3), found.GetApproval().GetPairEpoch())
	assert.NotEmpty(t, found.GetApproval().GetValidUntil())

	_, err = server.LLMGetStrategyApproval(ctx, &gctrpc.LLMGetStrategyApprovalRequest{})
	require.ErrorIs(t, err, errLLMStrategyInput)
}
