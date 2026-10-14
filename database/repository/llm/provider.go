package llm

import (
	"context"
	"database/sql"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	modelPSQL "github.com/thrasher-corp/gocryptotrader/database/models/postgres"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/boil"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
	"github.com/volatiletech/null"
)

// Profile mirrors llm_provider_profiles.
type Profile struct {
	ID          string
	Kind        string
	DisplayName string
	Enabled     bool
	CreatedAt   time.Time
	CreatedBy   string
	ArchivedAt  *time.Time
}

// InsertProfile inserts a new provider profile.
func InsertProfile(ctx context.Context, p *Profile) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var enabled int64
		if p.Enabled {
			enabled = 1
		}
		row := &modelSQLite.LLMProviderProfile{
			ID:          p.ID,
			Kind:        p.Kind,
			DisplayName: p.DisplayName,
			Enabled:     enabled,
			CreatedAt:   tsToText(p.CreatedAt),
			CreatedBy:   p.CreatedBy,
			ArchivedAt:  nullString(timeToTextPtr(p.ArchivedAt)),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMProviderProfile{
		ID:          p.ID,
		Kind:        p.Kind,
		DisplayName: p.DisplayName,
		Enabled:     p.Enabled,
		CreatedAt:   p.CreatedAt.UTC(),
		CreatedBy:   p.CreatedBy,
		ArchivedAt:  nullTime(p.ArchivedAt),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func timeToTextPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := tsToText(*t)
	return &s
}

// GetProfile returns a provider profile by id.
func GetProfile(ctx context.Context, id string) (*Profile, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMProviderProfiles(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return profileFromSQLite(row)
	}
	row, err := modelPSQL.LLMProviderProfiles(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Profile{
		ID:          row.ID,
		Kind:        row.Kind,
		DisplayName: row.DisplayName,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt.UTC(),
		CreatedBy:   row.CreatedBy,
		ArchivedAt:  timePtr(row.ArchivedAt),
	}, nil
}

func profileFromSQLite(row *modelSQLite.LLMProviderProfile) (*Profile, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &Profile{
		ID:          row.ID,
		Kind:        row.Kind,
		DisplayName: row.DisplayName,
		Enabled:     row.Enabled == 1,
		CreatedAt:   createdAt,
		CreatedBy:   row.CreatedBy,
	}
	if row.ArchivedAt.Valid {
		t, err := tsFromText(row.ArchivedAt.String)
		if err != nil {
			return nil, err
		}
		out.ArchivedAt = &t
	}
	return out, nil
}

// ListProfiles returns all provider profiles ordered by creation time,
// optionally including archived ones.
func ListProfiles(ctx context.Context, includeArchived bool) ([]Profile, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("created_at"), qm.OrderBy("id")}
	if !includeArchived {
		queries = append(queries, qm.Where("archived_at IS NULL"))
	}
	out := make([]Profile, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMProviderProfiles(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			p, err := profileFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *p)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMProviderProfiles(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, Profile{
			ID:          row.ID,
			Kind:        row.Kind,
			DisplayName: row.DisplayName,
			Enabled:     row.Enabled,
			CreatedAt:   row.CreatedAt.UTC(),
			CreatedBy:   row.CreatedBy,
			ArchivedAt:  timePtr(row.ArchivedAt),
		})
	}
	return out, nil
}

// SetProfileEnabled toggles the enabled flag of a provider profile.
func SetProfileEnabled(ctx context.Context, id string, enabled bool) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var v int64
		if enabled {
			v = 1
		}
		_, err := modelSQLite.LLMProviderProfiles(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"enabled": v})
		return err
	}
	_, err := modelPSQL.LLMProviderProfiles(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{"enabled": enabled})
	return err
}

// ArchiveProfile marks a provider profile archived at the given time.
func ArchiveProfile(ctx context.Context, id string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMProviderProfiles(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{colArchivedAt: tsToText(at)})
		return err
	}
	_, err := modelPSQL.LLMProviderProfiles(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{colArchivedAt: at.UTC()})
	return err
}

