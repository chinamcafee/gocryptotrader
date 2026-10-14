package llm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
)

func TestChatStorageRoundTripsEncryptedBodies(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()

	cipher, err := chat.NewConversationCipher(bytes.Repeat([]byte{0x7a}, chat.KeySize))
	require.NoError(t, err, "the cipher must build")

	require.NoError(t, InsertConversation(ctx, &Conversation{
		ID: "conv-cipher", Title: "加密存储验证", Kind: "chat",
		CreatedAt: baseTime, UpdatedAt: baseTime, MessageCount: 0,
	}))

	first, err := cipher.Seal("msg-cipher-1", []byte("用户的问题：BTC 现在能开多吗？"))
	require.NoError(t, err)
	_, err = AppendChatMessage(ctx, &ChatMessage{
		ID: first.ID, ConversationID: "conv-cipher", Role: "user",
		ContentCipher: first.Ciphertext, ContentNonce: first.Nonce, ContentSha256: first.Sha256,
		CharCount: 18, CreatedAt: atMinutes(1),
	})
	require.NoError(t, err)

	second, err := cipher.Seal("msg-cipher-2", []byte("assistant reply"))
	require.NoError(t, err)
	_, err = AppendChatMessage(ctx, &ChatMessage{
		ID: second.ID, ConversationID: "conv-cipher", Role: "assistant",
		ContentCipher: second.Ciphertext, ContentNonce: second.Nonce, ContentSha256: second.Sha256,
		CharCount: 15, CreatedAt: atMinutes(2),
	})
	require.NoError(t, err)

	stored, err := ListChatMessages(ctx, "conv-cipher", 0, 0)
	require.NoError(t, err)
	require.Len(t, stored, 2)

	// What comes back out of the database must open under the same row id and
	// be byte-identical to what went in -- the end-to-end claim of Q17 = C.
	opened, err := cipher.Open(&chat.Message{
		ID: stored[0].ID, Ciphertext: stored[0].ContentCipher,
		Nonce: stored[0].ContentNonce, Sha256: stored[0].ContentSha256,
	})
	require.NoError(t, err, "the stored row must open")
	assert.Equal(t, "用户的问题：BTC 现在能开多吗？", string(opened),
		"the body must survive store and load byte for byte")

	// Both rows are intact, yet a ciphertext cannot be opened under its
	// neighbour's id: the AAD binding survives the round trip through SQL.
	_, err = cipher.Open(&chat.Message{
		ID: stored[1].ID, Ciphertext: stored[0].ContentCipher,
		Nonce: stored[0].ContentNonce, Sha256: stored[0].ContentSha256,
	})
	assert.ErrorIs(t, err, chat.ErrDecrypt, "a ciphertext moved between rows should not open")
}
