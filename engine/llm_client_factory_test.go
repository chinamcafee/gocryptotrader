package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/llm/secrets"
)

// 生产装配 · client factory 测试。

// vaultFixture initialises a vault in a temp dir and stores one secret per side.
func vaultFixture(t *testing.T, llmSecret string) (dir, llmRef, jevRef string) {
	t.Helper()
	dir = t.TempDir()
	vault, err := secrets.Init(dir)
	require.NoError(t, err)
	llm, _, err := vault.Add("llm", llmSecret)
	require.NoError(t, err)
	jevRefObj, _, err := vault.Add("jev", "jev-token-1")
	require.NoError(t, err)
	return dir, llm.String(), jevRefObj.String()
}

func seedRevision(t *testing.T, id, kind, baseURL, model, keyRef string, allowLoopback bool) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, llmstore.InsertProfile(ctx, &llmstore.Profile{
		ID: "prof-" + id, Kind: kind, DisplayName: id, Enabled: true,
		CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	timeout := int64(30)
	require.NoError(t, llmstore.InsertProfileRevision(ctx, &llmstore.ProfileRevision{
		ID: id, ProfileID: "prof-" + id, RevisionNo: 1, Kind: kind,
		BaseURL: baseURL, ModelID: model, APIKeyRef: keyRef, APIKeyFingerprint: "fp",
		TimeoutSeconds: timeout, AllowInsecureLocalhost: allowLoopback,
		CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
}

func seedPair(t *testing.T) {
	t.Helper()
	llmRevision := "rev-llm"
	jevRevision := "rev-jev"
	require.NoError(t, llmstore.SetActiveModelPair(t.Context(), &llmstore.ActiveModelPair{
		LLMProfileRevisionID: &llmRevision, JevProfileRevisionID: &jevRevision,
		PairEpoch: 1, Readiness: LLMPairReady, UpdatedAt: providerAdminNow, UpdatedBy: "op",
	}))
}

func TestBuildActiveClientsMapsThePair(t *testing.T) {
	setupProviderAdminDB(t)
	dir, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	clients, err := BuildActiveClients(t.Context(), dir)
	require.NoError(t, err)
	assert.Equal(t, "model-a", clients.LLM.Model())
	assert.Equal(t, "jev-1.0.0", clients.Jev.Model())
	assert.Equal(t, "rev-llm", clients.LLMRevision.ID)
	assert.Equal(t, "rev-jev", clients.JevRevision.ID)
}

// TestBuildActiveClientsRevealsPerRequest proves the whole chain: revision row
// -> vault ref -> per-call reveal -> Authorization header on the wire.
func TestBuildActiveClientsRevealsPerRequest(t *testing.T) {
	setupProviderAdminDB(t)
	dir, llmRef, jevRef := vaultFixture(t, "sk-live-1")

	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	seedRevision(t, "rev-llm", ProviderKindLLM, srv.URL+"/v1", "model-a", llmRef, true)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	clients, err := BuildActiveClients(t.Context(), dir)
	require.NoError(t, err)
	reply, err := clients.LLM.ChatCompletion(t.Context(), &openai.ChatRequest{
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Len(t, reply.Choices, 1)
	assert.Equal(t, "Bearer sk-live-1", seenAuth, "the vault secret reaches the wire as a bearer token")
}

func TestBuildActiveClientsRequiresAPair(t *testing.T) {
	setupProviderAdminDB(t)
	dir := t.TempDir()
	_, err := BuildActiveClients(t.Context(), dir)
	require.Error(t, err, "no pair must fail the assembly, not the first call")
}

func TestBuildActiveClientsRequiresAnInitialisedVault(t *testing.T) {
	setupProviderAdminDB(t)
	_, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t)

	uninitialised := t.TempDir()
	_, err := BuildActiveClients(t.Context(), uninitialised)
	require.ErrorIs(t, err, secrets.ErrVaultNotInitialised)
}

func TestBuildActiveClientsRefusesAJevAlias(t *testing.T) {
	setupProviderAdminDB(t)
	dir, llmRef, jevRef := vaultFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "systemone-latest", jevRef, false)
	seedPair(t)

	_, err := BuildActiveClients(t.Context(), dir)
	require.ErrorIs(t, err, jev.ErrModelInvalid, "a Jev alias must be refused at assembly, not at replay time")
}

func TestBuildActiveClientsRefusesMissingVaultDir(t *testing.T) {
	_, err := BuildActiveClients(context.Background(), "   ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "vault directory is required")
}
