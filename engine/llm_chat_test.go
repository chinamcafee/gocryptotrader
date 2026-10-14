package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/types"
)

type chatTestTransport struct {
	responses []*openai.ChatResponse
	err       error
	requests  []*openai.ChatRequest
}

func (s *chatTestTransport) ChatCompletion(_ context.Context, req *openai.ChatRequest) (*openai.ChatResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		return nil, errors.New("chat test: no scripted response left")
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

type chatTestHistory struct {
	rows      []llmstore.ChatMessage
	appendErr error
}

func (f *chatTestHistory) ListMessages(_ context.Context, conversationID string) ([]chat.StoredMessage, error) {
	out := make([]chat.StoredMessage, 0, len(f.rows))
	for i := range f.rows {
		if f.rows[i].ConversationID != conversationID {
			continue
		}
		out = append(out, chat.StoredMessage{
			Seq:  f.rows[i].Seq,
			Role: f.rows[i].Role,
			Body: chat.Message{
				ID:         f.rows[i].ID,
				Ciphertext: f.rows[i].ContentCipher,
				Nonce:      f.rows[i].ContentNonce,
				Sha256:     f.rows[i].ContentSha256,
			},
		})
	}
	return out, nil
}

func (f *chatTestHistory) MessageDetails(_ context.Context, conversationID string) ([]llmstore.ChatMessage, error) {
	out := make([]llmstore.ChatMessage, 0, len(f.rows))
	for i := range f.rows {
		if f.rows[i].ConversationID == conversationID {
			out = append(out, f.rows[i])
		}
	}
	return out, nil
}

func (f *chatTestHistory) AppendMessage(_ context.Context, m *llmstore.ChatMessage) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.rows = append(f.rows, *m)
	return nil
}

type chatTestBudget struct {
	reserved, marked, released int
	lastPurpose                string
	markIn, markOut            int64
	reserveErr                 error
}

func (f *chatTestBudget) Reserve(_ context.Context, req BudgetReservationRequest, _ time.Time) (*llmstore.BudgetReservation, error) {
	if f.reserveErr != nil {
		return nil, f.reserveErr
	}
	f.reserved++
	f.lastPurpose = req.Purpose
	return &llmstore.BudgetReservation{ID: "res-1"}, nil
}

func (f *chatTestBudget) MarkUsed(_ context.Context, _ string, inputTokens, outputTokens, _ int64, _ time.Time) error {
	f.marked++
	f.markIn, f.markOut = inputTokens, outputTokens
	return nil
}

func (f *chatTestBudget) Release(_ context.Context, _ string, _ time.Time) error {
	f.released++
	return nil
}

type chatTestRevisions struct {
	revision *llmstore.ProfileRevision
	err      error
}

func (f chatTestRevisions) Revision(_ context.Context, _ string) (*llmstore.ProfileRevision, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.revision, nil
}

// chatTestPipelineOnly builds the pipeline when the test needs nothing else.
func chatTestPipelineOnly(t *testing.T, transport ChatCompleter) *ChatPipeline {
	t.Helper()
	p, history, budget, cipher := chatTestPipeline(t, transport)
	_ = history
	_ = budget
	_ = cipher
	return p
}

