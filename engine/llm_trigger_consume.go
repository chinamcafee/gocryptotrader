package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// M2-13 收官 · 触发队列的生产消费者（session-51 侦察登记的接缝）。
//
// 数据面此前止步于「扫描器把事件放进有界队列」：轮询 → 重载 → 扫描 → 队列
// ——队列没人排空。本文件把队列接到决策运行器：每个触发事件按 **市场 → 可
// 运行策略版本** 路由（活跃审批覆盖该市场、且版本状态为 FROZEN），逐个走
// `ShadowRunner.RunOnce`（去抖 → 门禁 → 分析 → Jev → 合成 → 落账）。
//
// 纪律：
//   - **READ_ONLY 不消费**：`modePermitsDecisions` 为假时**不排空队列**——
//     只读模式下一个决策都不该跑，把成百上千个触发落成 MODE_NOT_PERMITTED
//     的决策行是噪音而不是审计（队列有界且丢最旧，不会无限积压）。模式在
//     检查与运行之间的漂移仍由 RunOnce 的门禁兜底（拒绝也落账，语义不变）。
//   - **路由不是门禁**：版本过滤只回答"这个触发该送给谁"；预算 / readiness
//     / halt / 对账等闸门全部保留在 RunOnce 里，拒绝带理由码落账。
//   - **一个版本坏了不拖垮一批**：Build / RunOnce 的单个错误计数并继续，
//     错误文本限长收集供日志点名。
//   - 每轮排空有上限：触发风暴时消费者落后于生产者是可接受的，把一轮拉成
//     无界循环不是。

const (
	// defaultTriggerDrainLimit bounds one consume pass.
	defaultTriggerDrainLimit = 50
	// triggerConsumeInterval is how often the production loop drains the queue.
	triggerConsumeInterval = 30 * time.Second
	// maxTriggerConsumeErrors bounds the collected error strings per pass.
	maxTriggerConsumeErrors = 3
)

// iShadowRunOnce is the runner seam the consumer drives.
type iShadowRunOnce interface {
	RunOnce(ctx context.Context, req *ShadowRunRequest) (*ShadowRunResult, error)
}

// iTriggerApprovals is the approval-listing seam (the grant adapter satisfies
// it; one read per pass, not per event).
type iTriggerApprovals interface {
	ListActiveApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error)
}

// TriggerConsumer routes queued trigger events to runnable strategy versions.
type TriggerConsumer struct {
	// Drain reads up to limit queued events; wired to the rotation's scanner.
	Drain func(limit int) []trigger.Event
	// Approvals lists the live approvals the routing filter reads.
	Approvals iTriggerApprovals
	// VersionState reads one version row (runnable-state filter).
	VersionState func(ctx context.Context, id string) (*llmstore.StrategyVersion, error)
	// Build assembles the per-run request from live state.
	Build func(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error)
	// MarketKey normalises an event's or an approval's market identifier to
	// the run vocabulary (runMarketID): the rotator list and the events speak
	// BASE_QUOTE while the run entry demands the three-segment form. Both
	// sides of the routing comparison go through it, so mixed spellings meet
	// at the same key -- and the run is built with an id that parses.
	MarketKey func(raw string) (string, error)
	// Runner executes one shadow decision.
	Runner iShadowRunOnce
	// Mode reports the effective run mode; READ_ONLY consumes nothing.
	Mode func() string
	// AccountID/Network scope which approvals address this deployment.
	AccountID string
	Network   string
	// MaxPerDrain bounds one pass (default 50).
	MaxPerDrain int
}

// TriggerConsumeResult is one pass's routing outcome.
type TriggerConsumeResult struct {
	Mode       string `json:"mode"`
	Drained    int    `json:"drained"`
	Runs       int    `json:"runs"`
	Debounced  int    `json:"debounced"`
	Failed     int    `json:"failed"`
	NoStrategy int    `json:"noStrategy"`
	// Executed counts runs the bridge submitted in an executing mode
	// (PAPER/TESTNET/LIVE) with a full ALLOW chain; SHADOW passes leave it 0.
	Executed int      `json:"executed"`
	Errors   []string `json:"errors,omitempty"`
}

// ConsumeOnce drains the queue and routes every event once.
func (c *TriggerConsumer) ConsumeOnce(ctx context.Context) (*TriggerConsumeResult, error) {
	if c == nil || c.Drain == nil || c.Approvals == nil || c.VersionState == nil || c.Build == nil || c.Runner == nil || c.MarketKey == nil {
		return nil, ErrChatNotConfigured
	}
	result := &TriggerConsumeResult{Mode: c.mode()}
	if !modePermitsDecisions(result.Mode) {
		// READ_ONLY (or unknown) never decides; consuming would only turn
		// triggers into recorded refusals. The queue drops oldest when full,
		// so skipping here cannot grow without bound.
		return result, nil
	}
	limit := c.MaxPerDrain
	if limit <= 0 {
		limit = defaultTriggerDrainLimit
	}
	events := c.Drain(limit)
	result.Drained = len(events)
	if len(events) == 0 {
		return result, nil
	}
	approvals, err := c.Approvals.ListActiveApprovals(ctx)
	if err != nil {
		return result, fmt.Errorf("llm trigger consume: listing approvals: %w", err)
	}
	runnable, err := c.runnableVersionsByMarket(ctx, approvals)
	if err != nil {
		return result, err
	}
	for i := range events {
		market, err := c.MarketKey(events[i].MarketID)
		if err != nil {
			result.fail(fmt.Errorf("normalising the event market %q: %w", events[i].MarketID, err))
			continue
		}
		versions := runnable[market]
		if len(versions) == 0 {
			result.NoStrategy++
			continue
		}
		for _, versionID := range versions {
			req, err := c.Build(ctx, versionID, market)
			if err != nil {
				result.fail(fmt.Errorf("building run for %s on %s: %w", versionID, market, err))
				continue
			}
			out, err := c.Runner.RunOnce(ctx, req)
			if err != nil {
				result.fail(fmt.Errorf("running %s on %s: %w", versionID, market, err))
				continue
			}
			if out != nil && out.Skipped {
				result.Debounced++
				continue
			}
			result.Runs++
			// The PAPER/LIVE bridge: a completed run in an executing mode with
			// a full ALLOW chain submits its intent here. The runner never
			// submits anything itself, so a wiring bug cannot turn SHADOW into
			// an executing mode.
			if req.Execute != nil && out != nil {
				outcome, err := req.Execute(ctx, out)
				if err != nil {
					result.fail(fmt.Errorf("executing %s on %s: %w", versionID, market, err))
					continue
				}
				if outcome != nil && outcome.Executed {
					result.Executed++
				}
			}
		}
	}
	return result, nil
}

