package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// M1-14 测试：providers 命令族的 RPC 接缝——fail-closed、生命周期、CAS 切换、
// 分层探针的落库与「探针失败不是 RPC 错误」语义。

func TestLLMProviderRPCsFailClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var empty *RPCServer
	_, err := empty.LLMListProviderProfiles(ctx, &gctrpc.LLMListProviderProfilesRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMCreateProviderProfile(ctx, &gctrpc.LLMCreateProviderProfileRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMUpdateProviderProfile(ctx, &gctrpc.LLMUpdateProviderProfileRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMDeleteProviderProfile(ctx, &gctrpc.LLMDeleteProviderProfileRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMTestProviderProfile(ctx, &gctrpc.LLMTestProviderProfileRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMGetActiveModelPair(ctx, &gctrpc.LLMGetActiveModelPairRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
	_, err = empty.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{})
	require.ErrorIs(t, err, errLLMProvidersUnavailable)
}

func TestLLMProviderProfileLifecycleThroughRPC(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, _ := keyFixture(t, "sk-llm-1")
	server := &RPCServer{Engine: &Engine{}}
	ctx := t.Context()

	created, err := server.LLMCreateProviderProfile(ctx, &gctrpc.LLMCreateProviderProfileRequest{
		Kind: ProviderKindLLM, DisplayName: "DeepSeek", Actor: "op",
	})
	require.NoError(t, err)
	profileID := created.GetProfile().GetId()
	require.NotEmpty(t, profileID)
	assert.Equal(t, ProviderKindLLM, created.GetProfile().GetKind())
	assert.Zero(t, created.GetProfile().GetRevisionCount(), "a fresh profile has no revisions yet")
	assert.Nil(t, created.GetProfile().GetRevision())

	listed, err := server.LLMListProviderProfiles(ctx, &gctrpc.LLMListProviderProfilesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetProfiles(), 1)

	updated, err := server.LLMUpdateProviderProfile(ctx, &gctrpc.LLMUpdateProviderProfileRequest{
		Id: profileID, Actor: "op",
		BaseUrl: "https://api.example.com/v1", ModelId: "deepseek-chat",
		ApiKey:         llmKey,
		TimeoutSeconds: 30, MaxOutputTokens: 2048, PromptVersion: "analysis-readonly-v1",
		ReservationCap_24H: 3,
	})
	require.NoError(t, err)
	revision := updated.GetRevision()
	assert.Equal(t, int64(1), revision.GetRevisionNo())
	assert.Equal(t, "https://api.example.com/v1", revision.GetBaseUrl())
	assert.Equal(t, int64(2048), revision.GetMaxOutputTokens())
	assert.Equal(t, "analysis-readonly-v1", revision.GetPromptVersion())
	assert.Equal(t, int64(3), revision.GetReservationCap_24H())
	assert.Empty(t, revision.GetSupersededAt(), "the first revision is the live one")

	// The second revision supersedes the first; the profile keeps only the
	// newest as its live revision while history stays queryable.
	updated2, err := server.LLMUpdateProviderProfile(ctx, &gctrpc.LLMUpdateProviderProfileRequest{
		Id: profileID, Actor: "op",
		BaseUrl: "https://api.example.com/v1", ModelId: "deepseek-reasoner",
		ApiKey:         llmKey,
		TimeoutSeconds: 30,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated2.GetRevision().GetRevisionNo())

	listed, err = server.LLMListProviderProfiles(ctx, &gctrpc.LLMListProviderProfilesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetProfiles(), 1)
	assert.Equal(t, int64(2), listed.GetProfiles()[0].GetRevisionCount())
	assert.Equal(t, "deepseek-reasoner", listed.GetProfiles()[0].GetRevision().GetModelId())

	revisions, err := llmstore.ListProfileRevisions(ctx, profileID)
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	assert.Nil(t, revisions[0].SupersededAt, "newest revision first, still live")
	assert.NotNil(t, revisions[1].SupersededAt, "oldest revision carries its supersede time")

	// The wire shape must never leak the key: only the fingerprint appears.
	raw, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sk-llm-1", "the key itself never reaches the wire")

	// Deleting is archiving: hidden from the default list, visible on request.
	_, err = server.LLMDeleteProviderProfile(ctx, &gctrpc.LLMDeleteProviderProfileRequest{Id: profileID, Actor: "op"})
	require.NoError(t, err)
	listed, err = server.LLMListProviderProfiles(ctx, &gctrpc.LLMListProviderProfilesRequest{})
	require.NoError(t, err)
	assert.Empty(t, listed.GetProfiles())
	listed, err = server.LLMListProviderProfiles(ctx, &gctrpc.LLMListProviderProfilesRequest{IncludeArchived: true})
	require.NoError(t, err)
	require.Len(t, listed.GetProfiles(), 1)
	assert.NotEmpty(t, listed.GetProfiles()[0].GetArchivedAt())
}

func TestLLMProviderRPCsValidateInput(t *testing.T) {
	setupProviderAdminDB(t)
	server := &RPCServer{Engine: &Engine{}}
	ctx := t.Context()

	_, err := server.LLMCreateProviderProfile(ctx, &gctrpc.LLMCreateProviderProfileRequest{Kind: "BOGUS", DisplayName: "x", Actor: "op"})
	require.ErrorIs(t, err, ErrUnknownProviderKind)

	created, err := server.LLMCreateProviderProfile(ctx, &gctrpc.LLMCreateProviderProfileRequest{Kind: ProviderKindLLM, DisplayName: "P", Actor: "op"})
	require.NoError(t, err)

	_, err = server.LLMUpdateProviderProfile(ctx, &gctrpc.LLMUpdateProviderProfileRequest{
		Id: created.GetProfile().GetId(), Actor: "op",
		BaseUrl: "https://api.example.com/v1", ModelId: "m",
		ApiKey:         "",
		TimeoutSeconds: 30,
	})
	require.ErrorIs(t, err, ErrInvalidSecretRef, "an empty API key must refuse")

	_, err = server.LLMDeleteProviderProfile(ctx, &gctrpc.LLMDeleteProviderProfileRequest{Id: "missing", Actor: "op"})
	require.Error(t, err, "archiving an unknown profile must refuse")

	_, err = server.LLMDeleteProviderProfile(ctx, &gctrpc.LLMDeleteProviderProfileRequest{Id: created.GetProfile().GetId(), Actor: ""})
	require.ErrorIs(t, err, ErrInvalidActor)
}

func TestLLMGetActiveModelPairStates(t *testing.T) {
	setupProviderAdminDB(t)
	server := &RPCServer{Engine: &Engine{}}

	pair, err := server.LLMGetActiveModelPair(t.Context(), &gctrpc.LLMGetActiveModelPairRequest{})
	require.NoError(t, err)
	assert.False(t, pair.GetFound(), "never-activated is a state, not an error")

	seedPair(t)
	pair, err = server.LLMGetActiveModelPair(t.Context(), &gctrpc.LLMGetActiveModelPairRequest{})
	require.NoError(t, err)
	assert.True(t, pair.GetFound())
	assert.Equal(t, "rev-llm", pair.GetLlmProfileRevisionId())
	assert.Equal(t, "rev-jev", pair.GetJevProfileRevisionId())
	assert.Equal(t, int64(1), pair.GetPairEpoch())
	assert.Equal(t, LLMPairReady, pair.GetReadiness())
}

func TestLLMSwitchActiveModelPairCASAndReauth(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedRevision(t, "rev-llm2", ProviderKindLLM, "https://api.example.com/v1", "model-b", llmKey, false)
	seedPair(t)

	cfg := &config.Config{}
	cfg.RemoteControl.Password = "s3cret"
	server := &RPCServer{Engine: &Engine{Config: cfg}}
	ctx := t.Context()

	_, err := server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: "rev-llm2", JevProfileRevisionId: "rev-jev",
		ExpectedPairEpoch: 1, OperatorPassword: "wrong", IdempotencyKey: "k1", Actor: "op",
	})
	require.Error(t, err, "a wrong password must refuse the switch")

	_, err = server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: "rev-llm2", JevProfileRevisionId: "rev-jev",
		ExpectedPairEpoch: 0, OperatorPassword: "s3cret", IdempotencyKey: "k1", Actor: "op",
	})
	require.ErrorIs(t, err, ErrPairEpochConflict, "a stale epoch must refuse the switch")

	switched, err := server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: "rev-llm2", JevProfileRevisionId: "rev-jev",
		ExpectedPairEpoch: 1, OperatorPassword: "s3cret", IdempotencyKey: "k1", Actor: "op",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), switched.GetPairEpoch())
	assert.Equal(t, readinessDegraded, switched.GetReadiness(), "no passing probes means DEGRADED, not READY")

	// An empty revision id clears that slot; the pair can no longer be READY.
	switched2, err := server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: "rev-llm2", JevProfileRevisionId: "",
		ExpectedPairEpoch: 2, OperatorPassword: "s3cret", IdempotencyKey: "k2", Actor: "op",
	})
	require.NoError(t, err)
	assert.Equal(t, readinessIncomplete, switched2.GetReadiness(), "a cleared slot is INCOMPLETE by construction")
}