// ProfileRevision mirrors llm_provider_profile_revisions. Revisions are
// immutable apart from superseded_at (set when a newer revision replaces them).
type ProfileRevision struct {
	ID                      string
	ProfileID               string
	RevisionNo              int64
	Kind                    string
	BaseURL                 string
	ModelID                 string
	APIKeyRef               string
	APIKeyFingerprint       string
	TimeoutSeconds          int64
	MaxOutputTokens         *int64
	MaxToolRounds           *int64
	MaxToolCalls            *int64
	MaxCumulativeTokens     *int64
	PromptVersion           *string
	QuestionTemplateVersion *string
	DailyBudgetUSD          *string
	ReservationCap24H       int64
	AllowInsecureLocalhost  bool
	ProxyAddress            *string
	CreatedAt               time.Time
	CreatedBy               string
	SupersededAt            *time.Time
}

// InsertProfileRevision inserts a new revision and supersedes any live
// revision of the same profile in one transaction: at most one revision per
// profile is live (superseded_at IS NULL) at any time.
func InsertProfileRevision(ctx context.Context, r *ProfileRevision) error {
	if err := available(); err != nil {
		return err
	}
	return runInTx(ctx, func(tx *sql.Tx) error {
		if dialectIsSQLite() {
			var allowInsecure int64
			if r.AllowInsecureLocalhost {
				allowInsecure = 1
			}
			row := &modelSQLite.LLMProviderProfileRevision{
				ID:                      r.ID,
				ProfileID:               r.ProfileID,
				RevisionNo:              r.RevisionNo,
				Kind:                    r.Kind,
				BaseURL:                 r.BaseURL,
				ModelID:                 r.ModelID,
				APIKeyRef:               r.APIKeyRef,
				APIKeyFingerprint:       r.APIKeyFingerprint,
				TimeoutSeconds:          r.TimeoutSeconds,
				MaxOutputTokens:         nullInt(r.MaxOutputTokens),
				MaxToolRounds:           nullInt(r.MaxToolRounds),
				MaxToolCalls:            nullInt(r.MaxToolCalls),
				MaxCumulativeTokens:     nullInt(r.MaxCumulativeTokens),
				PromptVersion:           nullString(r.PromptVersion),
				QuestionTemplateVersion: nullString(r.QuestionTemplateVersion),
				DailyBudgetUsd:          nullString(r.DailyBudgetUSD),
				ReservationCap24H:       r.ReservationCap24H,
				AllowInsecureLocalhost:  allowInsecure,
				ProxyAddress:            nullString(r.ProxyAddress),
				CreatedAt:               tsToText(r.CreatedAt),
				CreatedBy:               r.CreatedBy,
			}
			if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
				return err
			}
			_, err := modelSQLite.LLMProviderProfileRevisions(qm.Where("profile_id = ? AND superseded_at IS NULL AND id != ?", r.ProfileID, r.ID)).
				UpdateAll(ctx, tx, modelSQLite.M{"superseded_at": tsToText(r.CreatedAt)})
			return err
		}
		row := &modelPSQL.LLMProviderProfileRevision{
			ID:                      r.ID,
			ProfileID:               r.ProfileID,
			RevisionNo:              int(r.RevisionNo),
			Kind:                    r.Kind,
			BaseURL:                 r.BaseURL,
			ModelID:                 r.ModelID,
			APIKeyRef:               r.APIKeyRef,
			APIKeyFingerprint:       r.APIKeyFingerprint,
			TimeoutSeconds:          int(r.TimeoutSeconds),
			MaxOutputTokens:         pgInt(r.MaxOutputTokens),
			MaxToolRounds:           pgInt(r.MaxToolRounds),
			MaxToolCalls:            pgInt(r.MaxToolCalls),
			MaxCumulativeTokens:     pgInt(r.MaxCumulativeTokens),
			PromptVersion:           nullString(r.PromptVersion),
			QuestionTemplateVersion: nullString(r.QuestionTemplateVersion),
			DailyBudgetUsd:          nullString(r.DailyBudgetUSD),
			ReservationCap24H:       int(r.ReservationCap24H),
			AllowInsecureLocalhost:  r.AllowInsecureLocalhost,
			ProxyAddress:            nullString(r.ProxyAddress),
			CreatedAt:               r.CreatedAt.UTC(),
			CreatedBy:               r.CreatedBy,
		}
		if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
			return err
		}
		_, err := modelPSQL.LLMProviderProfileRevisions(qm.Where("profile_id = ? AND superseded_at IS NULL AND id != ?", r.ProfileID, r.ID)).
			UpdateAll(ctx, tx, modelPSQL.M{"superseded_at": r.CreatedAt.UTC()})
		return err
	})
}

