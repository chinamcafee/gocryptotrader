package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// 对话配置提案（docs/llm-dev/16）：proposeAction 只做参数/作用域校验并归一化，
// 产出惰性草案；执行永远发生在操作者确认之后。

func proposalRunner(t *testing.T) *LLMToolRunner {
	t.Helper()
	scope := tools.NewScope(
		[]string{"Binance:usdtmarginedfutures:BTC_USDT", "Binance:usdtmarginedfutures:ETH_USDT"},
		[]string{"acct-1"},
		[]string{"version-live"},
	)
	runner, err := NewLLMToolRunner("Binance", asset.USDTMarginedFutures, "acct-1", scope, toolSourceFixture())
	require.NoError(t, err)
	return runner
}

func decodeProposalResult(t *testing.T, raw string) candidate.ActionProposal {
	t.Helper()
	var evidence candidate.ToolEvidence
	require.NoError(t, json.Unmarshal([]byte(raw), &evidence))
	var proposal candidate.ActionProposal
	require.NoError(t, json.Unmarshal(evidence.Data, &proposal))
	return proposal
}

func TestProposeActionBuildsScopedProposals(t *testing.T) {
	runner := proposalRunner(t)
	raw, err := runner.Run(t.Context(), "call-1", tools.ToolProposeAction, `{
		"action":"START_OPEN_DECISION_BATCH","summary":"对 BTC 生成开仓决策",
		"marketIds":["BTC_USDT","Binance:usdtmarginedfutures:ETH_USDT"],"strategyVersionId":"version-live"}`)
	require.NoError(t, err)
	proposal := decodeProposalResult(t, raw)
	assert.Equal(t, candidate.ActionStartOpenDecisionBatch, proposal.Action)
	assert.Equal(t, []string{"Binance:usdtmarginedfutures:BTC_USDT", "Binance:usdtmarginedfutures:ETH_USDT"}, proposal.MarketIDs,
		"both spellings normalise to the run vocabulary")
	assert.Equal(t, "webui-operator", proposal.Actor, "the default actor mirrors the manual pages")
}

func TestProposeActionRefusesOutOfScope(t *testing.T) {
	runner := proposalRunner(t)
	_, err := runner.Run(t.Context(), "call-1", tools.ToolProposeAction, `{
		"action":"START_OPEN_DECISION_BATCH","summary":"x",
		"marketIds":["Binance:usdtmarginedfutures:SOL_USDT"],"strategyVersionId":"version-live"}`)
	require.ErrorIs(t, err, ErrToolArgumentsInvalid, "a market outside the run scope must refuse")

	_, err = runner.Run(t.Context(), "call-2", tools.ToolProposeAction, `{
		"action":"START_OPEN_DECISION_BATCH","summary":"x",
		"marketIds":["Binance:usdtmarginedfutures:BTC_USDT"],"strategyVersionId":"version-elsewhere"}`)
	require.ErrorIs(t, err, ErrToolArgumentsInvalid, "a strategy version outside the run scope must refuse")
}

func TestProposeActionNormalisesTrackedMarket(t *testing.T) {
	runner := proposalRunner(t)
	raw, err := runner.Run(t.Context(), "call-1", tools.ToolProposeAction, `{
		"action":"SET_MARKET_TRACKED","summary":"启用 SOL","marketId":"SOL_USDT","tracked":true}`)
	require.NoError(t, err)
	proposal := decodeProposalResult(t, raw)
	assert.Equal(t, "Binance:usdtmarginedfutures:SOL_USDT", proposal.MarketID, "the card shows the canonical id")
	require.NotNil(t, proposal.Tracked)
	assert.True(t, *proposal.Tracked, "an untracked market is allowed: the toggle exists to track it")
}

func TestProposalCardsExtractDrafts(t *testing.T) {
	runner := proposalRunner(t)
	raw, err := runner.Run(t.Context(), "call-1", tools.ToolProposeAction, `{
		"action":"CANCEL_BATCH_JOB","summary":"停止作业","jobId":"job-1"}`)
	require.NoError(t, err)
	records := []ToolCallRecord{
		{CallID: "call-1", Name: tools.ToolProposeAction, Result: raw},
		{CallID: "call-2", Name: "getCandleWindow", Result: "not evidence"},
	}
	cards := ProposalCards(records)
	require.Len(t, cards, 1)
	assert.Equal(t, candidate.ActionCancelBatchJob, cards[0].Action)
	assert.Equal(t, "job-1", cards[0].JobID)
}

func TestChatRunCarriesProposalCards(t *testing.T) {
	runner := proposalRunner(t)
	raw, err := runner.Run(t.Context(), "call-1", tools.ToolProposeAction, `{
		"action":"START_POSITION_REVIEW","summary":"复核全部持仓","allPositions":true,"strategyVersionId":"version-live"}`)
	require.NoError(t, err)

	withTool := chatMessage("", 20, 8)
	withTool.Choices[0].FinishReason = "tool_calls"
	withTool.Choices[0].Message.ToolCalls = []openai.ToolCall{toolCall("call-1", tools.ToolProposeAction, `{}`)}
	transport := &chatTestTransport{responses: []*openai.ChatResponse{withTool, chatMessage("已生成提案，请在卡片上确认。", 30, 9)}}
	p := chatTestPipelineOnly(t, transport)
	p.Tools = &fakeRunner{
		supported: map[string]bool{tools.ToolProposeAction: true},
		results:   map[string]string{tools.ToolProposeAction: raw},
	}

	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err)
	require.Len(t, result.Proposals, 1, "the turn must carry the inert draft")
	assert.Equal(t, candidate.ActionStartPositionReview, result.Proposals[0].Action)
	assert.True(t, result.Proposals[0].AllPositions)
}
