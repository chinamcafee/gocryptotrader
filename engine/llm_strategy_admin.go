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

// M4-01 残余（W5 工作区后端出口）：策略创建 + 草案版本持久化。
//
// 此前的写路径只到 freeze/approve——没有生产方创建 strategy /
// strategy_version 行，也没有把一次条款提取的结果落成版本行（提取本身按
// M4-01 的定案不持久化）。本层补上「草案成为版本」这一步（07 §1）：
// 运营者审阅提取结果后显式保存为**新草案版本**，版本树因此是追加的
// （「继续这版」= 从同一父再存一版），没有任何就地图改的入口。
//
// 提案状态（NEEDS_CLARIFICATION / READY_FOR_REVIEW）由服务端从提案文档
// 推导，不接受客户端自报——带着未答澄清问题的草案不许自称就绪。

// Audit kinds written by the strategy admin operations.
const (
	AuditKindStrategyCreated    = "STRATEGY_CREATED"
	AuditKindStrategyDraftSaved = "STRATEGY_DRAFT_SAVED"
)

// StrategyStateDraft is the state of a version that holds an unreviewed
// proposal (the lifecycle names come from 07 §1).
const StrategyStateDraft = "DRAFT"

var (
	// ErrStrategyAdminInput marks a malformed admin command.
	ErrStrategyAdminInput = errors.New("llm strategy admin: the command is incomplete")
	// ErrStrategyArchived marks a draft save under an archived strategy.
	ErrStrategyArchived = errors.New("llm strategy admin: the strategy is archived")
)

// iStrategyAdminStore is the repository seam of the admin service.
type iStrategyAdminStore interface {
	InsertStrategy(ctx context.Context, s *llmstore.Strategy) error
	GetStrategy(ctx context.Context, id string) (*llmstore.Strategy, error)
	InsertStrategyVersion(ctx context.Context, v *llmstore.StrategyVersion) error
	GetStrategyVersion(ctx context.Context, id string) (*llmstore.StrategyVersion, error)
	InsertAuditEvent(ctx context.Context, e *llmstore.AuditEvent) error
}

// LLMStrategyAdminStore adapts the repository to the admin service.
type LLMStrategyAdminStore struct{}

// InsertStrategy inserts a strategy row.
func (LLMStrategyAdminStore) InsertStrategy(ctx context.Context, s *llmstore.Strategy) error {
	return llmstore.InsertStrategy(ctx, s)
}

// GetStrategy returns one strategy row.
func (LLMStrategyAdminStore) GetStrategy(ctx context.Context, id string) (*llmstore.Strategy, error) {
	return llmstore.GetStrategy(ctx, id)
}

// InsertStrategyVersion inserts a version row.
func (LLMStrategyAdminStore) InsertStrategyVersion(ctx context.Context, v *llmstore.StrategyVersion) error {
	return llmstore.InsertStrategyVersion(ctx, v)
}

// GetStrategyVersion returns one version row.
func (LLMStrategyAdminStore) GetStrategyVersion(ctx context.Context, id string) (*llmstore.StrategyVersion, error) {
	return llmstore.GetStrategyVersion(ctx, id)
}

// InsertAuditEvent appends one audit event.
func (LLMStrategyAdminStore) InsertAuditEvent(ctx context.Context, e *llmstore.AuditEvent) error {
	return llmstore.InsertAuditEvent(ctx, e)
}

// LLMStrategyAdmin creates strategies and persists policy drafts.
type LLMStrategyAdmin struct {
	Store iStrategyAdminStore
	Now   func() time.Time
	NewID func() string
}

