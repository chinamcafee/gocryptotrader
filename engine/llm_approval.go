package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-05 逐版限时审批（docs 07 §2.1，08 §3.2，10 §221）。
//
// 审批把一份冻结版本变成"可执行授权"，但它不复制任何限额：风控信封来自
// 冻结版本本身，审批只是**接受**它——审批时偷偷改限额在这里没有输入面。
// 三件套一起生效：显式绑定 8 要素、操作者重新认证、以及当次 pair_epoch 的
// CAS（配对在冻结之后换过，这版就不能再批）。

var (
	// ErrApprovalInputInvalid marks a request missing one of the eight
	// elements or carrying an out-of-range window.
	ErrApprovalInputInvalid = errors.New("llm approval: the request is missing required elements")

	// ErrApprovalRequired marks an operator command (for example a resume)
	// that needs a live approval the version does not have.
	ErrApprovalRequired = errors.New("llm approval: a live approval is required")
	// ErrReAuthFailed marks a failed or absent operator re-authentication.
	ErrReAuthFailed = errors.New("llm approval: operator re-authentication failed")
	// ErrStrategyNotFrozen marks an approval of a version without frozen
	// content.
	ErrStrategyNotFrozen = errors.New("llm approval: the strategy version is not frozen")
	// ErrApprovalBindingMismatch marks a request that does not match the
	// frozen policy it names.
	ErrApprovalBindingMismatch = errors.New("llm approval: the request does not match the frozen policy")
	// ErrStalePolicyPair marks a policy frozen under a different pair epoch.
	ErrStalePolicyPair = errors.New("llm approval: the policy was frozen under a different pair epoch")
	// ErrMarketNotGranted marks an approved market without an active grant.
	ErrMarketNotGranted = errors.New("llm approval: a named market has no active trade grant")
	// ErrApprovalConflict marks a second approval with a different binding.
	ErrApprovalConflict = errors.New("llm approval: this version already carries a different approval")
)

// ApprovalAuditKind is the audit event kind recorded per approval.
const ApprovalAuditKind = "strategy_approved"

// iApprovals is the seam the approval service needs.
type iApprovals interface {
	GetVersion(ctx context.Context, id string) (*llmstore.StrategyVersion, error)
	GetApprovalByVersion(ctx context.Context, strategyVersionID string) (*llmstore.StrategyApproval, error)
	InsertApproval(ctx context.Context, a *llmstore.StrategyApproval) error
	ListGrants(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error)
	ActivePairEpoch(ctx context.Context) (int64, error)
	RecordAudit(ctx context.Context, e *llmstore.AuditEvent) error
}

// LLMApprovals adapts the repository.
type LLMApprovals struct{}

// GetVersion returns a strategy version row.
func (LLMApprovals) GetVersion(ctx context.Context, id string) (*llmstore.StrategyVersion, error) {
	return llmstore.GetStrategyVersion(ctx, id)
}

// GetApprovalByVersion returns the version's approval, or llmstore.ErrNotFound.
func (LLMApprovals) GetApprovalByVersion(ctx context.Context, strategyVersionID string) (*llmstore.StrategyApproval, error) {
	return llmstore.GetStrategyApprovalByVersion(ctx, strategyVersionID)
}

// InsertApproval stores an approval row.
func (LLMApprovals) InsertApproval(ctx context.Context, a *llmstore.StrategyApproval) error {
	return llmstore.InsertStrategyApproval(ctx, a)
}

// ListGrants returns an account's market grants.
func (LLMApprovals) ListGrants(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error) {
	return llmstore.ListMarketTradeGrants(ctx, accountID)
}

// ActivePairEpoch returns the current pair epoch.
func (LLMApprovals) ActivePairEpoch(ctx context.Context) (int64, error) {
	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		return 0, fmt.Errorf("llm approval: reading the active pair: %w", err)
	}
	return pair.PairEpoch, nil
}

// RecordAudit appends one audit event.
func (LLMApprovals) RecordAudit(ctx context.Context, e *llmstore.AuditEvent) error {
	return llmstore.InsertAuditEvent(ctx, e)
}

// ApprovalRequest is the eight-element approval (08 §3.2): none of it is
// derived, because every element is part of what the operator is accepting.
type ApprovalRequest struct {
	Actor             string
	VersionID         string
	AccountID         string
	Network           string
	ExecutionMode     string
	Markets           []string
	ValidFor          time.Duration
	OperatorPassword  string
	IdempotencyKey    string
	ExpectedPairEpoch int64
}

