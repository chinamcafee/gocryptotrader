package candidate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"
)

// Action is the set of moves a candidate may propose. Hedged sides are absent
// on purpose: the Binance wrapper does not send positionSide, so only one-way
// positions are supported (docs/llm-dev/07-策略与自动决策引擎.md §6.1).
type Action string

// Proposed actions.
const (
	// ActionOpenLong opens or adds to a long position.
	ActionOpenLong Action = "OPEN_LONG"
	// ActionOpenShort opens or adds to a short position.
	ActionOpenShort Action = "OPEN_SHORT"
	// ActionReduce reduces or closes existing exposure.
	ActionReduce Action = "REDUCE"
	// ActionHold creates no exposure.
	ActionHold Action = "HOLD"
	// ActionReview asks a human to resolve ambiguity.
	ActionReview Action = "REVIEW"
)

// Uncertainty is the model's own confidence label.
type Uncertainty string

// Uncertainty levels.
const (
	// UncertaintyLow means the evidence is coherent.
	UncertaintyLow Uncertainty = "LOW"
	// UncertaintyMedium means the evidence is mixed.
	UncertaintyMedium Uncertainty = "MEDIUM"
	// UncertaintyHigh means the evidence is weak or contradictory.
	UncertaintyHigh Uncertainty = "HIGH"
)

// Field limits, taken from docs/llm-dev/05-LLM接入层-OpenAI兼容.md §5.1.
const (
	// MaxThesisLength bounds the rationale.
	MaxThesisLength = 2000
	// MaxInvalidationLength bounds the statement of what would falsify the thesis.
	MaxInvalidationLength = 1000
	// MaxEvidenceRefs bounds how many references a candidate may cite.
	MaxEvidenceRefs = 12
	// MinEvidenceRefs is one: a candidate must cite something.
	MinEvidenceRefs = 1
	// MinTTLSeconds and MaxTTLSeconds bound how long a candidate stays valid.
	MinTTLSeconds = 5
	MaxTTLSeconds = 300
)

var (
	// ErrCandidateIncomplete marks a candidate missing required fields.
	ErrCandidateIncomplete = errors.New("candidate is incomplete")
	// ErrUnknownAction marks an action outside the vocabulary.
	ErrUnknownAction = errors.New("unknown candidate action")
	// ErrUnknownUncertainty marks an uncertainty outside the vocabulary.
	ErrUnknownUncertainty = errors.New("unknown uncertainty level")
	// ErrSnapshotRefMissing marks references that do not start with the snapshot.
	ErrSnapshotRefMissing = errors.New("evidence refs must begin with the snapshot id")
	// ErrInvalidDecimal marks a money or price value that is not a plain positive decimal.
	ErrInvalidDecimal = errors.New("value must be a positive plain decimal string")
	// ErrTTLOutOfRange marks a time-to-live outside the accepted window.
	ErrTTLOutOfRange = errors.New("ttl is out of range")
	// ErrUnexpectedNotional marks a notional supplied for an action that must not size.
	ErrUnexpectedNotional = errors.New("notional must be null for this action")
)

// decimalString matches a plain positive decimal: no sign, no exponent, at most
// 20 integer digits and 18 fraction digits. Money is carried as a string rather
// than a float so that no rounding can occur between the model and the order.
var decimalString = regexp.MustCompile(`^\d{1,20}(\.\d{1,18})?$`)

// Candidate is a trade proposal produced by the language model.
//
// It is a proposal, never an order. Nothing in this package can submit, sign or
// approve anything; the deterministic risk engine decides separately and may
// lower the size or refuse outright.
type Candidate struct {
	StrategyVersionID string `json:"strategyVersionId"`
	SnapshotID        string `json:"snapshotId"`
	MarketID          string `json:"marketId"`
	Action            Action `json:"action"`

	Thesis       string   `json:"thesis"`
	EvidenceRefs []string `json:"evidenceRefs"`
	Invalidation string   `json:"invalidation"`

	// SuggestedEntry and SuggestedExit are decimal strings or empty when the
	// model cannot propose a level. They are advisory: the risk engine derives
	// the executable price from the live order book.
	SuggestedEntry string `json:"suggestedEntry"`
	SuggestedExit  string `json:"suggestedExit"`

	// ProposedMaxNotionalUSD is an upper bound suggestion only. Approval and the
	// risk envelope may lower or reject it; it is never used as an order size.
	ProposedMaxNotionalUSD string `json:"proposedMaxNotionalUsd"`

	Uncertainty Uncertainty `json:"uncertainty"`
	TTLSeconds  int         `json:"ttlSeconds"`
}

