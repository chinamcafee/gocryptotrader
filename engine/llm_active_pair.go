package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// Readiness states stored in llm_active_model_pair.readiness.
const (
	readinessReady    = "READY"
	readinessDegraded = "DEGRADED"
)

var (
	// ErrPairEpochConflict is returned when the active pair moved under the
	// caller: the CAS saw an epoch other than the expected one.
	ErrPairEpochConflict = errors.New("llm active pair: epoch conflict")
	// ErrMissingIdempotencyKey is returned when an activation carries no key.
	ErrMissingIdempotencyKey = errors.New("llm active pair: idempotency key is required")
	// ErrIdempotencyKeyReused is returned when a key is replayed with a
	// different payload than the command recorded under it.
	ErrIdempotencyKeyReused = errors.New("llm active pair: idempotency key reused with a different payload")
	// ErrRevisionNotLive is returned when a revision has already been
	// superseded; only live revisions may be activated.
	ErrRevisionNotLive = errors.New("llm active pair: revision is not the live revision of its profile")
	// ErrRevisionKindMismatch is returned when a revision's kind does not
	// match the slot it was put in (e.g. a Jev revision in the LLM slot).
	ErrRevisionKindMismatch = errors.New("llm active pair: revision kind does not match the slot")
)

// ActivatePairRequest asks for one CAS activation of the active model pair.
// ExpectedEpoch is the epoch the caller read; the write only lands if the
// stored epoch still equals it. IdempotencyKey makes retries safe: a replay
// with the identical payload returns the already-applied state.
type ActivatePairRequest struct {
	LLMRevisionID  *string
	JevRevisionID  *string
	ExpectedEpoch  int64
	IdempotencyKey string
	Actor          string
}

func validateLiveRevision(ctx context.Context, revisionID, wantKind string) error {
	revision, err := llmstore.GetProfileRevision(ctx, revisionID)
	if err != nil {
		return err
	}
	if revision.SupersededAt != nil {
		return fmt.Errorf("%w: %s", ErrRevisionNotLive, revisionID)
	}
	if revision.Kind != wantKind {
		return fmt.Errorf("%w: %s is %s, slot wants %s", ErrRevisionKindMismatch, revisionID, revision.Kind, wantKind)
	}
	return nil
}

// latestProbePassed reports whether the newest recorded probe of a revision
// passed. No probe at all counts as not passed: readiness claims evidence.
func latestProbePassed(ctx context.Context, revisionID string) (bool, error) {
	results, err := llmstore.ListProbeResults(ctx, revisionID)
	if err != nil {
		return false, err
	}
	if len(results) == 0 {
		return false, nil
	}
	return results[0].Passed, nil
}

// computeReadiness derives the pair's readiness: INCOMPLETE while a slot is
// empty; otherwise READY only when both revisions' newest probes passed, and
// DEGRADED when either side lacks a passing probe.
func computeReadiness(ctx context.Context, pair *llmstore.ActiveModelPair) (string, error) {
	if pair.LLMProfileRevisionID == nil || pair.JevProfileRevisionID == nil {
		return readinessIncomplete, nil
	}
	llmOK, err := latestProbePassed(ctx, *pair.LLMProfileRevisionID)
	if err != nil {
		return "", err
	}
	jevOK, err := latestProbePassed(ctx, *pair.JevProfileRevisionID)
	if err != nil {
		return "", err
	}
	if llmOK && jevOK {
		return readinessReady, nil
	}
	return readinessDegraded, nil
}

func sameRevision(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ActivateModelPair performs the single-row CAS activation described in
// docs 05 §2.4: validate the requested revisions are live and slot-compatible,
// derive readiness from the newest probes, and swap the pair only when the
// stored epoch still matches ExpectedEpoch. Every applied swap is recorded in
// llm_pair_switch_commands under the caller's idempotency key. Pausing
// strategies and revoking approvals are M4 concerns and are not yet wired
// here; their counts are recorded as zero for now.
func ActivateModelPair(ctx context.Context, req ActivatePairRequest, now time.Time) (*llmstore.ActiveModelPair, error) {
	if req.Actor == "" {
		return nil, ErrInvalidActor
	}
	if req.IdempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}

	// Idempotent replay: an identical command under the same key returns the
	// already-applied state without touching the pair.
	previous, err := llmstore.GetPairSwitchCommandByIdempotencyKey(ctx, req.IdempotencyKey)
	if err == nil && previous != nil {
		if previous.FromPairEpoch != req.ExpectedEpoch ||
			previous.ToPairEpoch != req.ExpectedEpoch+1 ||
			!sameRevision(previous.LLMRevisionID, req.LLMRevisionID) ||
			!sameRevision(previous.JevRevisionID, req.JevRevisionID) {
			return nil, fmt.Errorf("%w: %s", ErrIdempotencyKeyReused, req.IdempotencyKey)
		}
		current, err := llmstore.GetActiveModelPair(ctx)
		if err != nil {
			return nil, err
		}
		return current, nil
	}
	if err != nil && !errors.Is(err, llmstore.ErrNotFound) {
		return nil, err
	}

	if req.LLMRevisionID != nil {
		if err := validateLiveRevision(ctx, *req.LLMRevisionID, ProviderKindLLM); err != nil {
			return nil, err
		}
	}
	if req.JevRevisionID != nil {
		if err := validateLiveRevision(ctx, *req.JevRevisionID, ProviderKindJev); err != nil {
			return nil, err
		}
	}

	desired := llmstore.ActiveModelPair{
		LLMProfileRevisionID: req.LLMRevisionID,
		JevProfileRevisionID: req.JevRevisionID,
		PairEpoch:            req.ExpectedEpoch + 1,
		UpdatedAt:            now,
		UpdatedBy:            req.Actor,
	}
	readiness, err := computeReadiness(ctx, &desired)
	if err != nil {
		return nil, err
	}
	desired.Readiness = readiness

	applied, err := llmstore.CasActiveModelPair(ctx, &desired, req.ExpectedEpoch)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, fmt.Errorf("%w: expected %d", ErrPairEpochConflict, req.ExpectedEpoch)
	}

	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	command := llmstore.PairSwitchCommand{
		ID:                 id.String(),
		IdempotencyKey:     req.IdempotencyKey,
		FromPairEpoch:      req.ExpectedEpoch,
		ToPairEpoch:        req.ExpectedEpoch + 1,
		LLMRevisionID:      req.LLMRevisionID,
		JevRevisionID:      req.JevRevisionID,
		Operator:           req.Actor,
		AffectedStrategies: 0,
		RevokedApprovals:   0,
		CreatedAt:          now,
	}
	if err := llmstore.InsertPairSwitchCommand(ctx, &command); err != nil {
		// A concurrent writer may have recorded the same key between our
		// replay check and here; if its payload matches, this call is still
		// the same command and the swap has landed.
		if existing, lookupErr := llmstore.GetPairSwitchCommandByIdempotencyKey(ctx, req.IdempotencyKey); lookupErr == nil && existing != nil {
			if !sameRevision(existing.LLMRevisionID, req.LLMRevisionID) || !sameRevision(existing.JevRevisionID, req.JevRevisionID) {
				return nil, fmt.Errorf("%w: %s", ErrIdempotencyKeyReused, req.IdempotencyKey)
			}
			return &desired, nil
		}
		return nil, err
	}
	return &desired, nil
}
