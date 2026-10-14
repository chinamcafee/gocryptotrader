package jev

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

var guardNow = time.Date(2026, 10, 10, 12, 30, 0, 0, time.UTC)

func guardEvidence(ref, data string, asOf time.Time) candidate.ToolEvidence {
	return candidate.ToolEvidence{
		Ref:      ref,
		AsOf:     asOf,
		Quality:  candidate.QualityExchangeREST,
		Exchange: "Binance",
		Data:     json.RawMessage(data),
	}
}

// guardManifest mimics the manifest recorded when the analyst ran.
func guardManifest(evidence ...candidate.ToolEvidence) *InputManifest {
	manifest := &InputManifest{SchemaVersion: "jev-factual-input-v1"}
	for i := range evidence {
		manifest.ToolResponses = append(manifest.ToolResponses, NewToolResponseRef(
			"call-"+string(rune('1'+i)), strings.TrimPrefix(evidence[i].Ref, "tool:"), &evidence[i]))
	}
	return manifest
}

func TestReviewEvidenceAcceptedWithMatchingManifest(t *testing.T) {
	evidence := []candidate.ToolEvidence{
		guardEvidence("tool:getIndicators:call-1", `{"macd":{"histogram":"2.4"}}`, guardNow.Add(-time.Minute)),
		guardEvidence("tool:getFunding:call-2", `{"rate":"0.0001"}`, guardNow.Add(-2*time.Minute)),
	}
	manifest := guardManifest(evidence...)
	require.NoError(t, ValidateReviewEvidence(evidence, manifest, guardNow))
	require.NoError(t, CheckEvidenceScope(evidence, "", ""))
}

func TestReviewEvidenceRejectsChangedDataAgainstManifest(t *testing.T) {
	evidence := []candidate.ToolEvidence{
		guardEvidence("tool:getIndicators:call-1", `{"macd":{"histogram":"2.4"}}`, guardNow.Add(-time.Minute)),
	}
	manifest := guardManifest(evidence...)
	// The data is edited between the analyst call and the judge call: the
	// re-hash no longer matches the recorded reference.
	evidence[0].Data = json.RawMessage(`{"macd":{"histogram":"9.9"}}`)
	err := ValidateReviewEvidence(evidence, manifest, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)
	assert.Contains(t, err.Error(), "does not hash to the manifest reference")
}

func TestReviewEvidenceRejectsManifestMismatch(t *testing.T) {
	evidence := []candidate.ToolEvidence{
		guardEvidence("tool:getIndicators:call-1", `{"a":1}`, guardNow.Add(-time.Minute)),
	}
	manifest := guardManifest(evidence...)
	manifest.ToolResponses = append(manifest.ToolResponses, ToolResponseRef{Ref: "tool:getFunding:call-9"})
	err := ValidateReviewEvidence(evidence, manifest, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)
}

func TestReviewEvidenceRejectsBudgetsAndDuplicates(t *testing.T) {
	oversized := `{"blob":"` + strings.Repeat("x", MaxReviewEvidenceEntryBytes) + `"}`
	err := ValidateReviewEvidence([]candidate.ToolEvidence{
		guardEvidence("tool:a:1", oversized, guardNow.Add(-time.Minute)),
	}, nil, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)

	nine := make([]candidate.ToolEvidence, 0, 9)
	for i := range 9 {
		nine = append(nine, guardEvidence("tool:a:"+string(rune('0'+i)), `{}`, guardNow.Add(-time.Minute)))
	}
	err = ValidateReviewEvidence(nine, nil, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)

	err = ValidateReviewEvidence([]candidate.ToolEvidence{
		guardEvidence("tool:a:1", `{}`, guardNow.Add(-time.Minute)),
		guardEvidence("tool:a:1", `{}`, guardNow.Add(-time.Minute)),
	}, nil, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected, "a duplicated ref is two responses wearing one name")
}