// Validate checks the candidate against its schema and the connection rules.
//
// The reference project learned these rules the hard way: a live model returned
// evidence references as objects rather than strings, and an uncertainty value
// outside the enum. Both are caught here rather than downstream.
func (c *Candidate) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: nil candidate", ErrCandidateIncomplete)
	}
	if _, err := uuid.FromString(c.StrategyVersionID); err != nil {
		return fmt.Errorf("%w: strategyVersionId: %w", ErrCandidateIncomplete, err)
	}
	if _, err := uuid.FromString(c.SnapshotID); err != nil {
		return fmt.Errorf("%w: snapshotId: %w", ErrCandidateIncomplete, err)
	}
	if strings.TrimSpace(c.MarketID) == "" {
		return fmt.Errorf("%w: marketId", ErrCandidateIncomplete)
	}
	if err := c.Action.Validate(); err != nil {
		return err
	}

	n := len([]rune(c.Thesis))
	if n == 0 || n > MaxThesisLength {
		return fmt.Errorf("%w: thesis must be 1..%d characters, got %d", ErrCandidateIncomplete, MaxThesisLength, n)
	}
	n = len([]rune(c.Invalidation))
	if n == 0 || n > MaxInvalidationLength {
		return fmt.Errorf("%w: invalidation must be 1..%d characters, got %d", ErrCandidateIncomplete, MaxInvalidationLength, n)
	}

	if err := c.validateEvidenceRefs(); err != nil {
		return err
	}
	if c.SuggestedEntry != "" {
		if err := ValidatePositiveDecimal(c.SuggestedEntry); err != nil {
			return fmt.Errorf("suggestedEntry: %w", err)
		}
	}
	if c.SuggestedExit != "" {
		if err := ValidatePositiveDecimal(c.SuggestedExit); err != nil {
			return fmt.Errorf("suggestedExit: %w", err)
		}
	}
	if c.ProposedMaxNotionalUSD != "" {
		if err := ValidatePositiveDecimal(c.ProposedMaxNotionalUSD); err != nil {
			return fmt.Errorf("proposedMaxNotionalUsd: %w", err)
		}
	}
	if !c.Action.CreatesRisk() && c.ProposedMaxNotionalUSD != "" {
		return fmt.Errorf("%w: %s", ErrUnexpectedNotional, c.Action)
	}

	switch c.Uncertainty {
	case UncertaintyLow, UncertaintyMedium, UncertaintyHigh:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownUncertainty, c.Uncertainty)
	}

	if c.TTLSeconds < MinTTLSeconds || c.TTLSeconds > MaxTTLSeconds {
		return fmt.Errorf("%w: %d must be %d..%d", ErrTTLOutOfRange, c.TTLSeconds, MinTTLSeconds, MaxTTLSeconds)
	}
	return nil
}

func (c *Candidate) validateEvidenceRefs() error {
	n := len(c.EvidenceRefs)
	if n < MinEvidenceRefs || n > MaxEvidenceRefs {
		return fmt.Errorf("%w: evidenceRefs must hold %d..%d entries, got %d",
			ErrCandidateIncomplete, MinEvidenceRefs, MaxEvidenceRefs, n)
	}
	for i, ref := range c.EvidenceRefs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("%w: evidenceRefs[%d]", ErrEvidenceRefEmpty, i)
		}
		if len(ref) > MaxEvidenceRefLength {
			return fmt.Errorf("%w: evidenceRefs[%d] is %d characters", ErrEvidenceRefTooLong, i, len(ref))
		}
	}
	if c.EvidenceRefs[0] != c.SnapshotID {
		return fmt.Errorf("%w: got %q", ErrSnapshotRefMissing, c.EvidenceRefs[0])
	}
	return nil
}

// Validate checks the action against the vocabulary.
func (a Action) Validate() error {
	switch a {
	case ActionOpenLong, ActionOpenShort, ActionReduce, ActionHold, ActionReview:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAction, a)
	}
}

// CreatesRisk reports whether the action would increase exposure. Only these
// actions consume the new-risk frequency budget.
func (a Action) CreatesRisk() bool {
	return a == ActionOpenLong || a == ActionOpenShort
}

// IsHold reports whether the action proposes no change.
func (a Action) IsHold() bool { return a == ActionHold }

// String implements fmt.Stringer.
func (a Action) String() string { return string(a) }

// ValidatePositiveDecimal reports whether s is a plain positive decimal.
//
// "Positive" excludes zero: a zero notional is indistinguishable from a missing
// one, and an order of size zero is always a mistake.
func ValidatePositiveDecimal(s string) error {
	if !decimalString.MatchString(s) {
		return fmt.Errorf("%w: %q", ErrInvalidDecimal, s)
	}
	for _, r := range s {
		if r != '.' && r != '0' {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is zero", ErrInvalidDecimal, s)
}
