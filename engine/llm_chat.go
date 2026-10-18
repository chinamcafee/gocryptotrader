package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/llm/tools"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// The chat path (docs/llm-dev 05 §6.3) answers the operator's questions with
// the same read-only tools as the decision path but a different contract:
// free text instead of a Candidate, no decision_run, and every model call
// billed through the same budget gates. Configuration actions travel as inert
// proposals (docs 16) that only a human confirmation card can execute. The two
// modes share the tool set and the loop; they must not share the output
// contract.

var (
	// ErrChatInputInvalid marks a turn without the conversation id or text.
	ErrChatInputInvalid = errors.New("llm chat: conversation id and input are required")
	// ErrChatNotConfigured marks a pipeline missing one of its collaborators.
	ErrChatNotConfigured = errors.New("llm chat: pipeline is not fully configured")
	// ErrChatProfileKind marks a revision that cannot serve chat: a Jev judge
	// profile has no conversational endpoint.
	ErrChatProfileKind = errors.New("llm chat: revision is not an LLM provider")
)

// iChatHistory is the conversation store the chat pipeline needs.
type iChatHistory interface {
	ListMessages(ctx context.Context, conversationID string) ([]chat.StoredMessage, error)
	// MessageDetails returns the stored rows with their display metadata
	// (usage, latency, error codes); the sealed bodies are the same rows.
	MessageDetails(ctx context.Context, conversationID string) ([]llmstore.ChatMessage, error)
	AppendMessage(ctx context.Context, m *llmstore.ChatMessage) error
}

// iChatBudget is the budget gate the chat pipeline needs; the $0.8 reservation
// rules apply to conversations exactly as they do to decisions.
type iChatBudget interface {
	Reserve(ctx context.Context, req BudgetReservationRequest, now time.Time) (*llmstore.BudgetReservation, error)
	MarkUsed(ctx context.Context, id string, inputTokens, outputTokens, latencyMS int64, at time.Time) error
	Release(ctx context.Context, id string, at time.Time) error
}

// iChatRevisions resolves the provider revision a conversation runs under.
type iChatRevisions interface {
	Revision(ctx context.Context, id string) (*llmstore.ProfileRevision, error)
}

// LLMChatHistory adapts the repository to the chat pipeline.
type LLMChatHistory struct{}

// ListMessages reads the conversation's rows oldest first.
func (LLMChatHistory) ListMessages(ctx context.Context, conversationID string) ([]chat.StoredMessage, error) {
	rows, err := llmstore.ListChatMessages(ctx, conversationID, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]chat.StoredMessage, 0, len(rows))
	for i := range rows {
		out = append(out, chat.StoredMessage{
			Seq:  rows[i].Seq,
			Role: rows[i].Role,
			Body: chat.Message{
				ID:         rows[i].ID,
				Ciphertext: rows[i].ContentCipher,
				Nonce:      rows[i].ContentNonce,
				Sha256:     rows[i].ContentSha256,
			},
		})
	}
	return out, nil
}

// MessageDetails returns the raw stored rows of a conversation.
func (LLMChatHistory) MessageDetails(ctx context.Context, conversationID string) ([]llmstore.ChatMessage, error) {
	return llmstore.ListChatMessages(ctx, conversationID, 0, 0)
}

// AppendMessage writes one sealed row.
func (LLMChatHistory) AppendMessage(ctx context.Context, m *llmstore.ChatMessage) error {
	_, err := llmstore.AppendChatMessage(ctx, m)
	return err
}

// LLMChatBudget adapts the engine's budget gates.
type LLMChatBudget struct{}

// Reserve takes one slot of the sliding 24h cap.
func (LLMChatBudget) Reserve(ctx context.Context, req BudgetReservationRequest, now time.Time) (*llmstore.BudgetReservation, error) {
	return ReserveBudget(ctx, req, now)
}

// MarkUsed settles a reservation with the tokens the provider reported.
func (LLMChatBudget) MarkUsed(ctx context.Context, id string, inputTokens, outputTokens, latencyMS int64, at time.Time) error {
	return MarkReservationUsed(ctx, id, inputTokens, outputTokens, latencyMS, at)
}

// Release returns an unused slot.
func (LLMChatBudget) Release(ctx context.Context, id string, at time.Time) error {
	return ReleaseReservation(ctx, id, at)
}

// LLMChatRevisions adapts revision lookup.
type LLMChatRevisions struct{}

// Revision returns the live revision by id.
func (LLMChatRevisions) Revision(ctx context.Context, id string) (*llmstore.ProfileRevision, error) {
	return llmstore.GetProfileRevision(ctx, id)
}

// ChatRunRequest is one operator turn.
type ChatRunRequest struct {
	// ConversationID names the conversation the turn belongs to.
	ConversationID string
	// Input is the operator's instruction for this turn.
	Input string
	// ProfileRevisionID is the LLM profile revision to run under.
	ProfileRevisionID string
	// OnProgress receives live tool progress (nil is a no-op).
	OnProgress func(ToolProgress)
}

