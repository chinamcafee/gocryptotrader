package llm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	modelPSQL "github.com/thrasher-corp/gocryptotrader/database/models/postgres"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/boil"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
)

// DayStartInventory mirrors llm_day_start_inventory (append-only).
type DayStartInventory struct {
	ID            string
	AccountID     string
	UtcDay        string
	EquityUsdt    string
	BalanceUsdt   string
	UnrealisedPNL string
	PositionsJSON string
	Source        string
	ObservedAt    time.Time
	SealedAt      time.Time
}

// InsertDayStartInventory appends a day-start inventory snapshot.
func InsertDayStartInventory(ctx context.Context, d *DayStartInventory) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMDayStartInventory{
			ID:            d.ID,
			AccountID:     d.AccountID,
			UtcDay:        d.UtcDay,
			EquityUsdt:    d.EquityUsdt,
			BalanceUsdt:   d.BalanceUsdt,
			UnrealisedPNL: d.UnrealisedPNL,
			PositionsJSON: d.PositionsJSON,
			Source:        d.Source,
			ObservedAt:    tsToText(d.ObservedAt),
			SealedAt:      tsToText(d.SealedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMDayStartInventory{
		ID:            d.ID,
		AccountID:     d.AccountID,
		UtcDay:        d.UtcDay,
		EquityUsdt:    d.EquityUsdt,
		BalanceUsdt:   d.BalanceUsdt,
		UnrealisedPNL: d.UnrealisedPNL,
		PositionsJSON: d.PositionsJSON,
		Source:        d.Source,
		ObservedAt:    d.ObservedAt.UTC(),
		SealedAt:      d.SealedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// GetDayStartInventory returns the (account, utc_day) day-start row.
func GetDayStartInventory(ctx context.Context, accountID, utcDay string) (*DayStartInventory, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMDayStartInventories(qm.Where("account_id = ? AND utc_day = ?", accountID, utcDay)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		observedAt, err := tsFromText(row.ObservedAt)
		if err != nil {
			return nil, err
		}
		sealedAt, err := tsFromText(row.SealedAt)
		if err != nil {
			return nil, err
		}
		return &DayStartInventory{
			ID:            row.ID,
			AccountID:     row.AccountID,
			UtcDay:        row.UtcDay,
			EquityUsdt:    row.EquityUsdt,
			BalanceUsdt:   row.BalanceUsdt,
			UnrealisedPNL: row.UnrealisedPNL,
			PositionsJSON: row.PositionsJSON,
			Source:        row.Source,
			ObservedAt:    observedAt,
			SealedAt:      sealedAt,
		}, nil
	}
	row, err := modelPSQL.LLMDayStartInventories(qm.Where("account_id = $1 AND utc_day = $2", accountID, utcDay)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &DayStartInventory{
		ID:            row.ID,
		AccountID:     row.AccountID,
		UtcDay:        row.UtcDay,
		EquityUsdt:    row.EquityUsdt,
		BalanceUsdt:   row.BalanceUsdt,
		UnrealisedPNL: row.UnrealisedPNL,
		PositionsJSON: row.PositionsJSON,
		Source:        row.Source,
		ObservedAt:    row.ObservedAt.UTC(),
		SealedAt:      row.SealedAt.UTC(),
	}, nil
}

// ExternalFlow mirrors llm_external_flows (append-only).
type ExternalFlow struct {
	ID         string
	AccountID  string
	UtcDay     string
	Direction  string
	AmountUsdt string
	Source     string
	ObservedAt time.Time
}

// InsertExternalFlow appends an external flow record.
func InsertExternalFlow(ctx context.Context, f *ExternalFlow) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMExternalFlow{
			ID:         f.ID,
			AccountID:  f.AccountID,
			UtcDay:     f.UtcDay,
			Direction:  f.Direction,
			AmountUsdt: f.AmountUsdt,
			Source:     f.Source,
			ObservedAt: tsToText(f.ObservedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMExternalFlow{
		ID:         f.ID,
		AccountID:  f.AccountID,
		UtcDay:     f.UtcDay,
		Direction:  f.Direction,
		AmountUsdt: f.AmountUsdt,
		Source:     f.Source,
		ObservedAt: f.ObservedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ListExternalFlows returns flows of an account on a UTC day oldest first.
func ListExternalFlows(ctx context.Context, accountID, utcDay string) ([]ExternalFlow, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("account_id = ? AND utc_day = ?", accountID, utcDay), qm.OrderBy("observed_at")}
	out := make([]ExternalFlow, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMExternalFlows(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			observedAt, err := tsFromText(row.ObservedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, ExternalFlow{
				ID:         row.ID,
				AccountID:  row.AccountID,
				UtcDay:     row.UtcDay,
				Direction:  row.Direction,
				AmountUsdt: row.AmountUsdt,
				Source:     row.Source,
				ObservedAt: observedAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMExternalFlows(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, ExternalFlow{
			ID:         row.ID,
			AccountID:  row.AccountID,
			UtcDay:     row.UtcDay,
			Direction:  row.Direction,
			AmountUsdt: row.AmountUsdt,
			Source:     row.Source,
			ObservedAt: row.ObservedAt.UTC(),
		})
	}
	return out, nil
}

// Reconciliation mirrors llm_reconciliations.
type Reconciliation struct {
	ID                    string
	AccountID             string
	StartedAt             time.Time
	CompletedAt           *time.Time
	Status                string
	LocalPositionsJSON    string
	ExchangePositionsJSON string
	LocalOrdersJSON       string
	ExchangeOrdersJSON    string
	DeltasJSON            *string
	Reconciled            bool
	ValidUntil            *time.Time
}

// InsertReconciliation inserts a new reconciliation run.
func InsertReconciliation(ctx context.Context, r *Reconciliation) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMReconciliation{
			ID:                    r.ID,
			AccountID:             r.AccountID,
			StartedAt:             tsToText(r.StartedAt),
			Status:                r.Status,
			LocalPositionsJSON:    r.LocalPositionsJSON,
			ExchangePositionsJSON: r.ExchangePositionsJSON,
			LocalOrdersJSON:       r.LocalOrdersJSON,
			ExchangeOrdersJSON:    r.ExchangeOrdersJSON,
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMReconciliation{
		ID:                    r.ID,
		AccountID:             r.AccountID,
		StartedAt:             r.StartedAt.UTC(),
		Status:                r.Status,
		LocalPositionsJSON:    r.LocalPositionsJSON,
		ExchangePositionsJSON: r.ExchangePositionsJSON,
		LocalOrdersJSON:       r.LocalOrdersJSON,
		ExchangeOrdersJSON:    r.ExchangeOrdersJSON,
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// CompleteReconciliation writes the completion columns of a run.
func CompleteReconciliation(ctx context.Context, id, status string, deltasJSON *string, reconciled bool, validUntil *time.Time, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var rec int64
		if reconciled {
			rec = 1
		}
		_, err := modelSQLite.LLMReconciliations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			colStatus:      status,
			"completed_at": tsToText(at),
			"deltas_json":  nullString(deltasJSON),
			"reconciled":   rec,
			"valid_until":  nullString(timeToTextPtr(validUntil)),
		})
		return err
	}
	_, err := modelPSQL.LLMReconciliations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		colStatus:      status,
		"completed_at": at.UTC(),
		"deltas_json":  nullString(deltasJSON),
		"reconciled":   reconciled,
		"valid_until":  nullTime(validUntil),
	})
	return err
}

// Halt mirrors llm_halts.
type Halt struct {
	ID                 string
	Level              string
	MarketID           *string
	Reason             string
	SetBy              string
	SetAt              time.Time
	ClearedBy          *string
	ClearedAt          *time.Time
	ClearReason        *string
	AffectedStrategies int64
	CanceledOrders     int64
}

// InsertHalt inserts a new halt record.
func InsertHalt(ctx context.Context, h *Halt) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMHalt{
			ID:                 h.ID,
			Level:              h.Level,
			MarketID:           nullString(h.MarketID),
			Reason:             h.Reason,
			SetBy:              h.SetBy,
			SetAt:              tsToText(h.SetAt),
			AffectedStrategies: h.AffectedStrategies,
			CanceledOrders:     h.CanceledOrders,
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMHalt{
		ID:                 h.ID,
		Level:              h.Level,
		MarketID:           nullString(h.MarketID),
		Reason:             h.Reason,
		SetBy:              h.SetBy,
		SetAt:              h.SetAt.UTC(),
		AffectedStrategies: int(h.AffectedStrategies),
		CanceledOrders:     int(h.CanceledOrders),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ClearHalt writes only the clear columns of a halt.
func ClearHalt(ctx context.Context, id string, at time.Time, by, reason string) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMHalts(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			"cleared_at":   tsToText(at),
			"cleared_by":   by,
			"clear_reason": reason,
		})
		return err
	}
	_, err := modelPSQL.LLMHalts(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		"cleared_at":   at.UTC(),
		"cleared_by":   by,
		"clear_reason": reason,
	})
	return err
}

// LatestReconciliation returns the newest reconciliation run by start time, or
// ErrNotFound when no pass has ever run. Readiness uses it to prove the account
// was reconciled and until when.
func LatestReconciliation(ctx context.Context) (*Reconciliation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("started_at DESC"), qm.Limit(1)}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMReconciliations(queries...).One(ctx, database.DB.SQL)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		startedAt, err := tsFromText(row.StartedAt)
		if err != nil {
			return nil, err
		}
		out := &Reconciliation{
			ID:         row.ID,
			AccountID:  row.AccountID,
			StartedAt:  startedAt,
			Status:     row.Status,
			Reconciled: row.Reconciled == 1,
			DeltasJSON: stringPtr(row.DeltasJSON),
		}
		if row.CompletedAt.Valid {
			completedAt, err := tsFromText(row.CompletedAt.String)
			if err != nil {
				return nil, err
			}
			out.CompletedAt = &completedAt
		}
		if row.ValidUntil.Valid {
			validUntil, err := tsFromText(row.ValidUntil.String)
			if err != nil {
				return nil, err
			}
			out.ValidUntil = &validUntil
		}
		return out, nil
	}
	row, err := modelPSQL.LLMReconciliations(queries...).One(ctx, database.DB.SQL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &Reconciliation{
		ID:         row.ID,
		AccountID:  row.AccountID,
		StartedAt:  row.StartedAt,
		Status:     row.Status,
		Reconciled: row.Reconciled,
	}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time
		out.CompletedAt = &completedAt
	}
	if row.ValidUntil.Valid {
		validUntil := row.ValidUntil.Time
		out.ValidUntil = &validUntil
	}
	if row.DeltasJSON.Valid {
		deltas := row.DeltasJSON.String
		out.DeltasJSON = &deltas
	}
	return out, nil
}

func haltFromSQLite(row *modelSQLite.LLMHalt) (*Halt, error) {
	setAt, err := tsFromText(row.SetAt)
	if err != nil {
		return nil, err
	}
	out := &Halt{
		ID:                 row.ID,
		Level:              row.Level,
		MarketID:           stringPtr(row.MarketID),
		Reason:             row.Reason,
		SetBy:              row.SetBy,
		SetAt:              setAt,
		ClearedBy:          stringPtr(row.ClearedBy),
		ClearReason:        stringPtr(row.ClearReason),
		AffectedStrategies: row.AffectedStrategies,
		CanceledOrders:     row.CanceledOrders,
	}
	if row.ClearedAt.Valid {
		t, err := tsFromText(row.ClearedAt.String)
		if err != nil {
			return nil, err
		}
		out.ClearedAt = &t
	}
	return out, nil
}

// ListActiveHalts returns halts that have not been cleared, newest first.
func ListActiveHalts(ctx context.Context) ([]Halt, error) {
	if err := available(); err != nil {
		return nil, err
	}
	out := make([]Halt, 0)
	queries := []qm.QueryMod{qm.Where("cleared_at IS NULL"), qm.OrderBy("set_at DESC")}
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMHalts(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			h, err := haltFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *h)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMHalts(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, Halt{
			ID:                 row.ID,
			Level:              row.Level,
			MarketID:           stringPtr(row.MarketID),
			Reason:             row.Reason,
			SetBy:              row.SetBy,
			SetAt:              row.SetAt.UTC(),
			ClearedBy:          stringPtr(row.ClearedBy),
			ClearedAt:          timePtr(row.ClearedAt),
			ClearReason:        stringPtr(row.ClearReason),
			AffectedStrategies: int64(row.AffectedStrategies),
			CanceledOrders:     int64(row.CanceledOrders),
		})
	}
	return out, nil
}
