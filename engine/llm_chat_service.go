package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
)

// Conversation kinds (docs/llm-dev 08 §3.1): a chat conversation is the
// free-text conversation mode; policy extraction reuses the same store under
// its own kind so the two never mix in one list.
const (
	ConversationKindChat   = "CHAT"
	ConversationKindPolicy = "POLICY_EXTRACT"
)

// maxConversationTitleRunes bounds a title: it is stored in the clear and
// listed by the webUI, so it must not become a payload channel.
const maxConversationTitleRunes = 200

var (
	// ErrConversationInvalid marks a creation request without a usable title
	// or kind.
	ErrConversationInvalid = errors.New("llm chat: conversation title or kind is invalid")
	// ErrNoActiveLLMRevision marks a send when the active pair has no LLM side.
	ErrNoActiveLLMRevision = errors.New("llm chat: no active LLM profile revision in the model pair")
	// ErrMessageUnreadable marks a stored message whose sealed content cannot
	// be opened; the row is surfaced rather than hidden.
	ErrMessageUnreadable = errors.New("llm chat: stored message content cannot be read")
)

// iChatConversations is the conversation-table seam the service needs.
type iChatConversations interface {
	Create(ctx context.Context, c *llmstore.Conversation) error
	List(ctx context.Context, includeArchived bool) ([]llmstore.Conversation, error)
	Get(ctx context.Context, id string) (*llmstore.Conversation, error)
	Archive(ctx context.Context, id string, at time.Time) error
}

// LLMChatConversations adapts the repository to the service.
type LLMChatConversations struct{}

// Create inserts a conversation row.
func (LLMChatConversations) Create(ctx context.Context, c *llmstore.Conversation) error {
	return llmstore.InsertConversation(ctx, c)
}

// List returns conversations newest first.
func (LLMChatConversations) List(ctx context.Context, includeArchived bool) ([]llmstore.Conversation, error) {
	return llmstore.ListConversations(ctx, includeArchived)
}

// Get returns one conversation.
func (LLMChatConversations) Get(ctx context.Context, id string) (*llmstore.Conversation, error) {
	return llmstore.GetConversation(ctx, id)
}

// Archive marks a conversation archived.
func (LLMChatConversations) Archive(ctx context.Context, id string, at time.Time) error {
	return llmstore.ArchiveConversation(ctx, id, at)
}

