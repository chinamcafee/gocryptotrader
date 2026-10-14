package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// riskMarketFixture is a snapshot carrying book, ticker, funding and ATR.
func riskMarketFixture(now time.Time) *snapshot.MarketSnapshot {
	return &snapshot.MarketSnapshot{
		ID: "snap-mkt", Pair: "BTC_USDT",
		OrderBook: &snapshot.OrderBookFacts{
			Bids: []snapshot.OrderBookLevel{{Price: "49990", Size: "1"}},
			Asks: []snapshot.OrderBookLevel{{Price: "50010", Size: "1"}},
		},
		Ticker: &snapshot.TickerFacts{
			Last: "50000", Bid: "49995", Ask: "50005",
		},
		Funding: &snapshot.FundingFacts{
			LatestRate:      "0.0001",
			NextFundingTime: now.Add(45 * time.Minute),
		},
		Indicators: snapshot.IndicatorSet{
			ATR: &snapshot.SeriesFact{Period: 14, Latest: "500"},
		},
	}
}

// TestRiskMarketFactsFromSnapshot covers the D-group mapping: book-first
// touch, the derived ATR ratio, the funding countdown, and the explicit
// venue-phase inputs passing through untouched.
func TestRiskMarketFactsFromSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	in := &RiskMarketFactsInput{
		Now: now, MarkPrice: "50001", IndexPrice: "50002",
		EstimatedSlippageBPS: 7, QuoteVolumeRank: 12, FundingRequired: true,
	}
	facts, err := riskMarketFactsFromSnapshot(riskMarketFixture(now), in)
	require.NoError(t, err)
	assert.Equal(t, "49990", facts.BestBid, "the book's top levels win over the ticker")
	assert.Equal(t, "50010", facts.BestAsk)
	assert.Equal(t, "50000", facts.LastPrice)
	assert.Equal(t, "50001", facts.MarkPrice)
	assert.Equal(t, "50002", facts.IndexPrice)
	assert.Equal(t, 7, facts.EstimatedSlippageBPS)
	assert.Equal(t, 12, facts.QuoteVolumeRank)
	assert.True(t, facts.DepthAvailable)
	assert.True(t, facts.FundingRequired)
	assert.InDelta(t, 0.01, facts.ATRPercentOfPrice, 1e-9, "ATR 500 over a 50000 price is one percent")
	require.NotNil(t, facts.FundingRate)
	assert.Equal(t, "0.0001", *facts.FundingRate)
	assert.Equal(t, 45, facts.MinutesToFundingSettlement)

	// Without a book the ticker's touch is the next-best market reading.
	noBook := riskMarketFixture(now)
	noBook.OrderBook = nil
	facts, err = riskMarketFactsFromSnapshot(noBook, in)
	require.NoError(t, err)
	assert.Equal(t, "49995", facts.BestBid)
	assert.Equal(t, "50005", facts.BestAsk)
	assert.False(t, facts.DepthAvailable, "no book means the depth gate fails closed")

	// A funding requirement without a funding reading stays nil: the check
	// rejects unreadable itself.
	noFunding := riskMarketFixture(now)
	noFunding.Funding = nil
	facts, err = riskMarketFactsFromSnapshot(noFunding, in)
	require.NoError(t, err)
	assert.Nil(t, facts.FundingRate)
	assert.Zero(t, facts.MinutesToFundingSettlement)
}

// TestRiskMarketFactsFromSnapshotRefusals pins the fail-closed edges: a
// volatility gate must never evaluate an unmeasured market.
func TestRiskMarketFactsFromSnapshotRefusals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	in := &RiskMarketFactsInput{Now: now}

	_, err := riskMarketFactsFromSnapshot(nil, in)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = riskMarketFactsFromSnapshot(riskMarketFixture(now), nil)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noATR := riskMarketFixture(now)
	noATR.Indicators.ATR = nil
	_, err = riskMarketFactsFromSnapshot(noATR, in)
	assert.ErrorIs(t, err, ErrRiskFactsInput, "ATR is required, not defaulted to zero")

	badATR := riskMarketFixture(now)
	badATR.Indicators.ATR.Latest = "not-a-number"
	_, err = riskMarketFactsFromSnapshot(badATR, in)
	assert.ErrorIs(t, err, ErrRiskFactsInput)

	noLast := riskMarketFixture(now)
	noLast.Ticker = nil
	_, err = riskMarketFactsFromSnapshot(noLast, in)
	assert.ErrorIs(t, err, ErrRiskFactsInput, "the ratio needs a price anchor")

	zeroLast := riskMarketFixture(now)
	zeroLast.Ticker.Last = "0"
	_, err = riskMarketFactsFromSnapshot(zeroLast, in)
	assert.ErrorIs(t, err, ErrRiskFactsInput)
}
