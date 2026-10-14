package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

const (
	testKey     = "sk-test-0123456789abcdef"
	testKeyAlt  = "sk-test-fedcba9876543210"
	testUnicode = "密钥-🔐-with-mixed-种类"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Init(filepath.Join(t.TempDir(), DefaultDirName))
	require.NoError(t, err, "initialising a vault in a temporary directory must succeed")
	return v
}

func TestInitCreatesProtectedLayout(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), DefaultDirName)
	v, err := Init(dir)
	require.NoError(t, err, "creating a vault must succeed")
	require.NotNil(t, v, "a fresh vault must be returned")

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err, "the vault directory must exist")
	assert.Equal(t, DirMode, dirInfo.Mode().Perm(), "the vault directory should not be group or world readable")

	for _, name := range []string{PrivateKeyFile, PublicKeyFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoErrorf(t, err, "%s must be written", name)
		assert.Equalf(t, FileMode, info.Mode().Perm(), "%s should be owner-only", name)
	}

	secretsInfo, err := os.Stat(filepath.Join(dir, SecretsDirName))
	require.NoError(t, err, "the secrets directory must exist")
	assert.Equal(t, DirMode, secretsInfo.Mode().Perm(), "the secrets directory should not be group or world readable")
}

func TestInitReusesAnExistingKeyPair(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), DefaultDirName)
	first, err := Init(dir)
	require.NoError(t, err, "the first initialisation must succeed")
	ref, _, err := first.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	second, err := Init(dir)
	require.NoError(t, err, "re-opening the same directory must succeed")
	got, err := second.Reveal(ref)
	require.NoError(t, err, "a secret written by the first vault must open in the second")
	assert.Equal(t, testKey, got, "the second vault should read what the first wrote")
}

func TestOpenWithoutKeyPairIsRejected(t *testing.T) {
	t.Parallel()

	_, err := Open(t.TempDir())
	require.ErrorIs(t, err, ErrVaultNotInitialised, "an emptied directory must not count as a vault")

	_, err = Init("  ")
	require.ErrorIs(t, err, ErrVaultNotInitialised, "a blank directory must be rejected")
}

func TestAddRevealRoundTrip(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	for _, plaintext := range []string{testKey, testUnicode, strings.Repeat("a", 4096)} {
		ref, fingerprint, err := v.Add(keyKindLLM, plaintext)
		require.NoError(t, err, "storing a secret must succeed")
		assert.Equal(t, RefScheme, ref.Scheme, "the reference scheme should be pinned")
		assert.Equal(t, keyKindLLM, ref.Kind, "the reference kind should be recorded")

		got, err := v.Reveal(ref)
		require.NoError(t, err, "revealing a stored secret must succeed")
		assert.Equal(t, plaintext, got, "the revealed secret should match what was stored")
		assert.Len(t, fingerprint, fingerprintBytes*2, "the fingerprint should be a truncated hex digest")
		assert.Equal(t, Fingerprint(plaintext), fingerprint, "the returned fingerprint should match the helper")
	}
}

func TestAddRejectsEmptySecrets(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	_, _, err := v.Add(keyKindLLM, " \n\t ")
	require.ErrorIs(t, err, ErrEmptySecret, "a blank secret must be refused")
}

func TestAddDefaultKind(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add("", testKey)
	require.NoError(t, err, "storing without an explicit kind must succeed")
	assert.Equal(t, keyKindLLM, ref.Kind, "the default kind should be applied")
}

func TestRecordFilesNeverContainPlaintext(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	raw, err := os.ReadFile(filepath.Join(v.Dir(), SecretsDirName, ref.ID+".json"))
	require.NoError(t, err, "the record file must be readable")
	assert.NotContains(t, string(raw), testKey, "the plaintext should never appear on disk")
	assert.NotContains(t, string(raw), strings.TrimPrefix(testKey, "sk-"), "no fragment of the plaintext may appear on disk")

	info, err := os.Stat(filepath.Join(v.Dir(), SecretsDirName, ref.ID+".json"))
	require.NoError(t, err, "the record file must exist")
	assert.Equal(t, FileMode, info.Mode().Perm(), "the record should be owner-only")
}

func TestRevealDetectsAMovedRecord(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	first, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing the first secret must succeed")
	second, _, err := v.Add(keyKindLLM, testKeyAlt)
	require.NoError(t, err, "storing the second secret must succeed")

	source := filepath.Join(v.Dir(), SecretsDirName, first.ID+".json")
	target := filepath.Join(v.Dir(), SecretsDirName, second.ID+".json")
	body, err := os.ReadFile(source)
	require.NoError(t, err, "reading the first record must succeed")
	require.NoError(t, os.WriteFile(target, body, FileMode), "moving the record must succeed")

	// The reference is authenticated data, so a valid ciphertext under a different
	// reference must not open.
	_, err = v.Reveal(second)
	require.ErrorIs(t, err, ErrDecryptFailed, "a relocated record must be detected")
}

