package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// Budget constants (docs 05 §7.1). The hard gates are the 24h task-reservation
// count and per-request token caps; dailyBudgetUsd is a plan value except for
// the exactly-zero state, which forbids every model request.
const (
	// DefaultReservationCap24H applies when a revision does not configure a cap.
	DefaultReservationCap24H = 100
	// JevReservationCap24H caps Jev reservations regardless of configuration.
	JevReservationCap24H = 100
	// ReservationWindow is the sliding window the cap counts over.
	ReservationWindow = 24 * time.Hour
	// DefaultReservationTTL bounds an in-flight reservation; stale RESERVED
	// rows (dead process) stop counting once past it.
	DefaultReservationTTL = 5 * time.Minute

	reservationStatusReserved = "RESERVED"
	reservationStatusUsed     = "USED"
	reservationStatusReleased = "RELEASED"
)

var (
	// ErrBudgetDisabled is returned when the revision's daily budget is
	// exactly "0": that state forbids all model requests.
	ErrBudgetDisabled = errors.New("llm budget: revision forbids model requests (daily budget is exactly 0)")
	// ErrBudgetExhausted is returned when the 24h reservation cap is reached.
	ErrBudgetExhausted = errors.New("llm budget: 24h reservation cap reached")
)

// EffectiveReservationCap resolves the hard 24h cap of a revision: an unset
// (zero) configuration means the default, and the Jev endpoint is never
// allowed past its ceiling.
func EffectiveReservationCap(kind string, configured int64) int64 {
	if configured <= 0 {
		configured = DefaultReservationCap24H
	}
	if kind == ProviderKindJev && configured > JevReservationCap24H {
		return JevReservationCap24H
	}
	return configured
}

// BudgetReservationRequest asks for one reservation slot before a model call.
type BudgetReservationRequest struct {
	ProfileRevisionID string
	// ProviderKind, when set, must match the revision's kind.
	ProviderKind string
	// Purpose is a free-form reason (DECISION / CHAT / PROPOSAL / PROBE ...).
	Purpose string
	// DecisionRunID links the reservation to its decision run when known.
	DecisionRunID *string
}

// ReserveBudget enforces the pre-call gates and takes one slot of the 24h cap
// atomically. Callers must follow up with MarkReservationUsed on success or
// ReleaseReservation on failure/timeout, so a released slot does not consume
// the quota (docs 05 §7.4).
func ReserveBudget(ctx context.Context, req BudgetReservationRequest, now time.Time) (*llmstore.BudgetReservation, error) {
	if strings.TrimSpace(req.Purpose) == "" {
		return nil, errors.New("llm budget: purpose is required")
	}
	revision, err := llmstore.GetProfileRevision(ctx, req.ProfileRevisionID)
	if err != nil {
		return nil, err
	}
	if req.ProviderKind != "" && req.ProviderKind != revision.Kind {
		return nil, fmt.Errorf("%w: revision is %s, request said %s", ErrRevisionKindMismatch, revision.Kind, req.ProviderKind)
	}
	if revision.SupersededAt != nil {
		return nil, fmt.Errorf("%w: %s", ErrRevisionNotLive, req.ProfileRevisionID)
	}
	if revision.DailyBudgetUSD != nil && *revision.DailyBudgetUSD == "0" {
		return nil, fmt.Errorf("%w: %s", ErrBudgetDisabled, req.ProfileRevisionID)
	}

	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	reservation := llmstore.BudgetReservation{
		ID:                id.String(),
		ProfileRevisionID: revision.ID,
		Kind:              revision.Kind,
		Purpose:           req.Purpose,
		DecisionRunID:     req.DecisionRunID,
		Status:            reservationStatusReserved,
		ReservedAt:        now,
		ExpiresAt:         now.Add(DefaultReservationTTL),
	}
	cap24h := EffectiveReservationCap(revision.Kind, revision.ReservationCap24H)
	applied, err := llmstore.TryReserveBudget(ctx, &reservation, now.Add(-ReservationWindow), cap24h, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, fmt.Errorf("%w: %s (cap %d)", ErrBudgetExhausted, req.ProfileRevisionID, cap24h)
	}
	return &reservation, nil
}

// MarkReservationUsed settles a reservation after a successful model call,
// recording the token usage read from the response.
func MarkReservationUsed(ctx context.Context, id string, inputTokens, outputTokens, latencyMS int64, at time.Time) error {
	return llmstore.SettleBudgetReservation(ctx, id, reservationStatusUsed, at, &inputTokens, &outputTokens, &latencyMS)
}

// ReleaseReservation settles a failed or timed-out reservation. The slot does
// not count against the cap afterwards.
func ReleaseReservation(ctx context.Context, id string, at time.Time) error {
	return llmstore.SettleBudgetReservation(ctx, id, reservationStatusReleased, at, nil, nil, nil)
}
