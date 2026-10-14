package llm

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	modelPSQL "github.com/thrasher-corp/gocryptotrader/database/models/postgres"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/boil"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
)

// OrderIntent mirrors llm_order_intents. Only the lifecycle columns
// (status / internal_order_id / exchange_order_id / updated_at) may change
// after insert; the database trigger rejects anything else.
type OrderIntent struct {
	ID                string
	DecisionRunID     string
	RiskEvaluationID  string
	StrategyVersionID string
	ApprovalID        string
	Exchange          string
	Pair              string
	Asset             string
	Action            string
	Side              string
	OrderType         string
	Quantity          string
	Price             *string
	ReduceOnly        bool
	ClientOrderID     string
	NotionalUSD       string
	EnvelopeHash      string
	PairEpoch         int64
	Status            string
	InternalOrderID   *string
	ExchangeOrderID   *string
	ExpiresAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// InsertOrderIntent inserts a new order intent.
func InsertOrderIntent(ctx context.Context, o *OrderIntent) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var reduceOnly int64
		if o.ReduceOnly {
			reduceOnly = 1
		}
		row := &modelSQLite.LLMOrderIntent{
			ID:                o.ID,
			DecisionRunID:     o.DecisionRunID,
			RiskEvaluationID:  o.RiskEvaluationID,
			StrategyVersionID: o.StrategyVersionID,
			ApprovalID:        o.ApprovalID,
			Exchange:          o.Exchange,
			Pair:              o.Pair,
			Asset:             o.Asset,
			Action:            o.Action,
			Side:              o.Side,
			OrderType:         o.OrderType,
			Quantity:          o.Quantity,
			Price:             nullString(o.Price),
			ReduceOnly:        reduceOnly,
			ClientOrderID:     o.ClientOrderID,
			NotionalUsd:       o.NotionalUSD,
			EnvelopeHash:      o.EnvelopeHash,
			PairEpoch:         o.PairEpoch,
			Status:            o.Status,
			InternalOrderID:   nullString(o.InternalOrderID),
			ExchangeOrderID:   nullString(o.ExchangeOrderID),
			ExpiresAt:         tsToText(o.ExpiresAt),
			CreatedAt:         tsToText(o.CreatedAt),
			UpdatedAt:         tsToText(o.UpdatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMOrderIntent{
		ID:                o.ID,
		DecisionRunID:     o.DecisionRunID,
		RiskEvaluationID:  o.RiskEvaluationID,
		StrategyVersionID: o.StrategyVersionID,
		ApprovalID:        o.ApprovalID,
		Exchange:          o.Exchange,
		Pair:              o.Pair,
		Asset:             o.Asset,
		Action:            o.Action,
		Side:              o.Side,
		OrderType:         o.OrderType,
		Quantity:          o.Quantity,
		Price:             nullString(o.Price),
		ReduceOnly:        o.ReduceOnly,
		ClientOrderID:     o.ClientOrderID,
		NotionalUsd:       o.NotionalUSD,
		EnvelopeHash:      o.EnvelopeHash,
		PairEpoch:         int(o.PairEpoch),
		Status:            o.Status,
		InternalOrderID:   nullString(o.InternalOrderID),
		ExchangeOrderID:   nullString(o.ExchangeOrderID),
		ExpiresAt:         o.ExpiresAt.UTC(),
		CreatedAt:         o.CreatedAt.UTC(),
		UpdatedAt:         o.UpdatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ClaimOrderIntent atomically moves an intent from PREPARED to SUBMITTING and
// reports whether this caller won the claim. The conditional update is the
// double-submit guard: at most one writer can ever hold the claim.
func ClaimOrderIntent(ctx context.Context, id string, at time.Time) (bool, error) {
	if err := available(); err != nil {
		return false, err
	}
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMOrderIntents(qm.Where("id = ? AND status = ?", id, "PREPARED")).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			colStatus:    "SUBMITTING",
			colUpdatedAt: tsToText(at),
		})
		if err != nil {
			return false, err
		}
		return rows == 1, nil
	}
	rows, err := modelPSQL.LLMOrderIntents(qm.Where("id = ? AND status = ?", id, "PREPARED")).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		colStatus:    "SUBMITTING",
		colUpdatedAt: at.UTC(),
	})
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// UpdateOrderIntentLifecycle advances the lifecycle columns of an intent.
func UpdateOrderIntentLifecycle(ctx context.Context, id, status string, internalOrderID, exchangeOrderID *string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMOrderIntents(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			colStatus:           status,
			"internal_order_id": nullString(internalOrderID),
			"exchange_order_id": nullString(exchangeOrderID),
			colUpdatedAt:        tsToText(at),
		})
		return err
	}
	_, err := modelPSQL.LLMOrderIntents(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		colStatus:           status,
		"internal_order_id": nullString(internalOrderID),
		"exchange_order_id": nullString(exchangeOrderID),
		colUpdatedAt:        at.UTC(),
	})
	return err
}