// CreateStrategy inserts a strategy and audits the creation.
func (a *LLMStrategyAdmin) CreateStrategy(ctx context.Context, name, actor string) (*llmstore.Strategy, error) {
	if a == nil || a.Store == nil || a.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	name = strings.TrimSpace(name)
	actor = strings.TrimSpace(actor)
	if name == "" || actor == "" {
		return nil, fmt.Errorf("%w: name and actor", ErrStrategyAdminInput)
	}
	now := a.now()
	strategy := &llmstore.Strategy{ID: a.NewID(), Name: name, CreatedBy: actor, CreatedAt: now}
	if err := a.Store.InsertStrategy(ctx, strategy); err != nil {
		return nil, err
	}
	subject := strategy.ID
	if err := a.Store.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: a.NewID(), Kind: AuditKindStrategyCreated, Actor: actor,
		Subject: &subject, DetailJSON: fmt.Sprintf(`{"name":%q}`, name), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm strategy admin: audit event: %w", err)
	}
	return strategy, nil
}

// SavePolicyDraft persists one extraction proposal as a new draft version.
//
// The proposal state is derived server-side from the document itself
// (candidate.PolicyProposal.State); the client cannot declare a draft ready
// that still carries open clarifying questions. The version's source hash is
// the SHA-256 of the exact proposal bytes the operator reviewed.
//
// The injected clock is the version's created_at, which the schema pins as
// UNIQUE(strategy_id, created_at): a monotonic wall clock is fine, a frozen
// test clock must step between saves.
func (a *LLMStrategyAdmin) SavePolicyDraft(ctx context.Context, strategyID, parentVersionID, proposalJSON, actor string) (*llmstore.StrategyVersion, error) {
	if a == nil || a.Store == nil || a.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	strategyID = strings.TrimSpace(strategyID)
	actor = strings.TrimSpace(actor)
	if strategyID == "" || actor == "" || strings.TrimSpace(proposalJSON) == "" {
		return nil, fmt.Errorf("%w: strategy_id, proposal_json and actor", ErrStrategyAdminInput)
	}
	strategy, err := a.Store.GetStrategy(ctx, strategyID)
	if err != nil {
		return nil, err
	}
	if strategy.ArchivedAt != nil {
		return nil, fmt.Errorf("%w: %q", ErrStrategyArchived, strategyID)
	}
	parentVersionID = strings.TrimSpace(parentVersionID)
	var parent *string
	if parentVersionID != "" {
		parentVersion, err := a.Store.GetStrategyVersion(ctx, parentVersionID)
		if err != nil {
			return nil, fmt.Errorf("llm strategy admin: reading parent version: %w", err)
		}
		if parentVersion.StrategyID != strategyID {
			return nil, fmt.Errorf("%w: parent version %q belongs to another strategy", ErrStrategyAdminInput, parentVersionID)
		}
		parent = &parentVersionID
	}
	var proposal candidate.PolicyProposal
	if err := json.Unmarshal([]byte(proposalJSON), &proposal); err != nil {
		return nil, fmt.Errorf("llm strategy admin: proposal json: %w", err)
	}
	// A proposal with no stated risk limits must not claim readiness; State()
	// is the same derivation the extractor and the freeze gate use.
	state := string(proposal.State())
	questions := proposal.ClarifyingQuestions
	if questions == nil {
		questions = []string{}
	}
	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		return nil, fmt.Errorf("llm strategy admin: clarifying questions: %w", err)
	}
	questionsText := string(questionsJSON)
	now := a.now()
	version := &llmstore.StrategyVersion{
		ID:                  a.NewID(),
		StrategyID:          strategyID,
		ParentVersionID:     parent,
		State:               StrategyStateDraft,
		SourcePromptSha256:  sha256Hex([]byte(proposalJSON)),
		ProposalJSON:        &proposalJSON,
		ClarifyingQuestions: &questionsText,
		ProposalState:       &state,
		CreatedAt:           now,
	}
	if err := a.Store.InsertStrategyVersion(ctx, version); err != nil {
		return nil, err
	}
	subject := version.ID
	detail := fmt.Sprintf(`{"strategyId":%q,"proposalState":%q,"parentVersionId":%q}`, strategyID, state, parentVersionID)
	if err := a.Store.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: a.NewID(), Kind: AuditKindStrategyDraftSaved, Actor: actor,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm strategy admin: audit event: %w", err)
	}
	return version, nil
}

// now returns the injected clock or the wall clock.
func (a *LLMStrategyAdmin) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}