func TestReviewEvidenceRejectsSensitiveAndUnknownQuality(t *testing.T) {
	err := ValidateReviewEvidence([]candidate.ToolEvidence{
		guardEvidence("tool:a:1", `{"apiKey":"sk-live"}`, guardNow.Add(-time.Minute)),
	}, nil, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)

	bad := guardEvidence("tool:a:1", `{}`, guardNow.Add(-time.Minute))
	bad.Quality = candidate.DataQuality("TOTALLY_FINE")
	err = ValidateReviewEvidence([]candidate.ToolEvidence{bad}, nil, guardNow)
	assert.ErrorIs(t, err, ErrEvidenceRejected)
}

func TestReviewEvidenceRejectsOutOfScope(t *testing.T) {
	market := "Binance:usdtmarginedfutures:ETH_USDT"
	evidence := []candidate.ToolEvidence{
		guardEvidence("tool:a:1", `{}`, guardNow.Add(-time.Minute)),
	}
	evidence[0].MarketID = &market
	err := CheckEvidenceScope(evidence, "Binance:usdtmarginedfutures:BTC_USDT", "ver-1")
	assert.ErrorIs(t, err, ErrEvidenceRejected)

	version := "ver-9"
	evidence[0].MarketID = nil
	evidence[0].StrategyVersionID = &version
	err = CheckEvidenceScope(evidence, "Binance:usdtmarginedfutures:BTC_USDT", "ver-1")
	assert.ErrorIs(t, err, ErrEvidenceRejected)
}

func TestReviewRequestCarriesEvidenceByteForByte(t *testing.T) {
	// The exact bytes the analyst path produces (engine marshals the evidence
	// struct), then the same bytes fed to the judge constructor.
	original := candidate.ToolEvidence{
		Ref:      "tool:getCandleWindow:call-1",
		AsOf:     guardNow.Add(-time.Minute),
		Quality:  candidate.QualityExchangeREST,
		Exchange: "Binance",
		Data:     json.RawMessage(`{"candles":[{"close":"68000.5"}]}`),
	}
	analystBytes, err := json.Marshal(original)
	require.NoError(t, err)

	var replayed candidate.ToolEvidence
	require.NoError(t, json.Unmarshal(analystBytes, &replayed))

	in := testInput()
	in.Evidence = []candidate.ToolEvidence{replayed}
	in.Manifest = guardManifest(replayed)
	in.PromptVersion = openai.PromptVersionAnalysisReadOnly

	request, err := BuildReviewRequest(in)
	require.NoError(t, err)
	state, ok := request.State.(ReviewState)
	require.True(t, ok, "the request must carry the typed state block")
	require.Len(t, state.Evidence, 1)
	assert.True(t, bytes.Equal(original.Data, state.Evidence[0].Data),
		"the judge sees the analyst's data bytes verbatim")
	assert.Equal(t, HashBytes(original.Data), HashBytes(state.Evidence[0].Data),
		"and the hashes agree, so a byte difference cannot hide behind parsing")

	// Byte-identical reconstruction for replay: same input, same request.
	second, err := BuildReviewRequest(in)
	require.NoError(t, err)
	firstBytes, err := request.Marshal()
	require.NoError(t, err)
	secondBytes, err := second.Marshal()
	require.NoError(t, err)
	assert.True(t, bytes.Equal(firstBytes, secondBytes))
}

func TestReviewRequestRejectsBadPromptVersionAndMissingClock(t *testing.T) {
	in := testInput()
	in.PromptVersion = "policy-proposal-v1"
	_, err := BuildReviewRequest(in)
	assert.ErrorIs(t, err, ErrStateInvalid, "only the read-only analysis prompt may produce review facts")

	in = testInput()
	in.Now = time.Time{}
	_, err = BuildReviewRequest(in)
	assert.ErrorIs(t, err, ErrStateInvalid, "evidence should be aged against a caller-supplied clock")
}
