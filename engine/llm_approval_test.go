package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

type fakeApprovals struct {
	version     *llmstore.StrategyVersion
	versionErr  error
	approval    *llmstore.StrategyApproval
	lookupErr   error
	insertErr   error
	grants      []llmstore.MarketTradeGrant
	epoch       int64
	inserted    []llmstore.StrategyApproval
	audits      []llmstore.AuditEvent
	verifiedPwd []string
}

func (f *fakeApprovals) GetVersion(context.Context, string) (*llmstore.StrategyVersion, error) {
	if f.versionErr != nil {
		return nil, f.versionErr
	}
	return f.version, nil
}

func (f *fakeApprovals) GetApprovalByVersion(context.Context, string) (*llmstore.StrategyApproval, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.approval != nil {
		return f.approval, nil
	}
	return nil, llmstore.ErrNotFound
}

func (f *fakeApprovals) InsertApproval(_ context.Context, a *llmstore.StrategyApproval) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, *a)
	f.approval = a
	return nil
}

func (f *fakeApprovals) ListGrants(context.Context, string) ([]llmstore.MarketTradeGrant, error) {
	return f.grants, nil
}

func (f *fakeApprovals) ActivePairEpoch(context.Context) (int64, error) {
	return f.epoch, nil
}

func (f *fakeApprovals) RecordAudit(_ context.Context, e *llmstore.AuditEvent) error {
	f.audits = append(f.audits, *e)
	return nil
}

func approvalTestVersion(t *testing.T) *llmstore.StrategyVersion {
	t.Helper()
	policy := freezeTestPolicy(t)
	frozen, err := json.Marshal(policy)
	require.NoError(t, err)
	frozenJSON := string(frozen)
	frozenAt := providerAdminNow.Add(-time.Hour)
	ready := string(candidate.StateReadyForReview)
	return &llmstore.StrategyVersion{
		ID: "ver-1", StrategyID: "start-1", State: "FROZEN",
		ProposalState: &ready, FrozenJSON: &frozenJSON, FrozenAt: &frozenAt,
	}
}

func approvalTestService(t *testing.T) (*ApprovalService, *fakeApprovals) {
	t.Helper()
	fake := &fakeApprovals{
		version: approvalTestVersion(t),
		epoch:   3,
		grants: []llmstore.MarketTradeGrant{{
			ID: "grant-1", AccountID: "acct-1", Network: "binance-testnet",
			MarketID: policyTestMarket, ValidUntil: providerAdminNow.Add(48 * time.Hour),
		}},
	}
	ids := 0
	service := &ApprovalService{
		Approvals: fake,
		VerifyPassword: func(_ context.Context, password string) error {
			fake.verifiedPwd = append(fake.verifiedPwd, password)
			if password != "s3cret" {
				return errors.New("wrong password")
			}
			return nil
		},
		Now:   func() time.Time { return providerAdminNow },
		NewID: func() string { ids++; return "id-" + digitString(ids) },
	}
	return service, fake
}

func approvalTestRequest() *ApprovalRequest {
	return &ApprovalRequest{
		Actor: "ops-1", VersionID: "ver-1", AccountID: "acct-1", Network: "binance-testnet",
		ExecutionMode: "SHADOW", Markets: []string{policyTestMarket}, ValidFor: 4 * time.Hour,
		OperatorPassword: "s3cret", IdempotencyKey: "idem-1", ExpectedPairEpoch: 3,
	}
}

func TestApprovalBindsTheFrozenPolicy(t *testing.T) {
	service, fake := approvalTestService(t)

	approval, replayed, err := service.Approve(t.Context(), approvalTestRequest())
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, "ver-1", approval.StrategyVersionID)
	assert.Equal(t, "acct-1", approval.AccountID)
	assert.Equal(t, "binance-testnet", approval.Network)
	assert.Equal(t, "SHADOW", approval.ExecutionMode)
	assert.Equal(t, `["`+policyTestMarket+`"]`, approval.ApprovedMarketIDs)
	assert.Equal(t, int64(3), approval.PairEpoch)
	assert.Equal(t, "ops-1", approval.ApprovedBy)
	assert.True(t, providerAdminNow.Equal(approval.ApprovedAt))
	assert.True(t, providerAdminNow.Add(4*time.Hour).Equal(approval.ValidUntil))
	assert.Equal(t, []string{"s3cret"}, fake.verifiedPwd, "the operator should have re-authenticated")

	// The risk envelope is copied from the frozen policy, not the request:
	// approving must not be able to smuggle a new limit in.
	wantRisk, err := json.Marshal(freezeTestPolicy(t).Risk)
	require.NoError(t, err)
	assert.Equal(t, string(wantRisk), approval.RiskEnvelopeJSON)
	assert.Equal(t, goldenHash(t, string(wantRisk)), approval.RiskEnvelopeHash)
	assert.Equal(t, "rev-1", approval.LLMProfileRevisionID)
	assert.Equal(t, "rev-2", approval.JevProfileRevisionID)

	require.Len(t, fake.audits, 1)
	audit := fake.audits[0]
	assert.Equal(t, ApprovalAuditKind, audit.Kind)
	assert.Equal(t, "ops-1", audit.Actor)
	require.NotNil(t, audit.Subject)
	assert.Equal(t, "ver-1", *audit.Subject)
	assert.Contains(t, audit.DetailJSON, `"idempotencyKey":"idem-1"`, "the key is stamped into the audit trail")
	assert.Contains(t, audit.DetailJSON, `"riskEnvelopeHash":`)
}

