package engine

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchange/accounts"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/fundingrate"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

type fakeToolSource struct {
	candles   *kline.Item
	book      *orderbook.Book
	subAccts  accounts.SubAccounts
	positions []futures.PositionDetails
	funding   []fundingrate.LatestRateResponse
	history   *fundingrate.HistoricalRates
	enabled   currency.Pairs
	available currency.Pairs
}

func (f *fakeToolSource) GetHistoricCandles(_ context.Context, _ currency.Pair, _ asset.Item, _ kline.Interval, _, _ time.Time) (*kline.Item, error) {
	return f.candles, nil
}

func (f *fakeToolSource) GetCachedOrderbook(_ currency.Pair, _ asset.Item) (*orderbook.Book, error) {
	return f.book, nil
}

func (f *fakeToolSource) GetCachedSubAccounts(_ context.Context, _ asset.Item) (accounts.SubAccounts, error) {
	return f.subAccts, nil
}

func (f *fakeToolSource) GetFuturesPositions(_ context.Context, _ *futures.PositionsRequest) ([]futures.PositionDetails, error) {
	return f.positions, nil
}

func (f *fakeToolSource) GetLatestFundingRates(_ context.Context, _ *fundingrate.LatestRateRequest) ([]fundingrate.LatestRateResponse, error) {
	return f.funding, nil
}

func (f *fakeToolSource) GetHistoricalFundingRates(_ context.Context, _ *fundingrate.HistoricalRatesRequest) (*fundingrate.HistoricalRates, error) {
	return f.history, nil
}

func (f *fakeToolSource) GetEnabledPairs(_ asset.Item) (currency.Pairs, error) {
	return f.enabled, nil
}

func (f *fakeToolSource) GetAvailablePairs(_ asset.Item) (currency.Pairs, error) {
	return f.available, nil
}

func testMarketID(base string) string {
	return marketIDFor("Binance", asset.USDTMarginedFutures, currency.NewPairWithDelimiter(base, "USDT", "_"))
}

// toolSourceFixture builds a source whose candles end exactly at the fixture
// clock, so the newest candle is live and the 50 before it are closed.
func toolSourceFixture() *fakeToolSource {
	interval := kline.OneHour
	candles := make([]kline.Candle, 0, 61)
	start := providerAdminNow.Add(-61 * time.Hour)
	for i := range 61 {
		closePrice := 100.0 + float64(i)
		candles = append(candles, kline.Candle{
			Time: start.Add(time.Duration(i) * time.Hour),
			Open: closePrice - 0.5, High: closePrice + 1, Low: closePrice - 1,
			Close: closePrice, Volume: 10,
		})
	}
	return &fakeToolSource{
		candles: &kline.Item{
			Pair:  currency.NewPairWithDelimiter("BTC", "USDT", "_"),
			Asset: asset.USDTMarginedFutures, Interval: interval, Candles: candles,
		},
		book: &orderbook.Book{
			Pair:  currency.NewPairWithDelimiter("BTC", "USDT", "_"),
			Asset: asset.USDTMarginedFutures,
			Bids:  orderbook.Levels{{Price: 99.5, Amount: 2}, {Price: 99, Amount: 5}},
			Asks:  orderbook.Levels{{Price: 100, Amount: 1}, {Price: 101, Amount: 2}},
		},
		subAccts: accounts.SubAccounts{},
		funding: []fundingrate.LatestRateResponse{{
			Asset: asset.USDTMarginedFutures,
			Pair:  currency.NewPairWithDelimiter("BTC", "USDT", "_"),
			LatestRate: fundingrate.Rate{
				Time: providerAdminNow.Add(-time.Hour),
			},
			TimeOfNextRate: providerAdminNow.Add(7 * time.Hour),
			TimeChecked:    providerAdminNow,
		}},
		history: &fundingrate.HistoricalRates{
			FundingRates: []fundingrate.Rate{{Time: providerAdminNow.Add(-8 * time.Hour)}},
		},
		enabled:   currency.Pairs{currency.NewPairWithDelimiter("BTC", "USDT", "_"), currency.NewPairWithDelimiter("ETH", "USDT", "_")},
		available: currency.Pairs{currency.NewPairWithDelimiter("BTC", "USDT", "_"), currency.NewPairWithDelimiter("ETH", "USDT", "_"), currency.NewPairWithDelimiter("SOL", "USDT", "_")},
	}
}

