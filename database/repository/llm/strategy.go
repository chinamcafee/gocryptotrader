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
)

// Strategy mirrors llm_strategies.
type Strategy struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	CreatedBy  string
	ArchivedAt *time.Time
}

// InsertStrategy inserts a new strategy.
func InsertStrategy(ctx context.Context, s *Strategy) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMStrategy{
			ID:         s.ID,
			Name:       s.Name,
			CreatedAt:  tsToText(s.CreatedAt),
			CreatedBy:  s.CreatedBy,
			ArchivedAt: nullString(timeToTextPtr(s.ArchivedAt)),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMStrategy{
		ID:         s.ID,
		Name:       s.Name,
		CreatedAt:  s.CreatedAt.UTC(),
		CreatedBy:  s.CreatedBy,
		ArchivedAt: nullTime(s.ArchivedAt),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func strategyFromSQLite(row *modelSQLite.LLMStrategy) (*Strategy, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &Strategy{
		ID:        row.ID,
		Name:      row.Name,
		CreatedAt: createdAt,
		CreatedBy: row.CreatedBy,
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

// GetStrategy returns a strategy by id.
func GetStrategy(ctx context.Context, id string) (*Strategy, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMStrategies(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return strategyFromSQLite(row)
	}
	row, err := modelPSQL.LLMStrategies(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Strategy{
		ID:         row.ID,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt.UTC(),
		CreatedBy:  row.CreatedBy,
		ArchivedAt: timePtr(row.ArchivedAt),
	}, nil
}

// ListStrategies returns strategies newest first, optionally including
// archived ones.
func ListStrategies(ctx context.Context, includeArchived bool) ([]Strategy, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("created_at DESC"), qm.OrderBy("id")}
	if !includeArchived {
		queries = append(queries, qm.Where("archived_at IS NULL"))
	}
	out := make([]Strategy, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMStrategies(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			s, err := strategyFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *s)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMStrategies(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, Strategy{
			ID:         row.ID,
			Name:       row.Name,
			CreatedAt:  row.CreatedAt.UTC(),
			CreatedBy:  row.CreatedBy,
			ArchivedAt: timePtr(row.ArchivedAt),
		})
	}
	return out, nil
}

// ArchiveStrategy marks a strategy archived at the given time.
func ArchiveStrategy(ctx context.Context, id string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategies(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{colArchivedAt: tsToText(at)})
		return err
	}
	_, err := modelPSQL.LLMStrategies(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{colArchivedAt: at.UTC()})
	return err
}

// StrategyVersion mirrors llm_strategy_versions. The frozen columns
// (frozen_json / strategy_hash / risk_envelope_hash) can be set once by
// FreezeStrategyVersion; the database trigger rejects any later change.
type StrategyVersion struct {
	ID                  string
	StrategyID          string
	ParentVersionID     *string
	State               string
	SourcePromptCipher  []byte
	SourcePromptSha256  string
	ProposalJSON        *string
	FrozenJSON          *string
	StrategyHash        *string
	RiskEnvelopeHash    *string
	ClarifyingQuestions *string
	ProposalState       *string
	CreatedAt           time.Time
	FrozenAt            *time.Time
}

// InsertStrategyVersion inserts a new strategy version.
func InsertStrategyVersion(ctx context.Context, v *StrategyVersion) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMStrategyVersion{
			ID:                  v.ID,
			StrategyID:          v.StrategyID,
			ParentVersionID:     nullString(v.ParentVersionID),
			State:               v.State,
			SourcePromptCipher:  nullBytes(v.SourcePromptCipher),
			SourcePromptSha256:  v.SourcePromptSha256,
			ProposalJSON:        nullString(v.ProposalJSON),
			FrozenJSON:          nullString(v.FrozenJSON),
			StrategyHash:        nullString(v.StrategyHash),
			RiskEnvelopeHash:    nullString(v.RiskEnvelopeHash),
			ClarifyingQuestions: nullString(v.ClarifyingQuestions),
			ProposalState:       nullString(v.ProposalState),
			CreatedAt:           tsToText(v.CreatedAt),
			FrozenAt:            nullString(timeToTextPtr(v.FrozenAt)),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMStrategyVersion{
		ID:                  v.ID,
		StrategyID:          v.StrategyID,
		ParentVersionID:     nullString(v.ParentVersionID),
		State:               v.State,
		SourcePromptCipher:  nullBytes(v.SourcePromptCipher),
		SourcePromptSha256:  v.SourcePromptSha256,
		ProposalJSON:        nullString(v.ProposalJSON),
		FrozenJSON:          nullString(v.FrozenJSON),
		StrategyHash:        nullString(v.StrategyHash),
		RiskEnvelopeHash:    nullString(v.RiskEnvelopeHash),
		ClarifyingQuestions: nullString(v.ClarifyingQuestions),
		ProposalState:       nullString(v.ProposalState),
		CreatedAt:           v.CreatedAt.UTC(),
		FrozenAt:            nullTime(v.FrozenAt),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func strategyVersionFromSQLite(row *modelSQLite.LLMStrategyVersion) (*StrategyVersion, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &StrategyVersion{
		ID:                  row.ID,
		StrategyID:          row.StrategyID,
		ParentVersionID:     stringPtr(row.ParentVersionID),
		State:               row.State,
		SourcePromptCipher:  bytesPtr(row.SourcePromptCipher),
		SourcePromptSha256:  row.SourcePromptSha256,
		ProposalJSON:        stringPtr(row.ProposalJSON),
		FrozenJSON:          stringPtr(row.FrozenJSON),
		StrategyHash:        stringPtr(row.StrategyHash),
		RiskEnvelopeHash:    stringPtr(row.RiskEnvelopeHash),
		ClarifyingQuestions: stringPtr(row.ClarifyingQuestions),
		ProposalState:       stringPtr(row.ProposalState),
		CreatedAt:           createdAt,
	}
	if row.FrozenAt.Valid {
		t, err := tsFromText(row.FrozenAt.String)
		if err != nil {
			return nil, err
		}
		out.FrozenAt = &t
	}
	return out, nil
}

func strategyVersionFromPSQL(row *modelPSQL.LLMStrategyVersion) *StrategyVersion {
	return &StrategyVersion{
		ID:                  row.ID,
		StrategyID:          row.StrategyID,
		ParentVersionID:     stringPtr(row.ParentVersionID),
		State:               row.State,
		SourcePromptCipher:  bytesPtr(row.SourcePromptCipher),
		SourcePromptSha256:  row.SourcePromptSha256,
		ProposalJSON:        stringPtr(row.ProposalJSON),
		FrozenJSON:          stringPtr(row.FrozenJSON),
		StrategyHash:        stringPtr(row.StrategyHash),
		RiskEnvelopeHash:    stringPtr(row.RiskEnvelopeHash),
		ClarifyingQuestions: stringPtr(row.ClarifyingQuestions),
		ProposalState:       stringPtr(row.ProposalState),
		CreatedAt:           row.CreatedAt.UTC(),
		FrozenAt:            timePtr(row.FrozenAt),
	}
}

// GetStrategyVersion returns a version by id.
func GetStrategyVersion(ctx context.Context, id string) (*StrategyVersion, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMStrategyVersions(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return strategyVersionFromSQLite(row)
	}
	row, err := modelPSQL.LLMStrategyVersions(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return strategyVersionFromPSQL(row), nil
}

// ListStrategyVersions returns versions of a strategy newest first.
func ListStrategyVersions(ctx context.Context, strategyID string) ([]StrategyVersion, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("strategy_id = ?", strategyID), qm.OrderBy("created_at DESC"), qm.OrderBy("id")}
	out := make([]StrategyVersion, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMStrategyVersions(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			v, err := strategyVersionFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *v)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMStrategyVersions(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, *strategyVersionFromPSQL(row))
	}
	return out, nil
}

// UpdateStrategyVersionProposal updates the proposal-stage columns.
func UpdateStrategyVersionProposal(ctx context.Context, id string, proposalJSON, clarifyingQuestions, proposalState *string) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			"proposal_json":        nullString(proposalJSON),
			"clarifying_questions": nullString(clarifyingQuestions),
			"proposal_state":       nullString(proposalState),
		})
		return err
	}
	_, err := modelPSQL.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		"proposal_json":        nullString(proposalJSON),
		"clarifying_questions": nullString(clarifyingQuestions),
		"proposal_state":       nullString(proposalState),
	})
	return err
}

