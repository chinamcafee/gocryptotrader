package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"google.golang.org/grpc"
)

func newLLMChatRPCServer(t *testing.T, transport *chatTestTransport) *RPCServer {
	t.Helper()
	service, _, _ := chatTestService(t, transport)
	return &RPCServer{Engine: &Engine{llmChatService: service}}
}

func TestLLMConversationRPCRoundTrip(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("回答：区间震荡。", 90, 30)}}
	server := newLLMChatRPCServer(t, transport)
	ctx := t.Context()

	created, err := server.LLMCreateConversation(ctx, &gctrpc.LLMCreateConversationRequest{Title: "BTC 结构"})
	require.NoError(t, err)
	assert.NotEmpty(t, created.Id, "the created conversation should carry its id")
	assert.Equal(t, "BTC 结构", created.Title)
	assert.Equal(t, ConversationKindChat, created.Kind)

	listed, err := server.LLMListConversations(ctx, &gctrpc.LLMListConversationsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Conversations, 1)
	assert.Equal(t, created.Id, listed.Conversations[0].Id)

	sent, err := server.LLMSendMessage(ctx, &gctrpc.LLMSendMessageRequest{ConversationId: created.Id, Content: "现在什么形态？"})
	require.NoError(t, err)
	assert.Equal(t, "回答：区间震荡。", sent.AssistantMessage)
	assert.NotEmpty(t, sent.MessageId, "the response should name the stored reply row")
	assert.Equal(t, int64(90), sent.InputTokens)
	assert.Equal(t, int64(30), sent.OutputTokens)
	assert.Empty(t, sent.ErrorCode)

	detail, err := server.LLMGetConversation(ctx, &gctrpc.LLMGetConversationRequest{ConversationId: created.Id})
	require.NoError(t, err)
	require.Len(t, detail.Messages, 2)
	assert.Equal(t, "现在什么形态？", detail.Messages[0].Content, "the question should come back decrypted")
	assert.Equal(t, "回答：区间震荡。", detail.Messages[1].Content)
	assert.Equal(t, created.Id, detail.Conversation.Id)

	messages, err := server.LLMListMessages(ctx, &gctrpc.LLMListMessagesRequest{ConversationId: created.Id})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 2)

	archived, err := server.LLMArchiveConversation(ctx, &gctrpc.LLMArchiveConversationRequest{ConversationId: created.Id})
	require.NoError(t, err)
	assert.Equal(t, MsgStatusSuccess, archived.Status)

	listed, err = server.LLMListConversations(ctx, &gctrpc.LLMListConversationsRequest{})
	require.NoError(t, err)
	assert.Empty(t, listed.Conversations, "an archived conversation leaves the default list")
	listed, err = server.LLMListConversations(ctx, &gctrpc.LLMListConversationsRequest{IncludeArchived: true})
	require.NoError(t, err)
	assert.Len(t, listed.Conversations, 1, "the archived conversation is still listed when asked for")
}

func TestLLMSendMessageCarriesEvidenceCards(t *testing.T) {
	envelope := chatEvidenceEnvelope(t, providerAdminNow.Add(-time.Minute), "Binance:usdtmarginedfutures:BTC_USDT")
	withTool := chatMessage("", 20, 8)
	withTool.Choices[0].FinishReason = "tool_calls"
	withTool.Choices[0].Message.ToolCalls = []openai.ToolCall{toolCall("call-1", "getCandleWindow", `{}`)}
	transport := &chatTestTransport{responses: []*openai.ChatResponse{withTool, chatMessage("看完了。", 30, 9)}}
	service, _, _ := chatTestService(t, transport)
	service.Pipeline.Tools = &fakeRunner{
		supported: map[string]bool{"getCandleWindow": true},
		results:   map[string]string{"getCandleWindow": envelope},
	}
	server := &RPCServer{Engine: &Engine{llmChatService: service}}

	created, err := server.LLMCreateConversation(t.Context(), &gctrpc.LLMCreateConversationRequest{Title: "工具回合"})
	require.NoError(t, err)
	sent, err := server.LLMSendMessage(t.Context(), &gctrpc.LLMSendMessageRequest{ConversationId: created.Id, Content: "帮我看看"})
	require.NoError(t, err)
	require.Len(t, sent.Evidence, 1, "the response must carry the evidence card of the tool it used")
	card := sent.Evidence[0]
	assert.Equal(t, "tool:getCandleWindow:call-1", card.Ref)
	assert.Equal(t, "getCandleWindow", card.Tool)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", card.MarketId)
	assert.Equal(t, string(candidate.QualityExchangeREST), card.Quality)
	assert.NotEmpty(t, card.Sha256)
	assert.NotEmpty(t, sent.ToolTranscript, "the RPC should return the transcript it stored")
	assert.Contains(t, sent.ToolTranscript, "call-1")
}

