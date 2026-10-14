package chat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{0x42}, KeySize)
}

func testCipher(t *testing.T) *ConversationCipher {
	t.Helper()
	cipher, err := NewConversationCipher(testKey(t))
	require.NoError(t, err, "the cipher must build over a 32-byte key")
	return cipher
}

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	plaintext := []byte("现货与合约的对冲意图，含中文与 ASCII")
	sealed, err := cipher.Seal("msg-1", plaintext)
	require.NoError(t, err, "sealing must succeed")

	assert.Equal(t, "msg-1", sealed.ID, "the seal should carry its row id")
	assert.NotEqual(t, plaintext, sealed.Ciphertext, "the ciphertext should not be the plaintext")
	assert.False(t, bytes.Contains(sealed.Ciphertext, []byte("对冲")), "no plaintext fragment may survive in the ciphertext")
	assert.Len(t, sealed.Nonce, 12, "GCM's standard nonce is 12 bytes")
	sum := sha256.Sum256(plaintext)
	assert.Equal(t, hex.EncodeToString(sum[:]), sealed.Sha256, "the digest should be sha256 of the plaintext, lowercase hex")

	opened, err := cipher.Open(&sealed)
	require.NoError(t, err, "opening its own seal must succeed")
	assert.True(t, bytes.Equal(plaintext, opened), "the round trip should be byte-identical")
}

func TestSealUsesFreshNonces(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	first, err := cipher.Seal("msg-1", []byte("same content"))
	require.NoError(t, err)
	second, err := cipher.Seal("msg-1", []byte("same content"))
	require.NoError(t, err)

	assert.NotEqual(t, first.Nonce, second.Nonce, "each seal should draw a fresh nonce")
	assert.NotEqual(t, first.Ciphertext, second.Ciphertext, "same plaintext should not repeat its ciphertext under a fresh nonce")
	opened, err := cipher.Open(&second)
	require.NoError(t, err)
	assert.Equal(t, "same content", string(opened), "both seals should open to the same content")
}

func TestOpenRejectsMovedCiphertext(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	sealed, err := cipher.Seal("msg-1", []byte("belongs to row one"))
	require.NoError(t, err)

	// The id is the AAD: opening under another row's id must fail rather than
	// silently decrypt into the wrong message's slot.
	moved := sealed
	moved.ID = "msg-2"
	_, err = cipher.Open(&moved)
	assert.ErrorIs(t, err, ErrDecrypt, "a ciphertext moved to another row should not open")

	// The correct id still opens: the rejection is about the binding, not the bytes.
	opened, err := cipher.Open(&sealed)
	require.NoError(t, err)
	assert.Equal(t, "belongs to row one", string(opened))
}

func TestOpenRejectsTamperedBytes(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	sealed, err := cipher.Seal("msg-1", []byte("do not modify"))
	require.NoError(t, err)

	flipped := sealed
	flipped.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	flipped.Ciphertext[0] ^= 0x01
	_, err = cipher.Open(&flipped)
	assert.ErrorIs(t, err, ErrDecrypt, "a flipped ciphertext byte should not authenticate")

	nonced := sealed
	nonced.Nonce = append([]byte(nil), sealed.Nonce...)
	nonced.Nonce[0] ^= 0x01
	_, err = cipher.Open(&nonced)
	assert.ErrorIs(t, err, ErrDecrypt, "a flipped nonce should not authenticate")

	truncated := sealed
	truncated.Ciphertext = sealed.Ciphertext[:len(sealed.Ciphertext)-1]
	_, err = cipher.Open(&truncated)
	assert.ErrorIs(t, err, ErrDecrypt, "a truncated ciphertext should not authenticate")

	// A different key is the garden-variety failure this sentinel also covers.
	other, err := NewConversationCipher(bytes.Repeat([]byte{0x24}, KeySize))
	require.NoError(t, err)
	_, err = other.Open(&sealed)
	assert.ErrorIs(t, err, ErrDecrypt, "a foreign key should not open the message")
}

