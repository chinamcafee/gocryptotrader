package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-02 策略版本冻结（docs 07 §2.1/§2.2）。
//
// 冻结把一份 READY_FOR_REVIEW 的草案变成不可变的 reviewable-v1 版本，并把
// 两个哈希钉进版本行：risk_envelope_hash 与 strategy_hash。运行时每次决策前
// 重算并与审批记录比对，任何路径悄悄改限额都会以 RISK_ENVELOPE_MISMATCH
// 暴露（07 §2.2）。
//
// 序列化是确定性的：Go 的 encoding/json 对结构体按键声明序、无多余空白、
// 数字不重排——同一语义必然同一哈希；任何字段顺序/标签改动都会改哈希，
// 而 golden 哈希测试会让它当场失败，逼一次有意识的迁移。

var (
	// ErrFreezeInputInvalid marks a freeze without version id or actor.
	ErrFreezeInputInvalid = errors.New("llm strategy: version id and actor are required")
	// ErrProposalNotReady marks a version whose stored proposal state is not
	// READY_FOR_REVIEW.
	ErrProposalNotReady = errors.New("llm strategy: the stored proposal is not ready for review")
	// ErrAlreadyFrozen marks a second freeze of the same version.
	ErrAlreadyFrozen = errors.New("llm strategy: the version is already frozen")
)

// iStrategyVersions is the version-table seam the freezer needs.
type iStrategyVersions interface {
	Get(ctx context.Context, id string) (*llmstore.StrategyVersion, error)
	Freeze(ctx context.Context, id, frozenJSON, strategyHash, riskEnvelopeHash string, at time.Time) error
	RecordTransition(ctx context.Context, t *llmstore.StrategyStateTransition) error
}

// LLMStrategyVersions adapts the repository to the freezer.
type LLMStrategyVersions struct{}

// Get returns a strategy version row.
func (LLMStrategyVersions) Get(ctx context.Context, id string) (*llmstore.StrategyVersion, error) {
	return llmstore.GetStrategyVersion(ctx, id)
}

// Freeze writes the frozen fields.
func (LLMStrategyVersions) Freeze(ctx context.Context, id, frozenJSON, strategyHash, riskEnvelopeHash string, at time.Time) error {
	return llmstore.FreezeStrategyVersion(ctx, id, frozenJSON, strategyHash, riskEnvelopeHash, at)
}

// RecordTransition appends a state transition row.
func (LLMStrategyVersions) RecordTransition(ctx context.Context, t *llmstore.StrategyStateTransition) error {
	return llmstore.InsertStrategyStateTransition(ctx, t)
}

// StrategyFreezer freezes reviewable policies.
type StrategyFreezer struct {
	Versions iStrategyVersions
	Now      func() time.Time
	NewID    func() string
}

// FreezeResult is what a freeze produced.
type FreezeResult struct {
	StrategyHash     string    `json:"strategyHash"`
	RiskEnvelopeHash string    `json:"riskEnvelopeHash"`
	FrozenJSON       string    `json:"frozenJson"`
	FrozenAt         time.Time `json:"frozenAt"`
}

// Freeze validates the reviewable policy, computes the two canonical hashes
// and freezes the version.
//
// The stored proposal must already be READY_FOR_REVIEW: freezing a version
// whose proposal had open clarifying questions would mint an approvable
// version out of an unanswered one.
func (f *StrategyFreezer) Freeze(ctx context.Context, versionID string, policy *candidate.ReviewablePolicy, actor string) (*FreezeResult, error) {
	if f == nil || f.Versions == nil || f.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(versionID) == "" || strings.TrimSpace(actor) == "" {
		return nil, ErrFreezeInputInvalid
	}
	// Validate enforces the reviewable contract: READY_FOR_APPROVAL, false
	// trade authorization, <= 7-day window, leverage cap on perpetuals and
	// both model revisions present.
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	version, err := f.Versions.Get(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("llm strategy: reading version %q: %w", versionID, err)
	}
	if version.FrozenAt != nil || version.FrozenJSON != nil {
		return nil, ErrAlreadyFrozen
	}
	if version.ProposalState == nil || *version.ProposalState != string(candidate.StateReadyForReview) {
		return nil, fmt.Errorf("%w: %q", ErrProposalNotReady, versionID)
	}

	riskJSON, err := json.Marshal(policy.Risk)
	if err != nil {
		return nil, fmt.Errorf("llm strategy: canonical risk envelope: %w", err)
	}
	strategyJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("llm strategy: canonical policy: %w", err)
	}
	riskHash := sha256Hex(riskJSON)
	strategyHash := sha256Hex(strategyJSON)

	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	frozenAt := now()
	if err := f.Versions.Freeze(ctx, versionID, string(strategyJSON), strategyHash, riskHash, frozenAt); err != nil {
		return nil, fmt.Errorf("llm strategy: freezing version %q: %w", versionID, err)
	}
	transition := llmstore.StrategyStateTransition{
		ID:                f.NewID(),
		StrategyVersionID: versionID,
		FromState:         version.State,
		ToState:           "FROZEN",
		Reason:            "reviewable-v1 freeze",
		Actor:             actor,
		CreatedAt:         frozenAt,
	}
	if err := f.Versions.RecordTransition(ctx, &transition); err != nil {
		// The freeze is durable; the missing transition is reported rather than
		// swallowed, because the audit trail is what makes it reviewable.
		return nil, fmt.Errorf("llm strategy: version %q was frozen but the state transition was not recorded: %w", versionID, err)
	}
	return &FreezeResult{
		StrategyHash:     strategyHash,
		RiskEnvelopeHash: riskHash,
		FrozenJSON:       string(strategyJSON),
		FrozenAt:         frozenAt,
	}, nil
}

// RiskEnvelopeHashOf recomputes the risk envelope hash of a frozen JSON
// document; the runtime comparison in 07 §2.2 uses exactly this.
func RiskEnvelopeHashOf(frozenJSON string) (string, error) {
	var policy candidate.ReviewablePolicy
	if err := json.Unmarshal([]byte(frozenJSON), &policy); err != nil {
		return "", fmt.Errorf("llm strategy: frozen json: %w", err)
	}
	riskJSON, err := json.Marshal(policy.Risk)
	if err != nil {
		return "", fmt.Errorf("llm strategy: canonical risk envelope: %w", err)
	}
	return sha256Hex(riskJSON), nil
}

// sha256Hex returns the lowercase hex digest.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
