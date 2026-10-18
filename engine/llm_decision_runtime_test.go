package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// 生产装配 · 决策运行器测试：组装出的 ShadowRunner 的每个部件都指向真实接缝
// （客户端、仓储适配器、节流器），并且调度去抖短路不触网。

func TestSetupDecisionRuntimeAssemblesTheRunner(t *testing.T) {
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

	cfg := &config.LLMConfig{
		Exchange: "Binance", Asset: "usdtmarginedfutures", AccountID: "acct-1",
	}
	manager := &LLMManager{}
	cache, cacheErr := NewKlineCache(64)
	require.NoError(t, cacheErr)
	manager.cache = cache
	runner, err := manager.setupDecisionRuntimeFromActivePair(ctx, cfg, &fakeToolSource{}, []string{"BTC_USDT"})
	require.NoError(t, err)
	require.NotNil(t, runner)
	require.NotNil(t, runner.Scheduler)
	require.NotNil(t, runner.Analysis)
	require.NotNil(t, runner.Jev)
	require.NotNil(t, runner.Recorder)
	toolRunner, ok := runner.Analysis.Tools.(*LLMToolRunner)
	require.True(t, ok, "the analyst pipeline must carry the read-only tool runner")
	assert.Same(t, cache, toolRunner.Cache, "the screener cache is wired from the manager")
	assert.NotNil(t, runner.Analysis.Transport, "the analyst should run on the built client")
	assert.NotNil(t, runner.Jev.Judge, "the judge should run on the built client")
	client, ok := runner.Analysis.Transport.(*openai.Client)
	require.True(t, ok, "the analyst transport must be the built OpenAI client")
	assert.Equal(t, "model-a", client.Model())

	// The assembled scheduler short-circuits a second run in the debounce
	// window without touching either transport: the runner is live end to end.
	now := time.Now()
	admission, err := runner.Scheduler.Admit(EvaluationRequest{
		StrategyVersionID: "version-live", MarketID: testMarketID("BTC"),
		TriggerKind: DecisionTriggerAutoShadow, At: now,
	})
	require.NoError(t, err)
	require.True(t, admission.Allowed, "first admission in the window must pass")
	result, err := runner.RunOnce(ctx, &ShadowRunRequest{
		StrategyVersionID: "version-live",
		AccountID:         "acct-1",
		MarketID:          testMarketID("BTC"),
		RunMode:           LLMRunModeShadow,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Skipped, "the second run in the window is debounced")
}
