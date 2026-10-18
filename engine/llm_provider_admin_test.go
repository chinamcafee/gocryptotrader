package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

var providerAdminNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// setupProviderAdminDB connects a fresh migrated sqlite database with its own
// file so each test starts from an empty schema. The shared temp directory is
// created once per process: t.TempDir() would be removed after the first test
// and take the still-referenced sqlite path with it.
func setupProviderAdminDB(t *testing.T) {
	t.Helper()
	testhelpers.MigrationDir = filepath.Join("..", "database", "migrations")
	if testhelpers.TempDir == "" {
		dir, err := os.MkdirTemp("", "gct-engine-llm-provider-admin") //nolint:usetesting // t.TempDir() dies with the first test; the sqlite fixture must outlive the process (deliberate).
		if err != nil {
			t.Fatalf("creating temp dir: %v", err)
		}
		testhelpers.TempDir = dir
	}
	cfg := &database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: fmt.Sprintf("llm-provider-admin-%d.db", time.Now().UnixNano()),
	}
	conn, err := testhelpers.ConnectToDatabase(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testhelpers.CloseDatabase(conn))
	})
}

func validLLMParams(profileID string) *ProviderRevisionParams {
	timeout := int64(30)
	maxTokens := int64(4096)
	promptVersion := openai.PromptVersionAnalysisReadOnly
	budget := "5.00"
	return &ProviderRevisionParams{
		ProfileID:              profileID,
		Actor:                  "operator@test",
		BaseURL:                "https://api.example.com/v1",
		ModelID:                "model-a",
		APIKey:                 "sk-admin-test-0001",
		TimeoutSeconds:         timeout,
		MaxOutputTokens:        &maxTokens,
		PromptVersion:          &promptVersion,
		DailyBudgetUSD:         &budget,
		ReservationCap24H:      3,
		AllowInsecureLocalhost: false,
	}
}

func validJevParams(profileID string) *ProviderRevisionParams {
	timeout := int64(60)
	questionTemplate := "systemone-v1"
	return &ProviderRevisionParams{
		ProfileID:               profileID,
		Actor:                   "operator@test",
		BaseURL:                 "https://jev.example.com",
		ModelID:                 "jev-1.2.3",
		APIKey:                  "sk-admin-test-0002",
		TimeoutSeconds:          timeout,
		QuestionTemplateVersion: &questionTemplate,
		ReservationCap24H:       5,
	}
}

func TestCreateProviderProfile(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Primary LLM", "operator@test", providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, ProviderKindLLM, llmProfile.Kind)
	assert.False(t, llmProfile.Enabled)
	assert.True(t, providerAdminNow.Equal(llmProfile.CreatedAt))
	assert.NotEmpty(t, llmProfile.ID)

	jevProfile, err := CreateProviderProfile(ctx, ProviderKindJev, "System One", "operator@test", providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, ProviderKindJev, jevProfile.Kind)

	_, err = CreateProviderProfile(ctx, "GROK", "wrong kind", "operator@test", providerAdminNow)
	assert.ErrorIs(t, err, ErrUnknownProviderKind)

	_, err = CreateProviderProfile(ctx, ProviderKindLLM, "   ", "operator@test", providerAdminNow)
	assert.ErrorIs(t, err, ErrInvalidDisplayName)

	_, err = CreateProviderProfile(ctx, ProviderKindLLM, "Second LLM", "", providerAdminNow)
	assert.ErrorIs(t, err, ErrInvalidActor)

	// UNIQUE(kind, display_name) is enforced by the schema.
	_, err = CreateProviderProfile(ctx, ProviderKindLLM, "Primary LLM", "operator@test", providerAdminNow)
	assert.Error(t, err, "duplicate (kind, display_name) should be rejected")
}

