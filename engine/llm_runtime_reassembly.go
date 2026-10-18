package engine

import (
	"context"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/log"
)

// 生产装配 · 运行期重装：Engine.Start 的装配是一次性动作，而配对激活可以发生在
// 任意时刻（首次激活时启动阶段根本没有配对可装配）。激活成功后必须把 chat 走廊、
// 决策运行器与批量工作台重绑到新配对的客户端上——否则它们要么停留在 unavailable
// （首次激活），要么继续握着旧配对的客户端（切换），两者都与「对话/决策跟激活
// 配对走」相悖。

// currentChatService returns the assembled chat corridor, nil while unavailable.
func (bot *Engine) currentChatService() *LLMChatService {
	if bot == nil {
		return nil
	}
	bot.llmRuntimeMu.RLock()
	defer bot.llmRuntimeMu.RUnlock()
	return bot.llmChatService
}

// swapChatService replaces the chat corridor under the runtime lock.
func (bot *Engine) swapChatService(service *LLMChatService) {
	if bot == nil {
		return
	}
	bot.llmRuntimeMu.Lock()
	defer bot.llmRuntimeMu.Unlock()
	bot.llmChatService = service
}

// currentDecisionRunner returns the shadow decision runner, nil while unavailable.
func (bot *Engine) currentDecisionRunner() *ShadowRunner {
	if bot == nil {
		return nil
	}
	bot.llmRuntimeMu.RLock()
	defer bot.llmRuntimeMu.RUnlock()
	return bot.llmDecisionRunner
}

// swapDecisionRunner replaces the decision runner under the runtime lock.
func (bot *Engine) swapDecisionRunner(runner *ShadowRunner) {
	if bot == nil {
		return
	}
	bot.llmRuntimeMu.Lock()
	defer bot.llmRuntimeMu.Unlock()
	bot.llmDecisionRunner = runner
}

// currentBatchRunner returns the batch workbench, nil while unavailable.
func (bot *Engine) currentBatchRunner() *BatchRunner {
	if bot == nil {
		return nil
	}
	bot.llmRuntimeMu.RLock()
	defer bot.llmRuntimeMu.RUnlock()
	return bot.llmBatchRunner
}

// swapBatchRunner replaces the batch workbench under the runtime lock.
func (bot *Engine) swapBatchRunner(runner *BatchRunner) {
	if bot == nil {
		return
	}
	bot.llmRuntimeMu.Lock()
	defer bot.llmRuntimeMu.Unlock()
	bot.llmBatchRunner = runner
}

// newBatchRunner builds the GUI batch workbench (docs 13 §3.2): serial decision
// jobs bound to active approvals, record-only like trigger/preview.
func (bot *Engine) newBatchRunner(runner *ShadowRunner) *BatchRunner {
	return &BatchRunner{
		Runner:      runner,
		Build:       bot.buildShadowRunRequest,
		ReviewBuild: bot.buildPositionReviewRequest,
		Approvals:   LLMMarketGrants{},
		AccountID:   strings.TrimSpace(bot.Config.LLM.AccountID),
		Network:     strings.TrimSpace(bot.Config.LLM.Network),
		MaxMarkets:  bot.Config.LLM.Batch.MaxMarkets,
		NewID:       newLLMIdentifier,
		Now:         time.Now,
		Audit:       LLMDecisionStore{},
	}
}

// reassembleLLMRuntime rebuilds the chat corridor, the decision runner and the
// batch workbench from the pair currently stored as active, swapping them in
// for the running RPC handlers. It never fails its caller: every missing piece
// is a warning that leaves the affected service unavailable, the same
// fail-closed posture as startup. A failed rebuild clears the previous
// services on purpose -- after an activation the old clients belong to a
// retired pair, and serving the retired model would silently contradict the
// switch. The trigger loop rebuilds its consumer every tick, so it picks the
// new runner up without a restart.
func (bot *Engine) reassembleLLMRuntime(ctx context.Context, reason string) {
	if bot == nil {
		return
	}
	if chat, err := bot.setupLLMChat(ctx); err != nil {
		log.Warnf(log.LLMMgr, "LLM chat corridor re-assembly failed (%s; chat RPCs stay unavailable): %v", reason, err)
		bot.swapChatService(nil)
	} else {
		bot.swapChatService(chat)
	}
	if runner, err := bot.setupLLMDecisionRunner(ctx); err != nil {
		log.Warnf(log.LLMMgr, "LLM decision runner re-assembly failed (%s; trigger/preview stay unavailable): %v", reason, err)
		bot.swapDecisionRunner(nil)
		bot.swapBatchRunner(nil)
	} else {
		bot.swapDecisionRunner(runner)
		bot.swapBatchRunner(bot.newBatchRunner(runner))
	}
}
