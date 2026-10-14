package engine

import (
	"errors"
	"fmt"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// Tool transcript retention (docs 05 §4.4).
const (
	// MaxToolTranscriptEntries bounds how many tool exchanges are retained.
	MaxToolTranscriptEntries = 8
	// MaxToolTranscriptEntryBytes bounds one entry's stored payload.
	MaxToolTranscriptEntryBytes = 12 * 1024
	// MaxToolTranscriptTotalBytes bounds the serialised transcript.
	MaxToolTranscriptTotalBytes = 32 * 1024
)

// ErrTranscriptEntryTooLarge is returned when one entry's result exceeds the
// per-entry budget. The loop already truncates tool results to the same limit,
// so this is defence in depth for any other producer of records.
var ErrTranscriptEntryTooLarge = errors.New("llm transcript: entry exceeds the per-entry byte budget")

// toolTranscript is the stored shape: the retained exchanges plus the count of
// older ones evicted to fit the budget.
type toolTranscript struct {
	Entries []ToolCallRecord `json:"entries"`
	// Dropped counts older exchanges evicted to fit the retention budget.
	Dropped int `json:"dropped,omitempty"`
}

// BuildToolTranscriptJSON serialises the loop's tool records under the
// retention budget: at most eight exchanges, twelve kilobytes each, thirty-two
// kilobytes in total (docs 05 §4.4). When the budget is exceeded the OLDEST
// exchanges are dropped -- the newest evidence is the evidence a replay is
// re-litigating -- and the eviction count is recorded so a reader can tell a
// short transcript from a truncated one.
//
// Each entry carries the SHA-256 of the exact result bytes it stores and the
// original arguments, because a post-mortem must answer what the model ASKED,
// not only what it received: the arguments expose the reasoning that shaped
// the question.
func BuildToolTranscriptJSON(records []ToolCallRecord) (string, error) {
	entries := make([]ToolCallRecord, len(records))
	copy(entries, records)
	dropped := 0
	if len(entries) > MaxToolTranscriptEntries {
		dropped = len(entries) - MaxToolTranscriptEntries
		entries = entries[dropped:]
	}
	for i := range entries {
		if len(entries[i].Result) > MaxToolTranscriptEntryBytes {
			return "", fmt.Errorf("%w: %s result is %d bytes", ErrTranscriptEntryTooLarge, entries[i].CallID, len(entries[i].Result))
		}
	}
	for {
		encoded, marshalErr := json.Marshal(toolTranscript{Entries: entries, Dropped: dropped})
		if marshalErr != nil {
			return "", marshalErr
		}
		if len(encoded) <= MaxToolTranscriptTotalBytes || len(entries) == 0 {
			return string(encoded), nil
		}
		// Over the total budget: the oldest exchange goes first.
		entries = entries[1:]
		dropped++
	}
}

// TranscriptJSON renders the loop result's transcript under the retention
// budget; it is what the decision run stores in tool_transcript_json.
func (r *ToolLoopResult) TranscriptJSON() (string, error) {
	if r == nil {
		return "", errors.New("llm transcript: nil loop result")
	}
	return BuildToolTranscriptJSON(r.Transcript)
}
