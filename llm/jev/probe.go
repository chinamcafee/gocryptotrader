package jev

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// maxTimeoutSeconds is the upper bound on a configured request timeout.
const maxTimeoutSeconds = 120

// maxProbeResponseBytes bounds a probe reply. A probe only carries a handful of
// numbers, so a large reply is itself a failure signal.
const maxProbeResponseBytes = 4096

// Probe layer identifiers. A Jev profile has exactly one layer because the
// contract has no tool calling to exercise.
const (
	// ProbeLayerSystemOne is the single Jev capability layer.
	ProbeLayerSystemOne = "SYSTEMONE"
)

// Synthetic question keys. They are deliberately distinct from the review keys so
// a reply can never be mistaken for a real assessment.
const (
	probeKeyChoice = "synthetic_choice"
	probeKeyScore  = "synthetic_score"
	probeKeyNoul   = "synthetic_noul"
)

// ProbeResult records the outcome of a capability probe.
type ProbeResult struct {
	Passed        bool
	Layer         string
	ErrorCode     string
	ErrorMessage  string
	LatencyMS     int64
	InputTokens   int
	OutputTokens  int
	ResponseModel string
}

// Error codes. They form the stable vocabulary a probe writes to the database and
// the webUI renders, so they must not be reworded casually.
const (
	// CodeAuthFailed marks an authentication failure.
	CodeAuthFailed = "AUTH_FAILED"
	// CodeRateLimited marks a rate-limit rejection.
	CodeRateLimited = "RATE_LIMITED"
	// CodeTimeout marks a request that exceeded its deadline.
	CodeTimeout = "TIMEOUT"
	// CodeNetworkError marks a transport failure.
	CodeNetworkError = "NETWORK_ERROR"
	// CodeUpstreamUnavailable marks a 5xx response.
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	// CodeResponseTooLarge marks a reply over the byte cap.
	CodeResponseTooLarge = "RESPONSE_TOO_LARGE"
	// CodeInvalidJSON marks a body that is not valid JSON.
	CodeInvalidJSON = "INVALID_JSON_RESPONSE"
	// CodeSchemaInvalid marks a well-formed body that violates the contract.
	CodeSchemaInvalid = "JEV_SCHEMA_INVALID"
	// CodeChoiceInvalid marks a choice answer failing its rules.
	CodeChoiceInvalid = "JEV_CHOICE_INVALID"
	// CodeScoreInvalid marks a score answer failing its rules.
	CodeScoreInvalid = "JEV_SCORE_INVALID"
	// CodeNoulInvalid marks a noul answer failing its rules.
	CodeNoulInvalid = "JEV_NOUL_INVALID"
	// CodeModelInvalid marks a model identifier that is not a pinned version.
	CodeModelInvalid = "JEV_MODEL_INVALID"
	// CodeModelDrift marks a served model differing from the requested one.
	CodeModelDrift = "JEV_MODEL_DRIFT"
	// CodeRequestTooLarge marks a request over the byte cap.
	CodeRequestTooLarge = "JEV_REQUEST_TOO_LARGE"
	// CodeInvalidBaseURL marks an endpoint that is missing or unusable.
	CodeInvalidBaseURL = "INVALID_BASE_URL"
)

// ErrorCode maps an error to the stable vocabulary.
//
// A caller that needs to persist or display a failure uses this rather than
// inspecting messages, so the vocabulary stays stable while wording changes.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if status, ok := errors.AsType[*httpclient.StatusError](err); ok {
		switch {
		case status.IsAuthentication():
			return CodeAuthFailed
		case status.StatusCode == http.StatusTooManyRequests:
			return CodeRateLimited
		case status.StatusCode >= 500:
			return CodeUpstreamUnavailable
		default:
			return fmt.Sprintf("HTTP_%d", status.StatusCode)
		}
	}
	switch {
	case errors.Is(err, httpclient.ErrResponseTooLarge):
		return CodeResponseTooLarge
	case errors.Is(err, ErrRequestTooLarge):
		return CodeRequestTooLarge
	case errors.Is(err, ErrInvalidJSON):
		return CodeInvalidJSON
	case errors.Is(err, ErrModelDrift):
		return CodeModelDrift
	case errors.Is(err, ErrModelInvalid), errors.Is(err, ErrModelMismatch):
		return CodeModelInvalid
	case errors.Is(err, ErrChoiceInvalid), errors.Is(err, ErrDistributionInvalid), errors.Is(err, ErrProbabilityInvalid):
		return CodeChoiceInvalid
	case errors.Is(err, ErrScoreInvalid):
		return CodeScoreInvalid
	case errors.Is(err, ErrNoulInvalid):
		return CodeNoulInvalid
	case errors.Is(err, ErrSchemaInvalid), errors.Is(err, ErrAnswerMissing), errors.Is(err, ErrQuestionsInvalid),
		errors.Is(err, ErrStateInvalid), errors.Is(err, ErrEvidenceMissing):
		return CodeSchemaInvalid
	case errors.Is(err, httpclient.ErrNilEndpoint), errors.Is(err, httpclient.ErrEmptyHost),
		errors.Is(err, httpclient.ErrInsecureScheme), errors.Is(err, httpclient.ErrEmbeddedCredentials),
		errors.Is(err, httpclient.ErrBlockedAddress):
		return CodeInvalidBaseURL
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") {
		return CodeTimeout
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return CodeNetworkError
	}
	return CodeNetworkError
}

