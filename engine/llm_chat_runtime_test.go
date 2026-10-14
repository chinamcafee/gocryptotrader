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
// 运行器（真实 scope）→ LLMChatService。

func TestSetupChatFromActivePairAssemblesTheCorridor(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	dir, llmRef, jevRef := vaultFixture(t, "sk-llm-1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	seedRevision(t, "rev-llm", ProviderKindLLM, srv.URL+"/v1", "model-a", llmRef, true)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	require.NoError(t, llmstore.InsertStrategy(ctx, &llmstore.Strategy{
		ID: "strategy-live", Name: "trend", CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	require.NoError(t, llmstore.InsertStrategyVersion(ctx, &llmstore.StrategyVersion{
		ID: "version-live", StrategyID: "strategy-live", State: "APPROVED", SourcePromptSha256: "aa", CreatedAt: providerAdminNow,
	}))

	cfg := &config.LLMConfig{
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
		Vault: config.LLMVaultConfig{Directory: dir},
	}
	markets := []string{"BTC_USDT"} // the rotator's own BASE_QUOTE spelling
	service, err := (&LLMManager{}).setupChatFromActivePair(ctx, cfg, &fakeToolSource{candles: toolSourceFixture().candles}, markets)
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.NotNil(t, service.Conversations)
	assert.NotNil(t, service.Pipeline)
	assert.NotNil(t, service.Cipher)

	runner, ok := service.Pipeline.Tools.(*LLMToolRunner)
	require.True(t, ok, "the pipeline must carry the read-only tool runner")
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

func TestSetupChatFromActivePairFailsClosedWithoutAVault(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	_, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	cfg := &config.LLMConfig{
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
		Vault: config.LLMVaultConfig{Directory: t.TempDir()},
	}
	_, err := (&LLMManager{}).setupChatFromActivePair(ctx, cfg, &fakeToolSource{}, nil)
	require.Error(t, err, "an uninitialised vault must leave the corridor fail-closed")
}
