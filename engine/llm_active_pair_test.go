package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// fixtureRevisions creates one live LLM revision and one live Jev revision.
func fixtureRevisions(t *testing.T) (llmRevision, jevRevision *llmstore.ProfileRevision) {
	t.Helper()
	ctx := t.Context()
	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Primary LLM", "operator@test", providerAdminNow)
	require.NoError(t, err)
	llmRevision, err = AddProviderRevision(ctx, validLLMParams(llmProfile.ID), providerAdminNow)
	require.NoError(t, err)
	jevProfile, err := CreateProviderProfile(ctx, ProviderKindJev, "System One", "operator@test", providerAdminNow)
	require.NoError(t, err)
	jevRevision, err = AddProviderRevision(ctx, validJevParams(jevProfile.ID), providerAdminNow)
	require.NoError(t, err)
	return llmRevision, jevRevision
}

func probeFixture(t *testing.T, revisionID string, passed bool, at time.Time) {
	t.Helper()
	require.NoError(t, llmstore.InsertProbeResult(t.Context(), &llmstore.ProbeResult{
		ID:                fmt.Sprintf("probe-%s-%d", revisionID, at.UnixNano()),
		ProfileRevisionID: revisionID,
		Kind:              "llm",
		Layer:             "AUTH",
		Passed:            passed,
		ProbedAt:          at,
	}))
}

func TestActivateModelPairHappyPath(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmRevision, jevRevision := fixtureRevisions(t)
	probeFixture(t, llmRevision.ID, true, providerAdminNow)
	probeFixture(t, jevRevision.ID, true, providerAdminNow.Add(time.Minute))

	pair, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID:  &llmRevision.ID,
		JevRevisionID:  &jevRevision.ID,
		ExpectedEpoch:  0,
		IdempotencyKey: "switch-1",
		Actor:          "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), pair.PairEpoch)
	assert.Equal(t, "READY", pair.Readiness)
	require.NotNil(t, pair.LLMProfileRevisionID)
	assert.Equal(t, llmRevision.ID, *pair.LLMProfileRevisionID)
	require.NotNil(t, pair.JevProfileRevisionID)
	assert.Equal(t, jevRevision.ID, *pair.JevProfileRevisionID)

	command, err := llmstore.GetPairSwitchCommandByIdempotencyKey(ctx, "switch-1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), command.FromPairEpoch)
	assert.Equal(t, int64(1), command.ToPairEpoch)
	assert.Equal(t, "operator@test", command.Operator)
	require.NotNil(t, command.JevRevisionID)
	assert.Equal(t, jevRevision.ID, *command.JevRevisionID)
}

func TestActivateModelPairReadinessStates(t *testing.T) {
	t.Run("one slot empty is INCOMPLETE even with passing probes", func(t *testing.T) {
		setupProviderAdminDB(t)
		llmRevision, _ := fixtureRevisions(t)
		probeFixture(t, llmRevision.ID, true, providerAdminNow)
		pair, err := ActivateModelPair(t.Context(), ActivatePairRequest{
			LLMRevisionID:  &llmRevision.ID,
			ExpectedEpoch:  0,
			IdempotencyKey: "switch-partial",
			Actor:          "operator@test",
		}, providerAdminNow.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, "INCOMPLETE", pair.Readiness)
		assert.Nil(t, pair.JevProfileRevisionID)
	})

	t.Run("a failing newest probe degrades", func(t *testing.T) {
		setupProviderAdminDB(t)
		llmRevision, jevRevision := fixtureRevisions(t)
		probeFixture(t, llmRevision.ID, true, providerAdminNow)
		probeFixture(t, llmRevision.ID, false, providerAdminNow.Add(time.Minute))
		probeFixture(t, jevRevision.ID, true, providerAdminNow)
		pair, err := ActivateModelPair(t.Context(), ActivatePairRequest{
			LLMRevisionID:  &llmRevision.ID,
			JevRevisionID:  &jevRevision.ID,
			ExpectedEpoch:  0,
			IdempotencyKey: "switch-degraded",
			Actor:          "operator@test",
		}, providerAdminNow.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, "DEGRADED", pair.Readiness)
	})

	t.Run("no probe evidence degrades", func(t *testing.T) {
		setupProviderAdminDB(t)
		llmRevision, jevRevision := fixtureRevisions(t)
		pair, err := ActivateModelPair(t.Context(), ActivatePairRequest{
			LLMRevisionID:  &llmRevision.ID,
			JevRevisionID:  &jevRevision.ID,
			ExpectedEpoch:  0,
			IdempotencyKey: "switch-unprobed",
			Actor:          "operator@test",
		}, providerAdminNow.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, "DEGRADED", pair.Readiness, "readiness claims evidence; absence of probes is not READY")
	})
}

func TestActivateModelPairEpochConflict(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmRevision, jevRevision := fixtureRevisions(t)

	_, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-a", Actor: "operator@test",
	}, providerAdminNow)
	require.NoError(t, err)

	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-b", Actor: "operator@test",
	}, providerAdminNow.Add(time.Minute))
	assert.ErrorIs(t, err, ErrPairEpochConflict)

	pair, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 1, IdempotencyKey: "switch-c", Actor: "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), pair.PairEpoch)
}

