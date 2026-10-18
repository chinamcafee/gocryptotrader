// Package jev adapts the TypeSafe System One HTTP contract.
//
// System One is not a chat model. It answers a fixed set of bounded questions
// about a supplied state and returns typed answers, so this package deliberately
// has no conversation, no tool calling and no free-form output (see
// docs/llm-dev/06-Jev接入层-SystemOne.md §1).
//
// The role in the pipeline is a fast, independent judge: it sees the same facts
// as the language model and answers whether the proposed action is defensible.
// Its probabilities are never converted into expected return or position size.
package jev

import (
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// Contract version constants. They are pinned so a decision can be replayed
// against the exact constructor and question wording that produced it.
const (
	// SchemaVersionFactualInput is the evidence-manifest schema version.
	SchemaVersionFactualInput = "jev-factual-input-v1"
	// QuestionVersionReviewV2 is the review question template version.
	QuestionVersionReviewV2 = "jev-review-v2"
	// SystemOnePath is appended to the configured base URL.
	SystemOnePath = "/v1/systemone"
	// MaxRequestBytes caps the serialised request. The cap is enforced before
	// sending and a breach aborts the call rather than truncating it: truncation
	// would break the same-source invariant, because the judge would no longer
	// see what the analyst saw (docs/llm-dev/06-Jev接入层-SystemOne.md §4.4).
	MaxRequestBytes = 65536
	// MaxInputTokensBudget is the reference project's observed ceiling for one
	// review call.
	MaxInputTokensBudget = 8000
)

// QuestionType enumerates the three bounded question kinds.
type QuestionType string

// Question kinds. A single request may carry several independent questions;
// none of them may assume it can see another's answer.
const (
	QuestionChoice QuestionType = "choice"
	QuestionScore  QuestionType = "score"
	QuestionNoul   QuestionType = "noul"
)

// AnswerType mirrors QuestionType on the response side.
type AnswerType string

// Answer kinds.
const (
	AnswerChoice AnswerType = "choice"
	AnswerScore  AnswerType = "score"
	AnswerNoul   AnswerType = "noul"
)

// EvidencePolicy states, inside the request body itself, how the supplied
// evidence must be treated. Carrying this in the payload rather than only in the
// prompt means a reader of the audit log can see what the judge was told.
type EvidencePolicy struct {
	Source              string `json:"source"`
	Treatment           string `json:"treatment"`
	DataSourceCertified bool   `json:"dataSourceCertified"`
	TradeAuthorized     bool   `json:"tradeAuthorized"`
}

// SnapshotRef identifies the market snapshot a review concerns.
type SnapshotRef struct {
	ID         string `json:"id"`
	MarketID   string `json:"marketId"`
	MarketType string `json:"marketType"`
	SourceTime string `json:"sourceTime"`
	Quality    string `json:"quality"`
}

// SafetyStatus carries the deterministic pre-checks already applied.
type SafetyStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Question type tags. The wire contract discriminates question shapes on the
// "type" property (official OpenAPI discriminator: choice/score/noul), so every
// question must carry its tag (found live 2026-10-16: HTTP 422 without it).
const (
	QuestionTypeChoice = "choice"
	QuestionTypeScore  = "score"
	QuestionTypeNoul   = "noul"
)

// ChoiceQuestion asks the judge to pick one named option and returns a full
// probability distribution over them.
type ChoiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// ScoreQuestion asks the judge to rate on an ordinal scale.
type ScoreQuestion struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria"`
}

// NoulQuestion asks a binary natural-language yes/no question.
type NoulQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

// ChoiceAnswer is the reply to a ChoiceQuestion.
type ChoiceAnswer struct {
	Type          AnswerType         `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// ScoreAnswer is the reply to a ScoreQuestion (official OpenAPI shape): score
// is the probability-weighted expected level (may fall between integers) and
// legend maps the requested criteria to their levels as an object. There is no
// bucket distribution on this answer (live correction 2026-10-16).
type ScoreAnswer struct {
	Type       AnswerType     `json:"type"`
	Score      float64        `json:"score"`
	Confidence float64        `json:"confidence"`
	Legend     map[string]any `json:"legend"`
}

// NoulAnswer is the reply to a NoulQuestion. It has no confidence field, which
// is a documented property of the contract rather than an omission.
type NoulAnswer struct {
	Type AnswerType `json:"type"`
	Noul float64    `json:"noul"`
}

// Usage reports token consumption.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneRequest is the wire request.
//
// Questions is a raw payload because the contract overloads a single key
// ("criteria") with two different shapes: an object for choice questions and an
// array for score questions. Decoding happens through the typed accessors below.
type SystemOneRequest struct {
	Model     string                     `json:"model"`
	State     any                        `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

// SystemOneResponse is the wire response.
type SystemOneResponse struct {
	Model   string          `json:"model"`
	Answers json.RawMessage `json:"answers"`
	Usage   Usage           `json:"usage"`
}

// DecodeChoice decodes a choice question payload.
func DecodeChoice(raw json.RawMessage) (*ChoiceQuestion, error) {
	var q struct {
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &ChoiceQuestion{Instructions: q.Instructions, Criteria: q.Criteria}, nil
}

// DecodeScore decodes a score question payload.
func DecodeScore(raw json.RawMessage) (*ScoreQuestion, error) {
	var q struct {
		Instructions string   `json:"instructions"`
		Criteria     []string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &ScoreQuestion{Instructions: q.Instructions, Criteria: q.Criteria}, nil
}

// DecodeNoul decodes a noul question payload.
func DecodeNoul(raw json.RawMessage) (*NoulQuestion, error) {
	var q NoulQuestion
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, err
	}
	return &q, nil
}
