package llm

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/database"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
)

//nolint:forbidigo // TestMain reports setup and teardown failures before or after a *testing.T exists
func TestMain(m *testing.M) {
	var err error
	testhelpers.PostgresTestDatabase = testhelpers.GetConnectionDetails()
	testhelpers.TempDir, err = os.MkdirTemp("", "gct-llm-repo-temp")
	if err != nil {
		fmt.Printf("failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err = os.RemoveAll(testhelpers.TempDir); err != nil {
		fmt.Printf("failed to remove temp dir: %v\n", err)
	}
	os.Exit(code)
}

// setupSQLite connects a fresh migrated sqlite database for one test.
func setupSQLite(t *testing.T) {
	t.Helper()
	cfg := &database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: fmt.Sprintf("llm-repo-%d.db", time.Now().UnixNano()),
	}
	conn, err := testhelpers.ConnectToDatabase(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testhelpers.CloseDatabase(conn))
	})
}

func TestProfilesAndRevisions(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// No rows yet.
	_, err := GetProfile(ctx, "profile-1")
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, InsertProfile(ctx, &Profile{
		ID:          "profile-1",
		Kind:        "llm",
		DisplayName: "Primary LLM",
		CreatedAt:   now,
		CreatedBy:   "operator@test",
	}))

	p, err := GetProfile(ctx, "profile-1")
	require.NoError(t, err)
	assert.Equal(t, "llm", p.Kind)
	assert.False(t, p.Enabled)
	assert.True(t, now.Equal(p.CreatedAt))
	assert.Nil(t, p.ArchivedAt)

	require.NoError(t, SetProfileEnabled(ctx, "profile-1", true))
	p, err = GetProfile(ctx, "profile-1")
	require.NoError(t, err)
	assert.True(t, p.Enabled)

	// Revisions: latest insert supersedes the previous live revision.
	timeout := int64(30)
	budget := "5.00"
	require.NoError(t, InsertProfileRevision(ctx, &ProfileRevision{
		ID:                "rev-1",
		ProfileID:         "profile-1",
		RevisionNo:        1,
		Kind:              "llm",
		BaseURL:           "https://api.example.com/v1",
		ModelID:           "model-a",
		APIKeyRef:         "secret://llm/00000000-0000-0000-0000-000000000001",
		APIKeyFingerprint: "abcd1234",
		TimeoutSeconds:    timeout,
		DailyBudgetUSD:    &budget,
		ReservationCap24H: 3,
		CreatedAt:         now,
		CreatedBy:         "operator@test",
	}))
	live, err := GetLiveProfileRevision(ctx, "profile-1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), live.RevisionNo)
	assert.Nil(t, live.SupersededAt)
	require.NotNil(t, live.DailyBudgetUSD)
	assert.Equal(t, "5.00", *live.DailyBudgetUSD)
	assert.Equal(t, int64(3), live.ReservationCap24H)
	assert.Nil(t, live.MaxOutputTokens)

	require.NoError(t, InsertProfileRevision(ctx, &ProfileRevision{
		ID:                "rev-2",
		ProfileID:         "profile-1",
		RevisionNo:        2,
		Kind:              "llm",
		BaseURL:           "https://api.example.com/v1",
		ModelID:           "model-b",
		APIKeyRef:         "secret://llm/00000000-0000-0000-0000-000000000002",
		APIKeyFingerprint: "efgh5678",
		TimeoutSeconds:    60,
		ReservationCap24H: 5,
		CreatedAt:         now.Add(time.Hour),
		CreatedBy:         "operator@test",
	}))
	live, err = GetLiveProfileRevision(ctx, "profile-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), live.RevisionNo)
	assert.Nil(t, live.SupersededAt)

	old, err := GetProfileRevision(ctx, "rev-1")
	require.NoError(t, err)
	require.NotNil(t, old.SupersededAt)
	assert.True(t, now.Add(time.Hour).Equal(*old.SupersededAt))

	revisions, err := ListProfileRevisions(ctx, "profile-1")
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	assert.Equal(t, int64(2), revisions[0].RevisionNo)
	assert.Equal(t, int64(1), revisions[1].RevisionNo)

	// Archive hides the profile from the default listing.
	require.NoError(t, ArchiveProfile(ctx, "profile-1", now.Add(2*time.Hour)))
	list, err := ListProfiles(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, list)
	list, err = ListProfiles(ctx, true)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NotNil(t, list[0].ArchivedAt)
	assert.True(t, now.Add(2*time.Hour).Equal(*list[0].ArchivedAt))
}

func TestActiveModelPair(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	_, err := GetActiveModelPair(ctx)
	assert.ErrorIs(t, err, ErrNotFound)

	llmRev := "rev-llm-1"
	jevRev := "rev-jev-1"
	require.NoError(t, SetActiveModelPair(ctx, &ActiveModelPair{
		LLMProfileRevisionID: &llmRev,
		JevProfileRevisionID: &jevRev,
		PairEpoch:            0,
		Readiness:            "READY",
		UpdatedAt:            now,
		UpdatedBy:            "operator@test",
	}))
	pair, err := GetActiveModelPair(ctx)
	require.NoError(t, err)
	require.NotNil(t, pair.LLMProfileRevisionID)
	assert.Equal(t, llmRev, *pair.LLMProfileRevisionID)
	assert.Equal(t, int64(0), pair.PairEpoch)

	// Second write updates the same single row and bumps the epoch.
	require.NoError(t, SetActiveModelPair(ctx, &ActiveModelPair{
		LLMProfileRevisionID: &llmRev,
		JevProfileRevisionID: &jevRev,
		PairEpoch:            1,
		Readiness:            "READY",
		UpdatedAt:            now.Add(time.Minute),
		UpdatedBy:            "operator@test",
	}))
	pair, err = GetActiveModelPair(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), pair.PairEpoch)
	assert.True(t, now.Add(time.Minute).Equal(pair.UpdatedAt))

	rows, err := database.DB.SQL.QueryContext(t.Context(), "SELECT COUNT(*) FROM llm_active_model_pair")
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var count int
	require.NoError(t, rows.Scan(&count))
	require.NoError(t, rows.Err())
	assert.Equal(t, 1, count)
}

