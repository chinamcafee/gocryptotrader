package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

func TestSpecsIncludeProposeAction(t *testing.T) {
	t.Parallel()
	names := Names()
	assert.Contains(t, names, ToolProposeAction)
	specs := Specs()
	spec, ok := findSpec(specs, ToolProposeAction)
	require.True(t, ok, "the proposal tool must be part of the catalogue")
	assert.Contains(t, string(spec.Function.Parameters), candidate.ActionStartOpenDecisionBatch)
	assert.Contains(t, string(spec.Function.Parameters), candidate.ActionCancelBatchJob)
}

func findSpec(specs []openai.ToolSpec, name string) (openai.ToolSpec, bool) {
	for i := range specs {
		if specs[i].Function.Name == name {
			return specs[i], true
		}
	}
	return openai.ToolSpec{}, false
}

func TestDecodeProposeActionShapes(t *testing.T) {
	t.Parallel()

	proposal, err := DecodeProposeAction(`{
		"action":"START_OPEN_DECISION_BATCH",
		"summary":"对两个市场生成开仓决策",
		"marketIds":["Binance:usdtmarginedfutures:BTC_USDT","Binance:usdtmarginedfutures:ETH_USDT","Binance:usdtmarginedfutures:BTC_USDT"],
		"strategyVersionId":"version-live",
		"actor":" op "
	}`)
	require.NoError(t, err)
	assert.Equal(t, candidate.ActionStartOpenDecisionBatch, proposal.Action)
	assert.Equal(t, []string{"Binance:usdtmarginedfutures:BTC_USDT", "Binance:usdtmarginedfutures:ETH_USDT"}, proposal.MarketIDs, "duplicates collapse")
	assert.Equal(t, "op", proposal.Actor)

	proposal, err = DecodeProposeAction(`{"action":"start_position_review","summary":"全部持仓","allPositions":true,"strategyVersionId":"version-live"}`)
	require.NoError(t, err)
	assert.True(t, proposal.AllPositions)
	assert.Empty(t, proposal.MarketIDs)

	proposal, err = DecodeProposeAction(`{"action":"SET_MARKET_TRACKED","summary":"启用","marketId":"Binance:usdtmarginedfutures:SOL_USDT","tracked":true}`)
	require.NoError(t, err)
	require.NotNil(t, proposal.Tracked)
	assert.True(t, *proposal.Tracked)

	proposal, err = DecodeProposeAction(`{"action":"CANCEL_BATCH_JOB","summary":"停止作业","jobId":"job-1"}`)
	require.NoError(t, err)
	assert.Equal(t, "job-1", proposal.JobID)
}

func TestDecodeProposeActionRejections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"unknown action", `{"action":"DELETE_EVERYTHING","summary":"x"}`},
		{"missing summary", `{"action":"CANCEL_BATCH_JOB","jobId":"j"}`},
		{"batch without markets", `{"action":"START_OPEN_DECISION_BATCH","summary":"x","strategyVersionId":"v"}`},
		{"batch without version", `{"action":"START_OPEN_DECISION_BATCH","summary":"x","marketIds":["m"]}`},
		{"batch with foreign field", `{"action":"START_OPEN_DECISION_BATCH","summary":"x","marketIds":["m"],"strategyVersionId":"v","jobId":"j"}`},
		{"review with both scopes", `{"action":"START_POSITION_REVIEW","summary":"x","allPositions":true,"marketIds":["m"],"strategyVersionId":"v"}`},
		{"review with neither scope", `{"action":"START_POSITION_REVIEW","summary":"x","strategyVersionId":"v"}`},
		{"tracked without state", `{"action":"SET_MARKET_TRACKED","summary":"x","marketId":"m"}`},
		{"tracked with markets", `{"action":"SET_MARKET_TRACKED","summary":"x","marketId":"m","tracked":true,"marketIds":["m"]}`},
		{"cancel without job", `{"action":"CANCEL_BATCH_JOB","summary":"x"}`},
		{"unknown field", `{"action":"CANCEL_BATCH_JOB","summary":"x","jobId":"j","extra":1}`},
		{"too many markets", `{"action":"START_OPEN_DECISION_BATCH","summary":"x","strategyVersionId":"v","marketIds":["1","2","3","4","5","6","7","8","9","10","11","12","13","14","15","16","17","18","19","20","21","22","23","24","25","26","27","28","29","30","31","32","33","34","35","36","37","38","39","40","41","42","43","44","45","46","47","48","49","50","51"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeProposeAction(tc.raw)
			require.ErrorIs(t, err, ErrArgumentsInvalid)
		})
	}
}
