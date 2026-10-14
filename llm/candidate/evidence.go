package candidate

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm"
)

// Evidence limits. They are enforced on every tool response before the payload
// reaches a model, so a runaway tool cannot exhaust the context window or smuggle
// large blobs into the audit trail.
const (
	// MaxEvidenceRefLength bounds a single evidence reference identifier.
	MaxEvidenceRefLength = 200
	// MaxToolResultBytes bounds one tool response.
	MaxToolResultBytes = 12288
	// MaxTotalEvidenceBytes bounds the sum of all tool responses in one run.
	MaxTotalEvidenceBytes = 32768
	// MaxEvidenceDepth bounds how deep the sensitive-key scan descends.
	MaxEvidenceDepth = 8
	// MaxFutureSkew tolerates small clock differences when checking that a fact
	// is not dated in the future.
	MaxFutureSkew = 2 * time.Second
)

var (
	// ErrEvidenceRefEmpty marks a blank evidence reference.
	ErrEvidenceRefEmpty = errors.New("evidence ref is empty")
	// ErrEvidenceRefTooLong marks an over-long evidence reference.
	ErrEvidenceRefTooLong = errors.New("evidence ref is too long")
	// ErrEvidenceFromFuture marks a fact dated later than the check allows.
	ErrEvidenceFromFuture = errors.New("evidence is dated in the future")
	// ErrToolResultTooLarge marks a single tool response over the byte cap.
	ErrToolResultTooLarge = errors.New("tool result exceeds the byte limit")
	// ErrToolResultTooDeep marks a payload nested deeper than the scan allows.
	ErrToolResultTooDeep = errors.New("tool result is nested too deeply")
	// ErrToolResultSensitive marks a payload carrying a secret-shaped key.
	ErrToolResultSensitive = errors.New("tool result contains a sensitive key")
	// ErrEvidenceTotalTooLarge marks a run whose combined evidence exceeds the cap.
	ErrEvidenceTotalTooLarge = errors.New("combined evidence exceeds the byte limit")
)

// sensitiveKey matches key names that must never reach a model. The vocabulary
// follows docs/llm-dev/10-安全与风控.md §2.3; it deliberately over-matches,
// because a withheld tool response degrades to HOLD whereas a leaked credential
// cannot be recalled.
var sensitiveKey = regexp.MustCompile(`(?i)(secret|api[_-]?key|private[_-]?key|mnemonic|seed[_-]?phrase|password|token|signature)`)

// ToolEvidence is one retained tool response.
//
// Data holds the exact bytes the model saw. Retaining the original payload is
// what makes the Jev review reproducible: the second model must be given the
// same facts as the first, otherwise a "disagreement" cannot be distinguished
// from a difference in inputs (docs/llm-dev/06-Jev接入层-SystemOne.md §4).
type ToolEvidence struct {
	Ref               string          `json:"ref"`
	AsOf              time.Time       `json:"asOf"`
	Quality           DataQuality     `json:"quality"`
	Exchange          string          `json:"exchange"`
	MarketID          *string         `json:"marketId,omitempty"`
	AccountID         *string         `json:"accountId,omitempty"`
	StrategyVersionID *string         `json:"strategyVersionId,omitempty"`
	Data              json.RawMessage `json:"data"`
}

// Validate checks a single tool response against the connection rules.
//
// now is passed in rather than read from the clock so that a run can be
// replayed against its original time window.
func (e *ToolEvidence) Validate(now time.Time) error {
	if strings.TrimSpace(e.Ref) == "" {
		return ErrEvidenceRefEmpty
	}
	if len(e.Ref) > MaxEvidenceRefLength {
		return fmt.Errorf("%w: %d characters", ErrEvidenceRefTooLong, len(e.Ref))
	}
	if _, err := ParseQuality(string(e.Quality)); err != nil {
		return err
	}
	if !e.AsOf.IsZero() && e.AsOf.After(now.Add(MaxFutureSkew)) {
		return fmt.Errorf("%w: asOf %s is later than %s", ErrEvidenceFromFuture, e.AsOf.UTC(), now.UTC())
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("%w: evidence %q carries no data", llm.ErrMissingData, e.Ref)
	}
	if len(e.Data) > MaxToolResultBytes {
		return fmt.Errorf("%w: %d bytes", ErrToolResultTooLarge, len(e.Data))
	}
	return ScanForSensitiveKeys(e.Data)
}

// ScanForSensitiveKeys walks a JSON payload and rejects any object whose key
// name looks like a credential.
//
// Non-JSON payloads are rejected rather than skipped: a tool response that
// cannot be inspected cannot be proven safe to forward.
func ScanForSensitiveKeys(raw json.RawMessage) error {
	// The scan walks keys and depth only: scalar values are ignored, so no
	// number-preserving decoder option is needed (the codec abstraction does
	// not expose one anyway).
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%w: evidence is not valid JSON: %w", llm.ErrInvalidResponse, err)
	}
	return scanValue(v, 1)
}

func scanValue(v any, depth int) error {
	if depth > MaxEvidenceDepth {
		return fmt.Errorf("%w: exceeded %d levels", ErrToolResultTooDeep, MaxEvidenceDepth)
	}
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if sensitiveKey.MatchString(k) {
				return fmt.Errorf("%w: %q", ErrToolResultSensitive, k)
			}
			if err := scanValue(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := scanValue(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// TotalEvidenceBytes returns the combined payload size, failing when the run
// exceeds the aggregate cap.
//
// The cap exists so that a bounded number of bounded responses still cannot
// produce an unbounded request to the second model.
func TotalEvidenceBytes(evidence []ToolEvidence) (int, error) {
	var total int
	for x := range evidence {
		total += len(evidence[x].Data)
	}
	if total > MaxTotalEvidenceBytes {
		return total, fmt.Errorf("%w: %d bytes", ErrEvidenceTotalTooLarge, total)
	}
	return total, nil
}

// ValidateEvidence validates every response and the aggregate size.
func ValidateEvidence(evidence []ToolEvidence, now time.Time) error {
	for x := range evidence {
		if err := evidence[x].Validate(now); err != nil {
			return err
		}
	}
	_, err := TotalEvidenceBytes(evidence)
	return err
}
