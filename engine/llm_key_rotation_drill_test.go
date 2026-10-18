package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M6-07 密钥轮换演练（模型 Key 侧；币安 Key 侧为需真实凭据的操作手册，
// 见 acceptance/M6-07-key-rotation-drill.md）。
//
// 机器可验证的链路（2026-10-15 vault 移除后改写）：操作员拿到新 key →
// 档案追加新修订版（明文 key 入库、指纹服务端派生）→ 旧修订版 supersede →
// 激活指针清空 + readiness INCOMPLETE + epoch++ → 重新探针 → CAS 重新激活
// 回到 READY。旧修订版不可再被激活；配对切换命令留痕。明文 key 按设计存于
// 修订版行（api_key_ref 列承载明文——用户拍板的简化）。
func TestM6KeyRotationDrill(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := context.Background()
	llmKey, jevKey := keyFixture(t, "sk-llm-old-111")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t) // epoch 1, READY

	// 1. The provider issues a new key; the operator stores it verbatim.
	const newKey = "sk-llm-rotated-222"

	// 2. The new revision goes through the production admin path; the
	// fingerprint is derived server-side from the plaintext key.
	newRev, err := AddProviderRevision(ctx, &ProviderRevisionParams{
		ProfileID:              "prof-rev-llm",
		Actor:                  "op-rotator",
		BaseURL:                "https://api.example.com/v1",
		ModelID:                "model-a",
		APIKey:                 newKey,
		TimeoutSeconds:         30,
		ReservationCap24H:      3,
		AllowInsecureLocalhost: false,
	}, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, int64(2), newRev.RevisionNo)
	assert.Equal(t, deriveKeyFingerprint(newKey), newRev.APIKeyFingerprint, "the fingerprint is derived, never typed")

	// 3. The supersede dance: the LLM pointer is cleared, readiness demoted,
	// the epoch advanced -- running strategies on the old endpoint are now
	// structurally excluded.
	pair, err := llmstore.GetActiveModelPair(ctx)
	require.NoError(t, err)
	assert.Nil(t, pair.LLMProfileRevisionID, "the LLM pointer should be cleared by the rotation")
	assert.Equal(t, readinessIncomplete, pair.Readiness)
	assert.Equal(t, int64(2), pair.PairEpoch)
	require.NotNil(t, pair.JevProfileRevisionID, "the untouched side keeps its pointer")

	// 4. The old revision is superseded: it can no longer be activated; its
	// key stays readable on the row for audit replay (append, not destroy).
	oldRevision, err := llmstore.GetProfileRevision(ctx, "rev-llm")
	require.NoError(t, err)
	require.NotNil(t, oldRevision.SupersededAt)
	assert.Equal(t, "sk-llm-old-111", oldRevision.APIKeyRef, "the retired revision keeps its key for audit replay")

	oldLLMID, jevID := "rev-llm", "rev-jev"
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &oldLLMID, JevRevisionID: &jevID,
		ExpectedEpoch: 2, IdempotencyKey: "rot-old-rejected", Actor: "op-rotator",
	}, providerAdminNow)
	require.ErrorIs(t, err, ErrRevisionNotLive, "a superseded revision must never be re-activated")

	// 5. Probing the new revision and re-activating via CAS restores READY at
	// the next epoch; the switch command keeps the audit trail.
	probedAt := providerAdminNow.Add(time.Minute)
	require.NoError(t, llmstore.InsertProbeResult(ctx, &llmstore.ProbeResult{
		ID: newLLMIdentifier(), ProfileRevisionID: newRev.ID, Kind: ProviderKindLLM,
		Layer: "AUTH", Passed: true, ProbedAt: probedAt,
	}))
	require.NoError(t, llmstore.InsertProbeResult(ctx, &llmstore.ProbeResult{
		ID: newLLMIdentifier(), ProfileRevisionID: "rev-jev", Kind: ProviderKindJev,
		Layer: "SYSTEMONE", Passed: true, ProbedAt: probedAt,
	}))
	active, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &newRev.ID, JevRevisionID: &jevID,
		ExpectedEpoch: 2, IdempotencyKey: "rot-new-activate", Actor: "op-rotator",
	}, providerAdminNow.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(3), active.PairEpoch)
	assert.Equal(t, LLMPairReady, active.Readiness, "fresh probes on the rotated revision restore READY")
	require.NotNil(t, active.LLMProfileRevisionID)
	assert.Equal(t, newRev.ID, *active.LLMProfileRevisionID)

	command, err := llmstore.GetPairSwitchCommandByIdempotencyKey(ctx, "rot-new-activate")
	require.NoError(t, err)
	require.NotNil(t, command)
	assert.Equal(t, int64(2), command.FromPairEpoch)
	assert.Equal(t, int64(3), command.ToPairEpoch)
	assert.Equal(t, "op-rotator", command.Operator)
}
