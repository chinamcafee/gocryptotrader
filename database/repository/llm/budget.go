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

// BudgetReservation mirrors llm_budget_reservations. After insert only the
// settlement columns may change; the database trigger rejects anything else.
type BudgetReservation struct {
	ID                string
	ProfileRevisionID string
	Kind              string
	Purpose           string
	DecisionRunID     *string
	Status            string
	ReservedAt        time.Time
	ExpiresAt         time.Time
	CompletedAt       *time.Time
	InputTokens       *int64
	OutputTokens      *int64
	LatencyMS         *int64
}

// InsertBudgetReservation inserts a new reservation.
func InsertBudgetReservation(ctx context.Context, r *BudgetReservation) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMBudgetReservation{
			ID:                r.ID,
			ProfileRevisionID: r.ProfileRevisionID,
			Kind:              r.Kind,
			Purpose:           r.Purpose,
			DecisionRunID:     nullString(r.DecisionRunID),
			Status:            r.Status,
			ReservedAt:        tsToText(r.ReservedAt),
			ExpiresAt:         tsToText(r.ExpiresAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMBudgetReservation{
		ID:                r.ID,
		ProfileRevisionID: r.ProfileRevisionID,
		Kind:              r.Kind,
		Purpose:           r.Purpose,
		DecisionRunID:     nullString(r.DecisionRunID),
		Status:            r.Status,
		ReservedAt:        r.ReservedAt.UTC(),
		ExpiresAt:         r.ExpiresAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// SettleBudgetReservation writes only the settlement columns.
func SettleBudgetReservation(ctx context.Context, id, status string, completedAt time.Time, inputTokens, outputTokens, latencyMS *int64) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMBudgetReservations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			colStatus:       status,
			"completed_at":  tsToText(completedAt),
			"input_tokens":  nullInt(inputTokens),
			"output_tokens": nullInt(outputTokens),
			"latency_ms":    nullInt(latencyMS),
		})
		return err
	}
	_, err := modelPSQL.LLMBudgetReservations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		colStatus:       status,
		"completed_at":  completedAt.UTC(),
		"input_tokens":  pgInt(inputTokens),
		"output_tokens": pgInt(outputTokens),
		"latency_ms":    pgInt(latencyMS),
	})
	return err
}

func budgetReservationFromSQLite(row *modelSQLite.LLMBudgetReservation) (*BudgetReservation, error) {
	reservedAt, err := tsFromText(row.ReservedAt)
	if err != nil {
		return nil, err
	}
	expiresAt, err := tsFromText(row.ExpiresAt)
	if err != nil {
		return nil, err
	}
	out := &BudgetReservation{
		ID:                row.ID,
		ProfileRevisionID: row.ProfileRevisionID,
		Kind:              row.Kind,
		Purpose:           row.Purpose,
		DecisionRunID:     stringPtr(row.DecisionRunID),
		Status:            row.Status,
		ReservedAt:        reservedAt,
		ExpiresAt:         expiresAt,
		InputTokens:       intPtr(row.InputTokens),
		OutputTokens:      intPtr(row.OutputTokens),
		LatencyMS:         intPtr(row.LatencyMS),
	}
	if row.CompletedAt.Valid {
		t, err := tsFromText(row.CompletedAt.String)
		if err != nil {
			return nil, err
		}
		out.CompletedAt = &t
	}
	return out, nil
}

