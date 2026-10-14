package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// M4-11 收官测试：订单事实（02=B：开仓 LIMIT/平仓 MARKET）、保证金词汇翻译、
// 盘口转 Depth、走价方向。

func materialTestContract() RiskContractSpec {
	return RiskContractSpec{TickSize: "0.1", StepSize: "0.001", MinQuantity: "0.001", MinNotionalUSD: "5"}
}

func materialTestSnapshot() *snapshot.MarketSnapshot {
	return &snapshot.MarketSnapshot{
		OrderBook: &snapshot.OrderBookFacts{
			Bids: []snapshot.OrderBookLevel{{Price: "49990", Size: "2"}, {Price: "49980", Size: "3"}},
			Asks: []snapshot.OrderBookLevel{{Price: "50010", Size: "2"}, {Price: "50020", Size: "3"}},
		},
		Ticker: &snapshot.TickerFacts{Last: "50000", Bid: "49995", Ask: "50005"},
	}
}

func TestShadowOrderFactsOpenLimit(t *testing.T) {
	t.Parallel()
	cap20 := "20"
	proposed35 := "35"
	proposed15 := "15"
	bps20 := 20

	for _, tc := range []struct {
		name         string
		candidate    *candidate.Candidate
		envelope     candidate.RiskEnvelope
		wantPrice    string
		wantNotional string
		wantHasStop  bool
		wantErr      bool
	}{
		{
			name:      "long floors the suggested entry to the tick",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50000.05", ProposedMaxNotionalUSD: "15"},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20},
			wantPrice: "50000", wantNotional: "15",
		},
		{
			name:      "short ceils the suggested entry to the tick",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenShort, SuggestedEntry: "50000.05", ProposedMaxNotionalUSD: "15"},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20},
			wantPrice: "50000.1", wantNotional: "15",
		},
		{
			name:      "the notional bound is the min of candidate and approved cap",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50000", ProposedMaxNotionalUSD: proposed35},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20},
			wantPrice: "50000", wantNotional: "20",
		},
		{
			name:      "a candidate bound below the cap stands",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50000", ProposedMaxNotionalUSD: proposed15},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20},
			wantPrice: "50000", wantNotional: "15",
		},
		{
			name:      "no approved cap leaves the candidate bound",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50000", ProposedMaxNotionalUSD: proposed15},
			envelope:  candidate.RiskEnvelope{},
			wantPrice: "50000", wantNotional: "15",
		},
		{
			name:      "the protective stop is carried when the envelope requires it",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50000", ProposedMaxNotionalUSD: "15"},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20, RequiresProtectiveExit: true},
			wantPrice: "50000", wantNotional: "15", wantHasStop: true,
		},
		{
			name:      "a buy priced above the ask plus slippage is refused",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "50120", ProposedMaxNotionalUSD: "15"},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20, MaxSlippageBPS: &bps20},
			wantErr:   true,
		},
		{
			name:      "an open without an entry cannot be priced",
			candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, ProposedMaxNotionalUSD: "15"},
			envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: &cap20},
			wantErr:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := shadowOrderFacts(&shadowOrderMaterial{
				Candidate: tc.candidate, Envelope: tc.envelope, Contract: materialTestContract(),
				Snapshot: materialTestSnapshot(), BestBid: "49990", BestAsk: "50010",
			})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, OrderTypeLimit, facts.OrderType)
			assert.Equal(t, tc.wantPrice, facts.Price)
			assert.Equal(t, tc.wantNotional, facts.NotionalUSD)
			assert.Equal(t, tc.wantHasStop, facts.HasProtectiveExit)
			assert.False(t, facts.ConflictsWithResting)
		})
	}
}

