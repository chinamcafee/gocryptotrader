// Package llm implements the repository layer for the llm_* tables
// (see docs/llm-dev/09-数据存储与配置.md §3.3). It follows the conventions of
// the other repositories under database/repository: a dialect branch between
// the sqlite3 and postgres models, transactions for multi-statement writes,
// and neutral request/response types so callers (engine/, never llm/ -- the
// boundary test forbids that import) do not need to know which dialect is live.
//
// Timestamps cross this boundary as time.Time. The sqlite3 driver stores them
// as RFC3339 text, the postgres driver as TIMESTAMPTZ; the conversion happens
// here and nowhere else.
package llm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	"github.com/thrasher-corp/gocryptotrader/database/repository"
	"github.com/volatiletech/null"
)

// ErrDatabaseUnavailable is returned by every operation when the database is
// not configured or not connected. Unlike the fire-and-forget upstream audit
// repository, llm operations must fail loudly: an audit event or a budget
// reservation that was silently dropped is worse than an error.
var ErrDatabaseUnavailable = errors.New("llm repository: database unavailable")

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("llm repository: record not found")

const timeFormat = time.RFC3339

func available() error {
	if database.DB == nil || database.DB.SQL == nil {
		return ErrDatabaseUnavailable
	}
	return nil
}

func dialectIsSQLite() bool {
	switch repository.GetSQLDialect() {
	case database.DBSQLite, database.DBSQLite3:
		return true
	}
	return false
}

// runInTx runs fn in a transaction, rolling back on error. fn's error is
// returned unwrapped if the rollback succeeds.
func runInTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := database.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginTx: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				err = fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
			}
		}
	}()
	if err = fn(tx); err != nil { //nolint:gocritic // err must stay in the outer scope: the deferred rollback keys off it
		return err
	}
	return tx.Commit()
}

func tsToText(t time.Time) string { return t.UTC().Format(timeFormat) }

func tsFromText(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

func nullString(p *string) null.String {
	if p == nil {
		return null.NewString("", false)
	}
	return null.NewString(*p, true)
}

func stringPtr(n null.String) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullInt(p *int64) null.Int64 {
	if p == nil {
		return null.NewInt64(0, false)
	}
	return null.NewInt64(*p, true)
}

func intPtr(n null.Int64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// pgInt converts a nullable *int64 for a postgres model, whose INTEGER
// columns map to null.Int (the sqlite3 generator maps them to null.Int64).
func pgInt(p *int64) null.Int {
	if p == nil {
		return null.NewInt(0, false)
	}
	return null.NewInt(int(*p), true)
}

func pgIntPtr(n null.Int) *int64 {
	if !n.Valid {
		return nil
	}
	v := int64(n.Int)
	return &v
}

func nullTime(p *time.Time) null.Time {
	if p == nil {
		return null.NewTime(time.Time{}, false)
	}
	return null.NewTime(*p, true)
}

func timePtr(n null.Time) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

func nullBytes(p []byte) null.Bytes {
	if p == nil {
		return null.NewBytes(nil, false)
	}
	return null.NewBytes(p, true)
}

func bytesPtr(n null.Bytes) []byte {
	if !n.Valid {
		return nil
	}
	return n.Bytes
}

// Column names shared by the raw queries: one spelling per column so a rename
// breaks compilation instead of a query string.
const (
	colStatus     = "status"
	colUpdatedAt  = "updated_at"
	colArchivedAt = "archived_at"
)