func TestLLMConversationRPCsFailClosed(t *testing.T) {
	server := &RPCServer{Engine: &Engine{}}
	_, err := server.LLMListConversations(t.Context(), &gctrpc.LLMListConversationsRequest{})
	assert.ErrorIs(t, err, errLLMChatUnavailable, "a bot without the chat service should error, not return an empty list")

	empty := &RPCServer{}
	_, err = empty.LLMCreateConversation(t.Context(), &gctrpc.LLMCreateConversationRequest{Title: "x"})
	assert.ErrorIs(t, err, errLLMChatUnavailable, "a nil engine should fail closed rather than panic")

	live := newLLMChatRPCServer(t, &chatTestTransport{})
	_, err = live.LLMSendMessage(t.Context(), &gctrpc.LLMSendMessageRequest{ConversationId: "conv-1", Content: "   "})
	assert.ErrorIs(t, err, ErrChatInputInvalid, "a blank message should be refused before any work")
}

// fakeLLMChatStream captures what a server-streaming handler sends.
type fakeLLMChatStream struct {
	grpc.ServerStream
	ctx    context.Context //nolint:containedctx // the grpc stream interface is context-based; a double must carry one.
	events []*gctrpc.LLMChatStreamEvent
}

func (f *fakeLLMChatStream) Send(event *gctrpc.LLMChatStreamEvent) error {
	f.events = append(f.events, event)
	return nil
}

func (f *fakeLLMChatStream) Context() context.Context { return f.ctx }

func TestLLMChatStreamHandlerStreamsDeltasAndFinal(t *testing.T) {
	transport := &chatStreamTransport{chunks: []string{"看", "在", "区间。"}}
	service, _, _ := chatTestService(t, transport)
	server := &RPCServer{Engine: &Engine{llmChatService: service}}
	created, err := server.LLMCreateConversation(t.Context(), &gctrpc.LLMCreateConversationRequest{Title: "stream"})
	require.NoError(t, err)

	stream := &fakeLLMChatStream{ctx: t.Context()}
	require.NoError(t, server.LLMChatStream(&gctrpc.LLMChatStreamRequest{ConversationId: created.Id, Content: "看盘"}, stream))
	require.Len(t, stream.events, 4, "three deltas and one final event")
	assert.Equal(t, "看", stream.events[0].Delta)
	assert.Equal(t, "在", stream.events[1].Delta)
	assert.Equal(t, "区间。", stream.events[2].Delta)
	require.NotNil(t, stream.events[3].Final, "the last event carries the completed turn")
	assert.Equal(t, "看在区间。", stream.events[3].Final.AssistantMessage)
	assert.NotEmpty(t, stream.events[3].Final.MessageId)

	// The streamed turn is the stored turn: one version, no reconciling.
	detail, err := server.LLMGetConversation(t.Context(), &gctrpc.LLMGetConversationRequest{ConversationId: created.Id})
	require.NoError(t, err)
	require.Len(t, detail.Messages, 2)
	assert.Equal(t, "看在区间。", detail.Messages[1].Content)
}

func TestLLMChatStreamHandlerInterruptionSendsNothing(t *testing.T) {
	transport := &chatStreamTransport{err: fmt.Errorf("%w: cut", openai.ErrStreamInterrupted)}
	service, _, _ := chatTestService(t, transport)
	server := &RPCServer{Engine: &Engine{llmChatService: service}}
	created, err := server.LLMCreateConversation(t.Context(), &gctrpc.LLMCreateConversationRequest{Title: "cut"})
	require.NoError(t, err)

	stream := &fakeLLMChatStream{ctx: t.Context()}
	err = server.LLMChatStream(&gctrpc.LLMChatStreamRequest{ConversationId: created.Id, Content: "看盘"}, stream)
	require.ErrorIs(t, err, openai.ErrStreamInterrupted)
	assert.Empty(t, stream.events, "an interrupted stream should not present a final event")

	detail, err := server.LLMGetConversation(t.Context(), &gctrpc.LLMGetConversationRequest{ConversationId: created.Id})
	require.NoError(t, err)
	assert.Len(t, detail.Messages, 1, "only the question may be stored after an interruption")
}
