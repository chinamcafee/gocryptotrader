package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// riskFactsSnapshotFixture is a decidable snapshot with account facts.
func riskFactsSnapshotFixture(now time.Time) *snapshot.MarketSnapshot {
	return &snapshot.MarketSnapshot{
		ID: "snap-1", Exchange: "binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		SourceTime: now.Add(-time.Minute), BuiltAt: now, FreshnessMS: 1234,
		Quality: map[string]candidate.DataQuality{
			snapshot.CategoryCandles:   candidate.QualityExchangeREST,
			snapshot.CategoryOrderBook: candidate.QualityExchangeREST,
			snapshot.CategoryAccount:   candidate.QualityDatabaseCached,
		},
		Account: &snapshot.AccountFacts{
			TotalEquityUSD: "10000", AvailableBalanceUSD: "8000",
			ReadAt: now.Add(-2 * time.Second),
			Positions: []snapshot.PositionFact{
				{Symbol: "BTC_USDT", Side: PositionSideLong, Size: "0.5", EntryPrice: "50000", MarkPrice: "50100"},
			},
		},
	}
}

// TestRiskDataFactsFromSnapshot covers the B-group mapping: worst-quality
// folding, freshness, gaps and the account age, plus the fail-closed edges.
func TestRiskDataFactsFromSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	in := &RiskSnapshotFactsInput{
		Result:                   &SnapshotResult{Snapshot: riskFactsSnapshotFixture(now)},
		Now:                      now,
		MaxSnapshotAgeMs:         60_000,
		MaxAccountInventoryAgeMs: 5_000,
		AccountReconciled:        true,
	}

	facts, err := riskDataFactsFromSnapshot(in)
	require.NoError(t, err)
	assert.Equal(t, candidate.QualityExchangeREST, facts.SnapshotQuality,
		"account and funding are excluded from the market-quality fold")
	assert.Equal(t, int64(1234), facts.SnapshotAgeMs, "the age is the snapshot's own freshness figure")
	assert.Equal(t, int64(2000), facts.AccountInventoryAgeMs)
	assert.Equal(t, int64(60_000), facts.MaxSnapshotAgeMs)
	assert.Equal(t, int64(5_000), facts.MaxAccountInventoryAgeMs)
	assert.False(t, facts.CandleGap)
	assert.False(t, facts.OrderbookValid, "this fixture carries no book")
	assert.True(t, facts.AccountReconciled)

	// Local-tracked account labels and stale funding are excluded; a degraded
	// market category anywhere wins the fold.
	mixed := riskFactsSnapshotFixture(now)
	mixed.Quality[snapshot.CategoryAccount] = candidate.QualityLocalTracked
	mixed.Quality[snapshot.CategoryFunding] = candidate.QualityStale
	in.Result = &SnapshotResult{Snapshot: mixed}
	facts, err = riskDataFactsFromSnapshot(in)
	require.NoError(t, err)
	assert.Equal(t, candidate.QualityExchangeREST, facts.SnapshotQuality,
		"excluded categories cannot poison the market fold")

	stale := riskFactsSnapshotFixture(now)
	stale.Quality[snapshot.CategoryTicker] = candidate.QualityStale
	in.Result = &SnapshotResult{Snapshot: stale}
	facts, err = riskDataFactsFromSnapshot(in)
	require.NoError(t, err)
	assert.Equal(t, candidate.QualityStale, facts.SnapshotQuality)

	// Only excluded categories labelled: nothing market-side was assessed.
	onlyAccount := &snapshot.MarketSnapshot{
		ID: "snap-3", Account: &snapshot.AccountFacts{ReadAt: now},
		Quality: map[string]candidate.DataQuality{snapshot.CategoryAccount: candidate.QualityLocalTracked},
	}
	in.Result = &SnapshotResult{Snapshot: onlyAccount}
	facts, err = riskDataFactsFromSnapshot(in)
	require.NoError(t, err)
	assert.Equal(t, candidate.QualityUnknown, facts.SnapshotQuality)

	// An unlabelled snapshot is UNKNOWN, not trusted.
	in.Result = &SnapshotResult{Snapshot: &snapshot.MarketSnapshot{ID: "snap-2", Account: &snapshot.AccountFacts{ReadAt: now}}}
	facts, err = riskDataFactsFromSnapshot(in)
	require.NoError(t, err)
	assert.Equal(t, candidate.QualityUnknown, facts.SnapshotQuality)
}