// TestLLMTestProviderProfileRunsLayeredProbes proves the whole probe path: a
// revision row -> stored plaintext key -> layered capability probes -> per-layer
// rows in the append-only probe table -> verdicts on the wire.
func TestLLMTestProviderProfileRunsLayeredProbes(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, _ := keyFixture(t, "sk-llm-1")

	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		var body map[string]any
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		_ = json.Unmarshal(buf, &body)
		w.Header().Set("Content-Type", "application/json")
		if stream, _ := body["stream"].(bool); stream {
			_, _ = w.Write([]byte("data: {\"model\":\"model-a\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		if _, hasTools := body["tools"]; hasTools {
			if msgs, _ := body["messages"].([]any); len(msgs) > 2 {
				_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"getProbeFacts","arguments":"{\"key\":\"probe\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		if _, hasFormat := body["response_format"]; hasFormat {
			_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"not a candidate"},"finish_reason":"stop"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)

	profileID := "prof-test"
	require.NoError(t, llmstore.InsertProfile(t.Context(), &llmstore.Profile{
		ID: profileID, Kind: ProviderKindLLM, DisplayName: "mock", Enabled: true,
		CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	require.NoError(t, llmstore.InsertProfileRevision(t.Context(), &llmstore.ProfileRevision{
		ID: "rev-mock", ProfileID: profileID, RevisionNo: 1, Kind: ProviderKindLLM,
		BaseURL: srv.URL + "/v1", ModelID: "model-a", APIKeyRef: llmKey, APIKeyFingerprint: "fp",
		TimeoutSeconds: 30, AllowInsecureLocalhost: true,
		CreatedAt: providerAdminNow, CreatedBy: "op",
	}))

	cfg := &config.Config{}
	server := &RPCServer{Engine: &Engine{Config: cfg}}

	out, err := server.LLMTestProviderProfile(t.Context(), &gctrpc.LLMTestProviderProfileRequest{Id: profileID})
	require.NoError(t, err)
	require.Len(t, out.GetResults(), 4, "all four layers report independently")
	assert.Equal(t, "rev-mock", out.GetRevisionId())
	assert.Equal(t, "Bearer sk-llm-1", seenAuth, "the stored key reaches the probe on the wire")

	byLayer := map[string]*gctrpc.LLMProbeLayerResult{}
	for _, result := range out.GetResults() {
		byLayer[result.GetLayer()] = result
	}
	assert.True(t, byLayer["AUTH"].GetPassed())
	assert.True(t, byLayer["TOOL_CALL"].GetPassed())
	assert.False(t, byLayer["SCHEMA"].GetPassed(), "a non-candidate reply fails SCHEMA")
	assert.NotEmpty(t, byLayer["SCHEMA"].GetErrorCode())
	assert.True(t, byLayer["STREAM"].GetPassed())

	rows, err := llmstore.ListProbeResults(t.Context(), "rev-mock")
	require.NoError(t, err)
	require.Len(t, rows, 4, "every verdict is persisted, including failures")
	assert.Equal(t, ProviderKindLLM, rows[0].Kind)
}

func TestLLMTestProviderProfileInfrastructureFaultsFail(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)

	cfg := &config.Config{}
	server := &RPCServer{Engine: &Engine{Config: cfg}}
	ctx := t.Context()

	_, err := server.LLMTestProviderProfile(ctx, &gctrpc.LLMTestProviderProfileRequest{Id: "missing"})
	require.ErrorIs(t, err, llmstore.ErrNotFound)

	_, err = server.LLMTestProviderProfile(ctx, &gctrpc.LLMTestProviderProfileRequest{Id: "prof-rev-llm", RevisionId: "rev-jev"})
	require.Error(t, err, "a revision that belongs to another profile must refuse")

	require.NoError(t, llmstore.InsertProfile(ctx, &llmstore.Profile{
		ID: "prof-empty", Kind: ProviderKindLLM, DisplayName: "empty", Enabled: true,
		CreatedAt: providerAdminNow, CreatedBy: "op",
	}))
	_, err = server.LLMTestProviderProfile(ctx, &gctrpc.LLMTestProviderProfileRequest{Id: "prof-empty"})
	require.Error(t, err, "a profile without a live revision has nothing to probe")
}

func TestLLMProviderProfileConverter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	revision := providerRevisionToProto(&llmstore.ProfileRevision{
		ID: "r1", ProfileID: "p1", RevisionNo: 2, Kind: ProviderKindLLM,
		BaseURL: "https://api.example.com/v1", ModelID: "m",
		APIKeyRef: "sk-test-1234", APIKeyFingerprint: "fp",
		TimeoutSeconds: 30, DailyBudgetUSD: new("0"),
		CreatedAt: now, CreatedBy: "op", SupersededAt: &future,
	})
	assert.Equal(t, int64(2), revision.GetRevisionNo())
	assert.Equal(t, "0", revision.GetDailyBudgetUsd(), "an explicit zero budget survives the wire")
	assert.Equal(t, future.UTC().Format(time.RFC3339), revision.GetSupersededAt())
	assert.Zero(t, revision.GetMaxOutputTokens(), "unset numerics stay zero/empty on the wire")
}

func TestLLMGetRiskSwitchesCatalogue(t *testing.T) {
	t.Parallel()

	var empty *RPCServer
	_, err := empty.LLMGetRiskSwitches(context.Background(), &gctrpc.LLMGetRiskSwitchesRequest{})
	require.ErrorIs(t, err, errLLMRiskSwitchesUnavailable)

	server := &RPCServer{Engine: &Engine{llmRiskSwitches: &RiskSwitchService{}}}
	out, err := server.LLMGetRiskSwitches(context.Background(), &gctrpc.LLMGetRiskSwitchesRequest{})
	require.NoError(t, err)
	require.Len(t, out.GetSwitches(), 18, "the §5.6 catalogue is the full 18 rows")

	byName := map[string]*gctrpc.LLMRiskSwitch{}
	for _, row := range out.GetSwitches() {
		byName[row.GetName()] = row
	}
	for _, name := range []string{"haltOnDailyLoss", "blockOnUnknownOrders", "blockOnClockDrift"} {
		require.NotNil(t, byName[name], name)
		assert.Truef(t, byName[name].GetLocked(), "%s is a red-line lock", name)
		assert.Truef(t, byName[name].GetEnabled(), "%s defaults on", name)
	}
	assert.False(t, byName["blockOnExtremeFunding"].GetLocked())
	assert.False(t, byName["blockOnExtremeFunding"].GetEnabled(), "extreme funding defaults off")
	assert.Equal(t, RiskFundingExtreme, byName["blockOnExtremeFunding"].GetReasonCode())
}

func TestLLMGetStatusStates(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)

	var empty *RPCServer
	_, err := empty.LLMGetStatus(context.Background(), &gctrpc.LLMGetStatusRequest{})
	require.ErrorIs(t, err, errLLMStatusUnavailable)

	cfg := &config.Config{}
	cfg.LLM.RunMode = LLMRunModeShadow
	cfg.LLM.Budget.LLMReservationCap24h = 3
	server := &RPCServer{Engine: &Engine{Config: cfg}}
	ctx := t.Context()

	// Never-activated pair: INCOMPLETE is the state, not an error.
	status, err := server.LLMGetStatus(ctx, &gctrpc.LLMGetStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, LLMRunModeShadow, status.GetRunMode())
	assert.False(t, status.GetLiveUnlocked())
	assert.Equal(t, readinessIncomplete, status.GetPairReadiness())
	assert.False(t, status.GetHalted())
	assert.Equal(t, "NONE", status.GetHaltLevel())
	assert.False(t, status.GetAccountReconciled())
	assert.Zero(t, status.GetReservationsRemaining_24H(), "no pair means no reservation budget to report")

	seedPair(t)
	require.NoError(t, llmstore.InsertProbeResult(ctx, &llmstore.ProbeResult{
		ID: newLLMIdentifier(), ProfileRevisionID: "rev-llm", Kind: ProviderKindLLM,
		Layer: "AUTH", Passed: true, ProbedAt: time.Now().Add(-90 * time.Second),
	}))
	status, err = server.LLMGetStatus(ctx, &gctrpc.LLMGetStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, LLMPairReady, status.GetPairReadiness())
	assert.Equal(t, int64(1), status.GetPairEpoch())
	assert.Equal(t, "rev-llm", status.GetLlmProfileName(), "profile display name comes from the revision's profile")
	assert.Equal(t, "model-a", status.GetLlmModel())
	assert.Equal(t, "jev-1.0.0", status.GetJevModel())
	assert.GreaterOrEqual(t, status.GetLlmProbeAgeSeconds(), int64(80), "the newest probe's age is reported")
	assert.Equal(t, int64(3), status.GetReservationsRemaining_24H(), "cap minus an empty window is the full cap")
	assert.Equal(t, int64(12), status.GetReviewExpireHours(), "an unset window falls back to the sweeper's own 12h default")

	cfg.LLM.Halt.ReviewExpireHours = 2
	status, err = server.LLMGetStatus(ctx, &gctrpc.LLMGetStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), status.GetReviewExpireHours(), "a tightened window is what the UI derives the expired tag from")

	halts := []llmstore.Halt{{Level: HaltLevelSoft}, {Level: HaltLevelFull}, {Level: HaltLevelMarket}}
	assert.Equal(t, HaltLevelFull, highestHaltLevel(halts), "the harshest level wins")
	assert.Equal(t, "NONE", highestHaltLevel(nil))
}

// TestLLMSwitchActiveModelPairReassemblesTheRuntime proves the activation hook:
// a successful switch rebuilds the chat corridor and the decision runner onto
// the new pair (startup assembly is one-shot; first activations and later
// switches alike must not require a restart).
func TestLLMSwitchActiveModelPairReassemblesTheRuntime(t *testing.T) {
	setupProviderAdminDB(t)
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t)

	bot := CreateTestBot(t)
	bot.Settings.DataDir = t.TempDir()
	bot.llmManager = &LLMManager{}
	bot.Config.LLM = config.LLMConfig{Exchange: testExchange, Asset: "spot", AccountID: "acct-1"}
	bot.Config.RemoteControl.Password = "s3cret"
	server := &RPCServer{Engine: bot}
	ctx := t.Context()

	require.Nil(t, bot.currentChatService(), "the corridor starts unassembled")

	llmRevision, jevRevision := "rev-llm", "rev-jev"
	_, err := server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: llmRevision, JevProfileRevisionId: jevRevision,
		ExpectedPairEpoch: 1, OperatorPassword: "s3cret", IdempotencyKey: "k-reassemble-1", Actor: "op",
	})
	require.NoError(t, err)
	firstChat := bot.currentChatService()
	require.NotNil(t, firstChat, "the activation must assemble the chat corridor")
	require.NotNil(t, bot.currentDecisionRunner(), "the activation must assemble the decision runner")
	require.NotNil(t, bot.currentBatchRunner(), "the batch workbench must ride the assembled runner")

	// A second activation replaces the surfaces with fresh clients: the old
	// corridor belonged to the previous epoch of the pair.
	_, err = server.LLMSwitchActiveModelPair(ctx, &gctrpc.LLMSwitchActiveModelPairRequest{
		LlmProfileRevisionId: llmRevision, JevProfileRevisionId: jevRevision,
		ExpectedPairEpoch: 2, OperatorPassword: "s3cret", IdempotencyKey: "k-reassemble-2", Actor: "op",
	})
	require.NoError(t, err)
	assert.NotSame(t, firstChat, bot.currentChatService(), "a re-activation should rebind the corridor")
}
