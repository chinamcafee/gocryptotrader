package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// 2026-10-17 实测原文（deepseek-flash 第二轮）：工具调用以 DSML 文本返回。
const dsmlObserved = `<｜｜DSML｜｜ calls>
<｜｜DSML｜｜ invoke name="screenMarkets">
<｜｜DSML｜｜ parameter name="include" string="false">["lastprice", "change24h", "quotevolume24h"]</｜｜DSML｜｜ parameter>
<｜｜DSML｜｜ parameter name="limit" string="false">3</｜｜DSML｜｜ parameter>
<｜｜DSML｜｜ parameter name="sort" string="false">[{"desc": true, "metric": "change24h"}]</｜｜DSML｜｜ parameter>
<｜｜DSML｜｜ parameter name="interval" string="true">1h</｜｜DSML｜｜ parameter>
</｜｜DSML｜｜ invoke>
</｜｜DSML｜｜ calls>`

func TestParseDSMLToolCallsRecoversTheObservedShape(t *testing.T) {
	t.Parallel()

	calls, cleaned, hadMarkup := parseDSMLToolCalls(dsmlObserved)
	require.True(t, hadMarkup)
	require.Len(t, calls, 1)
	assert.Equal(t, "screenMarkets", calls[0].Function.Name)
	assert.True(t, strings.HasPrefix(calls[0].ID, "dsml-"), "recovered calls get a stable-looking id")

	var args struct {
		Include  []string         `json:"include"`
		Limit    int              `json:"limit"`
		Sort     []map[string]any `json:"sort"`
		Interval string           `json:"interval"`
	}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &args))
	assert.Equal(t, []string{"lastprice", "change24h", "quotevolume24h"}, args.Include, "string=false parameters decode as JSON")
	assert.Equal(t, 3, args.Limit)
	require.Len(t, args.Sort, 1)
	assert.Equal(t, true, args.Sort[0]["desc"])
	assert.Equal(t, "change24h", args.Sort[0]["metric"])
	assert.Equal(t, "1h", args.Interval, "string=true parameters stay plain strings")
	assert.Empty(t, cleaned, "the markup should not survive as the visible answer")
}

func TestParseDSMLToolCallsKeepsSurroundingText(t *testing.T) {
	t.Parallel()

	content := "先看一下数据。\n" + dsmlObserved + "\n以上是草稿。"
	calls, cleaned, hadMarkup := parseDSMLToolCalls(content)
	require.True(t, hadMarkup)
	require.Len(t, calls, 1)
	assert.Equal(t, "先看一下数据。\n\n以上是草稿。", cleaned, "prose around the block is preserved")
}

func TestParseDSMLToolCallsDropsUnterminatedMarkup(t *testing.T) {
	t.Parallel()

	calls, cleaned, hadMarkup := parseDSMLToolCalls(`好的<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="screenMarkets">`)
	require.True(t, hadMarkup)
	assert.Empty(t, calls)
	assert.Equal(t, "好的", cleaned, "a truncated stream should not leak the raw marker")
}

func TestParseDSMLToolCallsLeavesPlainTextAlone(t *testing.T) {
	t.Parallel()

	calls, cleaned, hadMarkup := parseDSMLToolCalls("普通回答，不含工具标记。")
	assert.False(t, hadMarkup)
	assert.Empty(t, calls)
	assert.Equal(t, "普通回答，不含工具标记。", cleaned)
}

func TestToolLoopExecutesDSMLToolCalls(t *testing.T) {
	t.Parallel()

	transport := &scriptedTransport{responses: []*openai.ChatResponse{
		chatResponse("stop", &openai.Message{Role: "assistant", Content: `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="lookup_market"><｜｜DSML｜｜ parameter name="marketId" string="true">BTC_USDT</｜｜DSML｜｜ parameter></｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`}, 20),
		chatResponse("stop", &openai.Message{Role: "assistant", Content: "筛选完成。"}, 10),
	}}
	runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{"ok":true}`}}

	result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
	require.NoError(t, err)
	require.Empty(t, result.ReasonCode)
	assert.Equal(t, "筛选完成。", result.Content, "the final answer is prose, not markup")
	require.Len(t, result.Transcript, 1)
	assert.Equal(t, "lookup_market", result.Transcript[0].Name)
	require.Len(t, runner.calls, 1, "the recovered call must actually run")

	require.Len(t, transport.requests, 2)
	assistant := transport.requests[1].Messages[len(transport.requests[1].Messages)-2]
	require.Len(t, assistant.ToolCalls, 1, "the follow-up request must carry the recovered call")
	assert.Empty(t, assistant.Content, "the DSML text should not be echoed back as content")
}