func revisionFromSQLite(row *modelSQLite.LLMProviderProfileRevision) (*ProfileRevision, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &ProfileRevision{
		ID:                      row.ID,
		ProfileID:               row.ProfileID,
		RevisionNo:              row.RevisionNo,
		Kind:                    row.Kind,
		BaseURL:                 row.BaseURL,
		ModelID:                 row.ModelID,
		APIKeyRef:               row.APIKeyRef,
		APIKeyFingerprint:       row.APIKeyFingerprint,
		TimeoutSeconds:          row.TimeoutSeconds,
		MaxOutputTokens:         intPtr(row.MaxOutputTokens),
		MaxToolRounds:           intPtr(row.MaxToolRounds),
		MaxToolCalls:            intPtr(row.MaxToolCalls),
		MaxCumulativeTokens:     intPtr(row.MaxCumulativeTokens),
		PromptVersion:           stringPtr(row.PromptVersion),
		QuestionTemplateVersion: stringPtr(row.QuestionTemplateVersion),
		DailyBudgetUSD:          stringPtr(row.DailyBudgetUsd),
		ReservationCap24H:       row.ReservationCap24H,
		AllowInsecureLocalhost:  row.AllowInsecureLocalhost == 1,
		ProxyAddress:            stringPtr(row.ProxyAddress),
		CreatedAt:               createdAt,
		CreatedBy:               row.CreatedBy,
	}
	if row.SupersededAt.Valid {
		t, err := tsFromText(row.SupersededAt.String)
		if err != nil {
			return nil, err
		}
		out.SupersededAt = &t
	}
	return out, nil
}

func revisionFromPSQL(row *modelPSQL.LLMProviderProfileRevision) *ProfileRevision {
	return &ProfileRevision{
		ID:                      row.ID,
		ProfileID:               row.ProfileID,
		RevisionNo:              int64(row.RevisionNo),
		Kind:                    row.Kind,
		BaseURL:                 row.BaseURL,
		ModelID:                 row.ModelID,
		APIKeyRef:               row.APIKeyRef,
		APIKeyFingerprint:       row.APIKeyFingerprint,
		TimeoutSeconds:          int64(row.TimeoutSeconds),
		MaxOutputTokens:         pgIntPtr(row.MaxOutputTokens),
		MaxToolRounds:           pgIntPtr(row.MaxToolRounds),
		MaxToolCalls:            pgIntPtr(row.MaxToolCalls),
		MaxCumulativeTokens:     pgIntPtr(row.MaxCumulativeTokens),
		PromptVersion:           stringPtr(row.PromptVersion),
		QuestionTemplateVersion: stringPtr(row.QuestionTemplateVersion),
		DailyBudgetUSD:          stringPtr(row.DailyBudgetUsd),
		ReservationCap24H:       int64(row.ReservationCap24H),
		AllowInsecureLocalhost:  row.AllowInsecureLocalhost,
		ProxyAddress:            stringPtr(row.ProxyAddress),
		CreatedAt:               row.CreatedAt.UTC(),
		CreatedBy:               row.CreatedBy,
		SupersededAt:            timePtr(row.SupersededAt),
	}
}