// ApprovalService approves frozen strategy versions.
//
// The version state stays FROZEN: the approval row itself (UNIQUE per version)
// is the APPROVED signal, so there is exactly one source of truth for "is this
// version approved" and no trigger question about mutating a frozen row.
type ApprovalService struct {
	Approvals      iApprovals
	VerifyPassword func(ctx context.Context, password string) error
	Now            func() time.Time
	NewID          func() string
}

// Approve runs the approval gates and stores the approval.
//
// The returned bool reports an idempotent replay: a second call with a binding
// identical to the stored one returns the stored row instead of a conflict.
func (s *ApprovalService) Approve(ctx context.Context, req *ApprovalRequest) (*llmstore.StrategyApproval, bool, error) {
	if s == nil || s.Approvals == nil || s.VerifyPassword == nil || s.NewID == nil {
		return nil, false, ErrChatNotConfigured
	}
	req.Actor = strings.TrimSpace(req.Actor)
	req.VersionID = strings.TrimSpace(req.VersionID)
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.Network = strings.TrimSpace(req.Network)
	req.ExecutionMode = strings.ToUpper(strings.TrimSpace(req.ExecutionMode))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Actor == "" || req.VersionID == "" || req.AccountID == "" || req.Network == "" ||
		req.IdempotencyKey == "" || req.ValidFor <= 0 || req.ValidFor > candidate.MaxApprovalDuration ||
		len(req.Markets) == 0 || len(req.Markets) > candidate.MaxMarketScopes {
		return nil, false, ErrApprovalInputInvalid
	}
	switch req.ExecutionMode {
	case LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive:
	default:
		return nil, false, fmt.Errorf("%w: execution mode %q", ErrApprovalInputInvalid, req.ExecutionMode)
	}
	seen := make(map[string]bool, len(req.Markets))
	for _, market := range req.Markets {
		if strings.TrimSpace(market) == "" {
			return nil, false, fmt.Errorf("%w: blank market", ErrApprovalInputInvalid)
		}
		if seen[market] {
			return nil, false, fmt.Errorf("%w: duplicate market %q", ErrApprovalInputInvalid, market)
		}
		seen[market] = true
	}

	// Re-authentication first: every later gate would be moot if the caller is
	// not the operator they claim to be.
	if strings.TrimSpace(req.OperatorPassword) == "" {
		return nil, false, ErrReAuthFailed
	}
	if err := s.VerifyPassword(ctx, req.OperatorPassword); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrReAuthFailed, err)
	}

	version, err := s.Approvals.GetVersion(ctx, req.VersionID)
	if err != nil {
		return nil, false, fmt.Errorf("llm approval: reading version %q: %w", req.VersionID, err)
	}
	if version.FrozenJSON == nil || version.FrozenAt == nil {
		return nil, false, ErrStrategyNotFrozen
	}
	var policy candidate.ReviewablePolicy
	if err := decodePolicyStrict(*version.FrozenJSON, &policy); err != nil {
		return nil, false, fmt.Errorf("llm approval: frozen content: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, false, fmt.Errorf("llm approval: frozen content: %w", err)
	}

	currentEpoch, err := s.Approvals.ActivePairEpoch(ctx)
	if err != nil {
		return nil, false, err
	}
	if req.ExpectedPairEpoch != currentEpoch {
		return nil, false, fmt.Errorf("%w: expected %d, current %d", ErrPairEpochConflict, req.ExpectedPairEpoch, currentEpoch)
	}
	if policy.PairEpoch != currentEpoch {
		return nil, false, fmt.Errorf("%w: frozen under %d, current %d", ErrStalePolicyPair, policy.PairEpoch, currentEpoch)
	}
	if policy.AccountID != req.AccountID || policy.Network != req.Network || policy.ExecutionMode != req.ExecutionMode {
		return nil, false, fmt.Errorf("%w: account/network/mode differ from the frozen policy", ErrApprovalBindingMismatch)
	}
	scoped := make(map[string]bool, len(policy.MarketScopes))
	for i := range policy.MarketScopes {
		scoped[policy.MarketScopes[i].MarketID] = true
	}
	for _, market := range req.Markets {
		if !scoped[market] {
			return nil, false, fmt.Errorf("%w: %q is outside the frozen policy", ErrApprovalBindingMismatch, market)
		}
	}

	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	nowAt := now()
	prospectiveValidUntil := nowAt.Add(req.ValidFor)
	grants, err := s.Approvals.ListGrants(ctx, req.AccountID)
	if err != nil {
		return nil, false, fmt.Errorf("llm approval: reading grants: %w", err)
	}
	for _, market := range req.Markets {
		if !hasActiveGrant(grants, req.Network, market, nowAt) {
			return nil, false, fmt.Errorf("%w: %s", ErrMarketNotGranted, market)
		}
	}

	marketsJSON, err := EncodeApprovedMarkets(req.Markets)
	if err != nil {
		return nil, false, err
	}
	riskJSON, err := json.Marshal(policy.Risk)
	if err != nil {
		return nil, false, fmt.Errorf("llm approval: canonical risk envelope: %w", err)
	}
	riskHash := sha256Hex(riskJSON)

	// Idempotency: one approval per version, so a second call is a replay only
	// when its binding is identical.
	existing, err := s.Approvals.GetApprovalByVersion(ctx, req.VersionID)
	switch {
	case err == nil:
		if existing.AccountID == req.AccountID && existing.Network == req.Network &&
			existing.ExecutionMode == req.ExecutionMode && existing.ApprovedMarketIDs == marketsJSON &&
			existing.RiskEnvelopeHash == riskHash && existing.PairEpoch == currentEpoch &&
			existing.ValidUntil.Equal(prospectiveValidUntil) {
			// The window is part of what was accepted: a replay with a
			// different window is a different approval, not the same one.
			return existing, true, nil
		}
		return nil, false, ErrApprovalConflict
	case !errors.Is(err, llmstore.ErrNotFound):
		return nil, false, fmt.Errorf("llm approval: reading the existing approval: %w", err)
	}

	approval := llmstore.StrategyApproval{
		ID: s.NewID(), StrategyVersionID: req.VersionID,
		AccountID: req.AccountID, Network: req.Network, ExecutionMode: req.ExecutionMode,
		ApprovedMarketIDs: marketsJSON, RiskEnvelopeJSON: string(riskJSON), RiskEnvelopeHash: riskHash,
		PairEpoch:            currentEpoch,
		LLMProfileRevisionID: policy.LLMRevisionID, JevProfileRevisionID: policy.JevRevisionID,
		ValidFrom: nowAt, ValidUntil: nowAt.Add(req.ValidFor),
		ApprovedBy: req.Actor, ApprovedAt: nowAt,
	}
	if err := s.Approvals.InsertApproval(ctx, &approval); err != nil {
		return nil, false, fmt.Errorf("llm approval: storing: %w", err)
	}
	auditDetail, err := json.Marshal(map[string]any{
		"versionId":        req.VersionID,
		"accountId":        req.AccountID,
		"network":          req.Network,
		"executionMode":    req.ExecutionMode,
		"markets":          req.Markets,
		"pairEpoch":        currentEpoch,
		"riskEnvelopeHash": riskHash,
		"validUntil":       approval.ValidUntil,
		"idempotencyKey":   req.IdempotencyKey,
	})
	if err != nil {
		return nil, false, fmt.Errorf("llm approval: audit detail: %w", err)
	}
	subject := req.VersionID
	if err := s.Approvals.RecordAudit(ctx, &llmstore.AuditEvent{
		ID: s.NewID(), Kind: ApprovalAuditKind, Actor: req.Actor,
		Subject: &subject, DetailJSON: string(auditDetail), CreatedAt: nowAt,
	}); err != nil {
		return nil, false, fmt.Errorf("llm approval: the approval is stored but its audit event was not recorded: %w", err)
	}
	return &approval, false, nil
}

// hasActiveGrant reports whether a live grant covers the market.
func hasActiveGrant(grants []llmstore.MarketTradeGrant, network, marketID string, now time.Time) bool {
	for i := range grants {
		grant := grants[i]
		if grant.Network != network || grant.MarketID != marketID {
			continue
		}
		if grant.RevokedAt != nil || !grant.ValidUntil.After(now) {
			continue
		}
		return true
	}
	return false
}