func TestAddProviderRevisionValidation(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Primary LLM", "operator@test", providerAdminNow)
	require.NoError(t, err)
	jevProfile, err := CreateProviderProfile(ctx, ProviderKindJev, "System One", "operator@test", providerAdminNow)
	require.NoError(t, err)

	tests := []struct {
		name    string
		mutate  func(p *ProviderRevisionParams)
		profile string
		wantErr error
	}{
		{
			name:    "plain http without the loopback exemption",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.BaseURL = "http://api.example.com/v1" },
			wantErr: ErrInvalidBaseURL,
		},
		{
			name:    "plain http to a remote host is rejected even with the flag",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				p.BaseURL = "http://api.example.com/v1"
				p.AllowInsecureLocalhost = true
			},
			wantErr: ErrInvalidBaseURL,
		},
		{
			name:    "unparseable base url",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.BaseURL = "://nope" },
			wantErr: ErrInvalidBaseURL,
		},
		{
			name:    "seconds-scheme base url",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.BaseURL = "ftp://api.example.com" },
			wantErr: ErrInvalidBaseURL,
		},
		{
			name:    "jev model must keep its version shape",
			profile: jevProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.ModelID = "gpt-4o" },
			wantErr: ErrInvalidModelID,
		},
		{
			name:    "empty model id",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.ModelID = " " },
			wantErr: ErrInvalidModelID,
		},
		{
			name:    "api key must be non-blank",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.APIKey = "   " },
			wantErr: ErrInvalidSecretRef,
		},
		{
			name:    "timeout must be positive",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.TimeoutSeconds = 0 },
			wantErr: ErrInvalidTimeout,
		},
		{
			name:    "timeout must be positive",
			profile: llmProfile.ID,
			mutate:  func(p *ProviderRevisionParams) { p.TimeoutSeconds = 0 },
			wantErr: ErrInvalidTimeout,
		},
		{
			name:    "llm-only field on a jev profile",
			profile: jevProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				zero := int64(1)
				p.MaxToolRounds = &zero
			},
			wantErr: ErrKindFieldMismatch,
		},
		{
			name:    "jev-only field on an llm profile",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				q := "systemone-v1"
				p.QuestionTemplateVersion = &q
			},
			wantErr: ErrKindFieldMismatch,
		},
		{
			name:    "unknown prompt version",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				v := "analysis-readonly-v2"
				p.PromptVersion = &v
			},
			wantErr: openai.ErrUnknownPromptVersion,
		},
		{
			name:    "budget with leading zero",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				b := "05"
				p.DailyBudgetUSD = &b
			},
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "budget with exponent",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				b := "1e3"
				p.DailyBudgetUSD = &b
			},
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "negative budget",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				b := "-1.00"
				p.DailyBudgetUSD = &b
			},
			wantErr: ErrInvalidBudget,
		},
		{
			name:    "proxy address must parse",
			profile: llmProfile.ID,
			mutate: func(p *ProviderRevisionParams) {
				px := "not a url"
				p.ProxyAddress = &px
			},
			wantErr: ErrInvalidProxyAddress,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var params *ProviderRevisionParams
			if tc.profile == jevProfile.ID {
				params = validJevParams(tc.profile)
			} else {
				params = validLLMParams(tc.profile)
			}
			tc.mutate(params)
			_, err := AddProviderRevision(ctx, params, providerAdminNow)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("loopback http with the exemption is accepted", func(t *testing.T) {
		params := validLLMParams(llmProfile.ID)
		params.BaseURL = "http://127.0.0.1:8080/v1"
		params.AllowInsecureLocalhost = true
		_, err := AddProviderRevision(ctx, params, providerAdminNow)
		require.NoError(t, err)
	})

	t.Run("budget three states all pass", func(t *testing.T) {
		for _, budget := range []*string{nil, new("0"), new("12.50")} {
			params := validJevParams(jevProfile.ID)
			params.DailyBudgetUSD = budget
			// bump the revision content so each add is a fresh revision
			params.ModelID = "jev-1.2." + map[bool]string{true: "4", false: "5"}[budget == nil]
			_, err := AddProviderRevision(ctx, params, providerAdminNow)
			require.NoError(t, err)
		}
	})
}