func TestShadowOrderFactsReduceTakesTheBook(t *testing.T) {
	t.Parallel()
	market := "Binance:usdtmarginedfutures:BTC_USDT"
	positions := []RiskPositionFact{{
		MarketID: market, Side: PositionSideLong, Quantity: "0.5", EntryPrice: "49000", MarkPrice: "50000",
	}}
	facts, err := shadowOrderFacts(&shadowOrderMaterial{
		Candidate: &candidate.Candidate{Action: candidate.ActionReduce, MarketID: market},
		Contract:  materialTestContract(), Snapshot: materialTestSnapshot(),
		Positions: positions, BestBid: "49990", BestAsk: "50010",
	})
	require.NoError(t, err)
	assert.Equal(t, OrderTypeMarket, facts.OrderType, "active exits take the book (decisions/02 B)")
	assert.Empty(t, facts.Price, "a MARKET order carries no price")
	assert.Equal(t, "0.5", facts.Quantity, "the reduce covers the existing position")
	assert.Equal(t, "25000", facts.NotionalUSD, "max(entry, mark) x quantity -- the conservative spelling")

	// A reduce with no position leaves the shape unreadable: the engine's own
	// min-quantity checks record the refusal instead of a fabricated size.
	facts, err = shadowOrderFacts(&shadowOrderMaterial{
		Candidate: &candidate.Candidate{Action: candidate.ActionReduce, MarketID: market},
		Contract:  materialTestContract(), Snapshot: materialTestSnapshot(),
	})
	require.NoError(t, err)
	assert.Empty(t, facts.Quantity)
	assert.Empty(t, facts.NotionalUSD)
}

func TestShadowOrderFactsRefusesNonOrderActions(t *testing.T) {
	t.Parallel()
	_, err := shadowOrderFacts(&shadowOrderMaterial{
		Candidate: &candidate.Candidate{Action: candidate.ActionHold},
		Contract:  materialTestContract(), Snapshot: materialTestSnapshot(),
	})
	require.ErrorIs(t, err, ErrRiskFactsInput)
}

func TestVenueMarginTypeTranslation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "CROSS", want: "multi"},
		{in: "cross", want: "multi"},
		{in: "ISOLATED", want: "isolated"},
		{in: "multi", want: "multi"},
		{in: "", wantErr: true},
		{in: "turbo", wantErr: true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := venueMarginType(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDepthFromSnapshotBookWalksTheBook(t *testing.T) {
	t.Parallel()
	pair := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	depth, err := depthFromSnapshotBook(materialTestSnapshot().OrderBook, "Binance", pair, asset.USDTMarginedFutures)
	require.NoError(t, err)
	walk := sizingWalkFromDepth(depth)
	price, err := walk("ask", "20")
	require.NoError(t, err)
	assert.Equal(t, "50010", price, "twenty dollars of asks sits in the top level")

	_, err = depthFromSnapshotBook(nil, "Binance", pair, asset.USDTMarginedFutures)
	require.ErrorIs(t, err, ErrRiskFactsInput)
	_, err = depthFromSnapshotBook(&snapshot.OrderBookFacts{Bids: materialTestSnapshot().OrderBook.Bids}, "Binance", pair, asset.USDTMarginedFutures)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a one-sided book cannot be walked")
}

func TestShadowSizingSideMirrorsTheAction(t *testing.T) {
	t.Parallel()
	market := "Binance:usdtmarginedfutures:BTC_USDT"
	assert.Equal(t, "ask", shadowSizingSide(&candidate.Candidate{Action: candidate.ActionOpenLong}, nil))
	assert.Equal(t, "bid", shadowSizingSide(&candidate.Candidate{Action: candidate.ActionOpenShort}, nil))
	assert.Equal(t, "bid", shadowSizingSide(&candidate.Candidate{Action: candidate.ActionReduce, MarketID: market},
		[]RiskPositionFact{{MarketID: market, Side: PositionSideLong}}))
	assert.Equal(t, "ask", shadowSizingSide(&candidate.Candidate{Action: candidate.ActionReduce, MarketID: market},
		[]RiskPositionFact{{MarketID: market, Side: PositionSideShort}}))
	assert.Equal(t, "ask", shadowSizingSide(&candidate.Candidate{Action: candidate.ActionReduce, MarketID: market}, nil))
}
