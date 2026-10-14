package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
)

// M2-13 末项 · 上游市场下架事件生产者（设计文档 10 §7 行 18、M6-02 #18）。
//
// 交易所把交易对下架后，`autoPairUpdates` 刷新会把它从启用列表里抹掉；
// 旋转器每 10 分钟重读一次列表（rotationLoop），但"消失了"此前是静默的。
// 本文件把"消失"变成完整响应链（行 18 的预期响应）：
//
//  1. 相关市场授权失效——配置账户上覆盖该市场的活跃 grant 逐条撤销；
//  2. 策略暂停——引用该市场的有效审批撤销（同 `MarketGrantService.Revoke`
//     的级联语义），其冻结版本从 FROZEN 移到 PAUSED（同 halt / pair-switch
//     先例：暂停不解除，恢复要重新授权 + 重新审批）；
//  3. 推送——LLMNotifier 高优事件，文内点名"确认下架并清理配置"；
//  4. 审计——一行 MARKET_DELISTED 记全部明细（机制而不是叙述）。
//
// 纪律：
//   - **首见只登记不响应**（priming）：第一次 Check 只建立已知集合，否则启动
//     时"从没见过的市场"会被当成下架。
//   - **外部下架与运营者移除同一响应**：两者都意味着该市场不再被覆盖，安全
//     方向一致；重新启用要走重新授权 + 重新审批，这正是"同一响应"要表达的。
//   - **失败中止但可重放**：任一写入失败即返回错误，该市场留在已知集合里，
//     下个周期重试；所有步骤幂等（grant 已撤 = 无活跃可撤、版本已 PAUSED =
//     同值重写），重试不会重复扣动扳机。
//   - Check 只在旋转循环的单 goroutine 里被调用（同 `RefreshMarkets`），
//     已知集合不加锁。

// AuditKindMarketDelisted records the automatic delisting response.
const AuditKindMarketDelisted = "MARKET_DELISTED"

// delistActor is the audit/notification actor for the automatic response: it
// is the system, not whichever operator happens to be logged in.
const delistActor = "llm-market-watch"

// iDelistGrants is the grant/approval seam the delist response needs.
type iDelistGrants interface {
	ListAccountGrants(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error)
	RevokeGrant(ctx context.Context, id string, at time.Time, by, reason string) error
	ListActiveApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error)
	RevokeApproval(ctx context.Context, id string, at time.Time, by, reason string) error
}

// iDelistVersions is the version-state seam.
type iDelistVersions interface {
	UpdateStrategyVersionState(ctx context.Context, id, state string) error
}

// MarketDelistWatcher compares the covered market list across refreshes and
// applies the row-18 response chain for every market that disappeared.
type MarketDelistWatcher struct {
	// Markets returns the current covered market identifiers; wired to the
	// rotator's own list by attachDelistWatcher.
	Markets func() []string
	// MarketKey normalises a market identifier to the run vocabulary the
	// stored grant and approval rows carry (runMarketID): the rotator list
	// speaks BASE_QUOTE, the stored rows speak the three-segment form.
	MarketKey func(raw string) (string, error)
	// Grants lists and revokes the grants/approvals covering a market.
	Grants iDelistGrants
	// Versions pauses a version whose approval just fell.
	Versions iDelistVersions
	// Audit records the automatic response; nil skips the row (tests).
	Audit iAuditEvents
	// Notify pushes the operator-facing event; nil drops silently.
	Notify *LLMNotifier
	// AccountID/Network scope the grants this deployment actually trades.
	AccountID string
	Network   string
	// Now is the injected clock.
	Now func() time.Time

	known  map[string]bool
	primed bool
}

// DelistWatchResult reports what one Check observed and did.
type DelistWatchResult struct {
	// Checked is the size of the refreshed market list.
	Checked int `json:"checked"`
	// Disappeared names the markets that left the list in this check.
	Disappeared []string `json:"disappeared,omitempty"`
	// RevokedGrants lists the grant ids revoked by this check.
	RevokedGrants []string `json:"revokedGrants,omitempty"`
	// PausedVersions lists the strategy versions paused by this check.
	PausedVersions []string `json:"pausedVersions,omitempty"`
}

// Check diffs the covered market list against the previous reading and runs
// the response chain for every market that disappeared.
func (w *MarketDelistWatcher) Check(ctx context.Context) (*DelistWatchResult, error) {
	if w == nil || w.Markets == nil || w.Grants == nil || w.Versions == nil || w.MarketKey == nil {
		return nil, ErrChatNotConfigured
	}
	current := w.Markets()
	result := &DelistWatchResult{Checked: len(current)}
	seen := make(map[string]bool, len(current))
	for _, market := range current {
		seen[market] = true
	}
	if !w.primed {
		// First reading only establishes the baseline: nothing is "gone" yet.
		w.known = seen
		w.primed = true
		return result, nil
	}
	gone := make([]string, 0)
	for market := range w.known {
		if !seen[market] {
			gone = append(gone, market)
		}
	}
	sort.Strings(gone)
	now := w.now()
	for _, market := range gone {
		if err := w.respond(ctx, market, now, result); err != nil {
			// The market stays known: the next cycle retries, and every step is
			// idempotent, so a retry cannot double-fire.
			return result, err
		}
		result.Disappeared = append(result.Disappeared, market)
		delete(w.known, market)
	}
	for market := range seen {
		w.known[market] = true
	}
	return result, nil
}

