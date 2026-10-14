package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-08 对账（docs 07 §9）。
//
// 每 5 分钟一轮最小可行对账：本地视图 vs 交易所实况逐仓比对。
// **差异 → 记 delta + `accountReconciled=false` + SOFT halt + 告警**；
// 无差异 → `accountReconciled=true`（有效期到下次对账——本文写的 validUntil）。
//
// `accountReconciled` 是硬门禁（M4-11 的 `RISK_ACCOUNT_NOT_RECONCILED` 拒一切
// 新开仓）：对账不是在发现问题时才介意的例行公事，而是"成交与仓位以交易所
// 查询为准"这条底线的落地。交易所侧的抓取（GetOrders/UAccountTradesHistory/
// UPositionsInfoV2/强平单）是调用方的 I/O，本层拥有比对语义与落账。

// Reconciliation constants (07 §9).
const (
	// ReconcileInterval is the documented cadence and the validity of a clean
	// result ("有效期到下次对账").
	ReconcileInterval = 5 * time.Minute
	// HaltLevelSoft is the level a mismatch trips: freeze new risk, keep exits.
	HaltLevelSoft = "SOFT"
	// ReasonReconcileMismatch marks the halt and the audit event.
	ReasonReconcileMismatch = "RECONCILE_MISMATCH"
	// AuditKindReconcileMismatch is the audit event kind for a mismatch.
	AuditKindReconcileMismatch = "RECONCILE_MISMATCH"
)

// ReconcilePosition is one market+side quantity as seen by either side.
type ReconcilePosition struct {
	MarketID string `json:"marketId"`
	Side     string `json:"side"`
	Quantity string `json:"quantity"`
}

// PositionDelta is one disagreement, with both readings kept.
type PositionDelta struct {
	MarketID string `json:"marketId"`
	Side     string `json:"side"`
	Local    string `json:"local"`
	Exchange string `json:"exchange"`
}

// ReconcileRequest is one pass's material. Order blobs are stored verbatim for
// the audit row; the position comparison is what decides the verdict.
type ReconcileRequest struct {
	AccountID          string
	Local              []ReconcilePosition
	Exchange           []ReconcilePosition
	LocalOrdersJSON    string
	ExchangeOrdersJSON string
	SetBy              string
}

// ReconcileResult reports the pass.
type ReconcileResult struct {
	ReconciliationID string          `json:"reconciliationId"`
	Reconciled       bool            `json:"reconciled"`
	Deltas           []PositionDelta `json:"deltas,omitempty"`
	HaltID           string          `json:"haltId,omitempty"`
}

// iReconciliationStore / iHaltStore are the write seams.
type iReconciliationStore interface {
	InsertReconciliation(ctx context.Context, r *llmstore.Reconciliation) error
	CompleteReconciliation(ctx context.Context, id, status string, deltasJSON *string, reconciled bool, validUntil *time.Time, at time.Time) error
}

type iHaltStore interface {
	InsertHalt(ctx context.Context, h *llmstore.Halt) error
}

// LLMReconciliations / LLMHalts adapt the repository.
type LLMReconciliations struct{}

// InsertReconciliation appends one run.
func (LLMReconciliations) InsertReconciliation(ctx context.Context, r *llmstore.Reconciliation) error {
	return llmstore.InsertReconciliation(ctx, r)
}

// CompleteReconciliation finalises one run.
func (LLMReconciliations) CompleteReconciliation(ctx context.Context, id, status string, deltasJSON *string, reconciled bool, validUntil *time.Time, at time.Time) error {
	return llmstore.CompleteReconciliation(ctx, id, status, deltasJSON, reconciled, validUntil, at)
}

// LLMHalts adapts the repository.
type LLMHalts struct{}

// InsertHalt appends one halt record.
func (LLMHalts) InsertHalt(ctx context.Context, h *llmstore.Halt) error {
	return llmstore.InsertHalt(ctx, h)
}

// Reconciler runs one reconciliation pass.
type Reconciler struct {
	Runs  iReconciliationStore
	Halts iHaltStore
	Audit iAuditEvents
	Now   func() time.Time
	NewID func() string
}

