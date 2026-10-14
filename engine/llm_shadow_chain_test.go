package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/common/key"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchange/order/limits"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// TestShadowRiskInputChainComposes is the composed smoke chain pinned by the
// session-51 reconnaissance (workLog tail 38): the seven mappings and the two
// assemblers interlock on one literal material set and the engine table runs
// on the assembled RiskInput. The assertion is composition -- the table ran
// (Checks non-empty) and nothing was refused as a malformed request
// (RiskInputInvalid); domain rejections from the deliberately sparse fixture
// are tolerable, because this proves the parts bite together, not that every
// gate lets the order through.
//
// The prospective order is a literal LIMIT (OrderType/Price/NotionalUSD): the
// production spelling of those three fields is pinned by decisions/02 and is
// deliberately not invented here.
func TestShadowRiskInputChainComposes(t *testing.T) {
	setupProviderAdminDB(t)
	const venue = "ChainComposeVenue"
	ctx := t.Context()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	item := asset.USDTMarginedFutures
	pair := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	require.NoError(t, limits.Load([]limits.MinMaxLevel{{
		Key:                     key.NewExchangeAssetPair(venue, item, pair),
		PriceStepIncrementSize:  0.1,
		AmountStepIncrementSize: 0.001,
		MinimumBaseAmount:       0.002,
		MinNotional:             5,
	}}))

	cand := &candidate.Candidate{
		Action:                 candidate.ActionOpenLong,
		MarketID:               "BTC_USDT",
		ProposedMaxNotionalUSD: "20",
		SuggestedEntry:         "50000",
	}
	snap := chainSnapshotFixture(now)

	// (1) Authorization: the reviewed approval material (frozen policy plus a
	// live grant), carrying the chain's candidate so scope and grant match it.
	authIn := riskAuthFixture(t, now)
	authIn.Candidate = cand
	auth, err := riskAuthorizationFromState(authIn)
	require.NoError(t, err)
	var frozen candidate.ReviewablePolicy
	require.NoError(t, json.Unmarshal([]byte(authIn.FrozenJSON), &frozen))
	envelope := frozen.Risk

	// (2) Data facts: the snapshot's quality fold, freshness, ages and the
	// run's reconciliation fact.
	dataFacts, err := riskDataFactsFromSnapshot(&RiskSnapshotFactsInput{
		Result:                   &SnapshotResult{Snapshot: snap},
		Now:                      now,
		MaxSnapshotAgeMs:         RiskMaxSnapshotAgeMs,
		MaxAccountInventoryAgeMs: RiskMaxAccountInventoryAgeMs,
		AccountReconciled:        true,
	})
	require.NoError(t, err)

	// (3) Account facts: the repo-backed state slice reads the empty store
	// (an unsealed day-start is absent, not zero), and the venue-phase
	// material is the chain's literals -- margin type in the venue's own
	// spelling (margin.Multi).
	acctState, err := buildRiskAccountStateFacts(ctx, "acct-1", now)
	require.NoError(t, err)
	require.Nil(t, acctState.DayStartEquityUSD, "an unsealed day-start is a state, not an error")
	account, positions, err := riskAccountFactsFromSnapshot(snap, &RiskAccountFactsInput{
		AccountID:               "acct-1",
		TargetAccountID:         "acct-1",
		MarginType:              "multi",
		ExpectedMarginType:      "multi",
		DayStartEquityUSD:       acctState.DayStartEquityUSD,
		UnresolvedUnknownOrders: acctState.UnresolvedUnknownOrders,
		UnreconciledPartials:    acctState.UnreconciledPartials,
	}, now)
	require.NoError(t, err)

	// (4) Market facts: the book-derived touch and ATR ratio plus the
	// venue-phase literals; the slippage estimate is computed by its own
	// piece over a scripted walk.
	walk := func(_, _ string) (string, error) { return "50015", nil }
	slippage, err := estimatedSlippageBPS(walk, "ask", *envelope.MaxOrderNotionalUSD, snap.OrderBook.Asks[0].Price)
	require.NoError(t, err)
	marketFacts, err := riskMarketFactsFromSnapshot(snap, &RiskMarketFactsInput{
		Now: now, MarkPrice: "50001", IndexPrice: "50002",
		EstimatedSlippageBPS: slippage, QuoteVolumeRank: 12, FundingRequired: false,
	})
	require.NoError(t, err)

	// (5) System facts: halts/readiness/managers from the fixture, with the
	// mode aligned to the approval's -- a mismatch here would be a fixture
	// clash, not a composition signal.
	sysIn := riskSystemFixture()
	sysIn.Mode = LLMRunModeShadow
	systemFacts, err := riskSystemFactsFromState(sysIn)
	require.NoError(t, err)

	// (6) Frequency facts: the windows read the empty store and fold the
	// prospective order in; the cooldown has no configured duration yet, so
	// it reports elapsed (the assembly value table's 0=no-gate translation).
	freqFacts, err := riskFrequencyFactsFromState(ctx, &RiskFrequencyFactsInput{
		AccountID: "acct-1", StrategyVersionID: "ver-1", Action: cand.Action,
		Now: now, WindowMinutes: envelope.RiskWindowMinutes,
		MarketCooldownElapsed: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, freqFacts.NewRiskIntentsInWindow, "the empty window plus the prospective open")

	// (7) Contract spec from the venue's limits store.
	spec, err := buildContractSpec(venue, item, pair)
	require.NoError(t, err)

	riskIn, err := buildShadowRiskInput(&ShadowRiskInputMaterial{
		Now: now, Candidate: cand, Envelope: envelope,
		Authorization: *auth, Data: *dataFacts, Account: account, Positions: positions,
		Frequency: *freqFacts, Contract: *spec,
		Order:  RiskOrderFacts{OrderType: OrderTypeLimit, Price: "50000", NotionalUSD: "20"},
		Market: *marketFacts, System: *systemFacts, Switches: DefaultRiskSwitches(),
	})
	require.NoError(t, err, "the material must assemble; a refusal here is a chain break")

	// The engine table runs end to end, and the request itself is well-formed:
	// no RiskInputInvalid anywhere in the rejections.
	out := Evaluate(riskIn)
	assert.NotEmpty(t, out.Checks, "the engine table ran on the assembled input")
	for i := range out.Rejections {
		assert.NotEqual(t, RiskInputInvalid, out.Rejections[i].Code, "the chain should not assemble a malformed request")
	}

	// The sibling sizing chain builds from the same material (an open is
	// sized here; passage is not required, being able to build is).
	sizing, err := buildShadowSizingInput(&ShadowSizingMaterial{
		Envelope: envelope, Candidate: cand, Snapshot: snap, Positions: positions,
		Contract: *spec, Walk: walk, DailyLossUSD: "0",
	})
	require.NoError(t, err)
	assert.Equal(t, "25050", sizing.ExistingExposureUSD, "max(entry, mark) x quantity -- the risk engine's own spelling")
	assert.Equal(t, "1000", sizing.StopDistancePerUnitUSD, "max(1.5xATR 750, pct-of-entry 1000), tick-rounded away from the market")
	require.NotNil(t, sizing.Walk)
}

// chainSnapshotFixture is the one snapshot literal every link of the chain
// reads: market facts, data facts, account facts and the sizing stop.
func chainSnapshotFixture(now time.Time) *snapshot.MarketSnapshot {
	return &snapshot.MarketSnapshot{
		ID: "snap-chain", Exchange: "binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		SourceTime: now.Add(-time.Minute), BuiltAt: now, FreshnessMS: 1234,
		Quality: map[string]candidate.DataQuality{
			snapshot.CategoryCandles:   candidate.QualityExchangeREST,
			snapshot.CategoryOrderBook: candidate.QualityExchangeREST,
			snapshot.CategoryAccount:   candidate.QualityDatabaseCached,
		},
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
		Account: &snapshot.AccountFacts{
			TotalEquityUSD: "10000", AvailableBalanceUSD: "8000",
			ReadAt: now.Add(-2 * time.Second),
			Positions: []snapshot.PositionFact{
				{Symbol: "BTC_USDT", Side: PositionSideLong, Size: "0.5", EntryPrice: "50000", MarkPrice: "50100"},
			},
		},
	}
}
