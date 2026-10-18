package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

func TestSpecsShape(t *testing.T) {
	specs := Specs()
	require.Len(t, specs, 11)
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		assert.Equal(t, "function", spec.Type)
		assert.NotEmpty(t, spec.Function.Description)
		require.Falsef(t, seen[spec.Function.Name], "duplicate tool %s", spec.Function.Name)
		seen[spec.Function.Name] = true

		var schema map[string]any
		require.NoErrorf(t, json.Unmarshal(spec.Function.Parameters, &schema), "parameters of %s must be a JSON object", spec.Function.Name)
		assert.Equal(t, "object", schema["type"])
		assert.Equalf(t, false, schema["additionalProperties"], "every tool should forbid extra fields: %s", spec.Function.Name)
	}

	assert.ElementsMatch(t, []string{
		"listMarkets", "getCandleWindow", "getIndicators", "getOrderBookSummary",
		"getFunding", "getAccountState", "getStrategyHistory", "getRiskBudget",
		"screenMarkets", "proposeAction", "assessOpeningOpportunities",
	}, Names())
}

func TestDecodeStrictRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	_, err := DecodeListMarkets(`{"query":"BTC","sneaky":true}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)

	_, err = DecodeListMarkets(`{"query":"BTC"} {"query":"ETH"}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)

	_, err = DecodeCandleWindow(`{"marketId":"m","interval":"1h","lookbackBars":10,"whoami":"root"}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
}

func TestCandleWindowValidation(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"happy", `{"marketId":"Binance:usdtmarginedfutures:BTC_USDT","interval":"1h","lookbackBars":100}`, false},
		{"missing market", `{"interval":"1h","lookbackBars":100}`, true},
		{"bad interval", `{"marketId":"m","interval":"2h","lookbackBars":100}`, true},
		{"zero lookback", `{"marketId":"m","interval":"1h","lookbackBars":0}`, true},
		{"lookback beyond request cap", `{"marketId":"m","interval":"1h","lookbackBars":201}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := DecodeCandleWindow(tc.raw)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrArgumentsInvalid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 100, args.LookbackBars)
		})
	}
}

func TestIndicatorsValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty indicators", `{"marketId":"m","interval":"1h","indicators":[],"lookbackBars":40}`},
		{"too many indicators", `{"marketId":"m","interval":"1h","indicators":["MACD","SMA","EMA","RSI","ATR","BBANDS","SMA"],"lookbackBars":500}`},
		{"unknown indicator", `{"marketId":"m","interval":"1h","indicators":["ICHIMOKU"],"lookbackBars":40}`},
		{"duplicate indicator", `{"marketId":"m","interval":"1h","indicators":["SMA","SMA"],"lookbackBars":40}`},
		{"lookback below floor", `{"marketId":"m","interval":"1h","indicators":["SMA"],"lookbackBars":39}`},
		{"lookback beyond cap", `{"marketId":"m","interval":"1h","indicators":["SMA"],"lookbackBars":501}`},
		{"macd fast not before slow", `{"marketId":"m","interval":"1h","indicators":["MACD"],"macdParams":{"fast":26,"slow":26,"signal":9},"lookbackBars":40}`},
		{"macd slow out of range", `{"marketId":"m","interval":"1h","indicators":["MACD"],"macdParams":{"fast":12,"slow":501,"signal":9},"lookbackBars":500}`},
		{"ma period out of range", `{"marketId":"m","interval":"1h","indicators":["SMA"],"maPeriods":[1],"lookbackBars":40}`},
		{"lookback does not cover ma period", `{"marketId":"m","interval":"1h","indicators":["SMA"],"maPeriods":[200],"lookbackBars":100}`},
		{"lookback does not cover macd", `{"marketId":"m","interval":"1h","indicators":["MACD"],"lookbackBars":40}`},
	}
	// The last case needs lookback < slow+signal-2 for a *custom* MACD.
	tests[len(tests)-1].raw = `{"marketId":"m","interval":"1h","indicators":["MACD"],"macdParams":{"fast":50,"slow":200,"signal":50},"lookbackBars":100}`

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeIndicators(tc.raw)
			assert.ErrorIs(t, err, ErrArgumentsInvalid)
		})
	}

	// Default MACD (12/26/9 -> needs 33 bars) inside the 40-bar floor.
	args, err := DecodeIndicators(`{"marketId":"m","interval":"4h","indicators":["MACD"],"lookbackBars":40}`)
	require.NoError(t, err)
	assert.Equal(t, 40, args.LookbackBars)

	// Long MA with a lookback that covers it.
	args, err = DecodeIndicators(`{"marketId":"m","interval":"1d","indicators":["SMA","MACD"],"maPeriods":[200],"lookbackBars":250}`)
	require.NoError(t, err)
	assert.Contains(t, args.Indicators, "SMA")
}

