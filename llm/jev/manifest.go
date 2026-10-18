package jev

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// FactsScopeRetained states that the judge was given the analyst's retained tool
// responses verbatim. It is the only accepted scope, because any other source
// would break the same-source invariant.
const FactsScopeRetained = "RETAINED_LLM_TOOL_RESPONSES"

var (
	// ErrManifestIncomplete marks a manifest missing required fields.
	ErrManifestIncomplete = errors.New("manifest is incomplete")
	// ErrManifestSchema marks an unrecognised manifest schema version.
	ErrManifestSchema = errors.New("manifest schema version is unrecognised")
	// ErrManifestScope marks a facts scope other than the retained responses.
	ErrManifestScope = errors.New("manifest facts scope is invalid")
	// ErrManifestAuthorized marks a manifest claiming authority to trade.
	ErrManifestAuthorized = errors.New("manifest must not claim trade authority")
)

// ToolResponseRef identifies one retained tool response by hash, so a reader can
// prove later that the judge was shown the same bytes the analyst saw.
type ToolResponseRef struct {
	CallID       string `json:"callId"`
	Name         string `json:"name"`
	Ref          string `json:"ref"`
	ResultSha256 string `json:"resultSha256"`
}

// InputManifest is the evidence manifest persisted alongside a decision.
//
// It answers the auditing question "what exactly was the judge given?" without
// needing the original payloads, and it is a prerequisite for preparing or
// sending an order: a decision whose inputs cannot be reconstructed is not
// allowed to move money (docs/llm-dev/06-Jev接入层-SystemOne.md §6).
type InputManifest struct {
	SchemaVersion   string `json:"schemaVersion"`
	QuestionVersion string `json:"questionVersion"`
	RequestModel    string `json:"requestModel"`
	// RequestSha256 hashes the exact request body that was sent.
	RequestSha256 string `json:"requestSha256"`
	RequestBytes  int    `json:"requestBytes"`
	// ParsedResponseSha256 hashes the response as received. The parsed view is a
	// lossy projection of those bytes, so hashing the bytes is the stronger
	// guarantee: it also covers fields this build does not know about.
	ParsedResponseSha256 string `json:"parsedResponseSha256"`
	// CandidateSha256 hashes the canonical candidate JSON that was reviewed.
	CandidateSha256 string `json:"candidateSha256"`
	// StrategyInstructionSha256 hashes the operator's strategy text.
	StrategyInstructionSha256 string            `json:"strategyInstructionSha256"`
	ToolResponses             []ToolResponseRef `json:"toolResponses"`
	FactsScope                string            `json:"factsScope"`
	// TradeAuthorized is always false. The field exists so a reader can see the
	// claim in the record itself rather than having to trust a convention.
	TradeAuthorized bool `json:"tradeAuthorized"`
}

// HashBytes returns the hex SHA-256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NewToolResponseRef builds a reference for one retained tool response.
func NewToolResponseRef(callID, name string, evidence *candidate.ToolEvidence) ToolResponseRef {
	return ToolResponseRef{
		CallID:       callID,
		Name:         name,
		Ref:          evidence.Ref,
		ResultSha256: HashBytes(evidence.Data),
	}
}

