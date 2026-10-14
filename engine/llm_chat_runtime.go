package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// 生产装配 · chat 走廊：把激活配对的两个活客户端（client factory）与只读工具
// 运行器接进 LLMChatService。装配失败不是崩溃而是 fail-closed 的日志——chat RPC
// 保持 errChatUnavailable，直到操作员把配对/密钥/交易所都配好。

// setupChatFromActivePair assembles the conversation corridor.
//
// The scope is the run's blast radius for evidence: the coverage rotator's
// markets, this bot's account, and every version of every non-archived
// strategy (chat may read the decision history of any strategy it can name;
// writes are still gated downstream by approval, not by scope).
func (m *LLMManager) setupChatFromActivePair(ctx context.Context, cfg *config.LLMConfig, source iToolDataSource, markets []string) (*LLMChatService, error) {
	if m == nil || cfg == nil {
		return nil, ErrChatNotConfigured
	}
	clients, err := BuildActiveClients(ctx, cfg.Vault.Directory)
	if err != nil {
		return nil, fmt.Errorf("llm chat assembly: %w", err)
	}
	runner, err := buildScopeToolRunner(ctx, cfg, source, markets)
	if err != nil {
		return nil, err
	}
	service, err := m.SetupChat(cfg.Vault.Directory, clients.LLM, runner)
	if err != nil {
		return nil, fmt.Errorf("llm chat assembly: %w", err)
	}
	return service, nil
}

// buildScopeToolRunner constructs the read-only tool runner shared by the chat
// corridor and the decision runtime: one scope (rotator markets, the
// configured account, every version of every non-archived strategy), one data
// source (the exchange).
func buildScopeToolRunner(ctx context.Context, cfg *config.LLMConfig, source iToolDataSource, markets []string) (*LLMToolRunner, error) {
	if cfg == nil {
		return nil, ErrChatNotConfigured
	}
	versions, err := scopeStrategyVersions(ctx)
	if err != nil {
		return nil, err
	}
	accounts := []string{}
	if trimmed := strings.TrimSpace(cfg.AccountID); trimmed != "" {
		accounts = append(accounts, trimmed)
	}
	assetItem, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: asset %q: %w", cfg.Asset, err)
	}
	// The scope must speak the run vocabulary the tool arguments and evidence
	// carry: the rotator's own list is BASE_QUOTE while every tool-facing id
	// is the three-segment form (runMarketID). Comparing the raw spellings
	// would deny every tool call in production.
	runMarkets := make([]string, 0, len(markets))
	for _, market := range markets {
		key, err := runMarketID(cfg.Exchange, assetItem, market)
		if err != nil {
			return nil, fmt.Errorf("llm runtime: scope market %q: %w", market, err)
		}
		runMarkets = append(runMarkets, key)
	}
	scope := tools.NewScope(runMarkets, accounts, versions)
	runner, err := NewLLMToolRunner(cfg.Exchange, assetItem, cfg.AccountID, scope, source)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: tool runner: %w", err)
	}
	return runner, nil
}

// setupLLMChat assembles the chat corridor from the bot's live state: the
// configured exchange as the tool data source, the rotator's markets as the
// scope's market set.
func (bot *Engine) setupLLMChat(ctx context.Context) (*LLMChatService, error) {
	if bot == nil || bot.llmManager == nil {
		return nil, ErrChatNotConfigured
	}
	if bot.ExchangeManager == nil {
		return nil, fmt.Errorf("%w: no exchange manager", ErrChatNotConfigured)
	}
	ex, err := bot.ExchangeManager.GetExchangeByName(bot.Config.LLM.Exchange)
	if err != nil {
		return nil, fmt.Errorf("llm chat: exchange %q: %w", bot.Config.LLM.Exchange, err)
	}
	markets := bot.llmManager.Rotation().Markets()
	return bot.llmManager.setupChatFromActivePair(ctx, &bot.Config.LLM, ex, markets)
}

// scopeStrategyVersions lists every version id of every non-archived strategy.
func scopeStrategyVersions(ctx context.Context) ([]string, error) {
	strategies, err := llmstore.ListStrategies(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("llm chat assembly: listing strategies: %w", err)
	}
	versions := make([]string, 0, len(strategies))
	for i := range strategies {
		strategyVersions, err := llmstore.ListStrategyVersions(ctx, strategies[i].ID)
		if err != nil {
			return nil, fmt.Errorf("llm chat assembly: versions of %s: %w", strategies[i].ID, err)
		}
		for j := range strategyVersions {
			versions = append(versions, strategyVersions[j].ID)
		}
	}
	return versions, nil
}