func TestOrderBookFundingAccountStrategyRiskValidation(t *testing.T) {
	_, err := DecodeOrderBook(`{"marketId":"m","depthLevels":101}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	_, err = DecodeOrderBook(`{"marketId":"m","depthLevels":0}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	_, err = DecodeOrderBook(`{"marketId":"m","depthLevels":20,"nominalUsd":"-5"}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	_, err = DecodeOrderBook(`{"marketId":"m","depthLevels":20,"nominalUsd":"0"}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid, "a zero notional is not a slippage probe")
	_, err = DecodeOrderBook(`{"marketId":"m","depthLevels":20,"nominalUsd":"abc"}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	book, err := DecodeOrderBook(`{"marketId":"m","depthLevels":20,"nominalUsd":"5000.50"}`)
	require.NoError(t, err)
	assert.Equal(t, "5000.50", book.NominalUSD)

	_, err = DecodeFunding(`{"marketId":"m","historyLimit":5}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid, "historyLimit without includeHistory")
	_, err = DecodeFunding(`{"marketId":"m","includeHistory":true}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid, "history requested without a limit")
	_, err = DecodeFunding(`{"marketId":"m","includeHistory":true,"historyLimit":22}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	funding, err := DecodeFunding(`{"marketId":"m","includeHistory":true,"historyLimit":21}`)
	require.NoError(t, err)
	assert.True(t, funding.IncludeHistory)

	_, err = DecodeAccountState(`{"accountId":"  "}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)

	_, err = DecodeStrategyHistory(`{"strategyVersionId":"v","maxItems":21}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	history, err := DecodeStrategyHistory(`{"strategyVersionId":"v"}`)
	require.NoError(t, err)
	assert.Equal(t, MaxStrategyItems, history.MaxItems, "maxItems defaults to the cap")

	_, err = DecodeRiskBudget(`{}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	_, err = DecodeRiskBudget(`{"strategyVersionId":"v"}`)
	require.NoError(t, err)

	list, err := DecodeListMarkets(`{}`)
	require.NoError(t, err)
	assert.Equal(t, MaxMarketItems, list.MaxItems, "maxItems defaults to the cap")
	list, err = DecodeListMarkets(`{"query":"BTC","maxItems":5}`)
	require.NoError(t, err)
	assert.Equal(t, 5, list.MaxItems)
	_, err = DecodeListMarkets(`{"maxItems":26}`)
	assert.ErrorIs(t, err, ErrArgumentsInvalid)
	longQuery := make([]byte, 0, MaxQueryChars+8)
	longQuery = append(longQuery, `{"query":"`...)
	for range MaxQueryChars + 1 {
		longQuery = append(longQuery, 'x')
	}
	longQuery = append(longQuery, `"}`...)
	_, err = DecodeListMarkets(string(longQuery))
	assert.ErrorIs(t, err, ErrArgumentsInvalid, "query beyond 80 characters")
}

func TestScopeLocking(t *testing.T) {
	scope := NewScope(
		[]string{"Binance:usdtmarginedfutures:BTC_USDT"},
		[]string{"acct-1"},
		[]string{"ver-1"},
	)

	allowed := "Binance:usdtmarginedfutures:BTC_USDT"
	denied := "Binance:usdtmarginedfutures:ETH_USDT"
	require.NoError(t, scope.Allows(&allowed, nil, nil))
	assert.ErrorIs(t, scope.Allows(&denied, nil, nil), ErrScopeDenied)
	assert.ErrorIs(t, scope.Allows(nil, new("acct-2"), nil), ErrScopeDenied)
	assert.ErrorIs(t, scope.Allows(nil, nil, new("ver-9")), ErrScopeDenied)
	require.NoError(t, scope.Allows(nil, new("acct-1"), new("ver-1")))
	require.NoError(t, scope.Allows(nil, nil, nil), "evidence naming nothing is not a scope violation")

	var nilScope *Scope
	assert.ErrorIs(t, nilScope.Allows(nil, nil, nil), ErrScopeDenied, "a missing scope is closed, not open")

	evidence := &candidate.ToolEvidence{
		Ref: "ev-1", Quality: candidate.QualityExchangeREST,
		MarketID: &allowed,
	}
	require.NoError(t, scope.CheckEvidence(evidence))
	evidence.MarketID = &denied
	assert.ErrorIs(t, scope.CheckEvidence(evidence), ErrScopeDenied)
	assert.ErrorIs(t, scope.CheckEvidence(nil), ErrScopeDenied)
}
