package chat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeUser seals one user instruction as a stored row.
func storeUser(t *testing.T, cipher *ConversationCipher, seq int64, content string) StoredMessage {
	t.Helper()
	id := "msg-" + strings.Repeat("0", 3) + string(rune('a'+seq%26)) + string(rune('0'+seq/26%10))
	sealed, err := cipher.Seal(id, []byte(content))
	require.NoError(t, err)
	return StoredMessage{Seq: seq, Role: "user", Body: sealed}
}

func TestBuildContextOpensUserInstructionsInSequenceOrder(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	messages := make([]StoredMessage, 0, 4)
	messages = append(messages,
		storeUser(t, cipher, 3, "第三句指令"),
		storeUser(t, cipher, 1, "第一句指令"),
		storeUser(t, cipher, 2, "第二句指令"),
	)

	// An assistant row whose body is deliberately unopenable: the rebuild must
	// never touch it, so a corrupt assistant row cannot hide the user's own
	// instructions.
	assistant := storeUser(t, cipher, 4, "assistant text")
	assistant.Role = "assistant"
	assistant.Body.Ciphertext[0] ^= 0xFF
	messages = append(messages, assistant)

	out, err := cipher.BuildContext(messages)
	require.NoError(t, err, "the rebuild must succeed despite the corrupt assistant row")
	require.Len(t, out, 3, "only user instructions are context")
	assert.Equal(t, int64(1), out[0].Seq, "the rebuild should sort by sequence regardless of input order")
	assert.Equal(t, "第一句指令", out[0].Content)
	assert.Equal(t, "第二句指令", out[1].Content)
	assert.Equal(t, "第三句指令", out[2].Content)
}

func TestBuildContextAcceptsExactLimits(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)

	// Exactly 256 user messages of 1 rune each: both bounds sit at their
	// inclusive maximum and must be accepted.
	messages := make([]StoredMessage, 0, MaxContextMessages)
	for i := range MaxContextMessages {
		messages = append(messages, storeUser(t, cipher, int64(i+1), "x"))
	}
	out, err := cipher.BuildContext(messages)
	require.NoErrorf(t, err, "exactly %d messages and %d characters are within the limits", MaxContextMessages, MaxContextMessages)
	assert.Len(t, out, MaxContextMessages)

	// Exactly 16000 runes: 160 messages of 100 runes.
	messages = messages[:0]
	for i := range 160 {
		messages = append(messages, storeUser(t, cipher, int64(i+1), strings.Repeat("策", 100)))
	}
	out, err = cipher.BuildContext(messages)
	require.NoErrorf(t, err, "exactly %d characters must be accepted", MaxContextChars)
	require.Len(t, out, 160)
}

func TestBuildContextRejectsOverMessageLimit(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	messages := make([]StoredMessage, 0, MaxContextMessages+1)
	for i := range MaxContextMessages + 1 {
		messages = append(messages, storeUser(t, cipher, int64(i+1), "x"))
	}
	_, err := cipher.BuildContext(messages)
	assert.ErrorIs(t, err, ErrContextTooLarge, "257 user messages should be refused, not truncated")
	assert.Contains(t, err.Error(), "summarise into a new conversation",
		"the refusal must tell the user what to do instead of silently dropping the oldest")
}

func TestBuildContextCountsCharactersAsRunes(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)

	// 8000 CJK runes (24000 bytes) + 8000 ASCII runes = 16000 characters at
	// 32000 bytes: within the limit, which only holds if characters are runes.
	messages := []StoredMessage{
		storeUser(t, cipher, 1, strings.Repeat("策", 8000)),
		storeUser(t, cipher, 2, strings.Repeat("x", 8000)),
	}
	_, err := cipher.BuildContext(messages)
	require.NoError(t, err, "16000 characters must be measured in runes: byte counting would refuse this")

	// One more rune crosses the line.
	messages = []StoredMessage{
		storeUser(t, cipher, 1, strings.Repeat("策", 8000)),
		storeUser(t, cipher, 2, strings.Repeat("x", 8001)),
	}
	_, err = cipher.BuildContext(messages)
	assert.ErrorIs(t, err, ErrContextTooLarge, "16001 characters should be refused")
}

func TestBuildContextRefusesWithoutInstructions(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	_, err := cipher.BuildContext(nil)
	assert.ErrorIs(t, err, ErrContextEmpty, "an empty conversation cannot produce an extraction context")

	assistant := storeUser(t, cipher, 1, "only an assistant row")
	assistant.Role = "assistant"
	_, err = cipher.BuildContext([]StoredMessage{assistant})
	assert.ErrorIs(t, err, ErrContextEmpty, "assistant rows alone are not instructions")
}

func TestBuildContextSurfacesABrokenUserRow(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	broken := storeUser(t, cipher, 1, "这一行会被篡改")
	broken.Body.Ciphertext = append([]byte(nil), broken.Body.Ciphertext...)
	broken.Body.Ciphertext[0] ^= 0xFF

	_, err := cipher.BuildContext([]StoredMessage{broken})
	require.Error(t, err, "a corrupt user row must fail the rebuild")
	assert.ErrorIs(t, err, ErrDecrypt, "skipping it silently would be truncation wearing a different hat")
	assert.Contains(t, err.Error(), broken.Body.ID, "the error should name the row so an operator can find it")
}

func TestBuildContextDoesNotMutateTheInput(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	messages := []StoredMessage{
		storeUser(t, cipher, 2, "second"),
		storeUser(t, cipher, 1, "first"),
	}
	before := messages[0].Seq
	cipherBefore := append([]byte(nil), messages[0].Body.Ciphertext...)
	_, err := cipher.BuildContext(messages)
	require.NoError(t, err)
	assert.Equal(t, before, messages[0].Seq, "the rebuild should work on a copy: the caller's slice order is theirs")
	assert.True(t, bytes.Equal(cipherBefore, messages[0].Body.Ciphertext), "the rebuild should not rewrite existing ciphertexts")
}