// FreezeStrategyVersion writes the frozen columns and frozen_at. The database
// trigger makes this a first-freeze-only write: a second call is rejected.
func FreezeStrategyVersion(ctx context.Context, id, frozenJSON, strategyHash, riskEnvelopeHash string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			"frozen_json":        frozenJSON,
			"strategy_hash":      strategyHash,
			"risk_envelope_hash": riskEnvelopeHash,
			"frozen_at":          tsToText(at),
		})
		return err
	}
	_, err := modelPSQL.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		"frozen_json":        frozenJSON,
		"strategy_hash":      strategyHash,
		"risk_envelope_hash": riskEnvelopeHash,
		"frozen_at":          at.UTC(),
	})
	return err
}

// UpdateStrategyVersionState advances the state machine column.
func UpdateStrategyVersionState(ctx context.Context, id, state string) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"state": state})
		return err
	}
	_, err := modelPSQL.LLMStrategyVersions(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{"state": state})
	return err
}

// StrategyStateTransition mirrors llm_strategy_state_transitions (append-only).
type StrategyStateTransition struct {
	ID                string
	StrategyVersionID string
	FromState         string
	ToState           string
	Reason            string
	Actor             string
	CreatedAt         time.Time
}

// InsertStrategyStateTransition appends a state transition record.
func InsertStrategyStateTransition(ctx context.Context, t *StrategyStateTransition) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMStrategyStateTransition{
			ID:                t.ID,
			StrategyVersionID: t.StrategyVersionID,
			FromState:         t.FromState,
			ToState:           t.ToState,
			Reason:            t.Reason,
			Actor:             t.Actor,
			CreatedAt:         tsToText(t.CreatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMStrategyStateTransition{
		ID:                t.ID,
		StrategyVersionID: t.StrategyVersionID,
		FromState:         t.FromState,
		ToState:           t.ToState,
		Reason:            t.Reason,
		Actor:             t.Actor,
		CreatedAt:         t.CreatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ListStrategyStateTransitions returns transitions of a version oldest first.
func ListStrategyStateTransitions(ctx context.Context, strategyVersionID string) ([]StrategyStateTransition, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("strategy_version_id = ?", strategyVersionID), qm.OrderBy("created_at")}
	out := make([]StrategyStateTransition, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMStrategyStateTransitions(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, StrategyStateTransition{
				ID:                row.ID,
				StrategyVersionID: row.StrategyVersionID,
				FromState:         row.FromState,
				ToState:           row.ToState,
				Reason:            row.Reason,
				Actor:             row.Actor,
				CreatedAt:         createdAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMStrategyStateTransitions(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, StrategyStateTransition{
			ID:                row.ID,
			StrategyVersionID: row.StrategyVersionID,
			FromState:         row.FromState,
			ToState:           row.ToState,
			Reason:            row.Reason,
			Actor:             row.Actor,
			CreatedAt:         row.CreatedAt.UTC(),
		})
	}
	return out, nil
}

// StrategyApproval mirrors llm_strategy_approvals. After insert only the
// revoke/expire columns may change; the database trigger rejects anything else.
type StrategyApproval struct {
	ID                   string
	StrategyVersionID    string
	AccountID            string
	Network              string
	ExecutionMode        string
	ApprovedMarketIDs    string
	RiskEnvelopeJSON     string
	RiskEnvelopeHash     string
	PairEpoch            int64
	LLMProfileRevisionID string
	JevProfileRevisionID string
	ValidFrom            time.Time
	ValidUntil           time.Time
	ApprovedBy           string
	ApprovedAt           time.Time
	RevokedAt            *time.Time
	RevokedBy            *string
	RevokeReason         *string
	ExpiredAt            *time.Time
}

// InsertStrategyApproval inserts a new approval.
func InsertStrategyApproval(ctx context.Context, a *StrategyApproval) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMStrategyApproval{
			ID:                   a.ID,
			StrategyVersionID:    a.StrategyVersionID,
			AccountID:            a.AccountID,
			Network:              a.Network,
			ExecutionMode:        a.ExecutionMode,
			ApprovedMarketIds:    a.ApprovedMarketIDs,
			RiskEnvelopeJSON:     a.RiskEnvelopeJSON,
			RiskEnvelopeHash:     a.RiskEnvelopeHash,
			PairEpoch:            a.PairEpoch,
			LLMProfileRevisionID: a.LLMProfileRevisionID,
			JevProfileRevisionID: a.JevProfileRevisionID,
			ValidFrom:            tsToText(a.ValidFrom),
			ValidUntil:           tsToText(a.ValidUntil),
			ApprovedBy:           a.ApprovedBy,
			ApprovedAt:           tsToText(a.ApprovedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMStrategyApproval{
		ID:                   a.ID,
		StrategyVersionID:    a.StrategyVersionID,
		AccountID:            a.AccountID,
		Network:              a.Network,
		ExecutionMode:        a.ExecutionMode,
		ApprovedMarketIds:    a.ApprovedMarketIDs,
		RiskEnvelopeJSON:     a.RiskEnvelopeJSON,
		RiskEnvelopeHash:     a.RiskEnvelopeHash,
		PairEpoch:            int(a.PairEpoch),
		LLMProfileRevisionID: a.LLMProfileRevisionID,
		JevProfileRevisionID: a.JevProfileRevisionID,
		ValidFrom:            a.ValidFrom.UTC(),
		ValidUntil:           a.ValidUntil.UTC(),
		ApprovedBy:           a.ApprovedBy,
		ApprovedAt:           a.ApprovedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func approvalFromSQLite(row *modelSQLite.LLMStrategyApproval) (*StrategyApproval, error) {
	validFrom, err := tsFromText(row.ValidFrom)
	if err != nil {
		return nil, err
	}
	validUntil, err := tsFromText(row.ValidUntil)
	if err != nil {
		return nil, err
	}
	approvedAt, err := tsFromText(row.ApprovedAt)
	if err != nil {
		return nil, err
	}
	out := &StrategyApproval{
		ID:                   row.ID,
		StrategyVersionID:    row.StrategyVersionID,
		AccountID:            row.AccountID,
		Network:              row.Network,
		ExecutionMode:        row.ExecutionMode,
		ApprovedMarketIDs:    row.ApprovedMarketIds,
		RiskEnvelopeJSON:     row.RiskEnvelopeJSON,
		RiskEnvelopeHash:     row.RiskEnvelopeHash,
		PairEpoch:            row.PairEpoch,
		LLMProfileRevisionID: row.LLMProfileRevisionID,
		JevProfileRevisionID: row.JevProfileRevisionID,
		ValidFrom:            validFrom,
		ValidUntil:           validUntil,
		ApprovedBy:           row.ApprovedBy,
		ApprovedAt:           approvedAt,
		RevokedBy:            stringPtr(row.RevokedBy),
		RevokeReason:         stringPtr(row.RevokeReason),
	}
	if row.RevokedAt.Valid {
		t, err := tsFromText(row.RevokedAt.String)
		if err != nil {
			return nil, err
		}
		out.RevokedAt = &t
	}
	if row.ExpiredAt.Valid {
		t, err := tsFromText(row.ExpiredAt.String)
		if err != nil {
			return nil, err
		}
		out.ExpiredAt = &t
	}
	return out, nil
}

// GetStrategyApprovalByVersion returns the approval of a strategy version.
func GetStrategyApprovalByVersion(ctx context.Context, strategyVersionID string) (*StrategyApproval, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMStrategyApprovals(qm.Where("strategy_version_id = ?", strategyVersionID)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return approvalFromSQLite(row)
	}
	row, err := modelPSQL.LLMStrategyApprovals(qm.Where("strategy_version_id = ?", strategyVersionID)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return approvalFromPSQL(row), nil
}

// approvalFromPSQL converts a postgres approval row.
func approvalFromPSQL(row *modelPSQL.LLMStrategyApproval) *StrategyApproval {
	return &StrategyApproval{
		ID:                   row.ID,
		StrategyVersionID:    row.StrategyVersionID,
		AccountID:            row.AccountID,
		Network:              row.Network,
		ExecutionMode:        row.ExecutionMode,
		ApprovedMarketIDs:    row.ApprovedMarketIds,
		RiskEnvelopeJSON:     row.RiskEnvelopeJSON,
		RiskEnvelopeHash:     row.RiskEnvelopeHash,
		PairEpoch:            int64(row.PairEpoch),
		LLMProfileRevisionID: row.LLMProfileRevisionID,
		JevProfileRevisionID: row.JevProfileRevisionID,
		ValidFrom:            row.ValidFrom.UTC(),
		ValidUntil:           row.ValidUntil.UTC(),
		ApprovedBy:           row.ApprovedBy,
		ApprovedAt:           row.ApprovedAt.UTC(),
		RevokedAt:            timePtr(row.RevokedAt),
		RevokedBy:            stringPtr(row.RevokedBy),
		RevokeReason:         stringPtr(row.RevokeReason),
		ExpiredAt:            timePtr(row.ExpiredAt),
	}
}

// RevokeStrategyApproval writes only the revoke columns; the trigger rejects
// any attempt to touch the rest of the row.
func RevokeStrategyApproval(ctx context.Context, id string, at time.Time, by, reason string) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategyApprovals(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			"revoked_at":    tsToText(at),
			"revoked_by":    by,
			"revoke_reason": reason,
		})
		return err
	}
	_, err := modelPSQL.LLMStrategyApprovals(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		"revoked_at":    at.UTC(),
		"revoked_by":    by,
		"revoke_reason": reason,
	})
	return err
}

// ExpireStrategyApproval writes only the expiry column.
func ExpireStrategyApproval(ctx context.Context, id string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMStrategyApprovals(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"expired_at": tsToText(at)})
		return err
	}
	_, err := modelPSQL.LLMStrategyApprovals(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{"expired_at": at.UTC()})
	return err
}

// MarketTradeGrant mirrors llm_market_trade_grants.
type MarketTradeGrant struct {
	ID           string
	AccountID    string
	Network      string
	MarketID     string
	GrantedBy    string
	GrantedAt    time.Time
	ValidUntil   time.Time
	RevokedAt    *time.Time
	RevokedBy    *string
	RevokeReason *string
}

// InsertMarketTradeGrant inserts a new trade grant.
func InsertMarketTradeGrant(ctx context.Context, g *MarketTradeGrant) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMMarketTradeGrant{
			ID:         g.ID,
			AccountID:  g.AccountID,
			Network:    g.Network,
			MarketID:   g.MarketID,
			GrantedBy:  g.GrantedBy,
			GrantedAt:  tsToText(g.GrantedAt),
			ValidUntil: tsToText(g.ValidUntil),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMMarketTradeGrant{
		ID:         g.ID,
		AccountID:  g.AccountID,
		Network:    g.Network,
		MarketID:   g.MarketID,
		GrantedBy:  g.GrantedBy,
		GrantedAt:  g.GrantedAt.UTC(),
		ValidUntil: g.ValidUntil.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// RevokeMarketTradeGrant writes only the revoke columns.
func RevokeMarketTradeGrant(ctx context.Context, id string, at time.Time, by, reason string) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMMarketTradeGrants(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			"revoked_at":    tsToText(at),
			"revoked_by":    by,
			"revoke_reason": reason,
		})
		return err
	}
	_, err := modelPSQL.LLMMarketTradeGrants(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		"revoked_at":    at.UTC(),
		"revoked_by":    by,
		"revoke_reason": reason,
	})
	return err
}