// TestRiskDataFactsFromSnapshotRefusals pins the fail-closed edges.
func TestRiskDataFactsFromSnapshotRefusals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	valid := func() *RiskSnapshotFactsInput {
		return &RiskSnapshotFactsInput{
			Result: &SnapshotResult{Snapshot: riskFactsSnapshotFixture(now)},
			Now:    now, MaxSnapshotAgeMs: 60_000, MaxAccountInventoryAgeMs: 5_000,
		}
	}

	missing := valid()
	missing.Result = nil
	_, err := riskDataFactsFromSnapshot(missing)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noClock := valid()
	noClock.Now = time.Time{}
	_, err = riskDataFactsFromSnapshot(noClock)
	assert.ErrorIs(t, err, ErrSnapshotClockMissing)

	noLimits := valid()
	noLimits.MaxSnapshotAgeMs = 0
	_, err = riskDataFactsFromSnapshot(noLimits)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noAccount := valid()
	noAccount.Result.Snapshot.Account = nil
	_, err = riskDataFactsFromSnapshot(noAccount)
	assert.ErrorIs(t, err, ErrRiskFactsInput, "a decision without account facts is not decidable")

	noReadAt := valid()
	noReadAt.Result.Snapshot.Account.ReadAt = time.Time{}
	_, err = riskDataFactsFromSnapshot(noReadAt)
	assert.ErrorIs(t, err, snapshot.ErrMissingReadTime)

	fromFuture := valid()
	fromFuture.Result.Snapshot.Account.ReadAt = now.Add(time.Minute)
	_, err = riskDataFactsFromSnapshot(fromFuture)
	assert.ErrorIs(t, err, snapshot.ErrFactFromTheFuture)
}

// TestRiskAccountFactsFromSnapshot covers the C-group mapping.
func TestRiskAccountFactsFromSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	snap := riskFactsSnapshotFixture(now)
	src := &RiskAccountFactsInput{
		AccountID: "acct-1", TargetAccountID: "acct-1",
		MarginType: "CROSS", ExpectedMarginType: "CROSS",
		DayStartEquityUSD: new("9900"), ExternalFlowsUSD: new("0"),
		UnresolvedUnknownOrders: 1, UnreconciledPartials: 2,
	}

	account, positions, err := riskAccountFactsFromSnapshot(snap, src, now)
	require.NoError(t, err)
	assert.Equal(t, "10000", account.EquityUSD)
	assert.Equal(t, "8000", account.AvailableMarginUSD)
	assert.Equal(t, "CROSS", account.MarginType)
	require.NotNil(t, account.DayStartEquityUSD)
	assert.Equal(t, "9900", *account.DayStartEquityUSD)
	assert.Equal(t, 1, account.UnresolvedUnknownOrders)
	assert.Equal(t, 2, account.UnreconciledPartials)
	require.Len(t, positions, 1)
	assert.Equal(t, "BTC_USDT", positions[0].MarketID, "the position names its market in the candidate's spelling")
	assert.Equal(t, PositionSideLong, positions[0].Side)
	assert.Equal(t, "0.5", positions[0].Quantity)
	assert.Equal(t, "50000", positions[0].EntryPrice)
	assert.Equal(t, "50100", positions[0].MarkPrice)
	assert.False(t, positions[0].RestingOrder, "an exchange position is not a resting order")

	// Refusals.
	_, _, err = riskAccountFactsFromSnapshot(nil, src, now)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
	_, _, err = riskAccountFactsFromSnapshot(snap, nil, now)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	badSide := riskFactsSnapshotFixture(now)
	badSide.Account.Positions[0].Side = "SIDEWAYS"
	_, _, err = riskAccountFactsFromSnapshot(badSide, src, now)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	emptySize := riskFactsSnapshotFixture(now)
	emptySize.Account.Positions[0].Size = " "
	_, _, err = riskAccountFactsFromSnapshot(emptySize, src, now)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noMargin := *src
	noMargin.MarginType = ""
	_, _, err = riskAccountFactsFromSnapshot(snap, &noMargin, now)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	_, _, err = riskAccountFactsFromSnapshot(snap, src, time.Time{})
	assert.ErrorIs(t, err, ErrSnapshotClockMissing)
}