func TestProbeResultsAndPairSwitchCommands(t *testing.T) {
	setupSQLite(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	code := "PROBE_HTTP_401"
	latency := int64(812)

	require.NoError(t, InsertProbeResult(ctx, &ProbeResult{
		ID:                "probe-1",
		ProfileRevisionID: "rev-1",
		Kind:              "llm",
		Layer:             "AUTH",
		Passed:            false,
		ErrorCode:         &code,
		LatencyMS:         &latency,
		ProbedAt:          now,
	}))
	require.NoError(t, InsertProbeResult(ctx, &ProbeResult{
		ID:                "probe-2",
		ProfileRevisionID: "rev-1",
		Kind:              "llm",
		Layer:             "TOOL_CALL",
		Passed:            true,
		ProbedAt:          now.Add(time.Minute),
	}))

	results, err := ListProbeResults(ctx, "rev-1")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "probe-2", results[0].ID, "newest first")
	assert.True(t, results[0].Passed)
	assert.Nil(t, results[0].ErrorCode)
	assert.False(t, results[1].Passed)
	require.NotNil(t, results[1].ErrorCode)
	assert.Equal(t, code, *results[1].ErrorCode)
	require.NotNil(t, results[1].LatencyMS)
	assert.Equal(t, latency, *results[1].LatencyMS)

	require.NoError(t, InsertPairSwitchCommand(ctx, &PairSwitchCommand{
		ID:                 "switch-1",
		IdempotencyKey:     "idem-1",
		FromPairEpoch:      0,
		ToPairEpoch:        1,
		LLMRevisionID:      &llmRevStr,
		Operator:           "operator@test",
		AffectedStrategies: 2,
		RevokedApprovals:   1,
		CreatedAt:          now,
	}))
	require.NoError(t, InsertPairSwitchCommand(ctx, &PairSwitchCommand{
		ID:                 "switch-2",
		IdempotencyKey:     "idem-2",
		FromPairEpoch:      1,
		ToPairEpoch:        2,
		Operator:           "operator@test",
		AffectedStrategies: 3,
		CreatedAt:          now.Add(time.Minute),
	}))

	commands, err := ListPairSwitchCommands(ctx, 1)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, "switch-2", commands[0].ID)
	assert.Equal(t, int64(3), commands[0].AffectedStrategies)

	commands, err = ListPairSwitchCommands(ctx, 0)
	require.NoError(t, err)
	require.Len(t, commands, 2)
	assert.Equal(t, "switch-1", commands[1].ID)
	require.NotNil(t, commands[1].LLMRevisionID)
	assert.Equal(t, llmRevStr, *commands[1].LLMRevisionID)
}

var llmRevStr = "rev-llm-1"

func TestDatabaseUnavailable(t *testing.T) {
	setupSQLite(t)
	sqlDB := database.DB.SQL
	database.DB.SQL = nil
	defer func() { database.DB.SQL = sqlDB }()

	_, err := GetProfile(t.Context(), "profile-1")
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
	assert.ErrorIs(t, InsertProfile(t.Context(), &Profile{}), ErrDatabaseUnavailable)
	_, err = ListProfiles(t.Context(), false)
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
}