func newToolRunner(t *testing.T, scopeMarkets []string) *LLMToolRunner {
	t.Helper()
	scope := tools.NewScope(scopeMarkets, []string{"acct-1"}, []string{"ver-1"})
	runner, err := NewLLMToolRunner("Binance", asset.USDTMarginedFutures, "acct-1", scope, toolSourceFixture())
	require.NoError(t, err)
	runner.Now = func() time.Time { return providerAdminNow }
	return runner
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.Truef(t, ok, "expected a JSON object, got %T", v)
	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	require.Truef(t, ok, "expected a JSON array, got %T", v)
	return l
}

func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	require.Truef(t, ok, "expected a JSON string, got %T", v)
	return s
}

func decodeEvidence(t *testing.T, result string) map[string]any {
	t.Helper()
	var evidence map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &evidence))
	return evidence
}

func TestLLMToolsSupportedAndUnknown(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	for _, name := range tools.Names() {
		assert.True(t, runner.Supported(name), name)
	}
	assert.False(t, runner.Supported("rm_rf"))
	_, err := runner.Run(t.Context(), "call-1", "rm_rf", `{}`)
	assert.ErrorIs(t, err, tools.ErrUnknownTool)
}

func TestLLMToolsListMarkets(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	result, err := runner.Run(t.Context(), "call-1", tools.ToolListMarkets, `{"query":"sol"}`)
	require.NoError(t, err)
	evidence := decodeEvidence(t, result)
	data := asMap(t, evidence["data"])
	markets := asList(t, data["markets"])
	require.Len(t, markets, 1)
	assert.Equal(t, testMarketID("SOL"), asMap(t, markets[0])["marketId"])
	assert.Equal(t, "available", asMap(t, markets[0])["status"])

	result, err = runner.Run(t.Context(), "call-2", tools.ToolListMarkets, `{"maxItems":2}`)
	require.NoError(t, err)
	data = asMap(t, decodeEvidence(t, result)["data"])
	assert.Len(t, asList(t, data["markets"]), 2, "maxItems caps the list")
}

func TestLLMToolsCandleWindow(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	result, err := runner.Run(t.Context(), "call-1", tools.ToolGetCandleWindow,
		`{"marketId":"`+testMarketID("BTC")+`","interval":"1h","lookbackBars":100}`)
	require.NoError(t, err)
	evidence := decodeEvidence(t, result)
	assert.Equal(t, "EXCHANGE_REST", evidence["quality"])
	require.NotNil(t, evidence["marketId"])
	data := asMap(t, evidence["data"])
	candles := asList(t, data["candles"])
	require.Len(t, candles, 50, "the closed tail is capped at 50")
	last := asMap(t, candles[len(candles)-1])
	assert.Equal(t, "160", last["close"], "61 candles end at now; the newest closed one closes at 100+59")
	// The live candle (close 160, stamped at now) must not appear.
	assert.NotEqual(t, providerAdminNow.Format(time.RFC3339), last["time"])

	_, err = runner.Run(t.Context(), "call-2", tools.ToolGetCandleWindow,
		`{"marketId":"Kraken:usdtmarginedfutures:BTC_USDT","interval":"1h","lookbackBars":100}`)
	assert.ErrorIs(t, err, ErrToolArgumentsInvalid, "wrong exchange")

	_, err = runner.Run(t.Context(), "call-3", tools.ToolGetCandleWindow,
		`{"marketId":"`+testMarketID("ETH")+`","interval":"1h","lookbackBars":100}`)
	assert.ErrorIs(t, err, tools.ErrScopeDenied, "ETH is not in this run's scope")
}