// ChatRunResult is what one turn produced.
type ChatRunResult struct {
	// AssistantMessageID names the stored reply row.
	AssistantMessageID string `json:"assistantMessageId"`
	Reply              string `json:"reply"`
	ReasonCode         string `json:"reasonCode,omitempty"`
	Rounds             int    `json:"rounds"`
	ToolCalls          int    `json:"toolCalls"`
	InputTokens        int64  `json:"inputTokens"`
	OutputTokens       int64  `json:"outputTokens"`
	LatencyMS          int64  `json:"latencyMs"`
	// ToolTranscript is the retained transcript JSON, when tools ran.
	ToolTranscript string `json:"toolTranscript,omitempty"`
	// Evidence is the display view of the tool responses this turn used
	// (docs/llm-dev 08 §3.1 LLMSendMessageResponse.evidence).
	Evidence []EvidenceCard `json:"evidence,omitempty"`
	// Proposals are inert configuration drafts awaiting operator confirmation
	// (docs/llm-dev 16). They carry no execution authority: the confirmation
	// card calls the same governed RPC the manual pages use.
	Proposals []candidate.ActionProposal `json:"proposals,omitempty"`
}

// ChatPipeline runs one conversational turn end to end: rebuild the context,
// persist the question, pass the budget gate, run the bounded tool loop, and
// persist the sealed reply with its usage and tool transcript.
type ChatPipeline struct {
	Transport ChatCompleter
	Tools     ToolRunner
	Cipher    *chat.ConversationCipher
	History   iChatHistory
	Budget    iChatBudget
	Revisions iChatRevisions
	// Limits bound the loop; the zero value means DefaultToolLoopLimits.
	Limits ToolLoopLimits
	// Now and NewID are injected so a turn is reproducible in tests.
	Now   func() time.Time
	NewID func() string
}

// Run executes one turn without streaming.
func (p *ChatPipeline) Run(ctx context.Context, req ChatRunRequest) (*ChatRunResult, error) {
	return p.run(ctx, req, nil)
}

// RunStream executes one turn, streaming each round's content fragments to
// onDelta as they arrive. The final result is identical to Run's -- the
// streamed text IS the stored reply -- so a caller may show and persist the
// same turn without reconciling two versions.
//
// An interrupted stream returns an error and persists no assistant row: half
// a reply is not a reply (docs 05 §1).
func (p *ChatPipeline) RunStream(ctx context.Context, req ChatRunRequest, onDelta func(string) error) (*ChatRunResult, error) {
	if onDelta == nil {
		return p.run(ctx, req, nil)
	}
	return p.run(ctx, req, onDelta)
}

