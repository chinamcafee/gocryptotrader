// Package secrets stores model API keys outside the application database.
//
// Design: docs/llm-dev/09-数据存储与配置.md §5.2. The properties that matter:
//
//   - The database stores only a reference and a fingerprint. A stolen database
//     yields no usable credential.
//   - Each secret is encrypted with its own random AES-256-GCM data key, and only
//     that data key is wrapped by the RSA key pair. This means a leaked data key
//     exposes one secret instead of the whole vault.
//   - The reference is used as additional authenticated data, so a ciphertext
//     cannot be moved to another record without detection.
//   - Key material never reaches a caller that did not ask for it: listing
//     returns fingerprints only.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// Vault layout and permission constants.
const (
	// DefaultDirName is the vault directory created inside a data directory.
	DefaultDirName = "llm-vault"
	// PrivateKeyFile holds the RSA private key.
	PrivateKeyFile = "vault-private.pem"
	// PublicKeyFile holds the RSA public key.
	PublicKeyFile = "vault-public.pem"
	// SecretsDirName holds one file per secret.
	SecretsDirName = "secrets"
	// FileMode is the permission of key material and secret files.
	FileMode = os.FileMode(0o600)
	// DirMode is the permission of vault directories.
	DirMode = os.FileMode(0o700)
	// rsaKeyBits is the key size of the wrapping key pair.
	rsaKeyBits = 3072
	// keyKindLLM is the reference kind used for model provider secrets.
	keyKindLLM = "llm"
	// fingerprintBytes is how much of the digest the fingerprint keeps.
	fingerprintBytes = 8
)

var (
	// ErrNotFound marks a reference with no stored secret.
	ErrNotFound = errors.New("secret not found")
	// ErrEmptySecret marks an empty plaintext.
	ErrEmptySecret = errors.New("secret is empty")
	// ErrDecryptFailed marks a secret that cannot be decrypted, either because
	// the ciphertext was altered or because it belongs to another wrapping key.
	ErrDecryptFailed = errors.New("secret cannot be decrypted")
	// ErrVaultNotInitialised marks a vault directory without a key pair.
	ErrVaultNotInitialised = errors.New("vault is not initialised")
	// ErrInvalidRef marks a reference outside the accepted shape.
	ErrInvalidRef = errors.New("invalid secret reference")
)

// Entry describes a stored secret without revealing it.
type Entry struct {
	Ref         Ref       `json:"ref"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
}

// storedSecret is the on-disk record. Every field is either ciphertext or a
// public value; no plaintext is persisted anywhere.
type storedSecret struct {
	Kind        string `json:"kind"`
	WrappedKey  string `json:"wrappedKey"`
	Nonce       string `json:"nonce"`
	Ciphertext  string `json:"ciphertext"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
}

// Vault reads and writes secrets under a directory.
type Vault struct {
	dir  string
	priv *rsa.PrivateKey
}

// Init creates the vault if it does not exist and opens it either way.
//
// Generation and loading share one entry point so a caller cannot accidentally
// create a second key pair and lose access to the first one's secrets.
func Init(dir string) (*Vault, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: empty directory", ErrVaultNotInitialised)
	}
	privPath := filepath.Join(dir, PrivateKeyFile)
	if _, err := os.Stat(privPath); err == nil {
		return Open(dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspecting vault: %w", err)
	}

	if err := os.MkdirAll(dir, DirMode); err != nil {
		return nil, fmt.Errorf("creating vault directory: %w", err)
	}
	if err := os.Chmod(dir, DirMode); err != nil {
		return nil, fmt.Errorf("setting vault directory mode: %w", err)
	}

	priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("generating wrapping key: %w", err)
	}
	if err := writePEM(privPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv)); err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("encoding public key: %w", err)
	}
	if err := writePEM(filepath.Join(dir, PublicKeyFile), "PUBLIC KEY", pubDER); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(dir, SecretsDirName), DirMode); err != nil {
		return nil, fmt.Errorf("creating secrets directory: %w", err)
	}
	return &Vault{dir: dir, priv: priv}, nil
}

// Open loads an existing vault.
func Open(dir string) (*Vault, error) {
	raw, err := os.ReadFile(filepath.Join(dir, PrivateKeyFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrVaultNotInitialised, dir)
		}
		return nil, fmt.Errorf("reading vault key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%w: key file is not PEM", ErrVaultNotInitialised)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing vault key: %w", err)
	}
	// Ensure the secrets directory exists so a caller does not have to know the
	// layout to add the first secret.
	if err = os.MkdirAll(filepath.Join(dir, SecretsDirName), DirMode); err != nil {
		return nil, fmt.Errorf("creating secrets directory: %w", err)
	}
	return &Vault{dir: dir, priv: priv}, nil
}

// Dir returns the vault directory.
func (v *Vault) Dir() string { return v.dir }

