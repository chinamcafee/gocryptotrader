package engine

import (
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// EvidenceCard is the display view of one tool response (docs/llm-dev 11 §M3-04:
// market + sourceTime + snapshotId + dataQuality + ref + sha256).
//
// The card is derived, never authoritative: the transcript keeps the raw
// evidence envelope byte for byte, and the card only spares the operator (and
// the webUI) from parsing it. A card that cannot be derived is simply absent;
// nothing here may fail a turn.
type EvidenceCard struct {
	// Ref is the evidence's unique reference, e.g. tool:getCandleWindow:call-1.
	Ref string `json:"ref"`
	// Tool is the tool that produced the evidence.
	Tool string `json:"tool"`
	// Exchange is the venue the facts came from.
	Exchange string `json:"exchange,omitempty"`
	// MarketID is the market the evidence belongs to, when market-scoped.
	MarketID string `json:"marketId,omitempty"`
	// AccountID is the account the evidence belongs to, when account-scoped.
	AccountID string `json:"accountId,omitempty"`
	// StrategyVersionID is the strategy version the evidence belongs to, when
	// strategy-scoped.
	StrategyVersionID string `json:"strategyVersionId,omitempty"`
	// SourceTime is the data source's own timestamp (asOf), never read time.
	SourceTime time.Time `json:"sourceTime"`
	// Quality is the data-quality label the producer declared.
	Quality string `json:"quality"`
	// SnapshotID is set when the evidence references a stored market
	// snapshot; conversation tools do not take snapshots, so it stays empty
	// here and the decision path fills it (M4).
	SnapshotID string `json:"snapshotId,omitempty"`
	// SHA256 is the hash of the exact result bytes the model saw.
	SHA256 string `json:"sha256"`
}

// EvidenceCards extracts the display cards of a loop's transcript.
//
// Each tool result is the marshalled candidate.ToolEvidence envelope the
// executors produce (llm_tools.go evidence()), so the extraction is a decode,
// not a guess. Entries that are not evidence envelopes -- an argument-error
// feedback string, for instance -- are skipped: the transcript remains the
// record of everything that happened.
func EvidenceCards(records []ToolCallRecord) []EvidenceCard {
	cards := make([]EvidenceCard, 0, len(records))
	for i := range records {
		var evidence candidate.ToolEvidence
		if err := json.Unmarshal([]byte(records[i].Result), &evidence); err != nil {
			continue
		}
		if evidence.Ref == "" || evidence.Quality == "" {
			continue
		}
		card := EvidenceCard{
			Ref:        evidence.Ref,
			Tool:       records[i].Name,
			Exchange:   evidence.Exchange,
			SourceTime: evidence.AsOf,
			Quality:    string(evidence.Quality),
			SHA256:     records[i].ResultSha256,
		}
		if evidence.MarketID != nil {
			card.MarketID = *evidence.MarketID
		}
		if evidence.AccountID != nil {
			card.AccountID = *evidence.AccountID
		}
		if evidence.StrategyVersionID != nil {
			card.StrategyVersionID = *evidence.StrategyVersionID
		}
		cards = append(cards, card)
	}
	return cards
}
