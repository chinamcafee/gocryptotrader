package jev

import (
	"errors"
	"fmt"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// Evidence retention budgets for a review request (docs 06 §4.2, the same
// numbers the LLM-side transcript keeps).
const (
	// MaxReviewEvidenceEntries bounds how many tool responses reach the judge.
	MaxReviewEvidenceEntries = 8
	// MaxReviewEvidenceEntryBytes bounds one response's retained payload.
	MaxReviewEvidenceEntryBytes = 12 * 1024
	// MaxReviewEvidenceTotalBytes bounds the evidence block as a whole.
	MaxReviewEvidenceTotalBytes = 32 * 1024
)

// ErrEvidenceRejected marks retained evidence that must not reach the
// judge; the message names the exact rule that failed.
var ErrEvidenceRejected = errors.New("review evidence rejected")

// ValidateReviewEvidence re-validates the retained tool responses before the
// judge is called (docs 06 §4.2). Both models must see the SAME facts, byte for
// byte -- otherwise a "disagreement" cannot be told apart from a difference in
// inputs. Any rule failing here means the judge is not called at all.
//
// The manifest cross-check is the strongest rule and runs when a manifest is
// provided: every retained response's data is re-hashed and compared with the
// reference recorded when the analyst ran, so evidence edited between the two
// calls cannot be reviewed.
func ValidateReviewEvidence(evidence []candidate.ToolEvidence, manifest *InputManifest, now time.Time) error {
	if len(evidence) == 0 {
		return fmt.Errorf("%w: no retained evidence", ErrEvidenceRejected)
	}
	if len(evidence) > MaxReviewEvidenceEntries {
		return fmt.Errorf("%w: %d entries exceed the limit of %d", ErrEvidenceRejected, len(evidence), MaxReviewEvidenceEntries)
	}
	total := 0
	seenRefs := make(map[string]struct{}, len(evidence))
	for i := range evidence {
		item := &evidence[i]
		if _, dup := seenRefs[item.Ref]; dup {
			return fmt.Errorf("%w: duplicate ref %q", ErrEvidenceRejected, item.Ref)
		}
		seenRefs[item.Ref] = struct{}{}
		if len(item.Data) > MaxReviewEvidenceEntryBytes {
			return fmt.Errorf("%w: %s data is %d bytes", ErrEvidenceRejected, item.Ref, len(item.Data))
		}
		total += len(item.Data)
		if total > MaxReviewEvidenceTotalBytes {
			return fmt.Errorf("%w: evidence exceeds %d bytes in total", ErrEvidenceRejected, MaxReviewEvidenceTotalBytes)
		}
		if _, err := candidate.ParseQuality(string(item.Quality)); err != nil {
			return fmt.Errorf("%w: %s quality %q: %w", ErrEvidenceRejected, item.Ref, item.Quality, err)
		}
		if err := candidate.ScanForSensitiveKeys(item.Data); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrEvidenceRejected, item.Ref, err)
		}
		if err := item.Validate(now); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrEvidenceRejected, item.Ref, err)
		}
	}
	if manifest != nil {
		if len(manifest.ToolResponses) != len(evidence) {
			return fmt.Errorf("%w: %d retained responses but the manifest records %d",
				ErrEvidenceRejected, len(evidence), len(manifest.ToolResponses))
		}
		for i := range manifest.ToolResponses {
			reference := manifest.ToolResponses[i]
			if _, ok := seenRefs[reference.Ref]; !ok {
				return fmt.Errorf("%w: manifest ref %q has no retained response", ErrEvidenceRejected, reference.Ref)
			}
			var matched *candidate.ToolEvidence
			for j := range evidence {
				if evidence[j].Ref == reference.Ref {
					matched = &evidence[j]
					break
				}
			}
			if HashBytes(matched.Data) != reference.ResultSha256 {
				return fmt.Errorf("%w: %s data does not hash to the manifest reference (evidence changed between calls)",
					ErrEvidenceRejected, reference.Ref)
			}
		}
	}
	return nil
}

// CheckEvidenceScope re-checks the run's authorization boundary: evidence that
// names a market or strategy other than the review's own is not reviewable.
func CheckEvidenceScope(evidence []candidate.ToolEvidence, marketID, strategyVersionID string) error {
	for i := range evidence {
		item := &evidence[i]
		if item.MarketID != nil && marketID != "" && *item.MarketID != marketID {
			return fmt.Errorf("%w: %s names market %q", ErrEvidenceRejected, item.Ref, *item.MarketID)
		}
		if item.StrategyVersionID != nil && strategyVersionID != "" && *item.StrategyVersionID != strategyVersionID {
			return fmt.Errorf("%w: %s names strategy version %q", ErrEvidenceRejected, item.Ref, *item.StrategyVersionID)
		}
	}
	return nil
}
