package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// 生产装配 · chat 走廊测试：激活配对 → 客户端（含 loopback httptest）→ 工具
// 运行器（真实 scope）→ LLMChatService（明文 key，vault 移除后）。

func TestSetupChatFromActivePairAssemblesTheCorridor(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmKey, jevKey := keyFixture(t, "sk-llm-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	seedRevision(t, "rev-llm", ProviderKindLLM, srv.URL+"/v1", "model-a", llmKey, true)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t)

	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{
		ID: "strategy-live", Name: "trend", CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "version-live", StrategyID: "strategy-live", State: "APPROVED", SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))

	cfg := &config.LLMConfig{
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
	}
	markets := []string{"BTC_USDT"} // the rotator's own BASE_QUOTE spelling
	manager := &LLMManager{}
	cache, cacheErr := NewKlineCache(64)
	require.NoError(t, cacheErr)
	manager.cache = cache
	service, err := manager.setupChatFromActivePair(ctx, cfg, &fakeToolSource{candles: toolSourceFixture().candles}, markets, t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.NotNil(t, service.Conversations)
	assert.NotNil(t, service.Pipeline)
	assert.NotNil(t, service.Cipher)

	runner, ok := service.Pipeline.Tools.(*LLMToolRunner)
	require.True(t, ok, "the pipeline must carry the read-only tool runner")
	assert.Same(t, cache, runner.Cache, "the screener cache is wired from the manager")
	require.NotNil(t, runner.Scope)
	_, inScope := runner.Scope.Markets[testMarketID("BTC")]
	assert.True(t, inScope, "the rotator's markets are in scope under the tool-facing id")
	_, rawInScope := runner.Scope.Markets["BTC_USDT"]
	assert.False(t, rawInScope, "the scope holds the normalised run ids, not the rotator's raw spelling")
	_, accountInScope := runner.Scope.Accounts["acct-1"]
	assert.True(t, accountInScope, "the configured account is in scope")
	_, versionInScope := runner.Scope.StrategyVersions["version-live"]
	assert.True(t, versionInScope, "every version of a live strategy is in scope")
	_, unknownOut := runner.Scope.StrategyVersions["version-elsewhere"]
	assert.False(t, unknownOut, "an unseeded version is not in scope")
}

func TestSetupChatFromActivePairFailsClosedWithoutAPair(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	// No pair: the corridor must stay fail-closed.

	cfg := &config.LLMConfig{
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
	}
	_, err := (&LLMManager{}).setupChatFromActivePair(ctx, cfg, &fakeToolSource{}, nil, t.TempDir())
	require.Error(t, err, "no active pair must leave the corridor fail-closed")
}