// Probe exercises the endpoint with a synthetic request and validates the reply.
//
// The probe never carries market data: it may run against a misconfigured
// endpoint, so the only information it may reveal is that a request occurred.
// Every outcome, including failure, is reported in the result rather than as an
// error, so a caller can persist a probe row unconditionally.
func (c *Client) Probe(ctx context.Context) *ProbeResult {
	result := &ProbeResult{Layer: ProbeLayerSystemOne}
	if c == nil {
		result.ErrorCode = CodeSchemaInvalid
		result.ErrorMessage = "nil Jev client"
		return result
	}
	started := time.Now()
	defer func() { result.LatencyMS = time.Since(started).Milliseconds() }()

	req, err := c.buildProbeRequest()
	if err != nil {
		result.ErrorCode = ErrorCode(err)
		result.ErrorMessage = err.Error()
		return result
	}
	body, err := req.Marshal()
	if err != nil {
		result.ErrorCode = ErrorCode(err)
		result.ErrorMessage = err.Error()
		return result
	}
	payload, err := c.post(ctx, body)
	if err != nil {
		result.ErrorCode = ErrorCode(err)
		result.ErrorMessage = err.Error()
		return result
	}
	var resp SystemOneResponse
	if err = json.Unmarshal(payload, &resp); err != nil {
		result.ErrorCode = CodeInvalidJSON
		result.ErrorMessage = err.Error()
		return result
	}
	result.InputTokens = resp.Usage.InputTokens
	result.OutputTokens = resp.Usage.OutputTokens
	result.ResponseModel = resp.Model

	if err = validateProbeResponse(req, &resp); err != nil {
		result.ErrorCode = ErrorCode(err)
		result.ErrorMessage = err.Error()
		return result
	}
	result.Passed = true
	return result
}

// buildProbeRequest assembles the synthetic three question request.
func (c *Client) buildProbeRequest() (*SystemOneRequest, error) {
	choice, err := json.Marshal(ChoiceQuestion{
		Instructions: "Synthetic capability test. Select the option marked as the expected answer.",
		Criteria: map[string]string{
			"expected": "The answer the probe expects",
			"other":    "Any other answer",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQuestionsInvalid, err)
	}
	score, err := json.Marshal(ScoreQuestion{
		Instructions: "Synthetic capability test. Rate the supplied note on the three point scale.",
		Criteria:     evidenceQualityScale,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQuestionsInvalid, err)
	}
	noul, err := json.Marshal(NoulQuestion{
		Instructions: "Synthetic capability test. Is this request a synthetic test?",
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQuestionsInvalid, err)
	}
	return &SystemOneRequest{
		Model: c.model,
		State: NewSyntheticProbeState(),
		Questions: map[string]json.RawMessage{
			probeKeyChoice: choice,
			probeKeyScore:  score,
			probeKeyNoul:   noul,
		},
	}, nil
}

// validateProbeResponse checks the synthetic reply against the same rules the
// review path uses, so a passing probe exercises the real validation code.
func validateProbeResponse(req *SystemOneRequest, resp *SystemOneResponse) error {
	if !IsPinnedModel(req.Model) {
		return fmt.Errorf("%w: %q", ErrModelInvalid, req.Model)
	}
	if strings.TrimSpace(resp.Model) != req.Model {
		return fmt.Errorf("%w: requested %q, served %q", ErrModelDrift, req.Model, resp.Model)
	}

	var envelope struct {
		Choice *ChoiceAnswer `json:"synthetic_choice"`
		Score  *ScoreAnswer  `json:"synthetic_score"`
		Noul   *NoulAnswer   `json:"synthetic_noul"`
	}
	if err := json.Unmarshal(resp.Answers, &envelope); err != nil {
		return fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	if envelope.Choice == nil || envelope.Score == nil || envelope.Noul == nil {
		return fmt.Errorf("%w: probe reply is missing a synthetic answer", ErrAnswerMissing)
	}
	choiceQuestion, err := DecodeChoice(req.Questions[probeKeyChoice])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrQuestionsInvalid, err)
	}
	scoreQuestion, err := DecodeScore(req.Questions[probeKeyScore])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrQuestionsInvalid, err)
	}
	if err := ValidateChoiceAnswer(choiceQuestion, envelope.Choice); err != nil {
		return err
	}
	if err := ValidateScoreAnswer(scoreQuestion, envelope.Score); err != nil {
		return err
	}
	return ValidateNoulAnswer(envelope.Noul)
}

// ProbeResponseCap is the response cap a probe profile should be built with.
func ProbeResponseCap() int64 { return maxProbeResponseBytes }