func TestOpenRejectsDigestMismatch(t *testing.T) {
	t.Parallel()

	cipher := testCipher(t)
	sealed, err := cipher.Seal("msg-1", []byte("hash-anchored"))
	require.NoError(t, err)

	wrongHash := sealed
	wrongHash.Sha256 = hex.EncodeToString(make([]byte, sha256.Size))
	_, err = cipher.Open(&wrongHash)
	assert.ErrorIs(t, err, ErrDigestMismatch, "a stored digest that does not match the plaintext should be distinguished from an auth failure")

	// The digest also catches two rows whose hashes were swapped: bytes that
	// decrypt cleanly can still be the wrong message.
	other, err := cipher.Seal("msg-2", []byte("other content"))
	require.NoError(t, err)
	swapped := sealed
	swapped.Sha256 = other.Sha256
	_, err = cipher.Open(&swapped)
	assert.ErrorIs(t, err, ErrDigestMismatch, "a hash from another message should be refused")
}

func TestSealAndOpenRefuseMisuse(t *testing.T) {
	t.Parallel()

	var nilCipher *ConversationCipher
	_, err := nilCipher.Seal("msg-1", []byte("x"))
	require.ErrorIs(t, err, ErrKeyInvalid, "sealing without a cipher must fail")

	cipher := testCipher(t)
	_, err = cipher.Seal("", []byte("x"))
	assert.ErrorIs(t, err, ErrMessageIDRequired, "a ciphertext without its row id cannot be bound")

	_, err = cipher.Open(&Message{ID: "", Ciphertext: []byte{1}, Nonce: []byte{1}})
	assert.ErrorIs(t, err, ErrMessageIDRequired, "opening without the row id should fail before any decryption")

	_, err = NewConversationCipher(nil)
	assert.ErrorIs(t, err, ErrKeyInvalid)
	_, err = NewConversationCipher(bytes.Repeat([]byte{1}, 16))
	assert.ErrorIs(t, err, ErrKeyInvalid, "AES-128 is not the agreed strength")
	_, err = NewConversationCipher(bytes.Repeat([]byte{1}, 31))
	assert.ErrorIs(t, err, ErrKeyInvalid)
	_, err = NewConversationCipher(bytes.Repeat([]byte{1}, 33))
	assert.ErrorIs(t, err, ErrKeyInvalid)
}

func TestConversationKeyFileLifecycle(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "llm-vault")
	key, err := LoadOrCreateConversationKey(dir)
	require.NoError(t, err, "a missing key must be created")
	require.Len(t, key, KeySize, "the generated key must be 32 bytes")

	info, err := os.Stat(filepath.Join(dir, ConversationKeyFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the key should be owner-only readable")

	again, err := LoadOrCreateConversationKey(dir)
	require.NoError(t, err)
	assert.Equal(t, key, again, "a second call should return the same key rather than replacing it")

	// The file is the source of truth across processes: a fresh cipher built
	// from a fresh read must open what the first cipher sealed.
	first, err := NewConversationCipher(key)
	require.NoError(t, err)
	sealed, err := first.Seal("msg-1", []byte("persisted"))
	require.NoError(t, err)
	reloaded, err := LoadOrCreateConversationKey(dir)
	require.NoError(t, err)
	second, err := NewConversationCipher(reloaded)
	require.NoError(t, err)
	opened, err := second.Open(&sealed)
	require.NoError(t, err)
	assert.Equal(t, "persisted", string(opened))

	// A key file of the wrong size is an error, not a regeneration: silently
	// replacing it would make every existing message unreadable.
	shortDir := filepath.Join(t.TempDir(), "llm-vault")
	require.NoError(t, os.MkdirAll(shortDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(shortDir, ConversationKeyFile), []byte("short"), 0o600))
	_, err = LoadOrCreateConversationKey(shortDir)
	assert.ErrorIs(t, err, ErrKeyInvalid, "an undersized key file should be refused rather than overwritten")
}
