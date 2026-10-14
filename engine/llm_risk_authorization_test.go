package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// riskAuthFixture builds the reviewed material class A maps from.
func riskAuthFixture(t *testing.T, now time.Time) *RiskAuthorizationInput {
	t.Helper()
	policy := candidate.ReviewablePolicy{
		SchemaVersion: "reviewable-v1", State: "READY_FOR_APPROVAL",
		AccountID: "acct-1", Network: "binance-testnet", ExecutionMode: LLMRunModeShadow,
		MarketScopes: []candidate.MarketScope{{
			MarketID: "BTC_USDT", MarketType: "PERP",
			Actions: []candidate.Action{candidate.ActionOpenLong, candidate.ActionReduce},
		}},
		ValidFrom:  now,
		ValidUntil: now.Add(24 * time.Hour),
		Risk: candidate.RiskEnvelope{
			MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
			MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"),
			MaxDailyLossUSD: new("20"), MaxSlippageBPS: new(20),
			MaxLeverage: new(1), MaxConcurrentPositions: 1, RequiresProtectiveExit: true,
		},
		LLMRevisionID: "rev-llm", JevRevisionID: "rev-jev", PairEpoch: 1,
	}
	require.NoError(t, policy.Validate())
	frozen, err := json.Marshal(&policy)
	require.NoError(t, err)
	frozenJSON := string(frozen)
	hash, err := RiskEnvelopeHashOf(frozenJSON)
	require.NoError(t, err)
	return &RiskAuthorizationInput{
		Approval: &llmstore.StrategyApproval{
			ID: "appr-1", StrategyVersionID: "ver-1", AccountID: "acct-1", Network: "binance-testnet",
			ExecutionMode: LLMRunModeShadow, ApprovedMarketIDs: `["BTC_USDT"]`,
			RiskEnvelopeHash: hash, PairEpoch: 1,
			ValidFrom: now, ValidUntil: now.Add(12 * time.Hour),
		},
		FrozenJSON:    frozenJSON,
		Candidate:     &candidate.Candidate{Action: candidate.ActionOpenLong, MarketID: "BTC_USDT"},
		PairEpoch:     1,
		Grants:        []llmstore.MarketTradeGrant{{ID: "grant-1", AccountID: "acct-1", Network: "binance-testnet", MarketID: "BTC_USDT", GrantedAt: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}},
		ManifestBound: true,
		Now:           now,
	}
}

// TestRiskAuthorizationFromState covers the A-group mapping on the live
// material: approval facts, scope/action membership, grant coverage, the
// frozen-hash comparison and the epoch.
func TestRiskAuthorizationFromState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	auth, err := riskAuthorizationFromState(riskAuthFixture(t, now))
	require.NoError(t, err)
	assert.True(t, auth.HasApproval)
	assert.Equal(t, LLMRunModeShadow, auth.ApprovedMode)
	assert.Equal(t, "acct-1", auth.ApprovedAccountID)
	assert.Equal(t, now, auth.ValidFrom)
	assert.True(t, auth.MarketGranted, "an active grant covers the market")
	assert.True(t, auth.MarketInScope)
	assert.True(t, auth.ActionAllowed)
	assert.True(t, auth.EnvelopeHashMatches, "the approval hash matches the frozen text recomputed")
	assert.True(t, auth.PairEpochMatches)
	assert.True(t, auth.JevManifestBound)

	// A missing approval is a fact, not an error: everything else stays zero
	// and the checks fail closed on their own.
	none := riskAuthFixture(t, now)
	none.Approval = nil
	auth, err = riskAuthorizationFromState(none)
	require.NoError(t, err)
	assert.False(t, auth.HasApproval)
	assert.False(t, auth.MarketGranted)

	// Divergences each surface in their own field.
	badAction := riskAuthFixture(t, now)
	badAction.Candidate.Action = candidate.ActionOpenShort
	auth, err = riskAuthorizationFromState(badAction)
	require.NoError(t, err)
	assert.True(t, auth.MarketInScope)
	assert.False(t, auth.ActionAllowed, "the scope lists only OPEN_LONG and REDUCE")

	badMarket := riskAuthFixture(t, now)
	badMarket.Candidate.MarketID = "ETH_USDT"
	auth, err = riskAuthorizationFromState(badMarket)
	require.NoError(t, err)
	assert.False(t, auth.MarketInScope)
	assert.False(t, auth.ActionAllowed)

	expiredGrant := riskAuthFixture(t, now)
	expiredGrant.Grants[0].ValidUntil = now.Add(-time.Minute)
	auth, err = riskAuthorizationFromState(expiredGrant)
	require.NoError(t, err)
	assert.False(t, auth.MarketGranted, "an expired grant does not cover")

	revokedGrant := riskAuthFixture(t, now)
	revokedGrant.Grants[0].RevokedAt = new(now.Add(-time.Minute))
	auth, err = riskAuthorizationFromState(revokedGrant)
	require.NoError(t, err)
	assert.False(t, auth.MarketGranted)

	hashMismatch := riskAuthFixture(t, now)
	hashMismatch.Approval.RiskEnvelopeHash = "tampered-hash"
	auth, err = riskAuthorizationFromState(hashMismatch)
	require.NoError(t, err)
	assert.False(t, auth.EnvelopeHashMatches, "a swapped frozen text should surface here")

	epochMismatch := riskAuthFixture(t, now)
	epochMismatch.PairEpoch = 2
	auth, err = riskAuthorizationFromState(epochMismatch)
	require.NoError(t, err)
	assert.False(t, auth.PairEpochMatches)

	// Refusals: incomplete material is an error, never a guess.
	_, err = riskAuthorizationFromState(nil)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
	noCandidate := riskAuthFixture(t, now)
	noCandidate.Candidate = nil
	_, err = riskAuthorizationFromState(noCandidate)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
	badJSON := riskAuthFixture(t, now)
	badJSON.FrozenJSON = "{not json"
	_, err = riskAuthorizationFromState(badJSON)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
	noClock := riskAuthFixture(t, now)
	noClock.Now = time.Time{}
	_, err = riskAuthorizationFromState(noClock)
	assert.ErrorIs(t, err, ErrSnapshotClockMissing)
}
