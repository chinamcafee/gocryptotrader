package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
	"github.com/thrasher-corp/gocryptotrader/types"
)

type fakePremiumIndex struct {
	rows []binance.UMarkPrice
	err  error
}

func (f *fakePremiumIndex) GetName() string { return "Binance" }

func (f *fakePremiumIndex) UGetMarkPrice(context.Context, currency.Pair) ([]binance.UMarkPrice, error) {
	return f.rows, f.err
}

// TestBuildFundingMarkIndexFacts covers prerequisite ④: one premium-index read
// yields the funding facts (signed rate, settlement time, read time) plus the
// mark/index prices the market-facts mapping consumes.
func TestBuildFundingMarkIndexFacts(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	settles := now.Add(4 * time.Hour)
	pair := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	src := &fakePremiumIndex{rows: []binance.UMarkPrice{{
		Symbol:          "BTCUSDT",
		MarkPrice:       types.Number(100.5),
		IndexPrice:      types.Number(100.4),
		LastFundingRate: types.Number(-0.00012),
		NextFundingTime: types.Time(settles),
	}}}

	facts, err := buildFundingMarkIndexFacts(context.Background(), src, pair, now)
	require.NoError(t, err)
	assert.Equal(t, "-0.00012", facts.Funding.LatestRate, "funding rates are signed")
	assert.True(t, settles.Equal(facts.Funding.NextFundingTime))
	assert.True(t, now.Equal(facts.Funding.ReadAt))
	assert.Equal(t, "100.5", facts.MarkPrice)
	assert.Equal(t, "100.4", facts.IndexPrice)

	_, err = buildFundingMarkIndexFacts(context.Background(), nil, pair, now)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = buildFundingMarkIndexFacts(context.Background(), &fakePremiumIndex{err: errors.New("venue down")}, pair, now)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRiskFactsInput, "a venue failure is a read failure, not bad input")

	_, err = buildFundingMarkIndexFacts(context.Background(), &fakePremiumIndex{}, pair, now)
	require.ErrorIs(t, err, ErrRiskFactsInput, "an empty payload is unreadable")

	zeroMark := &fakePremiumIndex{rows: []binance.UMarkPrice{{IndexPrice: types.Number(100)}}}
	_, err = buildFundingMarkIndexFacts(context.Background(), zeroMark, pair, now)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a zero mark price must refuse, not feed the deviation check")

	zeroIndex := &fakePremiumIndex{rows: []binance.UMarkPrice{{MarkPrice: types.Number(100)}}}
	_, err = buildFundingMarkIndexFacts(context.Background(), zeroIndex, pair, now)
	require.ErrorIs(t, err, ErrRiskFactsInput)
}

// TestApplyFunding pins the snapshot seam: a supplied reading lands as the
// funding facts and is labelled EXCHANGE_REST (a direct venue read), while an
// absent reading leaves the category untouched.
func TestApplyFunding(t *testing.T) {
	funding := &snapshot.FundingFacts{LatestRate: "0.0001", ReadAt: time.Now()}

	built := &snapshot.MarketSnapshot{}
	require.NoError(t, applyFunding(built, &SnapshotRequest{Funding: funding}))
	assert.Equal(t, funding, built.Funding)
	assert.Equal(t, "EXCHANGE_REST", string(built.QualityOf("funding")))

	untouched := &snapshot.MarketSnapshot{}
	require.NoError(t, applyFunding(untouched, &SnapshotRequest{}))
	assert.Nil(t, untouched.Funding)
	assert.Equal(t, "UNKNOWN", string(untouched.QualityOf("funding")), "an absent reading is unassessed, not trusted")
}