// GetProfileRevision returns a revision by id.
func GetProfileRevision(ctx context.Context, id string) (*ProfileRevision, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMProviderProfileRevisions(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return revisionFromSQLite(row)
	}
	row, err := modelPSQL.LLMProviderProfileRevisions(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return revisionFromPSQL(row), nil
}

// GetLiveProfileRevision returns the revision of a profile that has not been
// superseded yet.
func GetLiveProfileRevision(ctx context.Context, profileID string) (*ProfileRevision, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMProviderProfileRevisions(qm.Where("profile_id = ? AND superseded_at IS NULL", profileID)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return revisionFromSQLite(row)
	}
	row, err := modelPSQL.LLMProviderProfileRevisions(qm.Where("profile_id = ? AND superseded_at IS NULL", profileID)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return revisionFromPSQL(row), nil
}

// ListProfileRevisions returns all revisions of a profile newest first.
func ListProfileRevisions(ctx context.Context, profileID string) ([]ProfileRevision, error) {
	if err := available(); err != nil {
		return nil, err
	}
	out := make([]ProfileRevision, 0)
	queries := []qm.QueryMod{qm.Where("profile_id = ?", profileID), qm.OrderBy("revision_no DESC")}
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMProviderProfileRevisions(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			r, err := revisionFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMProviderProfileRevisions(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, *revisionFromPSQL(row))
	}
	return out, nil
}

// ActiveModelPair mirrors the single-row llm_active_model_pair table.
type ActiveModelPair struct {
	LLMProfileRevisionID *string
	JevProfileRevisionID *string
	PairEpoch            int64
	Readiness            string
	UpdatedAt            time.Time
	UpdatedBy            string
}

// GetActiveModelPair returns the active pair row; ErrNotFound before the first
// SetActiveModelPair.
func GetActiveModelPair(ctx context.Context) (*ActiveModelPair, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMActiveModelPairs(qm.Where("id = 1")).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		updatedAt, err := tsFromText(row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		return &ActiveModelPair{
			LLMProfileRevisionID: stringPtr(row.LLMProfileRevisionID),
			JevProfileRevisionID: stringPtr(row.JevProfileRevisionID),
			PairEpoch:            row.PairEpoch,
			Readiness:            row.Readiness,
			UpdatedAt:            updatedAt,
			UpdatedBy:            row.UpdatedBy,
		}, nil
	}
	row, err := modelPSQL.LLMActiveModelPairs(qm.Where("id = 1")).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ActiveModelPair{
		LLMProfileRevisionID: stringPtr(row.LLMProfileRevisionID),
		JevProfileRevisionID: stringPtr(row.JevProfileRevisionID),
		PairEpoch:            int64(row.PairEpoch),
		Readiness:            row.Readiness,
		UpdatedAt:            row.UpdatedAt.UTC(),
		UpdatedBy:            row.UpdatedBy,
	}, nil
}

// SetActiveModelPair upserts the single active pair row. The caller owns pair
// epoch arithmetic; this function writes what it is given.
func SetActiveModelPair(ctx context.Context, p *ActiveModelPair) error {
	if err := available(); err != nil {
		return err
	}
	return runInTx(ctx, func(tx *sql.Tx) error {
		if dialectIsSQLite() {
			values := modelSQLite.M{
				"llm_profile_revision_id": nullString(p.LLMProfileRevisionID),
				"jev_profile_revision_id": nullString(p.JevProfileRevisionID),
				"pair_epoch":              p.PairEpoch,
				"readiness":               p.Readiness,
				colUpdatedAt:              tsToText(p.UpdatedAt),
				"updated_by":              p.UpdatedBy,
			}
			affected, err := modelSQLite.LLMActiveModelPairs(qm.Where("id = 1")).UpdateAll(ctx, tx, values)
			if err != nil {
				return err
			}
			if affected > 0 {
				return nil
			}
			row := &modelSQLite.LLMActiveModelPair{
				ID:                   null.NewInt64(1, true),
				LLMProfileRevisionID: nullString(p.LLMProfileRevisionID),
				JevProfileRevisionID: nullString(p.JevProfileRevisionID),
				PairEpoch:            p.PairEpoch,
				Readiness:            p.Readiness,
				UpdatedAt:            tsToText(p.UpdatedAt),
				UpdatedBy:            p.UpdatedBy,
			}
			return row.Insert(ctx, tx, boil.Infer())
		}
		values := modelPSQL.M{
			"llm_profile_revision_id": nullString(p.LLMProfileRevisionID),
			"jev_profile_revision_id": nullString(p.JevProfileRevisionID),
			"pair_epoch":              int(p.PairEpoch),
			"readiness":               p.Readiness,
			colUpdatedAt:              p.UpdatedAt.UTC(),
			"updated_by":              p.UpdatedBy,
		}
		affected, err := modelPSQL.LLMActiveModelPairs(qm.Where("id = 1")).UpdateAll(ctx, tx, values)
		if err != nil {
			return err
		}
		if affected > 0 {
			return nil
		}
		row := &modelPSQL.LLMActiveModelPair{
			ID:                   1,
			LLMProfileRevisionID: nullString(p.LLMProfileRevisionID),
			JevProfileRevisionID: nullString(p.JevProfileRevisionID),
			PairEpoch:            int(p.PairEpoch),
			Readiness:            p.Readiness,
			UpdatedAt:            p.UpdatedAt.UTC(),
			UpdatedBy:            p.UpdatedBy,
		}
		return row.Insert(ctx, tx, boil.Infer())
	})
}

// ProbeResult mirrors llm_provider_probe_results (append-only).
type ProbeResult struct {
	ID                string
	ProfileRevisionID string
	Kind              string
	Layer             string
	Passed            bool
	ErrorCode         *string
	LatencyMS         *int64
	InputTokens       *int64
	OutputTokens      *int64
	ResponseModel     *string
	RequestSha256     *string
	ProbedAt          time.Time
}

// InsertProbeResult appends a probe result.
func InsertProbeResult(ctx context.Context, r *ProbeResult) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var passed int64
		if r.Passed {
			passed = 1
		}
		row := &modelSQLite.LLMProviderProbeResult{
			ID:                r.ID,
			ProfileRevisionID: r.ProfileRevisionID,
			Kind:              r.Kind,
			Layer:             r.Layer,
			Passed:            passed,
			ErrorCode:         nullString(r.ErrorCode),
			LatencyMS:         nullInt(r.LatencyMS),
			InputTokens:       nullInt(r.InputTokens),
			OutputTokens:      nullInt(r.OutputTokens),
			ResponseModel:     nullString(r.ResponseModel),
			RequestSha256:     nullString(r.RequestSha256),
			ProbedAt:          tsToText(r.ProbedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMProviderProbeResult{
		ID:                r.ID,
		ProfileRevisionID: r.ProfileRevisionID,
		Kind:              r.Kind,
		Layer:             r.Layer,
		Passed:            r.Passed,
		ErrorCode:         nullString(r.ErrorCode),
		LatencyMS:         pgInt(r.LatencyMS),
		InputTokens:       pgInt(r.InputTokens),
		OutputTokens:      pgInt(r.OutputTokens),
		ResponseModel:     nullString(r.ResponseModel),
		RequestSha256:     nullString(r.RequestSha256),
		ProbedAt:          r.ProbedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ListProbeResults returns the probe history of a revision, newest first.
func ListProbeResults(ctx context.Context, profileRevisionID string) ([]ProbeResult, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("profile_revision_id = ?", profileRevisionID), qm.OrderBy("probed_at DESC")}
	out := make([]ProbeResult, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMProviderProbeResults(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			probedAt, err := tsFromText(row.ProbedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, ProbeResult{
				ID:                row.ID,
				ProfileRevisionID: row.ProfileRevisionID,
				Kind:              row.Kind,
				Layer:             row.Layer,
				Passed:            row.Passed == 1,
				ErrorCode:         stringPtr(row.ErrorCode),
				LatencyMS:         intPtr(row.LatencyMS),
				InputTokens:       intPtr(row.InputTokens),
				OutputTokens:      intPtr(row.OutputTokens),
				ResponseModel:     stringPtr(row.ResponseModel),
				RequestSha256:     stringPtr(row.RequestSha256),
				ProbedAt:          probedAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMProviderProbeResults(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, ProbeResult{
			ID:                row.ID,
			ProfileRevisionID: row.ProfileRevisionID,
			Kind:              row.Kind,
			Layer:             row.Layer,
			Passed:            row.Passed,
			ErrorCode:         stringPtr(row.ErrorCode),
			LatencyMS:         pgIntPtr(row.LatencyMS),
			InputTokens:       pgIntPtr(row.InputTokens),
			OutputTokens:      pgIntPtr(row.OutputTokens),
			ResponseModel:     stringPtr(row.ResponseModel),
			RequestSha256:     stringPtr(row.RequestSha256),
			ProbedAt:          row.ProbedAt.UTC(),
		})
	}
	return out, nil
}

// PairSwitchCommand mirrors llm_pair_switch_commands (append-only).
type PairSwitchCommand struct {
	ID                 string
	IdempotencyKey     string
	FromPairEpoch      int64
	ToPairEpoch        int64
	LLMRevisionID      *string
	JevRevisionID      *string
	Operator           string
	AffectedStrategies int64
	RevokedApprovals   int64
	CreatedAt          time.Time
}

// InsertPairSwitchCommand appends a pair switch command record.
func InsertPairSwitchCommand(ctx context.Context, c *PairSwitchCommand) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMPairSwitchCommand{
			ID:                 c.ID,
			IdempotencyKey:     c.IdempotencyKey,
			FromPairEpoch:      c.FromPairEpoch,
			ToPairEpoch:        c.ToPairEpoch,
			LLMRevisionID:      nullString(c.LLMRevisionID),
			JevRevisionID:      nullString(c.JevRevisionID),
			Operator:           c.Operator,
			AffectedStrategies: c.AffectedStrategies,
			RevokedApprovals:   c.RevokedApprovals,
			CreatedAt:          tsToText(c.CreatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMPairSwitchCommand{
		ID:                 c.ID,
		IdempotencyKey:     c.IdempotencyKey,
		FromPairEpoch:      int(c.FromPairEpoch),
		ToPairEpoch:        int(c.ToPairEpoch),
		LLMRevisionID:      nullString(c.LLMRevisionID),
		JevRevisionID:      nullString(c.JevRevisionID),
		Operator:           c.Operator,
		AffectedStrategies: int(c.AffectedStrategies),
		RevokedApprovals:   int(c.RevokedApprovals),
		CreatedAt:          c.CreatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// CasActiveModelPair updates the single active pair row only when pair_epoch
// still equals expectedEpoch (compare-and-swap). The first activation
// (expectedEpoch 0, no row yet) inserts the row at epoch 1. A false result
// with nil error means the swap lost a race or the epoch moved: the caller
// maps it to its own conflict error.
func CasActiveModelPair(ctx context.Context, p *ActiveModelPair, expectedEpoch int64) (bool, error) {
	if err := available(); err != nil {
		return false, err
	}
	var applied bool
	err := runInTx(ctx, func(tx *sql.Tx) error {
		if dialectIsSQLite() {
			values := modelSQLite.M{
				"llm_profile_revision_id": nullString(p.LLMProfileRevisionID),
				"jev_profile_revision_id": nullString(p.JevProfileRevisionID),
				"pair_epoch":              p.PairEpoch,
				"readiness":               p.Readiness,
				colUpdatedAt:              tsToText(p.UpdatedAt),
				"updated_by":              p.UpdatedBy,
			}
			affected, err := modelSQLite.LLMActiveModelPairs(qm.Where("id = 1 AND pair_epoch = ?", expectedEpoch)).UpdateAll(ctx, tx, values)
			if err != nil {
				return err
			}
			if affected > 0 {
				applied = true
				return nil
			}
			if expectedEpoch != 0 {
				return nil
			}
			existing, err := modelSQLite.LLMActiveModelPairs(qm.Where("id = 1")).One(ctx, tx)
			if err == nil && existing != nil {
				return nil // another writer got there first
			}
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			row := &modelSQLite.LLMActiveModelPair{
				ID:                   null.NewInt64(1, true),
				LLMProfileRevisionID: nullString(p.LLMProfileRevisionID),
				JevProfileRevisionID: nullString(p.JevProfileRevisionID),
				PairEpoch:            p.PairEpoch,
				Readiness:            p.Readiness,
				UpdatedAt:            tsToText(p.UpdatedAt),
				UpdatedBy:            p.UpdatedBy,
			}
			if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
				// Losing an insert race is a lost CAS, not an infrastructure error.
				return nil //nolint:nilerr // losing the CAS insert race is a lost CAS, not an infrastructure error
			}
			applied = true
			return nil
		}
		values := modelPSQL.M{
			"llm_profile_revision_id": nullString(p.LLMProfileRevisionID),
			"jev_profile_revision_id": nullString(p.JevProfileRevisionID),
			"pair_epoch":              int(p.PairEpoch),
			"readiness":               p.Readiness,
			colUpdatedAt:              p.UpdatedAt.UTC(),
			"updated_by":              p.UpdatedBy,
		}
		affected, err := modelPSQL.LLMActiveModelPairs(qm.Where("id = 1 AND pair_epoch = $1", expectedEpoch)).UpdateAll(ctx, tx, values)
		if err != nil {
			return err
		}
		if affected > 0 {
			applied = true
			return nil
		}
		if expectedEpoch != 0 {
			return nil
		}
		existing, err := modelPSQL.LLMActiveModelPairs(qm.Where("id = 1")).One(ctx, tx)
		if err == nil && existing != nil {
			return nil
		}
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		row := &modelPSQL.LLMActiveModelPair{
			ID:                   1,
			LLMProfileRevisionID: nullString(p.LLMProfileRevisionID),
			JevProfileRevisionID: nullString(p.JevProfileRevisionID),
			PairEpoch:            int(p.PairEpoch),
			Readiness:            p.Readiness,
			UpdatedAt:            p.UpdatedAt.UTC(),
			UpdatedBy:            p.UpdatedBy,
		}
		if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
			return nil //nolint:nilerr // losing the CAS insert race is a lost CAS, not an infrastructure error
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// GetPairSwitchCommandByIdempotencyKey returns the command recorded under a
// given idempotency key, or ErrNotFound.
func GetPairSwitchCommandByIdempotencyKey(ctx context.Context, key string) (*PairSwitchCommand, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMPairSwitchCommands(qm.Where("idempotency_key = ?", key)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		createdAt, err := tsFromText(row.CreatedAt)
		if err != nil {
			return nil, err
		}
		return &PairSwitchCommand{
			ID:                 row.ID,
			IdempotencyKey:     row.IdempotencyKey,
			FromPairEpoch:      row.FromPairEpoch,
			ToPairEpoch:        row.ToPairEpoch,
			LLMRevisionID:      stringPtr(row.LLMRevisionID),
			JevRevisionID:      stringPtr(row.JevRevisionID),
			Operator:           row.Operator,
			AffectedStrategies: row.AffectedStrategies,
			RevokedApprovals:   row.RevokedApprovals,
			CreatedAt:          createdAt,
		}, nil
	}
	row, err := modelPSQL.LLMPairSwitchCommands(qm.Where("idempotency_key = $1", key)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &PairSwitchCommand{
		ID:                 row.ID,
		IdempotencyKey:     row.IdempotencyKey,
		FromPairEpoch:      int64(row.FromPairEpoch),
		ToPairEpoch:        int64(row.ToPairEpoch),
		LLMRevisionID:      stringPtr(row.LLMRevisionID),
		JevRevisionID:      stringPtr(row.JevRevisionID),
		Operator:           row.Operator,
		AffectedStrategies: int64(row.AffectedStrategies),
		RevokedApprovals:   int64(row.RevokedApprovals),
		CreatedAt:          row.CreatedAt.UTC(),
	}, nil
}

// ListPairSwitchCommands returns pair switch history, newest first.
func ListPairSwitchCommands(ctx context.Context, limit int) ([]PairSwitchCommand, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("created_at DESC")}
	if limit > 0 {
		queries = append(queries, qm.Limit(limit))
	}
	out := make([]PairSwitchCommand, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMPairSwitchCommands(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, PairSwitchCommand{
				ID:                 row.ID,
				IdempotencyKey:     row.IdempotencyKey,
				FromPairEpoch:      row.FromPairEpoch,
				ToPairEpoch:        row.ToPairEpoch,
				LLMRevisionID:      stringPtr(row.LLMRevisionID),
				JevRevisionID:      stringPtr(row.JevRevisionID),
				Operator:           row.Operator,
				AffectedStrategies: row.AffectedStrategies,
				RevokedApprovals:   row.RevokedApprovals,
				CreatedAt:          createdAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMPairSwitchCommands(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, PairSwitchCommand{
			ID:                 row.ID,
			IdempotencyKey:     row.IdempotencyKey,
			FromPairEpoch:      int64(row.FromPairEpoch),
			ToPairEpoch:        int64(row.ToPairEpoch),
			LLMRevisionID:      stringPtr(row.LLMRevisionID),
			JevRevisionID:      stringPtr(row.JevRevisionID),
			Operator:           row.Operator,
			AffectedStrategies: int64(row.AffectedStrategies),
			RevokedApprovals:   int64(row.RevokedApprovals),
			CreatedAt:          row.CreatedAt.UTC(),
		})
	}
	return out, nil
}