func TestLLMToolsIndicators(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	result, err := runner.Run(t.Context(), "call-1", tools.ToolGetIndicators,
		`{"marketId":"`+testMarketID("BTC")+`","interval":"1h","indicators":["SMA","MACD","RSI","ATR","BBANDS"],"maPeriods":[20],"lookbackBars":100}`)
	require.NoError(t, err)
	data := asMap(t, decodeEvidence(t, result)["data"])
	series := asMap(t, data["indicators"])

	sma := asList(t, series["SMA"])
	require.NotEmpty(t, sma)
	assert.LessOrEqual(t, len(sma), toolIndicatorPointLimit)
	// Linear ramp 101..160: the last SMA(20) is the mean of 141..160 = 150.5.
	assert.Equal(t, "150.5", sma[len(sma)-1])

	for _, key := range []string{"MACD.dif", "MACD.dea", "MACD.histogram", "RSI", "ATR", "BBANDS.middle", "BBANDS.upper", "BBANDS.lower"} {
		points, ok := series[key].([]any)
		require.Truef(t, ok, "series %s must be present", key)
		require.NotEmpty(t, points, key)
		assert.LessOrEqual(t, len(points), toolIndicatorPointLimit, key)
		if len(points) > 0 {
			_, err := strconv.ParseFloat(asString(t, points[len(points)-1]), 64)
			assert.NoErrorf(t, err, "series values are decimal strings: %s", key)
		}
	}
	// On a strictly rising ramp the RSI must sit at its ceiling.
	rsi := asList(t, series["RSI"])
	assert.Equal(t, "100", rsi[len(rsi)-1])
	// Bollinger upper must sit above the middle on the same ramp.
	upper := asList(t, series["BBANDS.upper"])
	middle := asList(t, series["BBANDS.middle"])
	upperValue, err := strconv.ParseFloat(asString(t, upper[len(upper)-1]), 64)
	require.NoError(t, err)
	middleValue, err := strconv.ParseFloat(asString(t, middle[len(middle)-1]), 64)
	require.NoError(t, err)
	assert.Greater(t, upperValue, middleValue)
}

func TestLLMToolsOrderBook(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	result, err := runner.Run(t.Context(), "call-1", tools.ToolGetOrderBook,
		`{"marketId":"`+testMarketID("BTC")+`","depthLevels":10,"nominalUsd":"150"}`)
	require.NoError(t, err)
	data := asMap(t, decodeEvidence(t, result)["data"])
	assert.Len(t, asList(t, data["bids"]), 2)
	assert.Len(t, asList(t, data["asks"]), 2)
	assert.Equal(t, "99.75", data["midPrice"], "mid of 99.5 and 100")
	spread, err := strconv.ParseFloat(asString(t, data["spreadBps"]), 64)
	require.NoError(t, err)
	assert.InDelta(t, 50.125, spread, 0.001)

	slippage := asMap(t, data["slippage"])
	assert.Equal(t, "150", slippage["nominalUsd"])
	assert.Equal(t, "150", slippage["fillableUsd"], "the book fills the whole probe")
	avg, err := strconv.ParseFloat(asString(t, slippage["averagePrice"]), 64)
	require.NoError(t, err)
	// 100 USD at 100 plus 50 USD at 101 -> qty 1.495, avg ~= 100.334.
	assert.InDelta(t, 150.0/1.495049504950495, avg, 0.0001)
	slipBPS, err := strconv.ParseFloat(asString(t, slippage["slippageBps"]), 64)
	require.NoError(t, err)
	assert.Greater(t, slipBPS, 0.0)

	_, err = runner.Run(t.Context(), "call-2", tools.ToolGetOrderBook,
		`{"marketId":"`+testMarketID("BTC")+`","depthLevels":10,"nominalUsd":"abc"}`)
	assert.ErrorIs(t, err, ErrToolArgumentsInvalid)
}

func TestLLMToolsFunding(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	result, err := runner.Run(t.Context(), "call-1", tools.ToolGetFunding,
		`{"marketId":"`+testMarketID("BTC")+`"}`)
	require.NoError(t, err)
	data := asMap(t, decodeEvidence(t, result)["data"])
	assert.NotEmpty(t, asMap(t, data["latestRate"])["time"])
	assert.NotEmpty(t, data["nextRateTime"])
	assert.Nil(t, data["history"])

	result, err = runner.Run(t.Context(), "call-2", tools.ToolGetFunding,
		`{"marketId":"`+testMarketID("BTC")+`","includeHistory":true,"historyLimit":5}`)
	require.NoError(t, err)
	data = asMap(t, decodeEvidence(t, result)["data"])
	require.Len(t, asList(t, data["history"]), 1)
}

