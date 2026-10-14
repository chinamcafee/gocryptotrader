package engine

import (
	"fmt"

	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-11 事件通知（docs 08 §5、Q19）。
//
// 复用 `CommunicationManager.PushEvent(base.Event{Type, Message})` 的既有通道
// （Telegram/...；通用 webhook 通道是 M5-16 的独立交付）。本层是 LLM 侧的
// 事件词汇与消息成文：**每个事件都给操作者回答同一个问题——"系统刚刚做了
// 什么、我该看哪里"**。
//
// 纪律：通知是告知，不是决策链的一环。`Emit` 永不返回错误、永不阻塞——
// 一个 Telegram 故障不该拦下一笔已经过闸的止损（PushEvent 自身在未运行时
// 即丢弃，本层与之同语义）。`requireConfirmation=false`（Q19 定案）：
// 批准/撤销/清 halt 必须回 Web UI/gctcli 重新认证，聊天软件里没有一键完成。

// LLM event types (08 §5). halt set/clear are two types, hence 15 constants.
const (
	EventDecisionAllow        = "llm_decision_allow"
	EventDecisionReject       = "llm_decision_reject"
	EventDecisionReview       = "llm_decision_review"
	EventOrderSubmitted       = "llm_order_submitted"
	EventOrderFilled          = "llm_order_filled"
	EventOrderUnknown         = "llm_order_unknown"
	EventPairChanged          = "llm_pair_changed"
	EventProbeFailed          = "llm_probe_failed"
	EventBudgetExhausted      = "llm_budget_exhausted"
	EventHaltSet              = "llm_halt_set"
	EventHaltCleared          = "llm_halt_cleared"
	EventReconcileMismatch    = "llm_reconcile_mismatch"
	EventStrategyExpiring     = "llm_strategy_expiring"
	EventProtectiveExitFailed = "llm_protective_exit_failed"
	EventDataStale            = "llm_data_stale"
)

// jsonKeyReason is the one spelling of the reason field shared by event
// details, decision records, delist audits and metric labels; a second
// spelling would make a dashboard query miss rows.
const jsonKeyReason = "reason"

// Notification severities. The doc flags exactly two event classes with
// warning levels; everything else is informational.
const (
	SeverityInfo     = "info"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// LLMEvent is one notification, already phrased for a human.
type LLMEvent struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
}

// LLMNotifier turns LLM events into communications pushes.
type LLMNotifier struct {
	// Push is the CommunicationsManager seam; nil drops silently, matching
	// PushEvent's own behaviour when the manager is not running.
	Push func(evt base.Event)
}

// Emit pushes one event. It never fails: notifications must not be able to
// block or fail the execution path they report on.
func (n *LLMNotifier) Emit(evt LLMEvent) {
	if n == nil || n.Push == nil {
		return
	}
	message := fmt.Sprintf("[%s] %s", evt.Severity, evt.Summary)
	if evt.Detail != "" {
		message += " | " + evt.Detail
	}
	n.Push(base.Event{Type: evt.Type, Message: message})
}

// notify pushes a packaged event.
func (n *LLMNotifier) notify(eventType, severity, summary string, detail any) {
	detailText := ""
	if detail != nil {
		if raw, err := json.Marshal(detail); err == nil {
			detailText = string(raw)
		}
	}
	n.Emit(LLMEvent{Type: eventType, Severity: severity, Summary: summary, Detail: detailText})
}

// DecisionAllow announces a risk-cleared order about to be placed.
func (n *LLMNotifier) DecisionAllow(strategyVersionID, marketID, action, quantity, notionalUSD, reason string) {
	n.notify(EventDecisionAllow, SeverityInfo,
		fmt.Sprintf("decision ALLOW: %s %s %s on %s", action, quantity, marketID, strategyVersionID),
		map[string]string{
			"strategyVersionId": strategyVersionID, "marketId": marketID, "action": action,
			"quantity": quantity, "notionalUsd": notionalUSD, jsonKeyReason: reason,
		})
}

// DecisionReject announces a risk refusal with actual-vs-allowed.
func (n *LLMNotifier) DecisionReject(rejections []RiskRejection) {
	summary := "decision REJECT"
	if len(rejections) > 0 {
		first := rejections[0]
		summary = fmt.Sprintf("decision REJECT: %s (actual %s vs allowed %s)", first.Code, first.Actual, first.Allowed)
	}
	n.notify(EventDecisionReject, SeverityInfo, summary, rejections)
}

// DecisionReview announces a hand-off to a human.
func (n *LLMNotifier) DecisionReview(strategyVersionID, marketID, reasonCode string) {
	n.notify(EventDecisionReview, SeverityInfo,
		fmt.Sprintf("decision REVIEW on %s: %s", marketID, reasonCode),
		map[string]string{"strategyVersionId": strategyVersionID, "marketId": marketID, "reasonCode": reasonCode})
}

// OrderSubmitted announces a venue acknowledgement.
func (n *LLMNotifier) OrderSubmitted(intentID, exchangeOrderID, clientOrderID string) {
	n.notify(EventOrderSubmitted, SeverityInfo,
		"order submitted: "+exchangeOrderID,
		map[string]string{"intentId": intentID, "exchangeOrderId": exchangeOrderID, "clientOrderId": clientOrderID})
}

// OrderFilled announces a fill.
func (n *LLMNotifier) OrderFilled(intentID, amount, averagePrice, fee string) {
	n.notify(EventOrderFilled, SeverityInfo,
		fmt.Sprintf("order filled: %s @ %s (fee %s)", amount, averagePrice, fee),
		map[string]string{"intentId": intentID, "amount": amount, "averagePrice": averagePrice, "fee": fee})
}

// OrderUnknown rings the loud bell: the submission's result is unclear and the
// account needs reconciliation.
func (n *LLMNotifier) OrderUnknown(intentID, detail string) {
	n.notify(EventOrderUnknown, SeverityHigh,
		fmt.Sprintf("ORDER RESULT UNKNOWN on %s; account flagged for reconciliation", intentID),
		map[string]string{"intentId": intentID, "detail": detail})
}

// PairChanged announces a model-pair switch and its blast radius.
func (n *LLMNotifier) PairChanged(newEpoch int64, affectedStrategies int) {
	n.notify(EventPairChanged, SeverityInfo,
		fmt.Sprintf("model pair switched: epoch %d, %d strategies affected", newEpoch, affectedStrategies),
		map[string]any{"pairEpoch": newEpoch, "affectedStrategies": affectedStrategies})
}

// ProbeFailed announces a capability probe failure.
func (n *LLMNotifier) ProbeFailed(profileRevisionID, layer, errorCode string) {
	n.notify(EventProbeFailed, SeverityInfo,
		fmt.Sprintf("probe failed for %s at layer %s: %s", profileRevisionID, layer, errorCode),
		map[string]string{"profileRevisionId": profileRevisionID, "layer": layer, "errorCode": errorCode})
}

// BudgetExhausted announces that the model budget is gone.
func (n *LLMNotifier) BudgetExhausted(profileRevisionID string) {
	n.notify(EventBudgetExhausted, SeverityInfo,
		"model budget exhausted: zero reservations left",
		map[string]string{"profileRevisionId": profileRevisionID})
}

// HaltSet announces a halt taking effect.
func (n *LLMNotifier) HaltSet(level, reason, scope string) {
	n.notify(EventHaltSet, SeverityHigh,
		fmt.Sprintf("HALT %s: %s (%s)", level, reason, scope), nil)
}

// HaltCleared announces an explicit un-halt; nothing restarts by itself.
func (n *LLMNotifier) HaltCleared(level, reason, by string) {
	n.notify(EventHaltCleared, SeverityInfo,
		fmt.Sprintf("halt %s cleared by %s: %s; strategies need individual re-enablement", level, by, reason), nil)
}

// ReconcileMismatch announces local-vs-exchange disagreements.
func (n *LLMNotifier) ReconcileMismatch(accountID string, deltas []PositionDelta) {
	n.notify(EventReconcileMismatch, SeverityHigh,
		fmt.Sprintf("reconciliation mismatch on %s: %d position delta(s)", accountID, len(deltas)), deltas)
}

// StrategyExpiring announces an approval about to lapse.
func (n *LLMNotifier) StrategyExpiring(strategyVersionID string, remainingMinutes int64) {
	n.notify(EventStrategyExpiring, SeverityInfo,
		fmt.Sprintf("approval expiring in %d minutes: %s", remainingMinutes, strategyVersionID), nil)
}

// ProtectiveExitFailed rings the loudest bell: a stop could not be placed and
// the fail-safe flatten was triggered.
func (n *LLMNotifier) ProtectiveExitFailed(parentIntentID, detail string) {
	n.notify(EventProtectiveExitFailed, SeverityCritical,
		fmt.Sprintf("PROTECTIVE EXIT FAILED on %s; fail-safe flatten triggered", parentIntentID),
		map[string]string{"parentIntentId": parentIntentID, "detail": detail})
}

// DataStale announces a fact that has aged out.
func (n *LLMNotifier) DataStale(fact string, ageSeconds int64) {
	n.notify(EventDataStale, SeverityInfo,
		fmt.Sprintf("data stale: %s is %ds old", fact, ageSeconds),
		map[string]any{"fact": fact, "ageSeconds": ageSeconds})
}

// EventMarketDelisted marks a covered market disappearing from the enabled
// list (10 §7 row 18) and its automatic response. It joins the catalogue as
// the same documented extension pattern as EventReviewExpired.
const EventMarketDelisted = "llm_market_delisted"

// MarketDelisted announces the delisting response: related grants were
// revoked and their strategy versions paused; confirming the delisting and
// cleaning the config is the operator's half.
func (n *LLMNotifier) MarketDelisted(marketID string, revokedGrants, pausedVersions int) {
	n.notify(EventMarketDelisted, SeverityHigh,
		fmt.Sprintf("market %s is gone from the enabled list: %d grant(s) revoked, %d strategy version(s) paused (confirm the delisting and clean the config)",
			marketID, revokedGrants, pausedVersions),
		map[string]any{"marketId": marketID, "revokedGrants": revokedGrants, "pausedVersions": pausedVersions})
}

// EventReviewExpired marks an expired REVIEW decision. The type is named by
// 10 §7 row 21; the 15-event catalogue predates it and it joins as the same
// documented extension pattern as EventModeSwitched.
const EventReviewExpired = "llm_review_expired"

// ReviewExpired announces that a review sat unanswered past its expiry.
func (n *LLMNotifier) ReviewExpired(decisionRunID string, ageMinutes int64) {
	n.notify(EventReviewExpired, SeverityInfo,
		fmt.Sprintf("review expired unanswered after %d minutes: %s (not executed)", ageMinutes, decisionRunID), nil)
}