func chatTestPipeline(t *testing.T, transport ChatCompleter) (*ChatPipeline, *chatTestHistory, *chatTestBudget, *chat.ConversationCipher) {
	t.Helper()
	cipher, err := chat.NewConversationCipher(bytes.Repeat([]byte{0x11}, chat.KeySize))
	require.NoError(t, err, "the test cipher must build")
	history := &chatTestHistory{}
	budget := &chatTestBudget{}
	maxTokens := int64(512)
	ids := 0
	return &ChatPipeline{
		Transport: transport,
		Tools:     &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{"ok":true}`}},
		Cipher:    cipher,
		History:   history,
		Budget:    budget,
		Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
			ID: "rev-1", Kind: ProviderKindLLM, ModelID: "gpt-test",
			TimeoutSeconds: 45, MaxOutputTokens: &maxTokens,
		}},
		Now:   func() time.Time { return providerAdminNow },
		NewID: func() string { ids++; return fmt.Sprintf("id-%d", ids) },
	}, history, budget, cipher
}

func chatRunRequest() ChatRunRequest {
	return ChatRunRequest{ConversationID: "conv-1", Input: "BTC 现在能开多吗？", ProfileRevisionID: "rev-1"}
}

// chatMessage builds a scripted assistant reply with a prompt/completion split.
func chatMessage(content string, promptTokens, completionTokens int) *openai.ChatResponse {
	return &openai.ChatResponse{
		Choices: []openai.Choice{{Index: 0, FinishReason: "stop", Message: openai.Message{Role: "assistant", Content: content}}},
		Usage: openai.Usage{
			PromptTokens:     types.Number(promptTokens),
			CompletionTokens: types.Number(completionTokens),
			TotalTokens:      types.Number(promptTokens + completionTokens),
		},
	}
}

func openStored(t *testing.T, cipher *chat.ConversationCipher, row *llmstore.ChatMessage) string {
	t.Helper()
	content, err := cipher.Open(&chat.Message{ID: row.ID, Ciphertext: row.ContentCipher, Nonce: row.ContentNonce, Sha256: row.ContentSha256})
	require.NoError(t, err, "the stored row must open")
	return string(content)
}

func TestChatRunSealsTheTurnAndReturnsTheReply(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("BTC 现在属于震荡区间，建议观望。", 120, 30)}}
	p, history, budget, cipher := chatTestPipeline(t, transport)

	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err, "the turn must succeed")
	assert.Equal(t, "BTC 现在属于震荡区间，建议观望。", result.Reply)
	assert.Empty(t, result.ReasonCode)
	assert.Equal(t, int64(120), result.InputTokens)
	assert.Equal(t, int64(30), result.OutputTokens)

	require.Len(t, history.rows, 2, "the question and the reply must both be persisted")
	assert.Equal(t, "user", history.rows[0].Role)
	assert.Equal(t, "assistant", history.rows[1].Role)
	assert.Equal(t, "BTC 现在能开多吗？", openStored(t, cipher, &history.rows[0]),
		"the question must survive sealing byte for byte")
	assert.Equal(t, "BTC 现在属于震荡区间，建议观望。", openStored(t, cipher, &history.rows[1]))
	assert.NotEqual(t, []byte("BTC 现在能开多吗？"), history.rows[0].ContentCipher, "the body should be ciphertext at rest")

	assistant := history.rows[1]
	require.NotNil(t, assistant.PromptVersion)
	assert.Equal(t, openai.PromptVersionChatAnalysis, *assistant.PromptVersion, "the turn should be attributed to the chat prompt version")
	require.NotNil(t, assistant.UsageInputTokens)
	assert.Equal(t, int64(120), *assistant.UsageInputTokens)
	require.NotNil(t, assistant.UsageOutputTokens)
	assert.Equal(t, int64(30), *assistant.UsageOutputTokens)
	require.NotNil(t, assistant.LLMProfileRevID)
	assert.Equal(t, "rev-1", *assistant.LLMProfileRevID)
	assert.Nil(t, assistant.ErrorCode, "a clean turn carries no error code")

	assert.Equal(t, 1, budget.reserved)
	assert.Equal(t, 1, budget.marked, "a completed turn settles its reservation")
	assert.Zero(t, budget.released)
	assert.Equal(t, "CHAT", budget.lastPurpose)
	assert.Equal(t, int64(120), budget.markIn)
	assert.Equal(t, int64(30), budget.markOut)

	// The provider must see the chat prompt, free-text mode, and the tools.
	require.Len(t, transport.requests, 1)
	request := transport.requests[0]
	require.NotEmpty(t, request.Messages)
	assert.Equal(t, "system", request.Messages[0].Role)
	chatPrompt, err := openai.PromptFor(openai.PromptVersionChatAnalysis)
	require.NoError(t, err)
	assert.Equal(t, chatPrompt, request.Messages[0].Content, "the chat prompt version should lead the context")
	assert.Equal(t, "user", request.Messages[len(request.Messages)-1].Role)
	assert.Equal(t, "BTC 现在能开多吗？", request.Messages[len(request.Messages)-1].Content)
	assert.Nil(t, request.ResponseFormat, "a conversation reply is free text: no json_object")
	assert.Len(t, request.Tools, 8, "chat shares the decision path's eight read-only tools")
	assert.Equal(t, "gpt-test", request.Model)
	assert.Equal(t, 512, request.MaxTokens)
}

func TestChatRunIncludesPriorInstructionsFromTheStore(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("ok", 10, 5)}}
	p, history, _, cipher := chatTestPipeline(t, transport)

	sealed, err := cipher.Seal("id-0", []byte("单笔最多 500U"))
	require.NoError(t, err)
	history.rows = append(history.rows, llmstore.ChatMessage{
		ID: "id-0", ConversationID: "conv-1", Seq: 1, Role: "user",
		ContentCipher: sealed.Ciphertext, ContentNonce: sealed.Nonce, ContentSha256: sealed.Sha256,
	})
	assistantRow, err := cipher.Seal("id-x", []byte("之前助手说过的"))
	require.NoError(t, err)
	history.rows = append(history.rows, llmstore.ChatMessage{
		ID: "id-x", ConversationID: "conv-1", Seq: 2, Role: "assistant",
		ContentCipher: assistantRow.Ciphertext, ContentNonce: assistantRow.Nonce, ContentSha256: assistantRow.Sha256,
	})

	_, err = p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	messages := transport.requests[0].Messages
	require.Len(t, messages, 3, "system + the prior instruction + the new question")
	assert.Equal(t, "单笔最多 500U", messages[1].Content, "prior user instructions are context")
	assert.Equal(t, "BTC 现在能开多吗？", messages[2].Content)
	for i := range messages {
		assert.NotEqual(t, "之前助手说过的", messages[i].Content,
			"the assistant's own prior replies are regenerated, not replayed (docs 05 §8.1 draws the context from user instructions)")
	}
}

func TestChatRunRefusesOversizedContextBeforeAnyWrite(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("never sent", 1, 1)}}
	p, history, budget, cipher := chatTestPipeline(t, transport)

	for i := 1; i <= chat.MaxContextMessages; i++ {
		sealed, err := cipher.Seal(fmt.Sprintf("id-%d", i), []byte("x"))
		require.NoError(t, err)
		history.rows = append(history.rows, llmstore.ChatMessage{
			ID: fmt.Sprintf("id-%d", i), ConversationID: "conv-1", Seq: int64(i), Role: "user",
			ContentCipher: sealed.Ciphertext, ContentNonce: sealed.Nonce, ContentSha256: sealed.Sha256,
		})
	}

	_, err := p.Run(t.Context(), chatRunRequest())
	require.Error(t, err)
	assert.ErrorIs(t, err, chat.ErrContextTooLarge,
		"256 prior instructions plus the new one must be refused, not truncated")
	assert.Empty(t, transport.requests, "the provider should not be called for a context that cannot be sent")
	assert.Zero(t, budget.reserved, "a refused turn should not take a budget slot")
	assert.Len(t, history.rows, chat.MaxContextMessages, "a refused turn should leave the conversation untouched")
}

func TestChatRunReleasesTheReservationWhenTheProviderFails(t *testing.T) {
	transport := &chatTestTransport{err: errors.New("upstream exploded")}
	p, history, budget, _ := chatTestPipeline(t, transport)

	_, err := p.Run(t.Context(), chatRunRequest())
	require.Error(t, err)
	assert.ErrorContains(t, err, "upstream exploded")
	assert.Equal(t, 1, budget.reserved)
	assert.Equal(t, 1, budget.released, "a failed call should return its slot")
	assert.Zero(t, budget.marked)
	require.Len(t, history.rows, 1, "the question stays, but no reply row may exist")
	assert.Equal(t, "user", history.rows[0].Role)
}

func TestChatRunPersistsHoldOutcomesWithTheirReason(t *testing.T) {
	truncated := chatMessage("半截回复", 40, 10)
	truncated.Choices[0].FinishReason = "length"
	transport := &chatTestTransport{responses: []*openai.ChatResponse{truncated}}
	p, history, budget, _ := chatTestPipeline(t, transport)

	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err, "a HOLD outcome is a completed turn, not a transport error")
	assert.Equal(t, ReasonTruncated, result.ReasonCode)
	assert.Equal(t, 1, budget.marked, "tokens were consumed even though the reply was cut off")
	assert.Zero(t, budget.released)
	require.Len(t, history.rows, 2)
	require.NotNil(t, history.rows[1].ErrorCode)
	assert.Equal(t, ReasonTruncated, *history.rows[1].ErrorCode,
		"the row must record why the operator saw a truncated reply")
}

func TestChatRunRecordsToolUseInTheTranscript(t *testing.T) {
	withTool := chatMessage("", 20, 8)
	withTool.Choices[0].FinishReason = "tool_calls"
	withTool.Choices[0].Message.ToolCalls = []openai.ToolCall{toolCall("call-1", "lookup_market", `{"marketId":"Binance:usdtmarginedfutures:BTC_USDT"}`)}
	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		withTool,
		chatMessage("从工具结果看，区间震荡。", 30, 12),
	}}
	p, history, _, _ := chatTestPipeline(t, transport)

	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err)
	assert.Equal(t, 1, result.ToolCalls)
	assert.Equal(t, "从工具结果看，区间震荡。", result.Reply)
	require.Len(t, history.rows, 2)
	require.NotNil(t, history.rows[1].ToolTranscript, "the transcript is what lets a reviewer see what the operator saw")
	assert.Contains(t, *history.rows[1].ToolTranscript, "call-1")
	assert.Contains(t, *history.rows[1].ToolTranscript, "lookup_market")
	assert.Equal(t, int64(50), result.InputTokens, "usage should aggregate across rounds")
	assert.Equal(t, int64(20), result.OutputTokens)
}

func TestChatRunRefusesMisuse(t *testing.T) {
	transport := &chatTestTransport{}
	p := chatTestPipelineOnly(t, transport)

	_, err := p.Run(t.Context(), ChatRunRequest{ConversationID: "", Input: "hi", ProfileRevisionID: "rev-1"})
	assert.ErrorIs(t, err, ErrChatInputInvalid)
	_, err = p.Run(t.Context(), ChatRunRequest{ConversationID: "conv-1", Input: "   ", ProfileRevisionID: "rev-1"})
	assert.ErrorIs(t, err, ErrChatInputInvalid)

	var nilPipeline *ChatPipeline
	_, err = nilPipeline.Run(t.Context(), chatRunRequest())
	assert.ErrorIs(t, err, ErrChatNotConfigured)

	// A Jev judge profile has no conversational endpoint.
	jevTransport := &chatTestTransport{}
	jev, _, jevBudget, _ := chatTestPipeline(t, jevTransport)
	jev.Revisions = chatTestRevisions{revision: &llmstore.ProfileRevision{ID: "rev-1", Kind: ProviderKindJev}}
	_, err = jev.Run(t.Context(), chatRunRequest())
	assert.ErrorIs(t, err, ErrChatProfileKind)
	assert.Empty(t, jevTransport.requests)
	assert.Zero(t, jevBudget.reserved)
	assert.True(t, strings.Contains(err.Error(), "LLM_OPENAI_COMPAT") || strings.Contains(err.Error(), ProviderKindJev),
		"the error must name the revision's actual kind")
}

func chatEvidenceEnvelope(t *testing.T, asOf time.Time, marketID string) string {
	t.Helper()
	evidence := candidate.ToolEvidence{
		Ref: "tool:getCandleWindow:call-1", AsOf: asOf,
		Quality: candidate.QualityExchangeREST, Exchange: "Binance",
		MarketID: &marketID, Data: json.RawMessage(`{"candles":[]}`),
	}
	encoded, err := json.Marshal(evidence)
	require.NoError(t, err)
	return string(encoded)
}

func TestChatRunCarriesEvidenceCards(t *testing.T) {
	asOf := providerAdminNow.Add(-2 * time.Minute)
	envelope := chatEvidenceEnvelope(t, asOf, "Binance:usdtmarginedfutures:BTC_USDT")
	sum := sha256.Sum256([]byte(envelope))

	withTool := chatMessage("", 20, 8)
	withTool.Choices[0].FinishReason = "tool_calls"
	withTool.Choices[0].Message.ToolCalls = []openai.ToolCall{toolCall("call-1", "getCandleWindow", `{}`)}
	transport := &chatTestTransport{responses: []*openai.ChatResponse{withTool, chatMessage("done", 30, 9)}}
	p := chatTestPipelineOnly(t, transport)
	p.Tools = &fakeRunner{
		supported: map[string]bool{"getCandleWindow": true},
		results:   map[string]string{"getCandleWindow": envelope},
	}

	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err)
	require.Len(t, result.Evidence, 1, "the turn must carry one display card per tool response")
	card := result.Evidence[0]
	assert.Equal(t, "tool:getCandleWindow:call-1", card.Ref)
	assert.Equal(t, "getCandleWindow", card.Tool)
	assert.Equal(t, "Binance", card.Exchange)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", card.MarketID)
	assert.True(t, asOf.Equal(card.SourceTime), "the card shows the source time, not the read time")
	assert.Equal(t, string(candidate.QualityExchangeREST), card.Quality)
	assert.Equal(t, hex.EncodeToString(sum[:]), card.SHA256, "the card's hash is the hash of the bytes the model saw")
	assert.Empty(t, card.SnapshotID, "conversation tools do not take snapshots")
}

func TestEvidenceCardsSkipNonEvidenceResults(t *testing.T) {
	envelope := chatEvidenceEnvelope(t, providerAdminNow, "Binance:usdtmarginedfutures:ETH_USDT")
	records := []ToolCallRecord{
		{CallID: "call-1", Name: "getCandleWindow", Arguments: "{}", Result: envelope, ResultSha256: "aa"},
		{CallID: "call-2", Name: "getIndicators", Arguments: "{}", Result: "tool arguments invalid: missing limit", ResultSha256: "bb"},
	}
	cards := EvidenceCards(records)
	require.Len(t, cards, 1, "an argument-feedback string is not an evidence envelope")
	assert.Equal(t, "tool:getCandleWindow:call-1", cards[0].Ref)
	assert.Equal(t, "aa", cards[0].SHA256)
	assert.Empty(t, EvidenceCards(nil))

	cards = EvidenceCards([]ToolCallRecord{{CallID: "call-3", Name: "x", Result: `{}`}})
	assert.Empty(t, cards, "a result without a ref or quality is not a card")
}