// DiffPositions compares two position readings. Quantities compare numerically
// (0.1 equals 0.10); a parse failure compares raw, which can only widen the
// delta -- an unreadable number is not a match. Output is sorted for replay.
func DiffPositions(local, exchange []ReconcilePosition) []PositionDelta {
	type key struct{ market, side string }
	index := func(rows []ReconcilePosition) map[key]string {
		out := make(map[key]string, len(rows))
		for _, row := range rows {
			out[key{row.MarketID, strings.ToUpper(strings.TrimSpace(row.Side))}] = strings.TrimSpace(row.Quantity)
		}
		return out
	}
	localIndex := index(local)
	exchangeIndex := index(exchange)
	keys := make(map[key]bool, len(localIndex)+len(exchangeIndex))
	for k := range localIndex {
		keys[k] = true
	}
	for k := range exchangeIndex {
		keys[k] = true
	}
	sorted := make([]key, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].market != sorted[j].market {
			return sorted[i].market < sorted[j].market
		}
		return sorted[i].side < sorted[j].side
	})
	var deltas []PositionDelta
	for _, k := range sorted {
		localQty, hasLocal := localIndex[k]
		exchangeQty, hasExchange := exchangeIndex[k]
		if hasLocal && hasExchange && quantitiesMatch(localQty, exchangeQty) {
			continue
		}
		if !hasLocal {
			localQty = "0"
		}
		if !hasExchange {
			exchangeQty = "0"
		}
		deltas = append(deltas, PositionDelta{MarketID: k.market, Side: k.side, Local: localQty, Exchange: exchangeQty})
	}
	return deltas
}

// quantitiesMatch compares two decimal strings numerically.
func quantitiesMatch(a, b string) bool {
	da, errA := decParse(a)
	db, errB := decParse(b)
	if errA != nil || errB != nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return da.Equal(db)
}

// Pass executes one reconciliation and persists its verdict.
func (r *Reconciler) Pass(ctx context.Context, req *ReconcileRequest) (*ReconcileResult, error) {
	if r == nil || r.Runs == nil || r.Halts == nil || r.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return nil, fmt.Errorf("%w: account id is required", ErrIntentInputInvalid)
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	newID := r.NewID
	if newID == nil {
		newID = newLLMIdentifier
	}
	localJSON, err := json.Marshal(req.Local)
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: local snapshot json: %w", err)
	}
	exchangeJSON, err := json.Marshal(req.Exchange)
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: exchange snapshot json: %w", err)
	}
	runID := newID()
	if err := r.Runs.InsertReconciliation(ctx, &llmstore.Reconciliation{
		ID: runID, AccountID: req.AccountID, StartedAt: now, Status: "RUNNING",
		LocalPositionsJSON: string(localJSON), ExchangePositionsJSON: string(exchangeJSON),
		LocalOrdersJSON: req.LocalOrdersJSON, ExchangeOrdersJSON: req.ExchangeOrdersJSON,
	}); err != nil {
		return nil, fmt.Errorf("llm reconcile: inserting the run: %w", err)
	}
	result := &ReconcileResult{ReconciliationID: runID}
	deltas := DiffPositions(req.Local, req.Exchange)
	result.Deltas = deltas

	if len(deltas) == 0 {
		validUntil := now.Add(ReconcileInterval)
		if err := r.Runs.CompleteReconciliation(ctx, runID, "CLEAN", nil, true, &validUntil, now); err != nil {
			return nil, fmt.Errorf("llm reconcile: completing CLEAN: %w", err)
		}
		result.Reconciled = true
		return result, nil
	}

	deltasJSON, err := json.Marshal(deltas)
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: deltas json: %w", err)
	}
	text := string(deltasJSON)
	if err := r.Runs.CompleteReconciliation(ctx, runID, "MISMATCH", &text, false, nil, now); err != nil {
		return nil, fmt.Errorf("llm reconcile: completing MISMATCH: %w", err)
	}
	haltID := newID()
	actor := req.SetBy
	if actor == "" {
		actor = auditActorSystem
	}
	if err := r.Halts.InsertHalt(ctx, &llmstore.Halt{
		ID: haltID, Level: HaltLevelSoft, Reason: ReasonReconcileMismatch,
		SetBy: actor, SetAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm reconcile: setting the halt: %w", err)
	}
	result.HaltID = haltID

	detail, err := json.Marshal(map[string]any{
		"reconciliationId": runID,
		"accountId":        req.AccountID,
		"haltId":           haltID,
		"deltas":           deltas,
	})
	if err != nil {
		return nil, fmt.Errorf("llm reconcile: audit detail: %w", err)
	}
	subject := runID
	if err := r.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: newID(), Kind: AuditKindReconcileMismatch, Actor: actor,
		Subject: &subject, DetailJSON: string(detail), CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm reconcile: audit event: %w", err)
	}
	return result, nil
}
