package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// 对话 · 开盘机会评估（用户要求）：逐市场问 Jev 是否有机会，判 open 的再问一次
// 方向；两次都是有界 choice，预算逐次预约/结算。

type fakeJevBoundedJudge struct {
	calls []*jev.SystemOneRequest
}

func (f *fakeJevBoundedJudge) Judge(_ context.Context, req *jev.SystemOneRequest) (*jev.AnswerSet, error) {
	f.calls = append(f.calls, req)
	state, _ := req.State.(*assessState)
	for key := range req.Questions {
		switch key {
		case assessQuestionOpportunity:
			choice := "no_trade"
			probabilities := map[string]float64{"open": 0.1, "no_trade": 0.9}
			if state != nil && state.Market == "Binance:usdtmarginedfutures:BTC_USDT" {
				choice = "open"
				probabilities = map[string]float64{"open": 0.8, "no_trade": 0.2}
			}
			return &jev.AnswerSet{
				Choices: map[string]*jev.ChoiceAnswer{key: {
					Type: jev.AnswerChoice, Choice: choice, Probabilities: probabilities, Confidence: 0.8,
				}},
				Usage: jev.Usage{InputTokens: 12, OutputTokens: 3},
			}, nil
		case assessQuestionDirection:
			return &jev.AnswerSet{
				Choices: map[string]*jev.ChoiceAnswer{key: {
					Type: jev.AnswerChoice, Choice: "long",
					Probabilities: map[string]float64{"long": 0.7, "short": 0.3}, Confidence: 0.7,
				}},
				Usage: jev.Usage{InputTokens: 8, OutputTokens: 2},
			}, nil
		}
	}
	return nil, errors.New("unexpected question")
}

func TestAssessOpeningOpportunitiesRunsTwoJevCalls(t *testing.T) {
	runner := screenerRunner(t)
	judge := &fakeJevBoundedJudge{}
	budget := &chatTestBudget{}
	runner.JevJudge = judge
	runner.JevModel = "jev-latest"
	runner.JevRevisionID = "rev-jev"
	runner.JevBudget = budget

	var updates []ToolProgress
	ctx := withToolProgress(t.Context(), func(progress ToolProgress) { updates = append(updates, progress) })
	raw, err := runner.Run(ctx, "call-1", tools.ToolAssessOpening, `{"markets":["Binance:usdtmarginedfutures:BTC_USDT","Binance:usdtmarginedfutures:ETH_USDT"],"horizon":"4-8h"}`)
	require.NoError(t, err)

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &envelope))
	var out struct {
		Horizon string      `json:"horizon"`
		Rows    []assessRow `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &out))
	assert.Equal(t, "4-8h", out.Horizon)
	require.Len(t, out.Rows, 2)
	assert.Equal(t, "OPEN", out.Rows[0].Opportunity)
	assert.Equal(t, "LONG", out.Rows[0].Direction)
	assert.Equal(t, "NO_TRADE", out.Rows[1].Opportunity)
	assert.Empty(t, out.Rows[1].Direction, "no direction call for a no-trade verdict")

	assert.Len(t, judge.calls, 3, "one opportunity call per market plus one direction call for the open one")
	assert.Equal(t, 3, budget.reserved)
	assert.Equal(t, 3, budget.marked)
	assert.Equal(t, "CHAT", budget.lastPurpose)
	require.NotEmpty(t, updates)
	assert.Equal(t, "JEV", updates[len(updates)-1].Phase)

	_, err = runner.Run(t.Context(), "call-2", tools.ToolAssessOpening,
		`{"markets":["Binance:usdtmarginedfutures:SOL_USDT"],"horizon":"4-8h"}`)
	require.ErrorIs(t, err, ErrToolArgumentsInvalid, "a market outside the run scope must refuse")
}
