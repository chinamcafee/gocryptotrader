package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

func transcriptRecord(index int, result string) ToolCallRecord {
	sum := sha256.Sum256([]byte(result))
	return ToolCallRecord{
		CallID:       "call-" + digitString(index),
		Name:         "lookup_market",
		Arguments:    `{"market":"BTC_USDT"}`,
		Result:       result,
		ResultSha256: hex.EncodeToString(sum[:]),
	}
}

func TestTranscriptRetainsNewestWithinEntryCap(t *testing.T) {
	records := make([]ToolCallRecord, 0, 9)
	for i := range 9 {
		records = append(records, transcriptRecord(i%10, `{"ok":true}`))
	}
	encoded, err := BuildToolTranscriptJSON(records)
	require.NoError(t, err)

	var parsed toolTranscript
	require.NoError(t, json.Unmarshal([]byte(encoded), &parsed))
	require.Len(t, parsed.Entries, MaxToolTranscriptEntries, "at most eight exchanges are retained")
	assert.Equal(t, 1, parsed.Dropped, "the eviction count tells a short transcript from a truncated one")
	assert.Equal(t, "call-1", parsed.Entries[0].CallID, "the OLDEST exchange is dropped, the newest evidence stays")

	// The stored hash still matches the stored bytes, so a later reader can
	// prove the transcript was not edited in storage.
	entry := parsed.Entries[0]
	sum := sha256.Sum256([]byte(entry.Result))
	assert.Equal(t, hex.EncodeToString(sum[:]), entry.ResultSha256)
	assert.NotEmpty(t, entry.Arguments, "the transcript carries what the model asked, not only what it received")
}

func TestTranscriptEnforcesTotalBudget(t *testing.T) {
	big := `{"blob":"` + strings.Repeat("x", 10*1024) + `"}`
	records := []ToolCallRecord{
		transcriptRecord(1, big),
		transcriptRecord(2, big),
		transcriptRecord(3, big),
		transcriptRecord(4, big),
	}
	encoded, err := BuildToolTranscriptJSON(records)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), MaxToolTranscriptTotalBytes, "the serialised transcript fits the total budget")

	var parsed toolTranscript
	require.NoError(t, json.Unmarshal([]byte(encoded), &parsed))
	assert.Equal(t, 1, parsed.Dropped)
	require.Len(t, parsed.Entries, 3)
	assert.Equal(t, "call-2", parsed.Entries[0].CallID, "dropping starts from the oldest")
}

func TestTranscriptRejectsOversizedEntry(t *testing.T) {
	oversized := strings.Repeat("y", MaxToolTranscriptEntryBytes+1)
	_, err := BuildToolTranscriptJSON([]ToolCallRecord{transcriptRecord(1, oversized)})
	assert.ErrorIs(t, err, ErrTranscriptEntryTooLarge)
}

func TestTranscriptDeterministicAndEmpty(t *testing.T) {
	encoded, err := BuildToolTranscriptJSON(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"entries":[]}`, encoded)

	records := []ToolCallRecord{transcriptRecord(1, `{"a":1}`), transcriptRecord(2, `{"b":2}`)}
	first, err := BuildToolTranscriptJSON(records)
	require.NoError(t, err)
	second, err := BuildToolTranscriptJSON(records)
	require.NoError(t, err)
	assert.Equal(t, first, second, "same records should serialise to the same bytes (hash-comparable)")
}

func TestLoopResultTranscriptJSON(t *testing.T) {
	result := &ToolLoopResult{Transcript: []ToolCallRecord{transcriptRecord(1, `{"ok":true}`)}}
	encoded, err := result.TranscriptJSON()
	require.NoError(t, err)
	assert.Contains(t, encoded, `"call-1"`)

	var nilResult *ToolLoopResult
	_, err = nilResult.TranscriptJSON()
	assert.Error(t, err)
}
