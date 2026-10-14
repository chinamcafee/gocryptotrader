package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/secrets"
)

// M6-07 密钥轮换演练（模型 Key 侧；币安 Key 侧为需真实凭据的操作手册，
// 见 acceptance/M6-07-key-rotation-drill.md）。
//
// 机器可验证的链路：新密钥在 vault 追加（**轮换是追加不是销毁**——旧密钥
// 仍可 Reveal，审计历史要能回读）→ 档案追加新修订版（只携带新引用+新指纹）
// → 旧修订版 supersede → 激活指针清空 + readiness INCOMPLETE + epoch++ →
// 重新探针 → CAS 重新激活回到 READY。全程明文不进任何行、旧修订版不可再
// 被激活、配对切换命令留痕。
func TestM6KeyRotationDrill(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := context.Background()
	dir, oldLLMRef, jevRef := vaultFixture(t, "sk-llm-old-111")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", oldLLMRef, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevRef, false)
	seedPair(t) // epoch 1, READY

	// 1. The provider issues a new key; it is stored as an ADD (re-opening the
	// vault is idempotent -- the wrapping pair is not re-minted).
	vault, err := secrets.Init(dir)
	require.NoError(t, err)
	newRef, newFingerprint, err := vault.Add("llm", "sk-llm-rotated-222")
	require.NoError(t, err)
	require.NotEqual(t, oldLLMRef, newRef.String(), "a rotation must mint a fresh secret reference")

	// 2. The new revision goes through the production admin path and carries
	// only the new reference and fingerprint.
	newRev, err := AddProviderRevision(ctx, &ProviderRevisionParams{
		ProfileID:              "prof-rev-llm",
		Actor:                  "op-rotator",
		BaseURL:                "https://api.example.com/v1",
		ModelID:                "model-a",
		APIKeyRef:              newRef.String(),
		APIKeyFingerprint:      newFingerprint,
		TimeoutSeconds:         30,
		ReservationCap24H:      3,
		AllowInsecureLocalhost: false,
	}, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, int64(2), newRev.RevisionNo)

	// 3. The supersede dance: the LLM pointer is cleared, readiness demoted,
	// the epoch advanced -- running strategies on the old endpoint are now
	// structurally excluded.
	pair, err := llmstore.GetActiveModelPair(ctx)
	require.NoError(t, err)
	assert.Nil(t, pair.LLMProfileRevisionID, "the LLM pointer should be cleared by the rotation")
	assert.Equal(t, readinessIncomplete, pair.Readiness)
	assert.Equal(t, int64(2), pair.PairEpoch)
	require.NotNil(t, pair.JevProfileRevisionID, "the untouched side keeps its pointer")

	// 4. The old revision is superseded: it can no longer be activated; the
	// old secret still decrypts (rotation is append, not destroy).
	oldRevision, err := llmstore.GetProfileRevision(ctx, "rev-llm")
	require.NoError(t, err)
	require.NotNil(t, oldRevision.SupersededAt)

	oldLLMID, jevID := "rev-llm", "rev-jev"
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &oldLLMID, JevRevisionID: &jevID,
		ExpectedEpoch: 2, IdempotencyKey: "rot-old-rejected", Actor: "op-rotator",
	}, providerAdminNow)
	require.ErrorIs(t, err, ErrRevisionNotLive, "a superseded revision must never be re-activated")

	oldRef, err := secrets.ParseRef(oldLLMRef)
	require.NoError(t, err)
	recoveredOld, err := vault.Reveal(oldRef)
	require.NoError(t, err)
	assert.Equal(t, "sk-llm-old-111", recoveredOld, "the retired key stays readable for audit replay")
	recoveredNew, err := vault.Reveal(newRef)
	require.NoError(t, err)
	assert.Equal(t, "sk-llm-rotated-222", recoveredNew)

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

	// 6. The machine proof of the secrecy claim: no plaintext key value
	// appears in any persisted LLM row rendered this drill touched.
	rendered := fmt.Sprintf("%+v %+v %+v", newRev, active, command)
	for _, plaintext := range []string{"sk-llm-old-111", "sk-llm-rotated-222"} {
		assert.NotContainsf(t, rendered, plaintext, "no row may carry %q", plaintext)
	}
	assert.Contains(t, newRev.APIKeyFingerprint, newFingerprint, "the revision carries the fingerprint, not the key")
}
