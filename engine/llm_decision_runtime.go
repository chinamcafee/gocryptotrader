package engine

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/thrasher-corp/gocryptotrader/config"
)

// 生产装配 · 决策运行器（ShadowRunner）：把分析流水线、Jev 管线、评估节流器与
// 决策记录器组装成一条可运行的影子决策链。这是 `trigger`/`preview` 两个
// fail-closed RPC 的运行底座；每市场的快照组装、Jev 证据与风控事实作为
// ShadowRunRequest 的闭包由调用方逐次注入（见 llm_decision_facts 侧）。
//
// 组装完成 ≠ 可以下单：SHADOW 语义下运行只记录不执行；把决策送进订单路径
// 是 Dispatch（M5）与审批闭环的事。

// setupDecisionRuntimeFromActivePair assembles the shadow decision runner.
//
// It reuses the chat corridor's clients and tool runner on purpose: the model
// layer, the scope and the budget gates must be identical between a chat turn
// and an automatic run, or the two would disagree about what the model may see.
func (m *LLMManager) setupDecisionRuntimeFromActivePair(ctx context.Context, cfg *config.LLMConfig, source iToolDataSource, markets []string) (*ShadowRunner, error) {
	if m == nil || cfg == nil {
		return nil, ErrChatNotConfigured
	}
	clients, err := BuildActiveClients(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm decision runtime: %w", err)
	}
	runner, err := buildScopeToolRunner(ctx, cfg, source, markets)
	if err != nil {
		return nil, err
	}
	// Same screener seam as the chat corridor: the manager's candle cache feeds
	// screenMarkets, and the rotator fills it on every reload.
	runner.Cache = m.Cache()
	runner.JevJudge = clients.Jev
	runner.JevModel = clients.JevRevision.ModelID
	runner.JevRevisionID = clients.JevRevision.ID
	runner.JevBudget = LLMChatBudget{}
	approvals := LLMApprovals{}
	return &ShadowRunner{
		Scheduler: NewEvaluationScheduler(time.Now),
		Analysis: &AnalysisPipeline{
			Transport: clients.LLM,
			Tools:     runner,
			Budget:    LLMChatBudget{},
			Revisions: LLMChatRevisions{},
			Now:       time.Now,
		},
		Jev: &JevPipeline{
			Judge:     clients.Jev,
			Budget:    LLMChatBudget{},
			Revisions: LLMChatRevisions{},
			PairEpoch: approvals.ActivePairEpoch,
			Now:       time.Now,
		},
		Recorder: &DecisionRecorder{
			Runs:  LLMDecisionStore{},
			Risk:  LLMDecisionStore{},
			Audit: LLMDecisionStore{},
			Now:   time.Now,
			NewID: func() string { return uuid.NewV4().String() },
		},
		Now: time.Now,
	}, nil
}

// setupLLMDecisionRunner assembles the decision runner from the bot's live
// state, mirroring the chat corridor's data source and scope choices.
func (bot *Engine) setupLLMDecisionRunner(ctx context.Context) (*ShadowRunner, error) {
	if bot == nil || bot.llmManager == nil {
		return nil, ErrChatNotConfigured
	}
	if bot.ExchangeManager == nil {
		return nil, fmt.Errorf("%w: no exchange manager", ErrChatNotConfigured)
	}
	ex, err := bot.ExchangeManager.GetExchangeByName(bot.Config.LLM.Exchange)
	if err != nil {
		return nil, fmt.Errorf("llm decision runtime: exchange %q: %w", bot.Config.LLM.Exchange, err)
	}
	markets := bot.llmManager.Rotation().Markets()
	if len(markets) == 0 {
		// Same startup fallback as the chat corridor: the rotation loop may not
		// have refreshed yet, and coverage can be disabled.
		markets, err = enabledMarketKeys(ex, bot.Config.LLM.Asset)
		if err != nil {
			return nil, err
		}
	}
	return bot.llmManager.setupDecisionRuntimeFromActivePair(ctx, &bot.Config.LLM, ex, markets)
}
