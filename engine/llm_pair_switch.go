package engine

import (
	"context"
	"fmt"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M4-14 配对切换失效语义（docs 05 §2.4）。
//
// 切换语义（原项目规则，本方案完整继承）：清空 active 指针 → pair_epoch 递增
// → **暂停运行中策略** → **撤销有效审批** → 所有进行中的旧配对决策标 STALE_PAIR。
//
// 前两步是 M1-10 的 CAS（`ActivateModelPair`，含幂等键与操作者）。本层是后两步的
// 级联：每个仍然有效的审批被撤销（原因 PAIR_SWITCHED），其策略版本转型 PAUSED，
// 并留一条状态迁移行；一次切换写一条 `PAIR_SWITCHED` 审计事件（含两个计数）。
//
// "STALE_PAIR 标记"不需要一张新表：进行中的运行由各自的 **epoch 重查**自我作废
// （M4-09 结束前重查、M4-11 的 `RISK_PAIR_EPOCH_MISMATCH`），已是不可变行的
// decision_run 无法也无需再加标；订单意图的 `pair_epoch` 列（M5）在提交前比对。
// 级联函数把两个计数返回给调用方，切换命令行落账时随之记录。

// AuditKindPairSwitched is the audit event kind for a landed pair switch.
const AuditKindPairSwitched = "PAIR_SWITCHED"

// PairSwitchRevokeReason is the revocation reason stamped on approvals the
// cascade withdraws.
const PairSwitchRevokeReason = "PAIR_SWITCHED"

// StrategyStatePaused is the state a suspended version moves to.
const StrategyStatePaused = "PAUSED"

// iPairSwitchApprovals / iPairSwitchVersions / iPairSwitchAudit are the cascade's
// write seams; LLMPairSwitchStore adapts them to the repository layer.
type iPairSwitchApprovals interface {
	ListActiveStrategyApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error)
	RevokeStrategyApproval(ctx context.Context, id string, at time.Time, by, reason string) error
}

type iPairSwitchVersions interface {
	UpdateStrategyVersionState(ctx context.Context, id, state string) error
	InsertStrategyStateTransition(ctx context.Context, t *llmstore.StrategyStateTransition) error
}

// LLMPairSwitchStore is the production adapter over database/repository/llm.
type LLMPairSwitchStore struct{}

// ListActiveStrategyApprovals returns every approval that is neither revoked
// nor expired.
func (LLMPairSwitchStore) ListActiveStrategyApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error) {
	return llmstore.ListActiveStrategyApprovals(ctx)
}

// RevokeStrategyApproval withdraws one approval.
func (LLMPairSwitchStore) RevokeStrategyApproval(ctx context.Context, id string, at time.Time, by, reason string) error {
	return llmstore.RevokeStrategyApproval(ctx, id, at, by, reason)
}

// UpdateStrategyVersionState advances one version's state column.
func (LLMPairSwitchStore) UpdateStrategyVersionState(ctx context.Context, id, state string) error {
	return llmstore.UpdateStrategyVersionState(ctx, id, state)
}

// InsertStrategyStateTransition appends a transition row.
func (LLMPairSwitchStore) InsertStrategyStateTransition(ctx context.Context, t *llmstore.StrategyStateTransition) error {
	return llmstore.InsertStrategyStateTransition(ctx, t)
}

// InsertAuditEvent appends an audit event.
func (LLMPairSwitchStore) InsertAuditEvent(ctx context.Context, e *llmstore.AuditEvent) error {
	return llmstore.InsertAuditEvent(ctx, e)
}

// PairSwitchCascade suspends the strategies a landed pair switch invalidates.
type PairSwitchCascade struct {
	Approvals iPairSwitchApprovals
	Versions  iPairSwitchVersions
	Audit     iAuditEvents
	Now       func() time.Time
	NewID     func() string
}

// PairSwitchCascadeInput identifies the switch.
type PairSwitchCascadeInput struct {
	Actor     string
	FromEpoch int64
	ToEpoch   int64
}

// PairSwitchCascadeResult reports what the cascade changed; the caller records
// the counts alongside the switch command.
type PairSwitchCascadeResult struct {
	SuspendedVersions []string `json:"suspendedVersions"`
	RevokedApprovals  []string `json:"revokedApprovals"`
}

// Apply runs the cascade. The audit event is written even when nothing was
// live: an epoch that advanced with no approvals to revoke is still a switch
// other components must be able to read from the event stream.
func (c *PairSwitchCascade) Apply(ctx context.Context, in PairSwitchCascadeInput) (*PairSwitchCascadeResult, error) {
	if c == nil || c.Approvals == nil || c.Versions == nil || c.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	if in.Actor == "" {
		return nil, ErrInvalidActor
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	newID := func() string { return "" }
	if c.NewID != nil {
		newID = c.NewID
	}

	approvals, err := c.Approvals.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm pair switch: listing active approvals: %w", err)
	}
	result := &PairSwitchCascadeResult{}
	for i := range approvals {
		approval := &approvals[i]
		if err := c.Approvals.RevokeStrategyApproval(ctx, approval.ID, now, in.Actor, PairSwitchRevokeReason); err != nil {
			return nil, fmt.Errorf("llm pair switch: revoking approval %q: %w", approval.ID, err)
		}
		if err := c.Versions.UpdateStrategyVersionState(ctx, approval.StrategyVersionID, StrategyStatePaused); err != nil {
			return nil, fmt.Errorf("llm pair switch: pausing version %q: %w", approval.StrategyVersionID, err)
		}
		if err := c.Versions.InsertStrategyStateTransition(ctx, &llmstore.StrategyStateTransition{
			ID:                newID(),
			StrategyVersionID: approval.StrategyVersionID,
			FromState:         "FROZEN",
			ToState:           StrategyStatePaused,
			Reason:            PairSwitchRevokeReason,
			Actor:             in.Actor,
			CreatedAt:         now,
		}); err != nil {
			return nil, fmt.Errorf("llm pair switch: recording the transition of %q: %w", approval.StrategyVersionID, err)
		}
		result.RevokedApprovals = append(result.RevokedApprovals, approval.ID)
		result.SuspendedVersions = append(result.SuspendedVersions, approval.StrategyVersionID)
	}

	detail, err := json.Marshal(map[string]any{
		"fromPairEpoch":     in.FromEpoch,
		"toPairEpoch":       in.ToEpoch,
		"suspendedVersions": len(result.SuspendedVersions),
		"revokedApprovals":  len(result.RevokedApprovals),
		"inFlightDecisions": "STALE_PAIR: re-checked by each running pipeline at its next epoch read",
	})
	if err != nil {
		return nil, fmt.Errorf("llm pair switch: audit detail: %w", err)
	}
	subject := fmt.Sprintf("pair-epoch-%d", in.ToEpoch)
	if err := c.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID:         newID(),
		Kind:       AuditKindPairSwitched,
		Actor:      in.Actor,
		Subject:    &subject,
		DetailJSON: string(detail),
		CreatedAt:  now,
	}); err != nil {
		return nil, fmt.Errorf("llm pair switch: audit event: %w", err)
	}
	return result, nil
}