// ChatMessageView is one message as the operator sees it: decrypted content
// plus the metadata a reader needs.
type ChatMessageView struct {
	ID           string    `json:"id"`
	Seq          int64     `json:"seq"`
	Role         string    `json:"role"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"createdAt"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	InputTokens  int64     `json:"inputTokens,omitempty"`
	OutputTokens int64     `json:"outputTokens,omitempty"`
	LatencyMS    int64     `json:"latencyMs,omitempty"`
}

// LLMChatService is the engine-side conversation corridor: conversation CRUD,
// message reading, and the send-turn that delegates to the pipeline. The RPC
// layer (M3-05) is a thin conversion over these calls, which is what keeps the
// conversation logic testable without a gRPC harness.
//
// Every field is a seam: the service holds no package-level state.
type LLMChatService struct {
	Conversations iChatConversations
	History       iChatHistory
	Pipeline      *ChatPipeline
	Cipher        *chat.ConversationCipher
	// ActiveRevision resolves the revision a send runs under; nil means the
	// active pair's LLM side (ActiveLLMRevisionID).
	ActiveRevision func(ctx context.Context) (string, error)
	Now            func() time.Time
	NewID          func() string
}

// SetupChat builds the conversation service.
//
// The conversation key is created in vaultDir on first use (docs 09 §5.4;
// 0600, never regenerated once present), and storage, budget and revision
// lookup are the real adapters. transport and runner are the production chat
// client and the read-only tool runner.
func (m *LLMManager) SetupChat(chatKeyDir string, transport ChatCompleter, runner ToolRunner) (*LLMChatService, error) {
	if m == nil {
		return nil, ErrNilSubsystem
	}
	if strings.TrimSpace(chatKeyDir) == "" {
		return nil, fmt.Errorf("%w: chat key directory is required", ErrChatNotConfigured)
	}
	if transport == nil || runner == nil {
		return nil, ErrChatNotConfigured
	}
	key, err := chat.LoadOrCreateConversationKey(chatKeyDir)
	if err != nil {
		return nil, fmt.Errorf("llm chat: conversation key: %w", err)
	}
	cipher, err := chat.NewConversationCipher(key)
	if err != nil {
		return nil, fmt.Errorf("llm chat: cipher: %w", err)
	}
	newID := func() string { return uuid.NewV4().String() }
	history := LLMChatHistory{}
	return &LLMChatService{
		Conversations:  LLMChatConversations{},
		History:        history,
		Cipher:         cipher,
		ActiveRevision: ActiveLLMRevisionID,
		Now:            time.Now,
		NewID:          newID,
		Pipeline: &ChatPipeline{
			Transport: transport,
			Tools:     runner,
			Cipher:    cipher,
			History:   history,
			Budget:    LLMChatBudget{},
			Revisions: LLMChatRevisions{},
			Now:       time.Now,
			NewID:     newID,
		},
	}, nil
}

// ActiveLLMRevisionID resolves the LLM side of the active model pair.
//
// Chat runs at whatever pairing is active, exactly like a decision would: an
// operator switching the pair switches the conversation model too.
func ActiveLLMRevisionID(ctx context.Context) (string, error) {
	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoActiveLLMRevision, err)
	}
	if pair.LLMProfileRevisionID == nil || strings.TrimSpace(*pair.LLMProfileRevisionID) == "" {
		return "", ErrNoActiveLLMRevision
	}
	return *pair.LLMProfileRevisionID, nil
}

// CreateConversation validates and stores a new conversation. An empty kind
// defaults to CHAT; the title is stored in the clear, so it is bounded.
func (s *LLMChatService) CreateConversation(ctx context.Context, title, kind string) (*llmstore.Conversation, error) {
	if s == nil || s.Conversations == nil || s.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxConversationTitleRunes {
		return nil, fmt.Errorf("%w: title must be 1..%d characters", ErrConversationInvalid, maxConversationTitleRunes)
	}
	if kind == "" {
		kind = ConversationKindChat
	}
	if kind != ConversationKindChat && kind != ConversationKindPolicy {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrConversationInvalid, kind)
	}
	now := s.now()
	conversation := llmstore.Conversation{
		ID: s.NewID(), Title: title, Kind: kind,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Conversations.Create(ctx, &conversation); err != nil {
		return nil, err
	}
	return &conversation, nil
}

// ListConversations returns conversations newest first.
func (s *LLMChatService) ListConversations(ctx context.Context, includeArchived bool) ([]llmstore.Conversation, error) {
	if s == nil || s.Conversations == nil {
		return nil, ErrChatNotConfigured
	}
	return s.Conversations.List(ctx, includeArchived)
}

// GetConversation returns a conversation and its decrypted messages.
//
// A message whose sealed content cannot be opened fails the read: returning
// the rest would show a conversation with a hole in it.
func (s *LLMChatService) GetConversation(ctx context.Context, id string) (*llmstore.Conversation, []ChatMessageView, error) {
	if s == nil || s.Conversations == nil || s.History == nil || s.Cipher == nil {
		return nil, nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(id) == "" {
		return nil, nil, fmt.Errorf("%w: conversation id is required", ErrConversationInvalid)
	}
	conversation, err := s.Conversations.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.History.MessageDetails(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	views := make([]ChatMessageView, 0, len(rows))
	for i := range rows {
		content, openErr := s.Cipher.Open(&chat.Message{
			ID:         rows[i].ID,
			Ciphertext: rows[i].ContentCipher,
			Nonce:      rows[i].ContentNonce,
			Sha256:     rows[i].ContentSha256,
		})
		if openErr != nil {
			return nil, nil, fmt.Errorf("%w: message %q: %v", ErrMessageUnreadable, rows[i].ID, openErr)
		}
		view := ChatMessageView{
			ID: rows[i].ID, Seq: rows[i].Seq, Role: rows[i].Role,
			Content: string(content), CreatedAt: rows[i].CreatedAt,
		}
		if rows[i].ErrorCode != nil {
			view.ErrorCode = *rows[i].ErrorCode
		}
		if rows[i].UsageInputTokens != nil {
			view.InputTokens = *rows[i].UsageInputTokens
		}
		if rows[i].UsageOutputTokens != nil {
			view.OutputTokens = *rows[i].UsageOutputTokens
		}
		if rows[i].LatencyMS != nil {
			view.LatencyMS = *rows[i].LatencyMS
		}
		views = append(views, view)
	}
	return conversation, views, nil
}

// ArchiveConversation marks a conversation archived at the service's clock.
func (s *LLMChatService) ArchiveConversation(ctx context.Context, id string) error {
	if s == nil || s.Conversations == nil {
		return ErrChatNotConfigured
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: conversation id is required", ErrConversationInvalid)
	}
	return s.Conversations.Archive(ctx, id, s.now())
}

// SendMessage runs one turn against the active model pair's LLM revision.
func (s *LLMChatService) SendMessage(ctx context.Context, conversationID, text string) (*ChatRunResult, error) {
	return s.send(ctx, conversationID, text, nil, nil)
}

// SendMessageStream runs one turn and streams the reply's text fragments to
// onDelta as they arrive, with live tool progress to onProgress. An
// interrupted stream persists no assistant row: half a reply is not a reply
// (docs 05 §1), and the budget slot is released.
func (s *LLMChatService) SendMessageStream(ctx context.Context, conversationID, text string, onDelta func(string) error, onProgress func(ToolProgress)) (*ChatRunResult, error) {
	if onDelta == nil {
		return s.send(ctx, conversationID, text, nil, nil)
	}
	return s.send(ctx, conversationID, text, onDelta, onProgress)
}

// send is the shared turn entry; a nil onDelta means non-streaming.
func (s *LLMChatService) send(ctx context.Context, conversationID, text string, onDelta func(string) error, onProgress func(ToolProgress)) (*ChatRunResult, error) {
	if s == nil || s.Pipeline == nil {
		return nil, ErrChatNotConfigured
	}
	resolve := s.ActiveRevision
	if resolve == nil {
		resolve = ActiveLLMRevisionID
	}
	revisionID, err := resolve(ctx)
	if err != nil {
		return nil, err
	}
	return s.Pipeline.RunStream(ctx, ChatRunRequest{
		ConversationID:    conversationID,
		Input:             text,
		ProfileRevisionID: revisionID,
		OnProgress:        onProgress,
	}, onDelta)
}

// now returns the service clock, defaulting to the wall clock.
func (s *LLMChatService) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}