func TestApprovalRequiresReAuthentication(t *testing.T) {
	service, fake := approvalTestService(t)

	request := approvalTestRequest()
	request.OperatorPassword = ""
	_, _, err := service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrReAuthFailed, "a blank password is a failed re-auth, not a skippable field")
	assert.Empty(t, fake.inserted)

	request = approvalTestRequest()
	request.OperatorPassword = "guess"
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrReAuthFailed)
	assert.Empty(t, fake.inserted, "a failed re-auth should stop before anything is written")
	assert.Empty(t, fake.audits)
}

func TestApprovalBindingGates(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ApprovalRequest)
		want   error
	}{
		{"account mismatch", func(r *ApprovalRequest) { r.AccountID = "acct-2" }, ErrApprovalBindingMismatch},
		{"network mismatch", func(r *ApprovalRequest) { r.Network = "binance-mainnet" }, ErrApprovalBindingMismatch},
		{"mode mismatch", func(r *ApprovalRequest) { r.ExecutionMode = "LIVE" }, ErrApprovalBindingMismatch},
		{"market outside the frozen scope", func(r *ApprovalRequest) { r.Markets = []string{"Binance:usdtmarginedfutures:ETH_USDT"} }, ErrApprovalBindingMismatch},
		{"stale expected epoch", func(r *ApprovalRequest) { r.ExpectedPairEpoch = 2 }, ErrPairEpochConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, fake := approvalTestService(t)
			request := approvalTestRequest()
			tc.mutate(request)
			_, _, err := service.Approve(t.Context(), request)
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, fake.inserted)
		})
	}

	// A policy frozen under an older pair cannot be approved at all: the pair
	// moved after the operator reviewed it.
	service, fake := approvalTestService(t)
	fake.epoch = 4
	request := approvalTestRequest()
	request.ExpectedPairEpoch = 4 // CAS passes; the frozen policy itself is stale
	_, _, err := service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrStalePolicyPair)
	assert.Empty(t, fake.inserted)
}

func TestApprovalRequiresActiveGrants(t *testing.T) {
	service, fake := approvalTestService(t)
	fake.grants = nil
	_, _, err := service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorIs(t, err, ErrMarketNotGranted, "a market without a live grant cannot become tradable via approval")

	revokedAt := providerAdminNow.Add(-time.Minute)
	service, fake = approvalTestService(t)
	fake.grants[0].RevokedAt = &revokedAt
	_, _, err = service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorIs(t, err, ErrMarketNotGranted)

	service, fake = approvalTestService(t)
	fake.grants[0].ValidUntil = providerAdminNow.Add(-time.Minute)
	_, _, err = service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorIs(t, err, ErrMarketNotGranted, "an expired grant is no grant")
	assert.Empty(t, fake.inserted)
}

func TestApprovalIdempotentReplayAndConflict(t *testing.T) {
	service, fake := approvalTestService(t)
	stored, _, err := service.Approve(t.Context(), approvalTestRequest())
	require.NoError(t, err)

	// Same binding again: a replay returns the stored row and writes nothing.
	replayed, replayedFlag, err := service.Approve(t.Context(), approvalTestRequest())
	require.NoError(t, err)
	assert.True(t, replayedFlag)
	assert.Equal(t, stored.ID, replayed.ID)
	assert.Len(t, fake.inserted, 1, "a replay should not insert")
	assert.Len(t, fake.audits, 1, "a replay should not double-audit")

	// Same version, different binding: a conflict, not a replacement.
	request := approvalTestRequest()
	request.Markets = []string{policyTestMarket}
	request.ValidFor = 2 * time.Hour
	request.IdempotencyKey = "idem-2"
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalConflict, "one version carries one approval; a different binding is a conflict")
}

func TestApprovalRefusesUnfrozenAndReportsAuditLoss(t *testing.T) {
	service, fake := approvalTestService(t)
	fake.version.FrozenJSON = nil
	_, _, err := service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorIs(t, err, ErrStrategyNotFrozen)
	assert.Empty(t, fake.inserted)

	service, fake = approvalTestService(t)
	fake.versionErr = errors.New("version missing")
	_, _, err = service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorContains(t, err, "version missing")

	service, fake = approvalTestService(t)
	fake.insertErr = errors.New("write refused")
	_, _, err = service.Approve(t.Context(), approvalTestRequest())
	assert.ErrorContains(t, err, "write refused")
}

func TestApprovalInputValidation(t *testing.T) {
	service, fake := approvalTestService(t)

	request := approvalTestRequest()
	request.ValidFor = 8 * 24 * time.Hour
	_, _, err := service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalInputInvalid, "the 7-day ceiling is the ceiling")

	request = approvalTestRequest()
	request.ExecutionMode = "TURBO"
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalInputInvalid)

	request = approvalTestRequest()
	request.Markets = []string{policyTestMarket, policyTestMarket}
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalInputInvalid, "a duplicated market is a malformed request")

	request = approvalTestRequest()
	request.IdempotencyKey = " "
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalInputInvalid)

	request = approvalTestRequest()
	request.Actor = ""
	_, _, err = service.Approve(t.Context(), request)
	assert.ErrorIs(t, err, ErrApprovalInputInvalid)
	assert.Empty(t, fake.inserted)

	var nilService *ApprovalService
	_, _, err = nilService.Approve(t.Context(), approvalTestRequest())
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

func TestApprovalAuditLossIsReported(t *testing.T) {
	service, fake := approvalTestService(t)
	fake.audits = nil
	// Simulate an audit table failure by swapping in a failing recorder.
	failing := &failingAuditApprovals{fakeApprovals: fake}
	service.Approvals = failing
	_, _, err := service.Approve(t.Context(), approvalTestRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit", "a stored approval without its audit event should be reported")
}

type failingAuditApprovals struct{ *fakeApprovals }

func (f *failingAuditApprovals) RecordAudit(context.Context, *llmstore.AuditEvent) error {
	return errors.New("audit table unavailable")
}
