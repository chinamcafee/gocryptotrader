package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

type fakeMarketGrants struct {
	grants       []llmstore.MarketTradeGrant
	approvals    []llmstore.StrategyApproval
	createErr    error
	revokeErr    error
	approveErr   error
	revokedGrant []string
	revokedApps  []string
}

func (f *fakeMarketGrants) Create(_ context.Context, g *llmstore.MarketTradeGrant) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.grants = append(f.grants, *g)
	return nil
}

func (f *fakeMarketGrants) List(_ context.Context, accountID string) ([]llmstore.MarketTradeGrant, error) {
	out := make([]llmstore.MarketTradeGrant, 0, len(f.grants))
	for i := range f.grants {
		if f.grants[i].AccountID == accountID {
			out = append(out, f.grants[i])
		}
	}
	return out, nil
}

func (f *fakeMarketGrants) Get(_ context.Context, id string) (*llmstore.MarketTradeGrant, error) {
	for i := range f.grants {
		if f.grants[i].ID == id {
			grant := f.grants[i]
			return &grant, nil
		}
	}
	return nil, errors.New("grant not found")
}

func (f *fakeMarketGrants) Revoke(_ context.Context, id string, at time.Time, _, _ string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revokedGrant = append(f.revokedGrant, id)
	for i := range f.grants {
		if f.grants[i].ID == id {
			f.grants[i].RevokedAt = &at
		}
	}
	return nil
}

func (f *fakeMarketGrants) ListActiveApprovals(context.Context) ([]llmstore.StrategyApproval, error) {
	return f.approvals, nil
}

func (f *fakeMarketGrants) RevokeApproval(_ context.Context, id string, at time.Time, _, reason string) error {
	if f.approveErr != nil {
		return f.approveErr
	}
	f.revokedApps = append(f.revokedApps, id+":"+reason)
	for i := range f.approvals {
		if f.approvals[i].ID == id {
			f.approvals[i].RevokedAt = &at
		}
	}
	return nil
}

const grantMarket = "Binance:usdtmarginedfutures:BTC_USDT"

func grantTestService(t *testing.T) (*MarketGrantService, *fakeMarketGrants) {
	t.Helper()
	fake := &fakeMarketGrants{}
	ids := 0
	return &MarketGrantService{
		Grants: fake,
		Now:    func() time.Time { return providerAdminNow },
		NewID:  func() string { ids++; return fmt.Sprintf("grant-%d", ids) },
	}, fake
}

func grantTestRequest() MarketGrantRequest {
	return MarketGrantRequest{
		AccountID: "acct-1", Network: "binance-testnet", MarketID: grantMarket,
		GrantedBy: "ops-1", ValidFor: 7 * 24 * time.Hour,
	}
}

func TestMarketGrantCreatesWithWindowChecks(t *testing.T) {
	service, fake := grantTestService(t)

	grant, err := service.Grant(t.Context(), grantTestRequest())
	require.NoError(t, err)
	assert.Equal(t, grantMarket, grant.MarketID)
	assert.True(t, providerAdminNow.Equal(grant.GrantedAt))
	assert.True(t, providerAdminNow.Add(7*24*time.Hour).Equal(grant.ValidUntil))
	require.Len(t, fake.grants, 1)

	// The 30-day ceiling is the ceiling: 31 days is refused.
	request := grantTestRequest()
	request.ValidFor = 31 * 24 * time.Hour
	_, err = service.Grant(t.Context(), request)
	assert.ErrorIs(t, err, ErrGrantInputInvalid, "a grant longer than 30 days should be refused")
	request.ValidFor = 0
	_, err = service.Grant(t.Context(), request)
	assert.ErrorIs(t, err, ErrGrantInputInvalid)
	request = grantTestRequest()
	request.MarketID = " "
	_, err = service.Grant(t.Context(), request)
	assert.ErrorIs(t, err, ErrGrantInputInvalid)

	// A live grant for the same account+network+market blocks a duplicate.
	_, err = service.Grant(t.Context(), grantTestRequest())
	assert.ErrorIs(t, err, ErrGrantAlreadyActive)

	// A revoked or expired one does not.
	revokedAt := providerAdminNow.Add(-time.Hour)
	fake.grants[0].RevokedAt = &revokedAt
	_, err = service.Grant(t.Context(), grantTestRequest())
	require.NoError(t, err, "a revoked grant must not block a fresh one")

	fake.grants[1].RevokedAt = nil
	fake.grants[1].ValidUntil = providerAdminNow.Add(-time.Minute)
	_, err = service.Grant(t.Context(), grantTestRequest())
	require.NoError(t, err, "an expired grant must not block a fresh one")
}