func TestActivateModelPairIdempotentReplay(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmRevision, jevRevision := fixtureRevisions(t)
	req := ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-replay", Actor: "operator@test",
	}

	first, err := ActivateModelPair(ctx, req, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.PairEpoch)

	replay, err := ActivateModelPair(ctx, req, providerAdminNow.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), replay.PairEpoch, "replay should not bump the epoch")

	// Same key, different payload: rejected.
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-replay", Actor: "operator@test",
	}, providerAdminNow.Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrIdempotencyKeyReused)
}

func TestActivateModelPairRejectsBadInput(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmRevision, jevRevision := fixtureRevisions(t)

	_, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, ExpectedEpoch: 0, Actor: "operator@test",
	}, providerAdminNow)
	assert.ErrorIs(t, err, ErrMissingIdempotencyKey)

	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, ExpectedEpoch: 0, IdempotencyKey: "switch-x",
	}, providerAdminNow)
	assert.ErrorIs(t, err, ErrInvalidActor)

	// A superseded revision cannot be activated.
	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Edited", "operator@test", providerAdminNow)
	require.NoError(t, err)
	oldRevision, err := AddProviderRevision(ctx, validLLMParams(llmProfile.ID), providerAdminNow)
	require.NoError(t, err)
	_, err = AddProviderRevision(ctx, validLLMParams(llmProfile.ID), providerAdminNow.Add(time.Minute))
	require.NoError(t, err)
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &oldRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-stale", Actor: "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrRevisionNotLive)

	// A Jev revision in the LLM slot is a kind mismatch.
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &jevRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-kind", Actor: "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrRevisionKindMismatch)

	// Unknown revision surfaces the repository's not-found.
	missing := "no-such-revision"
	_, err = ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &missing, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-missing", Actor: "operator@test",
	}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, llmstore.ErrNotFound)
}

func TestActivateModelPairDeactivation(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	llmRevision, jevRevision := fixtureRevisions(t)

	_, err := ActivateModelPair(ctx, ActivatePairRequest{
		LLMRevisionID: &llmRevision.ID, JevRevisionID: &jevRevision.ID,
		ExpectedEpoch: 0, IdempotencyKey: "switch-on", Actor: "operator@test",
	}, providerAdminNow)
	require.NoError(t, err)

	pair, err := ActivateModelPair(ctx, ActivatePairRequest{
		ExpectedEpoch: 1, IdempotencyKey: "switch-off", Actor: "operator@test",
	}, providerAdminNow.Add(time.Minute))
	require.NoError(t, err)
	assert.Nil(t, pair.LLMProfileRevisionID)
	assert.Nil(t, pair.JevProfileRevisionID)
	assert.Equal(t, "INCOMPLETE", pair.Readiness)
	assert.Equal(t, int64(2), pair.PairEpoch)
}
