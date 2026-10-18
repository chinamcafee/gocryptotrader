package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// 生产装配 · 运行期重装测试：启动后激活（或切换）配对必须把 chat 走廊、决策运行器
// 与批量工作台重绑到新配对；重装失败则失败关闭（清空旧指针），绝不带着退场配对的
// 客户端继续服务。

func TestReassembleLLMRuntimeBindsAndClears(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t)

	bot := CreateTestBot(t)
	bot.Settings.DataDir = t.TempDir()
	bot.llmManager = &LLMManager{}
	bot.Config.LLM = config.LLMConfig{Exchange: testExchange, Asset: "spot", AccountID: "acct-1"}

	bot.reassembleLLMRuntime(t.Context(), "test activation")
	require.NotNil(t, bot.currentChatService(), "an active pair must assemble the chat corridor")
	require.NotNil(t, bot.currentDecisionRunner(), "an active pair must assemble the decision runner")
	require.NotNil(t, bot.currentBatchRunner(), "the batch workbench must ride the assembled runner")

	// Clearing the LLM slot (an edit superseded the active revision) makes the
	// next reassembly fail closed: the retired pair's clients must not survive.
	jevRevision := "rev-jev"
	require.NoError(t, llmstore.SetActiveModelPair(t.Context(), &llmstore.ActiveModelPair{
		JevProfileRevisionID: &jevRevision, PairEpoch: 2, Readiness: readinessIncomplete,
		UpdatedAt: providerAdminNow, UpdatedBy: "op",
	}))
	bot.reassembleLLMRuntime(t.Context(), "test clearing")
	assert.Nil(t, bot.currentChatService(), "a half pair should leave the corridor unavailable")
	assert.Nil(t, bot.currentDecisionRunner(), "a half pair should leave the runner unavailable")
	assert.Nil(t, bot.currentBatchRunner(), "a half pair should leave the batch workbench unavailable")
}
