package engine

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// 对话走廊 × 真筛选执行器：脚本化传输让模型发出一次 screenMarkets 工具调用，
// 管线经真实工具循环执行（真缓存/真 ticker 全局存储/假 T2 源），证据信封回填给
// 模型后产出最终答复——需求①「自然语言→子 Agent 抓取」在本层的最强离线证明。

func TestChatCorridorExecutesScreenMarketsThroughTheLoop(t *testing.T) {
	runner := screenerRunner(t) // real *LLMToolRunner with seeded caches

	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		chatResponse("tool_calls", &openai.Message{
			Role: "assistant",
			ToolCalls: []openai.ToolCall{toolCall("call-9", "screenMarkets",
				`{"criteria":[{"metric":"change24h","op":"gte","value":0}],"sort":[{"metric":"quotevolume24h","desc":true}],"limit":10}`)},
		}, 210),
		chatResponse("stop", &openai.Message{Role: "assistant", Content: "筛选完成：按 24h 涨幅≥0 且成交额降序，BTC_USDT 最符合。"}, 60),
	}}

	cipher, err := chat.NewConversationCipher(bytes.Repeat([]byte{0x22}, chat.KeySize))
	require.NoError(t, err)
	history := &chatTestHistory{}
	budget := &chatTestBudget{}
	maxTokens := int64(512)
	ids := 0
	p := &ChatPipeline{
		Transport: transport,
		Tools:     runner,
		Cipher:    cipher,
		History:   history,
		Budget:    budget,
		Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
			ID: "rev-1", Kind: ProviderKindLLM, ModelID: "gpt-test",
			TimeoutSeconds: 45, MaxOutputTokens: &maxTokens,
		}},
		Now:   func() time.Time { return providerAdminNow },
		NewID: func() string { ids++; return fmt.Sprintf("id-%d", ids) },
	}

	result, err := p.Run(t.Context(), ChatRunRequest{ConversationID: "conv-9", Input: "帮我筛出今天上涨且成交额最大的合约，给我前 10 个", ProfileRevisionID: "rev-1"})
	require.NoError(t, err)
	assert.Contains(t, result.Reply, "BTC_USDT", "the final reply cites the screened market")

	// The provider saw two rounds: the tool call and then the executed result.
	require.Len(t, transport.requests, 2)
	var toolMessage string
	for _, message := range transport.requests[1].Messages {
		if message.Role == "tool" {
			toolMessage = message.Content
		}
	}
	require.NotEmpty(t, toolMessage, "the second round must carry the executed tool evidence")
	assert.Contains(t, toolMessage, "tool:screenMarkets:call-9", "the evidence envelope names the call")
	assert.Contains(t, toolMessage, "Binance:usdtmarginedfutures:BTC_USDT", "the rows are the real evaluated screen")
	assert.Contains(t, toolMessage, "DATABASE_CACHED", "the screen rides the caches")
	assert.NotContains(t, toolMessage, "ETH_USDT\":", "the falling market should not appear in the rows")

	// The turn sealed like any other: budget settled, both messages persisted.
	assert.Equal(t, 1, budget.marked)
	require.Len(t, history.rows, 2)
	assert.Contains(t, openStored(t, cipher, &history.rows[1]), "BTC_USDT")
}