func TestMarketGrantRevokeCascadesToApprovals(t *testing.T) {
	service, fake := grantTestService(t)
	grant, err := service.Grant(t.Context(), grantTestRequest())
	require.NoError(t, err)

	matching, err := EncodeApprovedMarkets([]string{grantMarket, "Binance:usdtmarginedfutures:ETH_USDT"})
	require.NoError(t, err)
	otherMarket, err := EncodeApprovedMarkets([]string{"Binance:usdtmarginedfutures:SOL_USDT"})
	require.NoError(t, err)
	fake.approvals = []llmstore.StrategyApproval{
		{ID: "app-match", AccountID: "acct-1", Network: "binance-testnet", ApprovedMarketIDs: matching},
		{ID: "app-other-market", AccountID: "acct-1", Network: "binance-testnet", ApprovedMarketIDs: otherMarket},
		{ID: "app-other-account", AccountID: "acct-2", Network: "binance-testnet", ApprovedMarketIDs: matching},
		{ID: "app-other-network", AccountID: "acct-1", Network: "binance-mainnet", ApprovedMarketIDs: matching},
	}

	result, err := service.Revoke(t.Context(), grant.ID, "ops-2", "risk review found the market was granted by mistake")
	require.NoError(t, err)
	assert.Equal(t, []string{"app-match"}, result.RevokedApprovalIDs,
		"only approvals of the same account+network naming the market may cascade")
	assert.Equal(t, []string{"grant-1"}, fake.revokedGrant)
	require.Len(t, fake.revokedApps, 1)
	assert.Contains(t, fake.revokedApps[0], "app-match:market grant revoked: "+grantMarket)

	// Revoking again is refused: a second revoke would rewrite the audit line.
	_, err = service.Revoke(t.Context(), grant.ID, "ops-2", "again")
	assert.ErrorIs(t, err, ErrGrantAlreadyRevoked)
}

func TestMarketGrantRevokeMisuseAndMalformedApprovals(t *testing.T) {
	service, fake := grantTestService(t)
	grant, err := service.Grant(t.Context(), grantTestRequest())
	require.NoError(t, err)

	_, err = service.Revoke(t.Context(), grant.ID, " ", "because")
	assert.ErrorIs(t, err, ErrGrantInputInvalid)
	_, err = service.Revoke(t.Context(), grant.ID, "ops-2", " ")
	assert.ErrorIs(t, err, ErrGrantInputInvalid, "a revocation without a reason is not auditable")
	_, err = service.Revoke(t.Context(), "ghost", "ops-2", "reason")
	assert.Error(t, err)
	assert.Empty(t, fake.revokedGrant, "a missing grant should not reach the table")

	// A malformed market list fails the cascade loudly: skipping it could
	// leave a live approval over a market whose authorization is gone.
	fake.approvals = []llmstore.StrategyApproval{{ID: "app-bad", AccountID: "acct-1", Network: "binance-testnet", ApprovedMarketIDs: "not-the-shape"}}
	_, err = service.Revoke(t.Context(), grant.ID, "ops-2", "reason")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app-bad")
}

func TestApprovedMarketsCodec(t *testing.T) {
	encoded, err := EncodeApprovedMarkets([]string{"A", "B"})
	require.NoError(t, err)
	assert.Equal(t, `["A","B"]`, encoded)
	decoded, err := DecodeApprovedMarkets(encoded)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, decoded)
	empty, err := DecodeApprovedMarkets("")
	require.NoError(t, err)
	assert.Nil(t, empty, "an empty column is no markets, not an error")
	_, err = DecodeApprovedMarkets("{oops}")
	assert.Error(t, err)
}