// run is the shared turn body; a nil onDelta means non-streaming.
//
// Ordering is deliberate: the prospective context (history plus the new
// instruction) is validated before anything is persisted, so a turn that
// breaches a context limit leaves the conversation untouched.
func (p *ChatPipeline) run(ctx context.Context, req ChatRunRequest, onDelta func(string) error) (*ChatRunResult, error) {
	if p == nil || p.Transport == nil || p.Tools == nil || p.Cipher == nil ||
		p.History == nil || p.Budget == nil || p.Revisions == nil || p.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.Input) == "" {
		return nil, ErrChatInputInvalid
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now()

	revision, err := p.Revisions.Revision(ctx, req.ProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm chat: resolving revision %q: %w", req.ProfileRevisionID, err)
	}
	if revision.Kind != ProviderKindLLM {
		return nil, fmt.Errorf("%w: %q is %s", ErrChatProfileKind, req.ProfileRevisionID, revision.Kind)
	}
	prompt, err := openai.PromptFor(openai.PromptVersionChatAnalysis)
	if err != nil {
		return nil, fmt.Errorf("llm chat: prompt: %w", err)
	}

	history, err := p.History.ListMessages(ctx, req.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("llm chat: reading history: %w", err)
	}

	userID := p.NewID()
	sealedInput, err := p.Cipher.Seal(userID, []byte(req.Input))
	if err != nil {
		return nil, fmt.Errorf("llm chat: sealing the input: %w", err)
	}
	// The prospective context is validated before any write: the limits from
	// docs 05 §8.1 include the message being sent, and a breach must leave the
	// conversation exactly as it was.
	nextSeq := int64(1)
	for i := range history {
		if history[i].Seq >= nextSeq {
			nextSeq = history[i].Seq + 1
		}
	}
	prospective := append(append([]chat.StoredMessage(nil), history...),
		chat.StoredMessage{Seq: nextSeq, Role: msgRoleUser, Body: sealedInput})
	builtContext, err := p.Cipher.BuildContext(prospective)
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}

	messages := make([]openai.Message, 0, len(builtContext)+1)
	messages = append(messages, openai.Message{Role: msgRoleSystem, Content: prompt})
	for i := range builtContext {
		messages = append(messages, openai.Message{Role: msgRoleUser, Content: builtContext[i].Content})
	}

	revisionID := revision.ID
	promptVersion := openai.PromptVersionChatAnalysis
	if err := p.History.AppendMessage(ctx, &llmstore.ChatMessage{
		ID: userID, ConversationID: req.ConversationID, Role: msgRoleUser,
		ContentCipher: sealedInput.Ciphertext, ContentNonce: sealedInput.Nonce, ContentSha256: sealedInput.Sha256,
		CharCount:       int64(utf8.RuneCountInString(req.Input)),
		LLMProfileRevID: &revisionID,
		PromptVersion:   &promptVersion,
		CreatedAt:       now(),
	}); err != nil {
		return nil, fmt.Errorf("llm chat: persisting the question: %w", err)
	}

	reservation, err := p.Budget.Reserve(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID,
		ProviderKind:      ProviderKindLLM,
		Purpose:           "CHAT",
	}, now())
	if err != nil {
		return nil, fmt.Errorf("llm chat: budget gate: %w", err)
	}

	limits := p.Limits
	if limits == (ToolLoopLimits{}) {
		limits = DefaultToolLoopLimits()
		// A conversation often needs to screen once and then read details for a
		// few markets (three markets x indicators/funding is already six to
		// nine calls in one round). The decision loop's tighter per-round cap
		// refuses that ordinary flow with TOOL_ROUND_LIMIT, so chat gets a
		// wider bound; the cumulative token budget and deadline still apply.
		limits.MaxRounds = 6
		limits.MaxToolsPerRound = 12
		limits.MaxToolCalls = 24
	}
	maxTokens := 0
	if revision.MaxOutputTokens != nil {
		maxTokens = int(*revision.MaxOutputTokens)
	}
	if maxTokens == 0 {
		// A conversation reply needs room: reasoning models spend tokens before
		// the visible answer, and a zero value lets the provider pick a small
		// default (observed truncation on deepseek-flash). 4096 is the same
		// budget the schema probe needed to stop truncating.
		maxTokens = 4096
	}
	result, err := RunToolLoop(ctx, p.Transport, p.Tools, &ToolLoopRequest{
		Model:          revision.ModelID,
		Messages:       messages,
		Tools:          tools.Specs(),
		MaxTokens:      maxTokens,
		ProfileTimeout: time.Duration(revision.TimeoutSeconds) * time.Second,
		OnDelta:        onDelta,
		OnProgress:     req.OnProgress,
		// No deadline: a manual turn is not on the decision pipeline's clock.
		// ResponseFormat stays nil: a conversation reply is free text.
	}, limits, now)
	if err != nil {
		if releaseErr := p.Budget.Release(ctx, reservation.ID, now()); releaseErr != nil {
			return nil, fmt.Errorf("llm chat: provider call failed (%w) and the reservation could not be released: %v", err, releaseErr)
		}
		return nil, fmt.Errorf("llm chat: provider call failed: %w", err)
	}

	latency := now().Sub(startedAt).Milliseconds()
	if err := p.Budget.MarkUsed(ctx, reservation.ID, result.UsageInputTokens, result.UsageOutputTokens, latency, now()); err != nil {
		return nil, fmt.Errorf("llm chat: settling the reservation: %w", err)
	}

	assistantID := p.NewID()
	sealedReply, err := p.Cipher.Seal(assistantID, []byte(result.Content))
	if err != nil {
		return nil, fmt.Errorf("llm chat: sealing the reply: %w", err)
	}
	modelID := revision.ModelID
	assistant := llmstore.ChatMessage{
		ID: assistantID, ConversationID: req.ConversationID, Role: "assistant",
		ContentCipher: sealedReply.Ciphertext, ContentNonce: sealedReply.Nonce, ContentSha256: sealedReply.Sha256,
		CharCount:       int64(utf8.RuneCountInString(result.Content)),
		LLMProfileRevID: &revisionID, LLMModel: &modelID, PromptVersion: &promptVersion,
		UsageInputTokens: &result.UsageInputTokens, UsageOutputTokens: &result.UsageOutputTokens,
		LatencyMS: &latency,
		CreatedAt: now(),
	}
	if result.ReasonCode != "" {
		assistant.ErrorCode = &result.ReasonCode
	}
	transcriptJSON := ""
	if len(result.Transcript) > 0 {
		transcript, transcriptErr := result.TranscriptJSON()
		if transcriptErr != nil {
			// The reply is already obtained; losing the transcript costs a
			// replay detail, not the answer, so it degrades to a log line.
			log.Warnf(log.LLMMgr, "chat tool transcript could not be built: %v", transcriptErr)
		} else {
			transcriptJSON = transcript
			assistant.ToolTranscript = &transcriptJSON
		}
	}
	if err := p.History.AppendMessage(ctx, &assistant); err != nil {
		return nil, fmt.Errorf("llm chat: persisting the reply: %w", err)
	}

	return &ChatRunResult{
		AssistantMessageID: assistantID,
		Reply:              result.Content,
		ReasonCode:         result.ReasonCode,
		Rounds:             result.Rounds,
		ToolCalls:          result.ToolCalls,
		InputTokens:        result.UsageInputTokens,
		OutputTokens:       result.UsageOutputTokens,
		LatencyMS:          latency,
		ToolTranscript:     transcriptJSON,
		Evidence:           EvidenceCards(result.Transcript),
		Proposals:          ProposalCards(result.Transcript),
	}, nil
}