func TestRevealDetectsTampering(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	path := filepath.Join(v.Dir(), SecretsDirName, ref.ID+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading the record must succeed")

	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record), "the record must be valid JSON for the test to tamper with it")
	ciphertext, ok := record["ciphertext"].(string)
	require.True(t, ok, "the record must carry a ciphertext field")
	flipped := []byte(ciphertext)
	flipped[0] ^= 0x01
	record["ciphertext"] = string(flipped)
	tampered, err := json.Marshal(record)
	require.NoError(t, err, "re-encoding the tampered record must succeed")
	require.NoError(t, os.WriteFile(path, tampered, FileMode), "writing the tampered record must succeed")

	_, err = v.Reveal(ref)
	require.ErrorIs(t, err, ErrDecryptFailed, "a modified ciphertext must be refused")
}

func TestRevealDetectsAMalformedRecord(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	path := filepath.Join(v.Dir(), SecretsDirName, ref.ID+".json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), FileMode), "writing a malformed record must succeed")

	_, err = v.Reveal(ref)
	require.ErrorIs(t, err, ErrDecryptFailed, "an unreadable record must be refused")
}

func TestRevealMissingSecret(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	_, err := v.Reveal(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "00000000-0000-4000-8000-000000000000"})
	require.ErrorIs(t, err, ErrNotFound, "an unknown reference must report not-found")

	_, err = v.Reveal(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "../escape"})
	require.ErrorIs(t, err, ErrInvalidRef, "a traversal attempt must be refused before any file access")
}

func TestFingerprintIsStableAndDistinct(t *testing.T) {
	t.Parallel()

	first := Fingerprint(testKey)
	assert.Equal(t, first, Fingerprint(testKey), "the same secret should always fingerprint the same way")
	assert.NotEqual(t, Fingerprint(testKey), Fingerprint(testKeyAlt), "different secrets should not share a fingerprint")
	assert.NotContains(t, Fingerprint(testKey), testKey[:6], "the fingerprint should not embed the plaintext")
	firstX := Fingerprint("x")
	assert.Equal(t, firstX, Fingerprint("x"), "repeated calls should agree")
	assert.NotEqual(t, Fingerprint("x"), Fingerprint("xx"), "off-by-one plaintexts should differ")
}

func TestVaultFingerprintMatchesTheHelper(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, fingerprint, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	got, err := v.Fingerprint(ref)
	require.NoError(t, err, "reading a fingerprint must succeed")
	assert.Equal(t, fingerprint, got, "the stored fingerprint should be returned")

	_, err = v.Fingerprint(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "missing"})
	require.ErrorIs(t, err, ErrNotFound, "an unknown reference must report not-found")
}

func TestListReturnsFingerprintsOnly(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	entries, err := v.List()
	require.NoError(t, err, "listing an empty vault must succeed")
	assert.Empty(t, entries, "a fresh vault should list nothing")

	first, firstFingerprint, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing the first secret must succeed")
	_, secondFingerprint, err := v.Add(keyKindLLM, testKeyAlt)
	require.NoError(t, err, "storing the second secret must succeed")

	entries, err = v.List()
	require.NoError(t, err, "listing a populated vault must succeed")
	require.Len(t, entries, 2, "every stored secret must be listed")

	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		require.NoError(t, entry.Ref.Validate(), "a listed reference must be well-formed")
		assert.False(t, entry.CreatedAt.IsZero(), "a listed entry should carry its creation time")
		seen[entry.Ref.String()] = entry.Fingerprint
	}
	require.Contains(t, seen, first.String(), "the first entry must be present")
	assert.Equal(t, firstFingerprint, seen[first.String()], "the listed fingerprint should match the one returned by Add")
	assert.NotEqual(t, firstFingerprint, secondFingerprint, "two different secrets should list two different fingerprints")
}

func TestListIgnoresForeignFiles(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	_, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	stray := filepath.Join(v.Dir(), SecretsDirName, "README.txt")
	require.NoError(t, os.WriteFile(stray, []byte("not a secret"), FileMode), "writing a stray file must succeed")
	require.NoError(t, os.MkdirAll(filepath.Join(v.Dir(), SecretsDirName, "nested"), DirMode), "creating a nested directory must succeed")

	entries, err := v.List()
	require.NoError(t, err, "listing must ignore anything that is not a record")
	assert.Len(t, entries, 1, "only real records should be listed")
}