// BuildInputManifest assembles the manifest for one review call.
//
// It is pure: given the same request bytes, response bytes, candidate and
// strategy text it produces the same manifest, which is what makes the record
// verifiable rather than merely descriptive.
func BuildInputManifest(
	req *SystemOneRequest,
	requestBody []byte,
	responseBody []byte,
	cand *candidate.Candidate,
	strategyInstruction string,
	toolResponses []ToolResponseRef,
) (*InputManifest, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrManifestIncomplete)
	}
	if len(requestBody) == 0 {
		return nil, fmt.Errorf("%w: empty request body", ErrManifestIncomplete)
	}
	if len(responseBody) == 0 {
		return nil, fmt.Errorf("%w: empty response body", ErrManifestIncomplete)
	}
	if cand == nil {
		return nil, fmt.Errorf("%w: nil candidate", ErrManifestIncomplete)
	}
	if !IsAcceptedModel(req.Model) {
		return nil, fmt.Errorf("%w: %q", ErrModelInvalid, req.Model)
	}
	candidateJSON, err := json.Marshal(cand)
	if err != nil {
		return nil, fmt.Errorf("%w: marshalling candidate: %w", ErrManifestIncomplete, err)
	}

	manifest := &InputManifest{
		SchemaVersion:             SchemaVersionFactualInput,
		QuestionVersion:           QuestionVersionReviewV2,
		RequestModel:              req.Model,
		RequestSha256:             HashBytes(requestBody),
		RequestBytes:              len(requestBody),
		ParsedResponseSha256:      HashBytes(responseBody),
		CandidateSha256:           HashBytes(candidateJSON),
		StrategyInstructionSha256: HashBytes([]byte(strategyInstruction)),
		ToolResponses:             toolResponses,
		FactsScope:                FactsScopeRetained,
		TradeAuthorized:           false,
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Validate checks that the manifest is complete and its claims are permitted.
func (m *InputManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrManifestIncomplete)
	}
	if m.TradeAuthorized {
		return ErrManifestAuthorized
	}
	if m.SchemaVersion != SchemaVersionFactualInput {
		return fmt.Errorf("%w: %q", ErrManifestSchema, m.SchemaVersion)
	}
	if strings.TrimSpace(m.QuestionVersion) == "" {
		return fmt.Errorf("%w: question version", ErrManifestIncomplete)
	}
	if strings.TrimSpace(m.RequestModel) == "" {
		return fmt.Errorf("%w: request model", ErrManifestIncomplete)
	}
	if m.RequestBytes <= 0 {
		return fmt.Errorf("%w: request bytes %d", ErrManifestIncomplete, m.RequestBytes)
	}
	for name, h := range map[string]string{
		"requestSha256":             m.RequestSha256,
		"parsedResponseSha256":      m.ParsedResponseSha256,
		"candidateSha256":           m.CandidateSha256,
		"strategyInstructionSha256": m.StrategyInstructionSha256,
	} {
		if !isHexDigest(h) {
			return fmt.Errorf("%w: %s", ErrManifestIncomplete, name)
		}
	}
	if m.FactsScope != FactsScopeRetained {
		return fmt.Errorf("%w: %q", ErrManifestScope, m.FactsScope)
	}
	if len(m.ToolResponses) == 0 {
		return fmt.Errorf("%w: no tool responses", ErrManifestIncomplete)
	}
	for x := range m.ToolResponses {
		ref := m.ToolResponses[x]
		if strings.TrimSpace(ref.CallID) == "" || strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Ref) == "" {
			return fmt.Errorf("%w: toolResponses[%d]", ErrManifestIncomplete, x)
		}
		if !isHexDigest(ref.ResultSha256) {
			return fmt.Errorf("%w: toolResponses[%d] hash", ErrManifestIncomplete, x)
		}
	}
	return nil
}

// ManifestState classifies a stored manifest.
//
// The order gates money: only a bound manifest may proceed to order preparation
// or send claim, because only then can a reviewer prove what the judge was shown.
type ManifestState string

// Manifest states.
const (
	// ManifestBound means the manifest is present, current and self-consistent.
	ManifestBound ManifestState = "MANIFEST_BOUND"
	// ManifestLegacyMissing means no manifest was recorded, which is expected
	// only for records written before manifests existed. It does not authorise
	// an order.
	ManifestLegacyMissing ManifestState = "LEGACY_MISSING"
	// ManifestInvalid means a manifest was recorded but fails validation.
	ManifestInvalid ManifestState = "INVALID"
	// ManifestNoAssessment means the judge was never called, so there is nothing
	// to bind. A caller that knows the call did not happen reports this state.
	ManifestNoAssessment ManifestState = "NO_ASSESSMENT"
)

// InspectManifest classifies the stored manifest of a decision.
//
// An absent or unparsable manifest is a classification rather than an error, so a
// caller can persist the state of every decision without special cases. A nil
// error together with a non-bound state means "classified, and the class blocks
// progress".
func InspectManifest(raw json.RawMessage) (ManifestState, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return ManifestLegacyMissing, nil
	}
	var manifest InputManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ManifestInvalid, fmt.Errorf("%w: %w", ErrManifestIncomplete, err)
	}
	if err := manifest.Validate(); err != nil {
		return ManifestInvalid, err
	}
	return ManifestBound, nil
}

// MayProceed reports whether a state permits order preparation or a send claim.
func (s ManifestState) MayProceed() bool { return s == ManifestBound }

// isHexDigest reports whether s looks like a hex SHA-256 digest.
func isHexDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