// respond applies the response chain for one disappeared market.
func (w *MarketDelistWatcher) respond(ctx context.Context, market string, now time.Time, result *DelistWatchResult) error {
	key, err := w.MarketKey(market)
	if err != nil {
		return fmt.Errorf("llm market delist: normalising %q: %w", market, err)
	}
	reason := "market disappeared from the enabled list: " + market
	grants, err := w.Grants.ListAccountGrants(ctx, w.AccountID)
	if err != nil {
		return fmt.Errorf("llm market delist: listing grants: %w", err)
	}
	revokedGrants := make([]string, 0)
	for i := range grants {
		grant := grants[i]
		if grant.Network != w.Network || grant.RevokedAt != nil || !grant.ValidUntil.After(now) {
			continue
		}
		grantKey, err := w.MarketKey(grant.MarketID)
		if err != nil {
			// A market that cannot be normalised cannot be proven unrelated to
			// the delisting: fail the pass rather than skip the row.
			return fmt.Errorf("llm market delist: grant %q carries market %q: %w", grant.ID, grant.MarketID, err)
		}
		if grantKey != key {
			continue
		}
		if err := w.Grants.RevokeGrant(ctx, grant.ID, now, delistActor, reason); err != nil {
			return fmt.Errorf("llm market delist: revoking grant %q: %w", grant.ID, err)
		}
		revokedGrants = append(revokedGrants, grant.ID)
	}

	approvals, err := w.Grants.ListActiveApprovals(ctx)
	if err != nil {
		return fmt.Errorf("llm market delist: listing approvals: %w", err)
	}
	paused := make(map[string]bool)
	revokedApprovals := make([]string, 0)
	pausedVersions := make([]string, 0)
	for i := range approvals {
		approval := approvals[i]
		if approval.AccountID != w.AccountID || approval.Network != w.Network {
			continue
		}
		markets, err := DecodeApprovedMarkets(approval.ApprovedMarketIDs)
		if err != nil {
			// A bad list is a corrupt row, not a skip: skipping could leave a
			// live approval over a market whose authorization is gone.
			return fmt.Errorf("llm market delist: approval %q: %w", approval.ID, err)
		}
		matches := false
		for _, stored := range markets {
			storedKey, err := w.MarketKey(stored)
			if err != nil {
				return fmt.Errorf("llm market delist: approval %q carries market %q: %w", approval.ID, stored, err)
			}
			if storedKey == key {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if err := w.Grants.RevokeApproval(ctx, approval.ID, now, delistActor, reason); err != nil {
			return fmt.Errorf("llm market delist: revoking approval %q: %w", approval.ID, err)
		}
		revokedApprovals = append(revokedApprovals, approval.ID)
		if paused[approval.StrategyVersionID] {
			continue
		}
		if err := w.Versions.UpdateStrategyVersionState(ctx, approval.StrategyVersionID, StrategyStatePaused); err != nil {
			return fmt.Errorf("llm market delist: pausing version %q: %w", approval.StrategyVersionID, err)
		}
		paused[approval.StrategyVersionID] = true
		pausedVersions = append(pausedVersions, approval.StrategyVersionID)
	}

	if w.Audit != nil {
		detail, err := json.Marshal(map[string]any{
			"market":           market,
			jsonKeyReason:      reason,
			"revokedGrants":    revokedGrants,
			"revokedApprovals": revokedApprovals,
			"pausedVersions":   pausedVersions,
			"action":           "grants revoked, related strategy versions paused; confirm the delisting and clean the config",
		})
		if err != nil {
			return fmt.Errorf("llm market delist: encoding the audit detail: %w", err)
		}
		if err := w.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
			ID: newLLMIdentifier(), Kind: AuditKindMarketDelisted, Actor: delistActor,
			Subject: new(market), DetailJSON: string(detail), CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("llm market delist: writing the audit row: %w", err)
		}
	}
	if w.Notify != nil {
		w.Notify.MarketDelisted(market, len(revokedGrants), len(pausedVersions))
	}
	result.RevokedGrants = append(result.RevokedGrants, revokedGrants...)
	result.PausedVersions = append(result.PausedVersions, pausedVersions...)
	return nil
}

// now returns the watcher clock, defaulting to the wall clock.
func (w *MarketDelistWatcher) now() time.Time {
	if w.Now == nil {
		return time.Now()
	}
	return w.Now()
}

// newMarketDelistWatch assembles the production watcher for Engine.Start.
//
// A zero account/network still yields a watcher whose grant filter matches
// nothing -- which is the honest state before an operator fills the config;
// the market diff and the notification still work.
func (bot *Engine) newMarketDelistWatch() *MarketDelistWatcher {
	if bot == nil {
		return nil
	}
	cfg := &bot.Config.LLM
	item, itemErr := asset.New(cfg.Asset)
	watch := &MarketDelistWatcher{
		Grants:    LLMMarketGrants{},
		Versions:  LLMPairSwitchStore{},
		Audit:     LLMDecisionStore{},
		Notify:    bot.LLMNotifier(),
		AccountID: strings.TrimSpace(cfg.AccountID),
		Network:   strings.TrimSpace(cfg.Network),
		Now:       time.Now,
		MarketKey: func(raw string) (string, error) {
			if itemErr != nil {
				return "", itemErr
			}
			return runMarketID(cfg.Exchange, item, raw)
		},
	}
	return watch
}

// ListAccountGrants adapts the repository's per-account grant listing to the
// delist seam. RevokeGrant/ListActiveApprovals/RevokeApproval already exist on
// LLMMarketGrants with the matching shapes.
func (LLMMarketGrants) ListAccountGrants(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error) {
	return llmstore.ListMarketTradeGrants(ctx, accountID)
}

// RevokeGrant adapts the grant revoke to the delist seam.
func (LLMMarketGrants) RevokeGrant(ctx context.Context, id string, at time.Time, by, reason string) error {
	return llmstore.RevokeMarketTradeGrant(ctx, id, at, by, reason)
}
