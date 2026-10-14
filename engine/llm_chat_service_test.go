package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// fakeChatConversations is the conversation table in memory.
type fakeChatConversations struct {
	rows                []llmstore.Conversation
	lastIncludeArchived bool
}

func (f *fakeChatConversations) Create(_ context.Context, c *llmstore.Conversation) error {
	f.rows = append(f.rows, *c)
	return nil
}

func (f *fakeChatConversations) List(_ context.Context, includeArchived bool) ([]llmstore.Conversation, error) {
	f.lastIncludeArchived = includeArchived
	out := make([]llmstore.Conversation, 0, len(f.rows))
	for i := range f.rows {
		if !includeArchived && f.rows[i].ArchivedAt != nil {
			continue
		}
		out = append(out, f.rows[i])
	}
	return out, nil
}

func (f *fakeChatConversations) Get(_ context.Context, id string) (*llmstore.Conversation, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			row := f.rows[i]
			return &row, nil
		}
	}
	return nil, errors.New("conversation not found")
}

func (f *fakeChatConversations) Archive(_ context.Context, id string, at time.Time) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			archivedAt := at
			f.rows[i].ArchivedAt = &archivedAt
			return nil
		}
	}
	return errors.New("conversation not found")
}

func chatTestService(t *testing.T, transport ChatCompleter) (*LLMChatService, *fakeChatConversations, *chatTestHistory) {
	t.Helper()
	cipher, err := chat.NewConversationCipher(bytes.Repeat([]byte{0x33}, chat.KeySize))
	require.NoError(t, err, "the test cipher must build")
	history := &chatTestHistory{}
	conversations := &fakeChatConversations{}
	maxTokens := int64(512)
	ids := 0
	newID := func() string { ids++; return fmt.Sprintf("svc-id-%d", ids) }
	clock := func() time.Time { return providerAdminNow }
	service := &LLMChatService{
		Conversations: conversations,
		History:       history,
		Cipher:        cipher,
		ActiveRevision: func(context.Context) (string, error) {
			return "rev-1", nil
		},
		Now:   clock,
		NewID: newID,
		Pipeline: &ChatPipeline{
			Transport: transport,
			Tools:     &fakeRunner{supported: map[string]bool{}, results: map[string]string{}},
			Cipher:    cipher,
			History:   history,
			Budget:    &chatTestBudget{},
			Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
				ID: "rev-1", Kind: ProviderKindLLM, ModelID: "gpt-test",
				TimeoutSeconds: 45, MaxOutputTokens: &maxTokens,
			}},
			Now:   clock,
			NewID: newID,
		},
	}
	return service, conversations, history
}

func TestChatServiceCreatesAndListsConversations(t *testing.T) {
	service, conversations, _ := chatTestService(t, &chatTestTransport{})

	created, err := service.CreateConversation(t.Context(), "  BTC 分析  ", "")
	require.NoError(t, err, "a titled conversation must be created")
	assert.Equal(t, "BTC 分析", created.Title, "the title should be trimmed")
	assert.Equal(t, ConversationKindChat, created.Kind, "an empty kind defaults to CHAT")
	assert.True(t, providerAdminNow.Equal(created.CreatedAt))

	_, err = service.CreateConversation(t.Context(), "   ", "")
	assert.ErrorIs(t, err, ErrConversationInvalid, "an empty title after trimming is not a conversation")
	_, err = service.CreateConversation(t.Context(), strings.Repeat("x", maxConversationTitleRunes+1), "")
	assert.ErrorIs(t, err, ErrConversationInvalid, "the title is stored in the clear and should stay bounded")
	_, err = service.CreateConversation(t.Context(), "ok", "MOON")
	assert.ErrorIs(t, err, ErrConversationInvalid, "an unknown kind should be refused")

	listed, err := service.ListConversations(t.Context(), true)
	require.NoError(t, err)
	assert.Len(t, listed, 1)
	assert.True(t, conversations.lastIncludeArchived, "the include-archived flag should be passed through")
}

