package engine

import (
	"context"
	"errors"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// LLM conversation RPCs (docs/llm-dev 08 §3.1).
//
// The handlers are conversions over LLMChatService: every piece of conversation
// logic lives in the service so it is testable without a gRPC harness, and the
// transport surface stays thin enough to audit line by line.

// errLLMChatUnavailable marks conversation RPCs on a bot whose chat service was
// never built. Fail-closed: the RPC errors instead of succeeding with empty
// data, because an empty conversation list and a broken subsystem must not look
// the same to the webUI.
var errLLMChatUnavailable = errors.New("llm chat service is not configured on this bot")

// chatService returns the conversation service or a fail-closed error.
func (s *RPCServer) chatService() (*LLMChatService, error) {
	if s.Engine == nil || s.Engine.llmChatService == nil {
		return nil, errLLMChatUnavailable
	}
	return s.Engine.llmChatService, nil
}

// conversationToProto renders a stored conversation.
func conversationToProto(c *llmstore.Conversation) *gctrpc.LLMConversation {
	return &gctrpc.LLMConversation{
		Id:           c.ID,
		Title:        c.Title,
		Kind:         c.Kind,
		CreatedAt:    c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    c.UpdatedAt.UTC().Format(time.RFC3339),
		Archived:     c.ArchivedAt != nil,
		MessageCount: c.MessageCount,
	}
}

// messageToProto renders one decrypted message.
func messageToProto(m *ChatMessageView) *gctrpc.LLMChatMessage {
	return &gctrpc.LLMChatMessage{
		Id:           m.ID,
		Seq:          m.Seq,
		Role:         m.Role,
		Content:      m.Content,
		CreatedAt:    m.CreatedAt.UTC().Format(time.RFC3339),
		ErrorCode:    m.ErrorCode,
		InputTokens:  m.InputTokens,
		OutputTokens: m.OutputTokens,
		LatencyMs:    m.LatencyMS,
	}
}

// cardToProto renders one evidence card.
func cardToProto(c *EvidenceCard) *gctrpc.LLMEvidenceCard {
	return &gctrpc.LLMEvidenceCard{
		Ref:               c.Ref,
		Tool:              c.Tool,
		Exchange:          c.Exchange,
		MarketId:          c.MarketID,
		AccountId:         c.AccountID,
		StrategyVersionId: c.StrategyVersionID,
		SourceTime:        c.SourceTime.UTC().Format(time.RFC3339),
		Quality:           c.Quality,
		SnapshotId:        c.SnapshotID,
		Sha256:            c.SHA256,
	}
}

// LLMListConversations lists conversations, newest first.
func (s *RPCServer) LLMListConversations(ctx context.Context, r *gctrpc.LLMListConversationsRequest) (*gctrpc.LLMListConversationsResponse, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	includeArchived := r != nil && r.IncludeArchived
	rows, err := service.ListConversations(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	response := &gctrpc.LLMListConversationsResponse{Conversations: make([]*gctrpc.LLMConversation, 0, len(rows))}
	for i := range rows {
		response.Conversations = append(response.Conversations, conversationToProto(&rows[i]))
	}
	return response, nil
}

// LLMCreateConversation creates a conversation.
func (s *RPCServer) LLMCreateConversation(ctx context.Context, r *gctrpc.LLMCreateConversationRequest) (*gctrpc.LLMConversation, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = &gctrpc.LLMCreateConversationRequest{}
	}
	conversation, err := service.CreateConversation(ctx, r.Title, r.Kind)
	if err != nil {
		return nil, err
	}
	return conversationToProto(conversation), nil
}

// LLMGetConversation returns a conversation with its decrypted messages.
func (s *RPCServer) LLMGetConversation(ctx context.Context, r *gctrpc.LLMGetConversationRequest) (*gctrpc.LLMConversationDetail, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrConversationInvalid
	}
	conversation, messages, err := service.GetConversation(ctx, r.ConversationId)
	if err != nil {
		return nil, err
	}
	detail := &gctrpc.LLMConversationDetail{
		Conversation: conversationToProto(conversation),
		Messages:     make([]*gctrpc.LLMChatMessage, 0, len(messages)),
	}
	for i := range messages {
		detail.Messages = append(detail.Messages, messageToProto(&messages[i]))
	}
	return detail, nil
}

// LLMArchiveConversation archives a conversation.
func (s *RPCServer) LLMArchiveConversation(ctx context.Context, r *gctrpc.LLMArchiveConversationRequest) (*gctrpc.GenericResponse, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrConversationInvalid
	}
	if err := service.ArchiveConversation(ctx, r.ConversationId); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: MsgStatusSuccess}, nil
}

// LLMListMessages returns a conversation's decrypted messages.
func (s *RPCServer) LLMListMessages(ctx context.Context, r *gctrpc.LLMListMessagesRequest) (*gctrpc.LLMListMessagesResponse, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrConversationInvalid
	}
	_, messages, err := service.GetConversation(ctx, r.ConversationId)
	if err != nil {
		return nil, err
	}
	response := &gctrpc.LLMListMessagesResponse{Messages: make([]*gctrpc.LLMChatMessage, 0, len(messages))}
	for i := range messages {
		response.Messages = append(response.Messages, messageToProto(&messages[i]))
	}
	return response, nil
}

// chatResultToProto renders a completed turn for the wire.
func chatResultToProto(result *ChatRunResult) *gctrpc.LLMSendMessageResponse {
	response := &gctrpc.LLMSendMessageResponse{
		MessageId:        result.AssistantMessageID,
		AssistantMessage: result.Reply,
		ToolTranscript:   result.ToolTranscript,
		InputTokens:      result.InputTokens,
		OutputTokens:     result.OutputTokens,
		LatencyMs:        result.LatencyMS,
		ErrorCode:        result.ReasonCode,
		Evidence:         make([]*gctrpc.LLMEvidenceCard, 0, len(result.Evidence)),
	}
	for i := range result.Evidence {
		response.Evidence = append(response.Evidence, cardToProto(&result.Evidence[i]))
	}
	return response
}

// LLMSendMessage runs one conversational turn and returns the reply with its
// evidence cards.
func (s *RPCServer) LLMSendMessage(ctx context.Context, r *gctrpc.LLMSendMessageRequest) (*gctrpc.LLMSendMessageResponse, error) {
	service, err := s.chatService()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrChatInputInvalid
	}
	result, err := service.SendMessage(ctx, r.ConversationId, r.Content)
	if err != nil {
		return nil, err
	}
	return chatResultToProto(result), nil
}

// LLMChatStream runs one conversational turn, streaming the reply's text
// fragments as they arrive and a final event carrying the completed turn.
//
// Only conversations stream (docs 05 §1, Q14 = C): a decision run has no
// streaming variant because half a JSON document cannot be validated. An
// interrupted stream is a gRPC error and persists no assistant row.
func (s *RPCServer) LLMChatStream(r *gctrpc.LLMChatStreamRequest, stream gctrpc.GoCryptoTraderService_LLMChatStreamServer) error {
	service, err := s.chatService()
	if err != nil {
		return err
	}
	if r == nil {
		return ErrChatInputInvalid
	}
	result, err := service.SendMessageStream(stream.Context(), r.ConversationId, r.Content, func(delta string) error {
		return stream.Send(&gctrpc.LLMChatStreamEvent{Delta: delta})
	})
	if err != nil {
		return err
	}
	return stream.Send(&gctrpc.LLMChatStreamEvent{Final: chatResultToProto(result)})
}
