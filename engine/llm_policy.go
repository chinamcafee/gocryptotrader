package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// M4-01 策略条款提取（docs/llm-dev 05 §6.2、§8.1/§8.2）。
//
// 提取把会话里的用户指令变成一份**不可执行**的草案：缺边界→澄清问题，
// 矛盾→提问而不是静默选一个。这里没有任何持久化——草案成为版本是冻结
// （M4-02）的事，提取只负责把模型的回答按契约收紧。

var (
	// ErrPolicyInputInvalid marks an extraction without conversation or markets.
	ErrPolicyInputInvalid = errors.New("llm policy: conversation id and selected markets are required")
	// ErrExtractionMarketDenied marks a proposal naming a market the operator
	// did not select. The prompt says "use only selected market IDs"; this is
	// the server-side half of that sentence.
	ErrExtractionMarketDenied = errors.New("llm policy: proposal names a market outside the selected set")
	// ErrProposalInvalid marks model output that is not a valid proposal.
	ErrProposalInvalid = errors.New("llm policy: extracted proposal is invalid")
)

// PolicyExtractionRequest is one extraction run.
type PolicyExtractionRequest struct {
	// ConversationID names the conversation holding the operator's instructions.
	ConversationID string
	// ProfileRevisionID is the LLM profile revision to run under.
	ProfileRevisionID string
	// Markets are the operator-selected market IDs the proposal may cover.
	Markets []string
}

// PolicyExtractionResult is the extracted draft plus its state.
type PolicyExtractionResult struct {
	Proposal     candidate.PolicyProposal `json:"proposal"`
	State        candidate.ProposalState  `json:"state"`
	InputTokens  int64                    `json:"inputTokens"`
	OutputTokens int64                    `json:"outputTokens"`
	LatencyMS    int64                    `json:"latencyMs"`
}

// PolicyExtractor runs the policy-proposal extraction.
//
// It shares the conversation seams with the chat pipeline (history, cipher,
// budget, revisions): the extraction context is exactly the chat context --
// every user instruction from the start of the conversation (05 §8.1) -- and a
// turn nobody can afford must not be sent.
type PolicyExtractor struct {
	Transport ChatCompleter
	History   iChatHistory
	Cipher    *chat.ConversationCipher
	Budget    iChatBudget
	Revisions iChatRevisions
	Now       func() time.Time
}

// Extract runs one extraction.
func (e *PolicyExtractor) Extract(ctx context.Context, req PolicyExtractionRequest) (*PolicyExtractionResult, error) {
	if e == nil || e.Transport == nil || e.History == nil || e.Cipher == nil ||
		e.Budget == nil || e.Revisions == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.ConversationID) == "" || len(req.Markets) == 0 || len(req.Markets) > candidate.MaxMarketScopes {
		return nil, fmt.Errorf("%w: 1..%d markets", ErrPolicyInputInvalid, candidate.MaxMarketScopes)
	}
	selected := make(map[string]bool, len(req.Markets))
	for _, market := range req.Markets {
		if strings.TrimSpace(market) == "" {
			return nil, fmt.Errorf("%w: blank market id", ErrPolicyInputInvalid)
		}
		selected[market] = true
	}
	now := e.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now()

	revision, err := e.Revisions.Revision(ctx, req.ProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm policy: resolving revision %q: %w", req.ProfileRevisionID, err)
	}
	if revision.Kind != ProviderKindLLM {
		return nil, fmt.Errorf("%w: %q is %s", ErrChatProfileKind, req.ProfileRevisionID, revision.Kind)
	}
	prompt, err := openai.PromptFor(openai.PromptVersionPolicyProposal)
	if err != nil {
		return nil, fmt.Errorf("llm policy: prompt: %w", err)
	}

	history, err := e.History.ListMessages(ctx, req.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("llm policy: reading history: %w", err)
	}
	contextMessages, err := e.Cipher.BuildContext(history)
	if err != nil {
		return nil, fmt.Errorf("llm policy: %w", err)
	}

	messages := make([]openai.Message, 0, len(contextMessages)+2)
	messages = append(messages, openai.Message{Role: msgRoleSystem, Content: prompt})
	for i := range contextMessages {
		messages = append(messages, openai.Message{Role: msgRoleUser, Content: contextMessages[i].Content})
	}
	// The prompt says "use only selected market IDs"; the last message is
	// where they arrive, deterministically ordered as given.
	messages = append(messages, openai.Message{Role: msgRoleUser, Content: "Selected market IDs: " + strings.Join(req.Markets, ", ")})

	reservation, err := e.Budget.Reserve(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID,
		ProviderKind:      ProviderKindLLM,
		Purpose:           "PROPOSAL",
	}, now())
	if err != nil {
		return nil, fmt.Errorf("llm policy: budget gate: %w", err)
	}

	maxTokens := 0
	if revision.MaxOutputTokens != nil {
		maxTokens = int(*revision.MaxOutputTokens)
	}
	request := &openai.ChatRequest{
		Model:          revision.ModelID,
		Messages:       messages,
		MaxTokens:      maxTokens,
		ResponseFormat: &openai.ResponseFormat{Type: "json_object"},
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(revision.TimeoutSeconds)*time.Second)
	response, err := e.Transport.ChatCompletion(callCtx, request)
	cancel()
	latency := now().Sub(startedAt).Milliseconds()
	if err != nil {
		if releaseErr := e.Budget.Release(ctx, reservation.ID, now()); releaseErr != nil {
			return nil, fmt.Errorf("llm policy: provider call failed (%w) and the reservation could not be released: %v", err, releaseErr)
		}
		return nil, fmt.Errorf("llm policy: provider call failed: %w", err)
	}
	if response == nil {
		return nil, errors.New("llm policy: nil chat response")
	}
	// The tokens were spent whatever the content turns out to be, so the
	// reservation settles before the output is judged.
	if err := e.Budget.MarkUsed(ctx, reservation.ID, int64(response.Usage.PromptTokens), int64(response.Usage.CompletionTokens), latency, now()); err != nil {
		return nil, fmt.Errorf("llm policy: settling the reservation: %w", err)
	}

	message, err := response.FirstMessage()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProposalInvalid, err)
	}
	var proposal candidate.PolicyProposal
	if err := decodePolicyStrict(message.Content, &proposal); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProposalInvalid, err)
	}
	if err := proposal.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProposalInvalid, err)
	}
	for i := range proposal.MarketScopes {
		if !selected[proposal.MarketScopes[i].MarketID] {
			return nil, fmt.Errorf("%w: %q", ErrExtractionMarketDenied, proposal.MarketScopes[i].MarketID)
		}
	}
	return &PolicyExtractionResult{
		Proposal:     proposal,
		State:        proposal.State(),
		InputTokens:  int64(response.Usage.PromptTokens),
		OutputTokens: int64(response.Usage.CompletionTokens),
		LatencyMS:    latency,
	}, nil
}

// decodePolicyStrict decodes the model's JSON document, refusing unknown
// fields and trailing data. The proposal decode and the tool-argument decode
// share the discipline: a field we did not ask for is an error, not something
// to ignore, because ignoring it hides a model reading a different contract.
func decodePolicyStrict(raw string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data after the proposal object")
	}
	return nil
}