func TestChatServiceArchivesAtItsClock(t *testing.T) {
	service, conversations, _ := chatTestService(t, &chatTestTransport{})
	created, err := service.CreateConversation(t.Context(), "archive me", "")
	require.NoError(t, err)

	require.NoError(t, service.ArchiveConversation(t.Context(), created.ID))
	require.Len(t, conversations.rows, 1)
	require.NotNil(t, conversations.rows[0].ArchivedAt)
	assert.True(t, providerAdminNow.Equal(*conversations.rows[0].ArchivedAt), "the archive carries the service clock")

	listed, err := service.ListConversations(t.Context(), false)
	require.NoError(t, err)
	assert.Empty(t, listed, "an archived conversation leaves the default list")
}

func TestChatServiceSendStoresAndReadsBackTheTurn(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("先看 1 小时结构。", 80, 20)}}
	service, conversations, _ := chatTestService(t, transport)
	created, err := service.CreateConversation(t.Context(), "live chat", "")
	require.NoError(t, err)

	result, err := service.SendMessage(t.Context(), created.ID, "BTC 现在什么形态？")
	require.NoError(t, err)
	assert.Equal(t, "先看 1 小时结构。", result.Reply)

	conversation, messages, err := service.GetConversation(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, conversation.ID)
	require.Len(t, messages, 2)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "BTC 现在什么形态？", messages[0].Content)
	assert.Equal(t, "assistant", messages[1].Role)
	assert.Equal(t, "先看 1 小时结构。", messages[1].Content, "the detail view should open the sealed reply")
	assert.Equal(t, int64(80), messages[1].InputTokens, "the stored usage should survive the reload")
	assert.Equal(t, int64(20), messages[1].OutputTokens)
	assert.True(t, providerAdminNow.Equal(messages[0].CreatedAt))
	assert.Len(t, conversations.rows, 1)
}

func TestChatServiceSurfacesUnreadableRows(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("ok", 5, 5)}}
	service, conversations, history := chatTestService(t, transport)
	created, err := service.CreateConversation(t.Context(), "broken", "")
	require.NoError(t, err)
	_, err = service.SendMessage(t.Context(), created.ID, "hello")
	require.NoError(t, err)

	history.rows[0].ContentCipher[0] ^= 0xFF
	_, _, err = service.GetConversation(t.Context(), created.ID)
	assert.ErrorIs(t, err, ErrMessageUnreadable,
		"a conversation with an unreadable row must not be shown with a hole in it")
	assert.Len(t, conversations.rows, 1)
}

func TestChatServiceSendRefusesWithoutActiveRevision(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("never sent", 1, 1)}}
	service, _, _ := chatTestService(t, transport)
	service.ActiveRevision = func(context.Context) (string, error) {
		return "", ErrNoActiveLLMRevision
	}
	_, err := service.SendMessage(t.Context(), "conv-1", "hi")
	assert.ErrorIs(t, err, ErrNoActiveLLMRevision)
	assert.Empty(t, transport.requests, "no provider call may happen without a revision")

	// The default resolution reads the active pair through the repository; in
	// a test process without a database it must fail closed, not guess.
	service.ActiveRevision = nil
	_, err = service.SendMessage(t.Context(), "conv-1", "hi")
	assert.ErrorIs(t, err, ErrNoActiveLLMRevision)
}

func TestSetupChatCreatesTheConversationKey(t *testing.T) {
	cfg := &config.LLMConfig{Enabled: true, Exchange: "Binance", Asset: "usdtmarginedfutures"}
	manager, err := SetupLLMManager(llmStubExchangeManager{}, cfg)
	require.NoError(t, err)

	vaultDir := filepath.Join(t.TempDir(), "llm-vault")
	service, err := manager.SetupChat(vaultDir, &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	require.NoError(t, err, "the service must build and create the key")
	require.NotNil(t, service)

	keyPath := filepath.Join(vaultDir, chat.ConversationKeyFile)
	key, err := os.ReadFile(keyPath)
	require.NoError(t, err, "the key file must exist after setup")
	assert.Len(t, key, chat.KeySize)

	second, err := manager.SetupChat(vaultDir, &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	require.NoError(t, err)
	again, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, key, again, "a second setup should reuse the key, never replace it")
	assert.NotNil(t, second)

	var nilManager *LLMManager
	_, err = nilManager.SetupChat(vaultDir, &chatTestTransport{}, nil)
	assert.ErrorIs(t, err, ErrNilSubsystem)
	_, err = manager.SetupChat("", &chatTestTransport{}, &fakeRunner{supported: map[string]bool{}, results: map[string]string{}})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
	_, err = manager.SetupChat(vaultDir, nil, nil)
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