// Add stores a secret and returns its reference plus a fingerprint.
//
// The plaintext is never written, logged or returned again; the caller must keep
// the reference to retrieve it.
func (v *Vault) Add(kind, plaintext string) (Ref, string, error) {
	if v == nil || v.priv == nil {
		return Ref{}, "", ErrVaultNotInitialised
	}
	if strings.TrimSpace(plaintext) == "" {
		return Ref{}, "", ErrEmptySecret
	}
	if kind == "" {
		kind = keyKindLLM
	}

	id, err := uuid.NewV4()
	if err != nil {
		return Ref{}, "", fmt.Errorf("generating secret id: %w", err)
	}
	ref := Ref{Scheme: RefScheme, Kind: kind, ID: id.String()}

	dataKey := make([]byte, 32)
	if _, err = rand.Read(dataKey); err != nil {
		return Ref{}, "", fmt.Errorf("generating data key: %w", err)
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return Ref{}, "", fmt.Errorf("building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Ref{}, "", fmt.Errorf("building GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return Ref{}, "", fmt.Errorf("generating nonce: %w", err)
	}
	// The reference is authenticated but not encrypted, so a ciphertext moved to
	// another record fails to open.
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(ref.String()))

	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &v.priv.PublicKey, dataKey, nil)
	if err != nil {
		return Ref{}, "", fmt.Errorf("wrapping data key: %w", err)
	}

	fingerprint := Fingerprint(plaintext)
	record := storedSecret{
		Kind:        kind,
		WrappedKey:  base64.StdEncoding.EncodeToString(wrapped),
		Nonce:       base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:  base64.StdEncoding.EncodeToString(ciphertext),
		Fingerprint: fingerprint,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(record)
	if err != nil {
		return Ref{}, "", fmt.Errorf("encoding secret: %w", err)
	}
	if err = os.WriteFile(v.pathFor(ref), body, FileMode); err != nil {
		return Ref{}, "", fmt.Errorf("writing secret: %w", err)
	}
	if err = os.Chmod(v.pathFor(ref), FileMode); err != nil {
		return Ref{}, "", fmt.Errorf("setting secret mode: %w", err)
	}
	return ref, fingerprint, nil
}

// Reveal decrypts a stored secret.
//
// Callers should hold the result for as short a time as possible; the adapters
// fetch it per request rather than caching it.
func (v *Vault) Reveal(ref Ref) (string, error) {
	if v == nil || v.priv == nil {
		return "", ErrVaultNotInitialised
	}
	record, err := v.load(ref)
	if err != nil {
		return "", err
	}
	wrapped, err := base64.StdEncoding.DecodeString(record.WrappedKey)
	if err != nil {
		return "", fmt.Errorf("%w: wrapped key is malformed", ErrDecryptFailed)
	}
	nonce, err := base64.StdEncoding.DecodeString(record.Nonce)
	if err != nil {
		return "", fmt.Errorf("%w: nonce is malformed", ErrDecryptFailed)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(record.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: ciphertext is malformed", ErrDecryptFailed)
	}
	dataKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, v.priv, wrapped, nil)
	if err != nil {
		return "", fmt.Errorf("%w: unwrapping data key: %w", ErrDecryptFailed, err)
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrDecryptFailed, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrDecryptFailed, err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(ref.String()))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrDecryptFailed, err)
	}
	return string(plaintext), nil
}

// Fingerprint returns the stored fingerprint of a secret.
func (v *Vault) Fingerprint(ref Ref) (string, error) {
	record, err := v.load(ref)
	if err != nil {
		return "", err
	}
	return record.Fingerprint, nil
}

// List returns every stored secret as a reference plus fingerprint, so a caller
// can present a chooser without handling key material.
func (v *Vault) List() ([]Entry, error) {
	if v == nil || v.priv == nil {
		return nil, ErrVaultNotInitialised
	}
	dir := filepath.Join(v.dir, SecretsDirName)
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading secret %s: %w", f.Name(), err)
		}
		var record storedSecret
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decoding secret %s: %w", f.Name(), err)
		}
		ref, err := ParseRef(refStringFor(record.Kind, strings.TrimSuffix(f.Name(), ".json")))
		if err != nil {
			return nil, err
		}
		created, _ := time.Parse(time.RFC3339, record.CreatedAt)
		entries = append(entries, Entry{Ref: ref, Fingerprint: record.Fingerprint, CreatedAt: created})
	}
	return entries, nil
}

// Remove deletes a stored secret.
func (v *Vault) Remove(ref Ref) error {
	if v == nil || v.priv == nil {
		return ErrVaultNotInitialised
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := os.Remove(v.pathFor(ref)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, ref.String())
		}
		return fmt.Errorf("removing secret: %w", err)
	}
	return nil
}

func (v *Vault) load(ref Ref) (*storedSecret, error) {
	if v == nil || v.priv == nil {
		return nil, ErrVaultNotInitialised
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(v.pathFor(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, ref.String())
		}
		return nil, fmt.Errorf("reading secret: %w", err)
	}
	var record storedSecret
	if err = json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("%w: decoding secret: %w", ErrDecryptFailed, err)
	}
	return &record, nil
}

func (v *Vault) pathFor(ref Ref) string {
	return filepath.Join(v.dir, SecretsDirName, ref.ID+".json")
}

// Fingerprint returns a truncated digest of a secret, suitable for display.
//
// It is a digest rather than a prefix so it reveals nothing about the secret's
// length or shape, and truncation keeps it short enough to read aloud.
func Fingerprint(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:fingerprintBytes])
}

func writePEM(path, blockType string, der []byte) error {
	body := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if body == nil {
		return fmt.Errorf("encoding %s", blockType)
	}
	if err := os.WriteFile(path, body, FileMode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return os.Chmod(path, FileMode)
}