func TestLLMToolsAccountState(t *testing.T) {
	runner := newToolRunner(t, []string{testMarketID("BTC")})
	source, ok := runner.Source.(*fakeToolSource)
	require.True(t, ok)
	source.positions = []futures.PositionDetails{{
		Exchange: "Binance", Asset: asset.USDTMarginedFutures,
		Pair: currency.NewPairWithDelimiter("BTC", "USDT", "_"),
	}}
	result, err := runner.Run(t.Context(), "call-1", tools.ToolGetAccountState, `{"accountId":"acct-1"}`)
	require.NoError(t, err)
	evidence := decodeEvidence(t, result)
	assert.Equal(t, "acct-1", evidence["accountId"])
	data := asMap(t, evidence["data"])
	positions := asList(t, data["positionMarketIds"])
	require.Len(t, positions, 1)
	assert.Equal(t, testMarketID("BTC"), positions[0])

	_, err = runner.Run(t.Context(), "call-2", tools.ToolGetAccountState, `{"accountId":"acct-2"}`)
	assert.ErrorIs(t, err, tools.ErrScopeDenied, "another account is not this run's account")
}

func TestLLMToolsStrategyHistoryAndRiskBudget(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{ID: "start-1", Name: "trend", CreatedAt: providerAdminNow, CreatedBy: "op"}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "ver-1", StrategyID: "start-1", State: "APPROVED", SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))
	require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
		ID: "appr-1", StrategyVersionID: "ver-1", AccountID: "acct-1", Network: "testnet",
		ExecutionMode: "paper", ApprovedMarketIDs: `["BTC_USDT"]`, RiskEnvelopeJSON: `{"maxNotionalUsd":"1000"}`,
		RiskEnvelopeHash: "env", LLMProfileRevisionID: "rev-llm", JevProfileRevisionID: "rev-jev",
		ValidFrom: providerAdminNow, ValidUntil: providerAdminNow.Add(24 * time.Hour),
		ApprovedBy: "op", ApprovedAt: providerAdminNow,
	}))
	require.NoError(t, llmstore.InsertDecisionRun(ctx, &llmstore.DecisionRun{
		ID: "run-1", StrategyVersionID: new("ver-1"), Trigger: "MANUAL", RunMode: "PAPER", LLMProfileRevisionID: "rev-llm",
		LLMModel: "model-a", PromptVersion: "p1", Decision: "HOLD", Reason: "nothing to do",
		InputHash: "ee", CreatedAt: providerAdminNow,
	}))

	runner := newToolRunner(t, []string{testMarketID("BTC")})
	runner.Source = toolSourceFixture()

	result, err := runner.Run(ctx, "call-1", tools.ToolGetStrategyHistory, `{"strategyVersionId":"ver-1","maxItems":5}`)
	require.NoError(t, err)
	data := asMap(t, decodeEvidence(t, result)["data"])
	runs := asList(t, data["runs"])
	require.Len(t, runs, 1)
	assert.Equal(t, "HOLD", asMap(t, runs[0])["decision"])
	assert.Equal(t, "DATABASE_CACHED", decodeEvidence(t, result)["quality"])

	result, err = runner.Run(ctx, "call-2", tools.ToolGetRiskBudget, `{"strategyVersionId":"ver-1"}`)
	require.NoError(t, err)
	data = asMap(t, decodeEvidence(t, result)["data"])
	assert.Equal(t, "paper", data["executionMode"])
	assert.JSONEq(t, `{"maxNotionalUsd":"1000"}`, asString(t, data["riskEnvelopeJson"]))
	assert.Equal(t, false, data["revoked"])

	// A strategy version outside the scope is denied before any query result
	// could leak: the scope gate names the offending id.
	_, err = runner.Run(ctx, "call-3", tools.ToolGetStrategyHistory, `{"strategyVersionId":"ver-9","maxItems":5}`)
	assert.ErrorIs(t, err, tools.ErrScopeDenied)
}