func TestRemoveDeletesTheSecret(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	require.NoError(t, v.Remove(ref), "removing a stored secret must succeed")
	_, err = v.Reveal(ref)
	require.ErrorIs(t, err, ErrNotFound, "a removed secret must no longer open")

	err = v.Remove(ref)
	require.ErrorIs(t, err, ErrNotFound, "removing twice must report not-found")

	err = v.Remove(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "../escape"})
	require.ErrorIs(t, err, ErrInvalidRef, "a malformed reference must be refused")
}

func TestNilVaultIsSafe(t *testing.T) {
	t.Parallel()

	var v *Vault
	_, _, err := v.Add(keyKindLLM, testKey)
	require.ErrorIs(t, err, ErrVaultNotInitialised, "a nil vault must refuse to store")
	_, err = v.Reveal(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "x"})
	require.ErrorIs(t, err, ErrVaultNotInitialised, "a nil vault must refuse to reveal")
	_, err = v.Fingerprint(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "x"})
	require.ErrorIs(t, err, ErrVaultNotInitialised, "a nil vault must refuse to fingerprint")
	_, err = v.List()
	require.ErrorIs(t, err, ErrVaultNotInitialised, "a nil vault must refuse to list")
	require.ErrorIs(t, v.Remove(Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "x"}), ErrVaultNotInitialised, "a nil vault must refuse to remove")
}

func TestRefStringAndValidate(t *testing.T) {
	t.Parallel()

	ref := Ref{Scheme: RefScheme, Kind: keyKindLLM, ID: "1f0c9e30-0000-4000-8000-000000000000"}
	assert.Equal(t, "secret://llm/1f0c9e30-0000-4000-8000-000000000000", ref.String(), "the stored form should be scheme://kind/id")
	require.NoError(t, ref.Validate(), "a well-formed reference must validate")

	for name, bad := range map[string]Ref{
		"wrong scheme": {Scheme: "vault", Kind: keyKindLLM, ID: "x"},
		"empty scheme": {Kind: keyKindLLM, ID: "x"},
		"empty kind":   {Scheme: RefScheme, ID: "x"},
		"empty id":     {Scheme: RefScheme, Kind: keyKindLLM},
		"slash in id":  {Scheme: RefScheme, Kind: keyKindLLM, ID: "a/b"},
		"blank kind":   {Scheme: RefScheme, Kind: "  ", ID: "x"},
	} {
		require.ErrorIsf(t, bad.Validate(), ErrInvalidRef, "%s must be rejected", name)
	}
	assert.Empty(t, (Ref{}).String(), "a zero reference should render as empty rather than a partial URI")
}

func TestParseRef(t *testing.T) {
	t.Parallel()

	ref, err := ParseRef("  secret://llm/1f0c9e30-0000-4000-8000-000000000000  ")
	require.NoError(t, err, "a stored reference must parse")
	assert.Equal(t, RefScheme, ref.Scheme, "the scheme should be read")
	assert.Equal(t, keyKindLLM, ref.Kind, "the kind should be read")
	assert.Equal(t, "1f0c9e30-0000-4000-8000-000000000000", ref.ID, "the id should be read")
	assert.Equal(t, "secret://llm/1f0c9e30-0000-4000-8000-000000000000", ref.String(), "the round trip should be lossless")

	for _, bad := range []string{
		"",
		"llm/abc",
		"secret://llm",
		"secret://llm/",
		"://llm/abc",
		"secret:///abc",
		"vault://llm/abc",
		"secret://llm/x/y",
	} {
		_, err := ParseRef(bad)
		require.ErrorIsf(t, err, ErrInvalidRef, "%q must be rejected", bad)
	}
}

func TestParseRefRoundTripsEveryStoredSecret(t *testing.T) {
	t.Parallel()

	v := newTestVault(t)
	ref, _, err := v.Add(keyKindLLM, testKey)
	require.NoError(t, err, "storing a secret must succeed")

	parsed, err := ParseRef(ref.String())
	require.NoError(t, err, "a reference produced by Add must parse")
	assert.Equal(t, ref, parsed, "parsing should be the inverse of String")

	entries, err := v.List()
	require.NoError(t, err, "listing must succeed")
	require.Len(t, entries, 1, "the single secret must be listed")
	assert.Equal(t, ref, entries[0].Ref, "the listed reference should be the one Add returned")
}
