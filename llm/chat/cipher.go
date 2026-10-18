// Package chat holds the conversation-layer primitives the LLM subsystem
// shares: sealing message bodies for storage and opening them for reading.
//
// Storage keeps conversation titles and metadata in the clear (docs/llm-dev
// 09-数据存储与配置.md §5.4, Q17 = C): titles stay searchable while the body —
// which carries trading intent — is encrypted. The message id is the
// additional authenticated data, so a ciphertext moved to another row fails to
// open instead of decrypting into the wrong message's slot.
package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConversationKeyFile is the key file's name inside the chat key directory
// (docs/llm-dev 09 §5.4; the directory became llm-chat-keys after the vault
// removal on 2026-10-15).
const ConversationKeyFile = "conversation-key.bin"

// KeySize is 32 bytes: AES-256, the strength the design fixed.
const KeySize = 32

var (
	// ErrKeyInvalid marks a key that is not the required 32 bytes.
	ErrKeyInvalid = errors.New("conversation key must be 32 bytes")
	// ErrMessageIDRequired marks a seal or open without the row id that binds
	// the ciphertext to its place.
	ErrMessageIDRequired = errors.New("message id is required as additional authenticated data")
	// ErrDecrypt marks ciphertext that does not authenticate: wrong key,
	// tampered bytes, or a ciphertext from a different row.
	ErrDecrypt = errors.New("message content failed to authenticate")
	// ErrDigestMismatch marks plaintext whose hash differs from the stored one.
	ErrDigestMismatch = errors.New("message content hash mismatch")
)

// Message is a sealed body: what the database stores and what the reader hands
// back to Open.
type Message struct {
	// ID is the row id, used as additional authenticated data.
	ID string
	// Ciphertext is the AES-256-GCM sealed plaintext.
	Ciphertext []byte
	// Nonce is the per-message nonce.
	Nonce []byte
	// Sha256 is the lowercase hex digest of the plaintext, kept for an
	// integrity check that survives a key rotation audit.
	Sha256 string
}

// ConversationCipher seals and opens message bodies.
type ConversationCipher struct {
	aead cipher.AEAD
}

// NewConversationCipher builds the cipher over a 32-byte key.
func NewConversationCipher(key []byte) (*ConversationCipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrKeyInvalid, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("building AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("building GCM: %w", err)
	}
	return &ConversationCipher{aead: aead}, nil
}

// LoadOrCreateConversationKey returns the conversation key inside dir,
// generating it with owner-only permissions when absent.
//
// The create is O_EXCL: when two processes race, the loser re-reads the
// winner's key rather than overwriting it, because two keys for one store
// would make half the history unreadable.
func LoadOrCreateConversationKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, ConversationKeyFile)
	key, err := readConversationKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating chat key directory: %w", err)
	}
	key = make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating conversation key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return readConversationKey(path)
		}
		return nil, fmt.Errorf("writing conversation key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("writing conversation key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("writing conversation key: %w", err)
	}
	return key, nil
}

// readConversationKey reads and validates a key file.
func readConversationKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading conversation key: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: %s holds %d bytes", ErrKeyInvalid, path, len(key))
	}
	return key, nil
}

// Seal encrypts a message body. The id is the additional authenticated data,
// so the ciphertext can only be opened under the same row id.
func (c *ConversationCipher) Seal(id string, plaintext []byte) (Message, error) {
	if c == nil || c.aead == nil {
		return Message{}, fmt.Errorf("%w: cipher is nil", ErrKeyInvalid)
	}
	if id == "" {
		return Message{}, ErrMessageIDRequired
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Message{}, fmt.Errorf("generating nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, []byte(id))
	sum := sha256.Sum256(plaintext)
	return Message{
		ID:         id,
		Ciphertext: sealed,
		Nonce:      nonce,
		Sha256:     hex.EncodeToString(sum[:]),
	}, nil
}

// Open decrypts a sealed body and verifies the plaintext digest.
//
// A failure to authenticate is ErrDecrypt; a digest mismatch on successfully
// decrypted bytes is ErrDigestMismatch — the two are distinct because the
// first means the stored row cannot be trusted and the second means the
// stored hash was written by something other than this cipher's Seal.
func (c *ConversationCipher) Open(m *Message) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, fmt.Errorf("%w: cipher is nil", ErrKeyInvalid)
	}
	if m.ID == "" {
		return nil, ErrMessageIDRequired
	}
	plaintext, err := c.aead.Open(nil, m.Nonce, m.Ciphertext, []byte(m.ID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	sum := sha256.Sum256(plaintext)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), m.Sha256) {
		return nil, ErrDigestMismatch
	}
	return plaintext, nil
}
