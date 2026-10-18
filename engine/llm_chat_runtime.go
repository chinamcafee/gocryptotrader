package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/coverage"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
)

// conversationKeyDirName is the directory (under the bot's data dir) that
// stores the chat conversation cipher key. It belonged to the removed vault
// (2026-10-15); the cipher keeps its own key file, the vault custody does not
// come back with it.
const conversationKeyDirName = "llm-chat-keys"

// 生产装配 · chat 走廊：把激活配对的两个活客户端（client factory）与只读工具
// 运行器接进 LLMChatService。装配失败不是崩溃而是 fail-closed 的日志——chat RPC
// 保持 errChatUnavailable，直到操作员把配对/密钥/交易所都配好。

// setupChatFromActivePair assembles the conversation corridor.
//
// The scope is the run's blast radius for evidence: the coverage rotator's
// markets, this bot's account, and every version of every non-archived
// strategy (chat may read the decision history of any strategy it can name;
// writes are still gated downstream by approval, not by scope).
func (m *LLMManager) setupChatFromActivePair(ctx context.Context, cfg *config.LLMConfig, source iToolDataSource, markets []string, chatKeyDir string) (*LLMChatService, error) {
	if m == nil || cfg == nil {
		return nil, ErrChatNotConfigured
	}
	clients, err := BuildActiveClients(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm chat assembly: %w", err)
	}
	runner, err := buildScopeToolRunner(ctx, cfg, source, markets)
	if err != nil {
		return nil, err
	}
	// The screener reads closed candles from the manager's candle cache (M2-02),
	// which the coverage rotator fills on every reload. Without this seam the
	// screenMarkets tool refuses with "needs ... the candle cache".
	runner.Cache = m.Cache()
	// The advisory Jev seam (assessOpeningOpportunities) rides the same active
	// pair: the judge client, its revision and the shared budget adapter.
	runner.JevJudge = clients.Jev
	runner.JevModel = clients.JevRevision.ModelID
	runner.JevRevisionID = clients.JevRevision.ID
	runner.JevBudget = LLMChatBudget{}
	service, err := m.SetupChat(chatKeyDir, clients.LLM, runner)
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
	// N10: with the full-universe screen on, the evidence scope must cover the
	// same universe -- otherwise a top-ranked market the operator has not
	// enabled cannot be read in detail ("evidence outside the run scope").
	if cfg.Screener.FullUniverse == nil || *cfg.Screener.FullUniverse {
		if available, availErr := source.GetAvailablePairs(assetItem); availErr == nil && len(available) > 0 {
			symbols := make([]string, 0, len(available))
			for i := range available {
				symbols = append(symbols, available[i].Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String())
			}
			if selected, _, scopeErr := coverage.SelectMarkets(coverage.ScopeAllUSDTMPerpetual, symbols); scopeErr == nil {
				runMarkets = runMarkets[:0]
				for _, symbol := range selected {
					key, keyErr := runMarketID(cfg.Exchange, assetItem, symbol)
					if keyErr != nil {
						continue
					}
					runMarkets = append(runMarkets, key)
				}
			}
		}
	}
	scope := tools.NewScope(runMarkets, accounts, versions)
	runner, err := NewLLMToolRunner(cfg.Exchange, assetItem, cfg.AccountID, scope, source)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: tool runner: %w", err)
	}
	// The screener's three ingredients (docs 13 §3.1): the covered markets it
	// scans, the cached candle interval it reads, and the T2 coverage bound.
	interval, err := intervalFromConfig(cfg.Indicators.DefaultInterval)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: screener interval: %w", err)
	}
	runner.Markets = runMarkets
	runner.Interval = interval
	runner.FlowScanLimit = cfg.Screener.FlowScanLimit
	runner.FullUniverse = cfg.Screener.FullUniverse == nil || *cfg.Screener.FullUniverse
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
	if len(markets) == 0 {
		// The rotation loop refreshes asynchronously (and coverage may be
		// disabled); the enabled pairs are the same source it reads, so
		// deriving the list here keeps the screener's universe correct.
		markets, err = enabledMarketKeys(ex, bot.Config.LLM.Asset)
		if err != nil {
			return nil, err
		}
	}
	chatKeyDir := filepath.Join(bot.Settings.DataDir, conversationKeyDirName)
	return bot.llmManager.setupChatFromActivePair(ctx, &bot.Config.LLM, ex, markets, chatKeyDir)
}

// enabledMarketKeys lists the exchange's enabled pairs in the rotator's own
// BASE_QUOTE spelling.
func enabledMarketKeys(exchangeSource iToolDataSource, assetType string) ([]string, error) {
	item, err := asset.New(assetType)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: asset %q: %w", assetType, err)
	}
	pairs, err := exchangeSource.GetEnabledPairs(item)
	if err != nil {
		return nil, fmt.Errorf("llm runtime: reading enabled pairs: %w", err)
	}
	markets := make([]string, 0, len(pairs))
	for i := range pairs {
		markets = append(markets, pairs[i].Format(currency.PairFormat{Delimiter: "_", Uppercase: true}).String())
	}
	return markets, nil
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