// TryReserveBudget atomically reserves one slot of the 24h cap: the INSERT
// only lands while the window count stays below cap. A row counts against the
// cap when it is USED, or when it is still RESERVED and unexpired -- stale
// reservations from a dead process must not block the window forever.
// Returns false (nil error) when the cap is reached.
func TryReserveBudget(ctx context.Context, r *BudgetReservation, since time.Time, limit int64, now time.Time) (bool, error) {
	if err := available(); err != nil {
		return false, err
	}
	var decisionRunID any
	if r.DecisionRunID != nil {
		decisionRunID = *r.DecisionRunID
	}
	const sqliteInsert = `INSERT INTO llm_budget_reservations
		(id, profile_revision_id, kind, purpose, decision_run_id, status, reserved_at, expires_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE (
			SELECT COUNT(*) FROM llm_budget_reservations
			WHERE profile_revision_id = ?
			  AND reserved_at >= ?
			  AND (status = 'USED' OR (status = 'RESERVED' AND expires_at > ?))
		) < ?`
	const psqlInsert = `INSERT INTO llm_budget_reservations
		(id, profile_revision_id, kind, purpose, decision_run_id, status, reserved_at, expires_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8
		WHERE (
			SELECT COUNT(*) FROM llm_budget_reservations
			WHERE profile_revision_id = $2
			  AND reserved_at >= $9
			  AND (status = 'USED' OR (status = 'RESERVED' AND expires_at > $10))
		) < $11`
	var result sql.Result
	var err error
	if dialectIsSQLite() {
		result, err = database.DB.SQL.ExecContext(ctx, sqliteInsert,
			r.ID, r.ProfileRevisionID, r.Kind, r.Purpose, decisionRunID, r.Status, tsToText(r.ReservedAt), tsToText(r.ExpiresAt),
			r.ProfileRevisionID, tsToText(since), tsToText(now), limit)
	} else {
		result, err = database.DB.SQL.ExecContext(ctx, psqlInsert,
			r.ID, r.ProfileRevisionID, r.Kind, r.Purpose, decisionRunID, r.Status, r.ReservedAt.UTC(), r.ExpiresAt.UTC(),
			since.UTC(), now.UTC(), limit)
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// GetBudgetReservation returns a reservation by id.
func GetBudgetReservation(ctx context.Context, id string) (*BudgetReservation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMBudgetReservations(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return budgetReservationFromSQLite(row)
	}
	row, err := modelPSQL.LLMBudgetReservations(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &BudgetReservation{
		ID:                row.ID,
		ProfileRevisionID: row.ProfileRevisionID,
		Kind:              row.Kind,
		Purpose:           row.Purpose,
		DecisionRunID:     stringPtr(row.DecisionRunID),
		Status:            row.Status,
		ReservedAt:        row.ReservedAt.UTC(),
		ExpiresAt:         row.ExpiresAt.UTC(),
		CompletedAt:       timePtr(row.CompletedAt),
		InputTokens:       pgIntPtr(row.InputTokens),
		OutputTokens:      pgIntPtr(row.OutputTokens),
		LatencyMS:         pgIntPtr(row.LatencyMS),
	}, nil
}

// ListReservationsInWindow returns reservations of a profile revision created
// at or after since, oldest first. The caller aggregates by status.
func ListReservationsInWindow(ctx context.Context, profileRevisionID string, since time.Time) ([]BudgetReservation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{
		qm.Where("profile_revision_id = ?", profileRevisionID),
		qm.OrderBy("reserved_at"),
	}
	out := make([]BudgetReservation, 0)
	if dialectIsSQLite() {
		queries = append(queries, qm.Where("reserved_at >= ?", tsToText(since)))
		rows, err := modelSQLite.LLMBudgetReservations(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			r, err := budgetReservationFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
		return out, nil
	}
	queries = append(queries, qm.Where("reserved_at >= ?", since.UTC()))
	rows, err := modelPSQL.LLMBudgetReservations(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, BudgetReservation{
			ID:                row.ID,
			ProfileRevisionID: row.ProfileRevisionID,
			Kind:              row.Kind,
			Purpose:           row.Purpose,
			DecisionRunID:     stringPtr(row.DecisionRunID),
			Status:            row.Status,
			ReservedAt:        row.ReservedAt.UTC(),
			ExpiresAt:         row.ExpiresAt.UTC(),
			CompletedAt:       timePtr(row.CompletedAt),
			InputTokens:       pgIntPtr(row.InputTokens),
			OutputTokens:      pgIntPtr(row.OutputTokens),
			LatencyMS:         pgIntPtr(row.LatencyMS),
		})
	}
	return out, nil
}

// NewRiskCounter mirrors llm_new_risk_counters.
type NewRiskCounter struct {
	ID            string
	AccountID     string
	Scope         string
	ScopeID       string
	IntentID      string
	CountedAt     time.Time
	WindowMinutes int64
	ReleasedAt    *time.Time
}

// InsertNewRiskCounter inserts a new-risk counter row.
func InsertNewRiskCounter(ctx context.Context, r *NewRiskCounter) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMNewRiskCounter{
			ID:            r.ID,
			AccountID:     r.AccountID,
			Scope:         r.Scope,
			ScopeID:       r.ScopeID,
			IntentID:      r.IntentID,
			CountedAt:     tsToText(r.CountedAt),
			WindowMinutes: r.WindowMinutes,
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMNewRiskCounter{
		ID:            r.ID,
		AccountID:     r.AccountID,
		Scope:         r.Scope,
		ScopeID:       r.ScopeID,
		IntentID:      r.IntentID,
		CountedAt:     r.CountedAt.UTC(),
		WindowMinutes: int(r.WindowMinutes),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ReleaseNewRiskCounter marks a counter released (e.g. after the intent is
// canceled) so it no longer counts toward the window.
func ReleaseNewRiskCounter(ctx context.Context, id string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMNewRiskCounters(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{"released_at": tsToText(at)})
		return err
	}
	_, err := modelPSQL.LLMNewRiskCounters(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{"released_at": at.UTC()})
	return err
}

// CountNewRiskInWindow counts unreleased new-risk counters of an account
// counted at or after since — the sliding-window query behind the new-risk cap.
func CountNewRiskInWindow(ctx context.Context, accountID string, since time.Time) (int64, error) {
	if err := available(); err != nil {
		return 0, err
	}
	var count int64
	if dialectIsSQLite() {
		err := database.DB.SQL.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM llm_new_risk_counters WHERE account_id = ? AND counted_at >= ? AND released_at IS NULL",
			accountID, tsToText(since)).Scan(&count)
		return count, err
	}
	err := database.DB.SQL.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM llm_new_risk_counters WHERE account_id = $1 AND counted_at >= $2 AND released_at IS NULL",
		accountID, since.UTC()).Scan(&count)
	return count, err
}