// GetMarketTradeGrant returns one grant by id.
func GetMarketTradeGrant(ctx context.Context, id string) (*MarketTradeGrant, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMMarketTradeGrants(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		grantedAt, err := tsFromText(row.GrantedAt)
		if err != nil {
			return nil, err
		}
		validUntil, err := tsFromText(row.ValidUntil)
		if err != nil {
			return nil, err
		}
		g := &MarketTradeGrant{
			ID: row.ID, AccountID: row.AccountID, Network: row.Network, MarketID: row.MarketID,
			GrantedBy: row.GrantedBy, GrantedAt: grantedAt, ValidUntil: validUntil,
			RevokedBy: stringPtr(row.RevokedBy), RevokeReason: stringPtr(row.RevokeReason),
		}
		if row.RevokedAt.Valid {
			t, err := tsFromText(row.RevokedAt.String)
			if err != nil {
				return nil, err
			}
			g.RevokedAt = &t
		}
		return g, nil
	}
	row, err := modelPSQL.LLMMarketTradeGrants(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	return &MarketTradeGrant{
		ID: row.ID, AccountID: row.AccountID, Network: row.Network, MarketID: row.MarketID,
		GrantedBy: row.GrantedBy, GrantedAt: row.GrantedAt.UTC(), ValidUntil: row.ValidUntil.UTC(),
		RevokedAt: timePtr(row.RevokedAt), RevokedBy: stringPtr(row.RevokedBy), RevokeReason: stringPtr(row.RevokeReason),
	}, nil
}

// ListActiveStrategyApprovals returns approvals that are neither revoked nor
// expired, oldest first: the market-revoke cascade walks them in approval
// order so a replay reads the same sequence.
func ListActiveStrategyApprovals(ctx context.Context) ([]StrategyApproval, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{
		qm.Where("revoked_at IS NULL AND expired_at IS NULL"),
		qm.OrderBy("approved_at ASC"),
	}
	out := make([]StrategyApproval, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMStrategyApprovals(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			approval, err := approvalFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *approval)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMStrategyApprovals(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, *approvalFromPSQL(row))
	}
	return out, nil
}

// ListMarketTradeGrants returns grants of an account newest first.
func ListMarketTradeGrants(ctx context.Context, accountID string) ([]MarketTradeGrant, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("account_id = ?", accountID), qm.OrderBy("granted_at DESC")}
	out := make([]MarketTradeGrant, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMMarketTradeGrants(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			grantedAt, err := tsFromText(row.GrantedAt)
			if err != nil {
				return nil, err
			}
			validUntil, err := tsFromText(row.ValidUntil)
			if err != nil {
				return nil, err
			}
			g := MarketTradeGrant{
				ID:           row.ID,
				AccountID:    row.AccountID,
				Network:      row.Network,
				MarketID:     row.MarketID,
				GrantedBy:    row.GrantedBy,
				GrantedAt:    grantedAt,
				ValidUntil:   validUntil,
				RevokedBy:    stringPtr(row.RevokedBy),
				RevokeReason: stringPtr(row.RevokeReason),
			}
			if row.RevokedAt.Valid {
				t, err := tsFromText(row.RevokedAt.String)
				if err != nil {
					return nil, err
				}
				g.RevokedAt = &t
			}
			out = append(out, g)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMMarketTradeGrants(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, MarketTradeGrant{
			ID:           row.ID,
			AccountID:    row.AccountID,
			Network:      row.Network,
			MarketID:     row.MarketID,
			GrantedBy:    row.GrantedBy,
			GrantedAt:    row.GrantedAt.UTC(),
			ValidUntil:   row.ValidUntil.UTC(),
			RevokedAt:    timePtr(row.RevokedAt),
			RevokedBy:    stringPtr(row.RevokedBy),
			RevokeReason: stringPtr(row.RevokeReason),
		})
	}
	return out, nil
}
