package llm

import (
	"context"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	modelPSQL "github.com/thrasher-corp/gocryptotrader/database/models/postgres"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/boil"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
)

// AuditEvent mirrors llm_audit_events (append-only; updates and deletes are
// rejected by database triggers).
type AuditEvent struct {
	ID         string
	Kind       string
	Actor      string
	Subject    *string
	DetailJSON string
	CreatedAt  time.Time
}

// InsertAuditEvent appends a governance audit event.
func InsertAuditEvent(ctx context.Context, e *AuditEvent) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMAuditEvent{
			ID:         e.ID,
			Kind:       e.Kind,
			Actor:      e.Actor,
			Subject:    nullString(e.Subject),
			DetailJSON: e.DetailJSON,
			CreatedAt:  tsToText(e.CreatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMAuditEvent{
		ID:         e.ID,
		Kind:       e.Kind,
		Actor:      e.Actor,
		Subject:    nullString(e.Subject),
		DetailJSON: e.DetailJSON,
		CreatedAt:  e.CreatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ListAuditEvents returns audit events newest first, optionally filtered by
// kind, capped by limit (0 = no cap).
func ListAuditEvents(ctx context.Context, kind string, limit int) ([]AuditEvent, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("created_at DESC"), qm.OrderBy("id")}
	if kind != "" {
		queries = append(queries, qm.Where("kind = ?", kind))
	}
	if limit > 0 {
		queries = append(queries, qm.Limit(limit))
	}
	out := make([]AuditEvent, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMAuditEvents(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, AuditEvent{
				ID:         row.ID,
				Kind:       row.Kind,
				Actor:      row.Actor,
				Subject:    stringPtr(row.Subject),
				DetailJSON: row.DetailJSON,
				CreatedAt:  createdAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMAuditEvents(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, AuditEvent{
			ID:         row.ID,
			Kind:       row.Kind,
			Actor:      row.Actor,
			Subject:    stringPtr(row.Subject),
			DetailJSON: row.DetailJSON,
			CreatedAt:  row.CreatedAt.UTC(),
		})
	}
	return out, nil
}