// runnableVersionsByMarket maps each covered market to the strategy versions
// a trigger for that market should be routed to: a live approval for this
// account+network whose version is still FROZEN.
func (c *TriggerConsumer) runnableVersionsByMarket(ctx context.Context, approvals []llmstore.StrategyApproval) (map[string][]string, error) {
	out := make(map[string][]string)
	for i := range approvals {
		approval := approvals[i]
		if approval.AccountID != c.AccountID || approval.Network != c.Network {
			continue
		}
		version, err := c.VersionState(ctx, approval.StrategyVersionID)
		if err != nil {
			return nil, fmt.Errorf("llm trigger consume: version %q: %w", approval.StrategyVersionID, err)
		}
		if version == nil || version.State != strategyStateFrozen {
			// A suspended or archived version is not routed to: halt and delist
			// already moved it, and the gates would only record a refusal.
			continue
		}
		markets, err := DecodeApprovedMarkets(approval.ApprovedMarketIDs)
		if err != nil {
			// A corrupt market list fails the pass rather than being skipped:
			// skipping could hide a live approval behind a parse error.
			return nil, fmt.Errorf("llm trigger consume: approval %q: %w", approval.ID, err)
		}
		for _, market := range markets {
			key, err := c.MarketKey(market)
			if err != nil {
				return nil, fmt.Errorf("llm trigger consume: approval %q carries market %q: %w", approval.ID, market, err)
			}
			if !slices.Contains(out[key], approval.StrategyVersionID) {
				out[key] = append(out[key], approval.StrategyVersionID)
			}
		}
	}
	return out, nil
}

// mode returns the effective mode, defaulting to the fail-closed READ_ONLY.
func (c *TriggerConsumer) mode() string {
	if c.Mode == nil {
		return LLMRunModeReadOnly
	}
	return c.Mode()
}

// fail counts one per-item failure and keeps a bounded number of its texts.
func (r *TriggerConsumeResult) fail(err error) {
	r.Failed++
	if len(r.Errors) < maxTriggerConsumeErrors {
		r.Errors = append(r.Errors, err.Error())
	}
}

// newTriggerConsumer assembles the production consumer; nil when the
// rotation, its scanner or the decision runner is missing -- each of those is
// a legitimate configuration that leaves the consumer off.
func (bot *Engine) newTriggerConsumer() *TriggerConsumer {
	if bot == nil || bot.llmManager == nil {
		return nil
	}
	runner := bot.currentDecisionRunner()
	if runner == nil {
		return nil
	}
	rotation := bot.llmManager.Rotation()
	if rotation == nil {
		return nil
	}
	scanner := rotation.Scanner()
	if scanner == nil {
		return nil
	}
	cfg := &bot.Config.LLM
	item, itemErr := asset.New(cfg.Asset)
	consumer := &TriggerConsumer{
		Drain:        scanner.Drain,
		Approvals:    LLMMarketGrants{},
		VersionState: llmstore.GetStrategyVersion,
		Build:        bot.buildShadowRunRequest,
		Runner:       runner,
		Mode:         bot.llmManager.EffectiveRunMode,
		AccountID:    strings.TrimSpace(cfg.AccountID),
		Network:      strings.TrimSpace(cfg.Network),
		MarketKey: func(raw string) (string, error) {
			if itemErr != nil {
				return "", itemErr
			}
			return runMarketID(cfg.Exchange, item, raw)
		},
	}
	return consumer
}

// triggerConsumeLoop drains the trigger queue until the context ends.
//
// The consumer is rebuilt every tick so a runner swapped in later (a pair
// activation after startup) is picked up without restarting the loop; before
// the first assembly it is legitimately nil and the tick is a no-op.
//
// A failed pass is logged and retried next tick: the events it could not
// consume were drained and are lost (the queue's documented drop semantics),
// but the next triggers of the same markets arrive on the next sweep.
func (bot *Engine) triggerConsumeLoop(ctx context.Context) {
	ticker := time.NewTicker(triggerConsumeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		consumer := bot.newTriggerConsumer()
		if consumer == nil {
			continue
		}
		result, err := consumer.ConsumeOnce(ctx)
		if err != nil {
			log.Warnf(log.LLMMgr, "trigger consume: %v", err)
			continue
		}
		if result.Runs > 0 || result.Failed > 0 {
			log.Debugf(log.LLMMgr, "trigger consume: drained=%d runs=%d debounced=%d failed=%d noStrategy=%d",
				result.Drained, result.Runs, result.Debounced, result.Failed, result.NoStrategy)
		}
	}
}