func TestAddProviderRevisionInvalidatesActivePair(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Primary LLM", "operator@test", providerAdminNow)
	require.NoError(t, err)
	jevProfile, err := CreateProviderProfile(ctx, ProviderKindJev, "System One", "operator@test", providerAdminNow)
	require.NoError(t, err)

	llmRev, err := AddProviderRevision(ctx, validLLMParams(llmProfile.ID), providerAdminNow)
	require.NoError(t, err)
	jevRev, err := AddProviderRevision(ctx, validJevParams(jevProfile.ID), providerAdminNow)
	require.NoError(t, err)

	// Activate both, then edit the LLM profile only.
	require.NoError(t, llmstore.SetActiveModelPair(ctx, &llmstore.ActiveModelPair{
		LLMProfileRevisionID: &llmRev.ID,
		JevProfileRevisionID: &jevRev.ID,
		PairEpoch:            7,
		Readiness:            "READY",
		UpdatedAt:            providerAdminNow,
		UpdatedBy:            "operator@test",
	}))

	newRev, err := AddProviderRevision(ctx, validLLMParams(llmProfile.ID), providerAdminNow.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), newRev.RevisionNo)

	pair, err := llmstore.GetActiveModelPair(ctx)
	require.NoError(t, err)
	assert.Nil(t, pair.LLMProfileRevisionID, "the edited profile's pointer should be cleared")
	require.NotNil(t, pair.JevProfileRevisionID, "the untouched profile's pointer must survive")
	assert.Equal(t, jevRev.ID, *pair.JevProfileRevisionID)
	assert.Equal(t, "INCOMPLETE", pair.Readiness)
	assert.Equal(t, int64(8), pair.PairEpoch, "editing the active profile should bump the epoch")
}

func TestAddProviderRevisionLeavesUnrelatedPairAlone(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	activeProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Active", "operator@test", providerAdminNow)
	require.NoError(t, err)
	otherProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Other", "operator@test", providerAdminNow)
	require.NoError(t, err)

	activeRev, err := AddProviderRevision(ctx, validLLMParams(activeProfile.ID), providerAdminNow)
	require.NoError(t, err)
	require.NoError(t, llmstore.SetActiveModelPair(ctx, &llmstore.ActiveModelPair{
		LLMProfileRevisionID: &activeRev.ID,
		PairEpoch:            1,
		Readiness:            "READY",
		UpdatedAt:            providerAdminNow,
		UpdatedBy:            "operator@test",
	}))

	// Edit the *other* profile; the pair points at a different revision.
	_, err = AddProviderRevision(ctx, validLLMParams(otherProfile.ID), providerAdminNow.Add(time.Minute))
	require.NoError(t, err)

	pair, err := llmstore.GetActiveModelPair(ctx)
	require.NoError(t, err)
	require.NotNil(t, pair.LLMProfileRevisionID)
	assert.Equal(t, activeRev.ID, *pair.LLMProfileRevisionID)
	assert.Equal(t, int64(1), pair.PairEpoch)
	assert.Equal(t, "READY", pair.Readiness)
}

func TestAddProviderRevisionToArchivedProfileRejected(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	profile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Doomed", "operator@test", providerAdminNow)
	require.NoError(t, err)
	require.NoError(t, llmstore.ArchiveProfile(ctx, profile.ID, providerAdminNow.Add(time.Minute)))

	_, err = AddProviderRevision(ctx, validLLMParams(profile.ID), providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrProfileArchived)
}

func TestAddProviderRevisionAcceptsJevLatestAndDefaultsTemplate(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	profile, err := CreateProviderProfile(ctx, ProviderKindJev, "Jev latest", "op", providerAdminNow)
	require.NoError(t, err)

	revision, err := AddProviderRevision(ctx, &ProviderRevisionParams{
		ProfileID: profile.ID, Actor: "op", BaseURL: "https://jev.example.com/v1",
		ModelID: "jev-latest", APIKey: "jev-token-1", TimeoutSeconds: 30, ReservationCap24H: 1,
	}, providerAdminNow)
	require.NoError(t, err, "the jev-latest alias is accepted by operator decision (2026-10-15)")
	require.NotNil(t, revision.QuestionTemplateVersion)
	assert.Equal(t, jev.QuestionVersionReviewV2, *revision.QuestionTemplateVersion,
		"the template version defaults to the system constant; operators never type it")
}