func orderIntentFromSQLite(row *modelSQLite.LLMOrderIntent) (*OrderIntent, error) {
	expiresAt, err := tsFromText(row.ExpiresAt)
	if err != nil {
		return nil, err
	}
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	updatedAt, err := tsFromText(row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &OrderIntent{
		ID:                row.ID,
		DecisionRunID:     row.DecisionRunID,
		RiskEvaluationID:  row.RiskEvaluationID,
		StrategyVersionID: row.StrategyVersionID,
		ApprovalID:        row.ApprovalID,
		Exchange:          row.Exchange,
		Pair:              row.Pair,
		Asset:             row.Asset,
		Action:            row.Action,
		Side:              row.Side,
		OrderType:         row.OrderType,
		Quantity:          row.Quantity,
		Price:             stringPtr(row.Price),
		ReduceOnly:        row.ReduceOnly == 1,
		ClientOrderID:     row.ClientOrderID,
		NotionalUSD:       row.NotionalUsd,
		EnvelopeHash:      row.EnvelopeHash,
		PairEpoch:         row.PairEpoch,
		Status:            row.Status,
		InternalOrderID:   stringPtr(row.InternalOrderID),
		ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
		ExpiresAt:         expiresAt,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, nil
}

// GetOrderIntent returns an intent by id.
func GetOrderIntent(ctx context.Context, id string) (*OrderIntent, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMOrderIntents(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return orderIntentFromSQLite(row)
	}
	row, err := modelPSQL.LLMOrderIntents(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &OrderIntent{
		ID:                row.ID,
		DecisionRunID:     row.DecisionRunID,
		RiskEvaluationID:  row.RiskEvaluationID,
		StrategyVersionID: row.StrategyVersionID,
		ApprovalID:        row.ApprovalID,
		Exchange:          row.Exchange,
		Pair:              row.Pair,
		Asset:             row.Asset,
		Action:            row.Action,
		Side:              row.Side,
		OrderType:         row.OrderType,
		Quantity:          row.Quantity,
		Price:             stringPtr(row.Price),
		ReduceOnly:        row.ReduceOnly,
		ClientOrderID:     row.ClientOrderID,
		NotionalUSD:       row.NotionalUsd,
		EnvelopeHash:      row.EnvelopeHash,
		PairEpoch:         int64(row.PairEpoch),
		Status:            row.Status,
		InternalOrderID:   stringPtr(row.InternalOrderID),
		ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
		ExpiresAt:         row.ExpiresAt.UTC(),
		CreatedAt:         row.CreatedAt.UTC(),
		UpdatedAt:         row.UpdatedAt.UTC(),
	}, nil
}

// ListOrderIntentsByDecisionRun returns the intents created for a decision
// run, oldest first.
func ListOrderIntentsByDecisionRun(ctx context.Context, decisionRunID string) ([]OrderIntent, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("decision_run_id = ?", decisionRunID), qm.OrderBy("created_at")}
	out := make([]OrderIntent, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMOrderIntents(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			o, err := orderIntentFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *o)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMOrderIntents(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, OrderIntent{
			ID:                row.ID,
			DecisionRunID:     row.DecisionRunID,
			RiskEvaluationID:  row.RiskEvaluationID,
			StrategyVersionID: row.StrategyVersionID,
			ApprovalID:        row.ApprovalID,
			Exchange:          row.Exchange,
			Pair:              row.Pair,
			Asset:             row.Asset,
			Action:            row.Action,
			Side:              row.Side,
			OrderType:         row.OrderType,
			Quantity:          row.Quantity,
			Price:             stringPtr(row.Price),
			ReduceOnly:        row.ReduceOnly,
			ClientOrderID:     row.ClientOrderID,
			NotionalUSD:       row.NotionalUsd,
			EnvelopeHash:      row.EnvelopeHash,
			PairEpoch:         int64(row.PairEpoch),
			Status:            row.Status,
			InternalOrderID:   stringPtr(row.InternalOrderID),
			ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
			ExpiresAt:         row.ExpiresAt.UTC(),
			CreatedAt:         row.CreatedAt.UTC(),
			UpdatedAt:         row.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

// ListOrderIntentsByStatus returns the intents currently in one lifecycle
// status, oldest first. It is the crash-recovery read: after a restart this
// is how the process finds claims (SUBMITTING) that never got an outcome.
func ListOrderIntentsByStatus(ctx context.Context, status string) ([]OrderIntent, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("status = ?", status), qm.OrderBy("created_at")}
	out := make([]OrderIntent, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMOrderIntents(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			o, err := orderIntentFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *o)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMOrderIntents(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, OrderIntent{
			ID:                row.ID,
			DecisionRunID:     row.DecisionRunID,
			RiskEvaluationID:  row.RiskEvaluationID,
			StrategyVersionID: row.StrategyVersionID,
			ApprovalID:        row.ApprovalID,
			Exchange:          row.Exchange,
			Pair:              row.Pair,
			Asset:             row.Asset,
			Action:            row.Action,
			Side:              row.Side,
			OrderType:         row.OrderType,
			Quantity:          row.Quantity,
			Price:             stringPtr(row.Price),
			ReduceOnly:        row.ReduceOnly,
			ClientOrderID:     row.ClientOrderID,
			NotionalUSD:       row.NotionalUsd,
			EnvelopeHash:      row.EnvelopeHash,
			PairEpoch:         int64(row.PairEpoch),
			Status:            row.Status,
			InternalOrderID:   stringPtr(row.InternalOrderID),
			ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
			ExpiresAt:         row.ExpiresAt.UTC(),
			CreatedAt:         row.CreatedAt.UTC(),
			UpdatedAt:         row.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

// ProtectiveExit mirrors llm_protective_exits.
type ProtectiveExit struct {
	ID                string
	ParentIntentID    string
	ExitKind          string
	ClientOrderID     string
	Quantity          string
	TriggerPrice      string
	Status            string
	FailSafeTriggered bool
	InternalOrderID   *string
	ExchangeOrderID   *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// InsertProtectiveExit inserts a protective exit attached to a parent intent.
func InsertProtectiveExit(ctx context.Context, e *ProtectiveExit) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var failSafe int64
		if e.FailSafeTriggered {
			failSafe = 1
		}
		row := &modelSQLite.LLMProtectiveExit{
			ID:                e.ID,
			ParentIntentID:    e.ParentIntentID,
			ExitKind:          e.ExitKind,
			ClientOrderID:     e.ClientOrderID,
			Quantity:          e.Quantity,
			TriggerPrice:      e.TriggerPrice,
			Status:            e.Status,
			FailSafeTriggered: failSafe,
			InternalOrderID:   nullString(e.InternalOrderID),
			ExchangeOrderID:   nullString(e.ExchangeOrderID),
			CreatedAt:         tsToText(e.CreatedAt),
			UpdatedAt:         tsToText(e.UpdatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMProtectiveExit{
		ID:                e.ID,
		ParentIntentID:    e.ParentIntentID,
		ExitKind:          e.ExitKind,
		ClientOrderID:     e.ClientOrderID,
		Quantity:          e.Quantity,
		TriggerPrice:      e.TriggerPrice,
		Status:            e.Status,
		FailSafeTriggered: e.FailSafeTriggered,
		InternalOrderID:   nullString(e.InternalOrderID),
		ExchangeOrderID:   nullString(e.ExchangeOrderID),
		CreatedAt:         e.CreatedAt.UTC(),
		UpdatedAt:         e.UpdatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// UpdateProtectiveExitStatus advances a protective exit's status.
func UpdateProtectiveExitStatus(ctx context.Context, id, status string, failSafeTriggered bool, internalOrderID, exchangeOrderID *string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		var failSafe int64
		if failSafeTriggered {
			failSafe = 1
		}
		_, err := modelSQLite.LLMProtectiveExits(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{
			colStatus:             status,
			"fail_safe_triggered": failSafe,
			"internal_order_id":   nullString(internalOrderID),
			"exchange_order_id":   nullString(exchangeOrderID),
			colUpdatedAt:          tsToText(at),
		})
		return err
	}
	_, err := modelPSQL.LLMProtectiveExits(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{
		colStatus:             status,
		"fail_safe_triggered": failSafeTriggered,
		"internal_order_id":   nullString(internalOrderID),
		"exchange_order_id":   nullString(exchangeOrderID),
		colUpdatedAt:          at.UTC(),
	})
	return err
}

// ListOpenProtectiveExits returns exits that may still be live at the venue
// (PREPARED or PLACED). It is the restart-recovery read: after a reboot this is
// how the process learns which stops are supposed to be hanging.
func ListOpenProtectiveExits(ctx context.Context) ([]ProtectiveExit, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("status IN (?, ?)", "PREPARED", "PLACED"), qm.OrderBy("created_at")}
	out := make([]ProtectiveExit, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMProtectiveExits(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			updatedAt, err := tsFromText(row.UpdatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, ProtectiveExit{
				ID:                row.ID,
				ParentIntentID:    row.ParentIntentID,
				ExitKind:          row.ExitKind,
				ClientOrderID:     row.ClientOrderID,
				Quantity:          row.Quantity,
				TriggerPrice:      row.TriggerPrice,
				Status:            row.Status,
				FailSafeTriggered: row.FailSafeTriggered != 0,
				InternalOrderID:   stringPtr(row.InternalOrderID),
				ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
				CreatedAt:         createdAt,
				UpdatedAt:         updatedAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMProtectiveExits(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, ProtectiveExit{
			ID:                row.ID,
			ParentIntentID:    row.ParentIntentID,
			ExitKind:          row.ExitKind,
			ClientOrderID:     row.ClientOrderID,
			Quantity:          row.Quantity,
			TriggerPrice:      row.TriggerPrice,
			Status:            row.Status,
			FailSafeTriggered: row.FailSafeTriggered,
			InternalOrderID:   stringPtr(row.InternalOrderID),
			ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
			CreatedAt:         row.CreatedAt.UTC(),
			UpdatedAt:         row.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

// ListProtectiveExitsByParent returns protective exits of a parent intent.
func ListProtectiveExitsByParent(ctx context.Context, parentIntentID string) ([]ProtectiveExit, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("parent_intent_id = ?", parentIntentID), qm.OrderBy("created_at")}
	out := make([]ProtectiveExit, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMProtectiveExits(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			updatedAt, err := tsFromText(row.UpdatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, ProtectiveExit{
				ID:                row.ID,
				ParentIntentID:    row.ParentIntentID,
				ExitKind:          row.ExitKind,
				ClientOrderID:     row.ClientOrderID,
				Quantity:          row.Quantity,
				TriggerPrice:      row.TriggerPrice,
				Status:            row.Status,
				FailSafeTriggered: row.FailSafeTriggered == 1,
				InternalOrderID:   stringPtr(row.InternalOrderID),
				ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
				CreatedAt:         createdAt,
				UpdatedAt:         updatedAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMProtectiveExits(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, ProtectiveExit{
			ID:                row.ID,
			ParentIntentID:    row.ParentIntentID,
			ExitKind:          row.ExitKind,
			ClientOrderID:     row.ClientOrderID,
			Quantity:          row.Quantity,
			TriggerPrice:      row.TriggerPrice,
			Status:            row.Status,
			FailSafeTriggered: row.FailSafeTriggered,
			InternalOrderID:   stringPtr(row.InternalOrderID),
			ExchangeOrderID:   stringPtr(row.ExchangeOrderID),
			CreatedAt:         row.CreatedAt.UTC(),
			UpdatedAt:         row.UpdatedAt.UTC(),
		})
	}
	return out, nil
}

// LastEntryIntentAt returns the creation time of the most recent opening
// intent (OPEN_LONG/OPEN_SHORT) for one market.
//
// It is the data side of the risk engine's market-cooldown check ("time since
// this market was last entered"); the cooldown duration itself is a policy
// input raised by the assembler. ok is false when the market has no opening
// intent yet -- a market never entered is past any cooldown. Reduces are not
// entries and do not count.
func LastEntryIntentAt(ctx context.Context, exchange, pair, asset string) (time.Time, bool, error) {
	if err := available(); err != nil {
		return time.Time{}, false, err
	}
	if dialectIsSQLite() {
		var raw sql.NullString
		err := database.DB.SQL.QueryRowContext(ctx,
			"SELECT MAX(created_at) FROM llm_order_intents WHERE exchange = ? AND pair = ? AND asset = ? AND action IN (?, ?)",
			exchange, pair, asset, "OPEN_LONG", "OPEN_SHORT").Scan(&raw)
		if err != nil {
			return time.Time{}, false, err
		}
		if !raw.Valid {
			return time.Time{}, false, nil
		}
		at, err := tsFromText(raw.String)
		if err != nil {
			return time.Time{}, false, err
		}
		return at, true, nil
	}
	var at sql.NullTime
	err := database.DB.SQL.QueryRowContext(ctx,
		"SELECT MAX(created_at) FROM llm_order_intents WHERE exchange = $1 AND pair = $2 AND asset = $3 AND action IN ($4, $5)",
		exchange, pair, asset, "OPEN_LONG", "OPEN_SHORT").Scan(&at)
	if err != nil {
		return time.Time{}, false, err
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	return at.Time.UTC(), true, nil
}

// CountOrderIntentsByStatus counts the intents currently in one lifecycle
// status. It is the read behind the risk engine's unresolved-count facts:
// UNKNOWN intents that need reconciliation, and PARTIALLY_FILLED fills whose
// books have not settled.
func CountOrderIntentsByStatus(ctx context.Context, status string) (int64, error) {
	if err := available(); err != nil {
		return 0, err
	}
	var count int64
	if dialectIsSQLite() {
		err := database.DB.SQL.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM llm_order_intents WHERE status = ?", status).Scan(&count)
		return count, err
	}
	err := database.DB.SQL.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM llm_order_intents WHERE status = $1", status).Scan(&count)
	return count, err
}

// CountOrderIntentsInWindow counts order intents created at or after since,
// optionally narrowed to one strategy version and one action.
//
// The filters are equality predicates rather than an IN list so both dialects
// share one statement shape: the risk frequency rules count per version, per
// action and account-wide, and the caller picks the filter matching the rule
// it enforces. The prospective order is NOT included -- the caller adds it
// when asking "would this order breach the window".
func CountOrderIntentsInWindow(ctx context.Context, strategyVersionID, action string, since time.Time) (int64, error) {
	if err := available(); err != nil {
		return 0, err
	}
	clauses := []string{"created_at >= ?"}
	args := []any{tsToText(since)}
	if strings.TrimSpace(strategyVersionID) != "" {
		clauses = append(clauses, "strategy_version_id = ?")
		args = append(args, strategyVersionID)
	}
	if strings.TrimSpace(action) != "" {
		clauses = append(clauses, "action = ?")
		args = append(args, action)
	}
	if dialectIsSQLite() {
		var count int64
		err := database.DB.SQL.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM llm_order_intents WHERE "+strings.Join(clauses, " AND "), args...).Scan(&count)
		return count, err
	}
	pgClauses := make([]string, len(clauses))
	for i := range clauses {
		pgClauses[i] = strings.Replace(clauses[i], "?", fmt.Sprintf("$%d", i+1), 1)
	}
	pgArgs := make([]any, len(args))
	copy(pgArgs, args)
	pgArgs[0] = since.UTC()
	var count int64
	err := database.DB.SQL.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM llm_order_intents WHERE "+strings.Join(pgClauses, " AND "), pgArgs...).Scan(&count)
	return count, err
}
