package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// Probe layers. The names are the values written to the probe table's layer
// column, so they are part of the stored vocabulary rather than a label.
//
// The layers exist because each one fails differently, and a single "is the
// endpoint alive" check cannot tell an operator which capability to fix:
// a wrong key fails AUTH, a provider without function calling fails TOOL_CALL,
// and a model that invents trades on missing data fails SCHEMA.
const (
	// ProbeLayerAuth is the minimal authenticated round trip.
	ProbeLayerAuth = "AUTH"
	// ProbeLayerToolCall is a function-calling round trip including the
	// tool_call_id backfill.
	ProbeLayerToolCall = "TOOL_CALL"
	// ProbeLayerSchema requires a full candidate on an empty snapshot.
	ProbeLayerSchema = "SCHEMA"
	// ProbeLayerStream checks that streaming is available for the chat path.
	ProbeLayerStream = "STREAM"
)

// ProbeResponseCap bounds a probe reply. A probe carries a handful of numbers,
// so a large reply is itself a signal, and keeping the cap small bounds what a
// misconfigured endpoint can send back.
const (
	ProbeResponseCap = 32 * 1024
	// probeMaxTokens is the reply budget for a probe. It is smaller than a real
	// analysis budget because a probe answer is short by construction.
	probeMaxTokens = 256
	// probeSchemaMaxTokens is the SCHEMA layer's budget: that layer must carry
	// a complete candidate, and reasoning-capable models spend budget before
	// the answer starts. (Found live 2026-10-16: 256 truncated deepseek-flash;
	// 1024 was also consumed whole by the model's reasoning, 4096 clears it.)
	probeSchemaMaxTokens = 4096
	// sseDone is the terminator every OpenAI-compatible stream ends with.
	sseDone = "[DONE]"
)

// Synthetic identifiers for the SCHEMA layer. They are fixed so the probe is
// deterministic, and clearly impossible as real identifiers so a probe reply
// can never be mistaken for a decision.
const (
	probeStrategyVersionID = "00000000-0000-4000-8000-00000000dead"
	probeSnapshotID        = "00000000-0000-4000-8000-00000000beef"
	probeMarketID          = "PROBE"
	probeToolName          = "getProbeFacts"
	probeTTLSeconds        = 30
)

// ProbeResult records the outcome of a capability probe.
//
// Every outcome, including failure, is reported here rather than as an error, so
// a caller can persist a probe row unconditionally. The fields mirror the
// provider_probe_results columns (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §3).
type ProbeResult struct {
	// Passed is true only when the layer's check completed.
	Passed bool
	// Layer is the layer that ran.
	Layer string
	// ErrorCode is the stable code, empty when the layer passed.
	ErrorCode string
	// ErrorMessage explains the failure in prose, never carrying market data or
	// the secret.
	ErrorMessage string
	// LatencyMS is the wall time of the probe.
	LatencyMS int64
	// InputTokens and OutputTokens are reported where the layer sees them.
	InputTokens  int
	OutputTokens int
	// ResponseModel is the served model, recorded so drift is visible.
	ResponseModel string
}

// Probe exercises one capability layer.
//
// The probe deliberately carries no market data: it may run against a
// misconfigured endpoint, so the only information it may reveal is that a
// request occurred (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §3).
func (c *Client) Probe(ctx context.Context, layer string) *ProbeResult {
	result := &ProbeResult{Layer: layer}
	if c == nil {
		result.ErrorCode = CodeChatSchemaInvalid
		result.ErrorMessage = "nil OpenAI client"
		return result
	}
	started := time.Now()
	defer func() { result.LatencyMS = time.Since(started).Milliseconds() }()

	var err error
	switch layer {
	case ProbeLayerAuth:
		err = c.probeAuth(ctx, result)
	case ProbeLayerToolCall:
		err = c.probeToolCall(ctx, result)
	case ProbeLayerSchema:
		err = c.probeSchema(ctx, result)
	case ProbeLayerStream:
		err = c.probeStream(ctx, result)
	default:
		err = fmt.Errorf("%w: %q", ErrUnsupportedLayer, layer)
	}
	if err != nil {
		result.Passed = false
		result.ErrorCode = ErrorCode(err)
		result.ErrorMessage = err.Error()
		return result
	}
	result.Passed = true
	return result
}

// ProbeAll runs every layer in order and returns each result.
//
// The layers are independent: a caller that only needs the conversational path
// still gets the stream verdict even when tool calling failed, because a
// provider can support one and not the other.
func (c *Client) ProbeAll(ctx context.Context) []*ProbeResult {
	layers := []string{ProbeLayerAuth, ProbeLayerToolCall, ProbeLayerSchema, ProbeLayerStream}
	results := make([]*ProbeResult, 0, len(layers))
	for _, layer := range layers {
		results = append(results, c.Probe(ctx, layer))
	}
	return results
}

// probeAuth performs the minimal authenticated round trip.
func (c *Client) probeAuth(ctx context.Context, result *ProbeResult) error {
	resp, err := c.ChatCompletion(ctx, &ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "You are a connectivity probe. Reply with the single word OK."},
			{Role: RoleUser, Content: "Reply with OK."},
		},
		MaxTokens: probeMaxTokens,
	})
	if err != nil {
		return err
	}
	recordUsage(result, resp)
	return checkUsable(resp)
}

// probeToolCall exercises function calling, including the tool result backfill.
//
// The second round is the part that matters: a provider can emit tool_calls and
// still be unusable if the follow-up turn with tool_call_id is rejected, which
// only shows up when the round is actually completed.
func (c *Client) probeToolCall(ctx context.Context, result *ProbeResult) error {
	spec := ToolSpec{
		Type: ToolTypeFunction,
		Function: ToolFunction{
			Name:        probeToolName,
			Description: "Returns a fixed acknowledgement. It is the only tool in this capability probe.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"An opaque key"}},"required":["key"],"additionalProperties":false}`),
		},
	}
	choice := ToolChoice{Mode: ToolChoiceAuto}
	messages := []Message{
		{Role: RoleSystem, Content: "You are a tool-calling probe. Call the tool you are given, then answer with OK."},
		{Role: RoleUser, Content: "Call getProbeFacts with {\"key\":\"probe\"}, then answer with OK."},
	}

	first, err := c.ChatCompletion(ctx, &ChatRequest{
		Messages:    messages,
		Tools:       []ToolSpec{spec},
		ToolChoice:  &choice,
		MaxTokens:   probeMaxTokens,
		Temperature: new(float64),
	})
	if err != nil {
		return err
	}
	recordUsage(result, first)
	if err := checkUsable(first); err != nil {
		return err
	}

	msg, err := first.FirstMessage()
	if err != nil {
		return err
	}
	if len(msg.ToolCalls) == 0 {
		return fmt.Errorf("%w: reply carried no tool_calls", ErrToolCallUnsupported)
	}
	call := msg.ToolCalls[0]
	if call.Function.Name != probeToolName {
		return fmt.Errorf("%w: got %q", ErrToolCallUnsupported, call.Function.Name)
	}
	if strings.TrimSpace(call.ID) == "" {
		return fmt.Errorf("%w: tool call has no id to backfill", ErrToolRoundIncomplete)
	}
	var args struct {
		Key string `json:"key"`
	}
	if err = call.Function.DecodeArguments(&args); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedArguments, err)
	}
	if args.Key != "probe" {
		return fmt.Errorf("%w: the tool arguments did not round-trip", ErrMalformedArguments)
	}

	none := ToolChoice{Mode: ToolChoiceNone}
	second, err := c.ChatCompletion(ctx, &ChatRequest{
		Messages: append(append([]Message{}, messages...),
			Message{Role: RoleAssistant, ToolCalls: msg.ToolCalls},
			Message{Role: RoleTool, ToolCallID: call.ID, Content: `{"ok":true}`},
		),
		Tools:       []ToolSpec{spec},
		ToolChoice:  &none,
		MaxTokens:   probeMaxTokens,
		Temperature: new(float64),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrToolRoundIncomplete, err)
	}
	recordUsage(result, second)
	if err = checkUsable(second); err != nil {
		return fmt.Errorf("%w: %w", ErrToolRoundIncomplete, err)
	}
	content, err := ContentOf(second)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrToolRoundIncomplete, err)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: the follow-up turn was empty", ErrToolRoundIncomplete)
	}
	return nil
}

// probeSchema requires a complete candidate on a snapshot with no data at all.
//
// This is the layer that catches the failures that matter: a model that invents
// an action when there is nothing to act on, a model that cannot hold the output
// shape, and a model that answers a different question than the one asked. The
// reference project caught exactly these with a live model pair.
func (c *Client) probeSchema(ctx context.Context, result *ProbeResult) error {
	resp, err := c.ChatCompletion(ctx, &ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: probeSchemaSystemPrompt},
			{Role: RoleUser, Content: probeSchemaUserPrompt},
		},
		ResponseFormat: &ResponseFormat{Type: ResponseFormatJSONObject},
		MaxTokens:      probeSchemaMaxTokens,
		Temperature:    new(float64),
	})
	if err != nil {
		return err
	}
	recordUsage(result, resp)
	if err := checkUsable(resp); err != nil {
		return err
	}
	content, err := ContentOf(resp)
	if err != nil {
		return err
	}
	return validateProbeCandidate(content)
}

// probeStream verifies that the chat path can stream.
func (c *Client) probeStream(ctx context.Context, result *ProbeResult) error {
	req := &ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "You are a streaming probe. Reply with the single word OK."},
			{Role: RoleUser, Content: "Reply with OK."},
		},
		MaxTokens: probeMaxTokens,
		Stream:    true,
	}
	// The stream path posts directly, so it must normalise itself: skipping
	// this sent an empty model to the provider (found live 2026-10-16:
	// HTTP 400 "you passed ." on deepseek-flash).
	if err := c.normaliseRequest(req); err != nil {
		return err
	}
	body, err := req.Marshal()
	if err != nil {
		return err
	}
	payload, err := c.post(ctx, body, ProbeResponseCap)
	if err != nil {
		return err
	}
	frames, done := ParseSSE(payload)
	if !done || len(frames) == 0 {
		return fmt.Errorf("%w: no %s sentinel and %d frames", ErrStreamUnsupported, sseDone, len(frames))
	}
	for _, frame := range frames {
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err = json.Unmarshal([]byte(frame), &chunk); err != nil {
			return fmt.Errorf("%w: stream frame is not valid JSON", ErrInvalidJSON)
		}
		if chunk.Model != "" {
			result.ResponseModel = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			result.InputTokens = chunk.Usage.PromptTokens
			result.OutputTokens = chunk.Usage.CompletionTokens
		}
	}
	return nil
}

// checkUsable refuses a reply that no caller could act on.
func checkUsable(resp *ChatResponse) error {
	switch resp.FirstFinishReason() {
	case FinishLength:
		return ErrTruncated
	case FinishContentFilter:
		return ErrRefused
	}
	msg, err := resp.FirstMessage()
	if err != nil {
		return err
	}
	if strings.TrimSpace(msg.Refusal) != "" {
		return fmt.Errorf("%w: %s", ErrRefused, msg.Refusal)
	}
	return nil
}

func recordUsage(result *ProbeResult, resp *ChatResponse) {
	result.InputTokens = int(resp.Usage.PromptTokens)
	result.OutputTokens = int(resp.Usage.CompletionTokens)
	if resp.Model != "" {
		result.ResponseModel = resp.Model
	}
}

// validateProbeCandidate checks the reply against the real candidate contract
// plus the two rules that only a probe can state.
func validateProbeCandidate(content string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		return fmt.Errorf("%w: %w", ErrCandidateNotObject, err)
	}
	raw, present := fields["proposedMaxNotionalUsd"]
	if !present {
		return ErrCandidateNotionalNotExplicit
	}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "null" {
		return fmt.Errorf("%w: got %s", ErrCandidateNotionalNotExplicit, trimmed)
	}

	var cand candidate.Candidate
	if err := json.Unmarshal([]byte(content), &cand); err != nil {
		return fmt.Errorf("%w: %w", ErrCandidateNotObject, err)
	}
	if err := cand.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrCandidateInvalid, err)
	}
	if cand.Action != candidate.ActionHold {
		return fmt.Errorf("%w: got %q", ErrCandidateNotHold, cand.Action)
	}
	return nil
}

// ParseSSE splits a stream body into data frames and reports whether the
// terminator was seen.
//
// A frame is returned without its "data: " prefix. Comment lines and blank
// separators are skipped; a truncated final frame is still returned, because
// partial content is the caller's problem to reject, not this function's to
// hide.
func ParseSSE(payload []byte) (frames []string, done bool) {
	for line := range strings.SplitSeq(string(payload), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == sseDone {
			done = true
			continue
		}
		frames = append(frames, data)
	}
	return frames, done
}

// probeSchemaSystemPrompt is the layer-③ system prompt: the model must answer
// the real candidate contract on a request that carries no market data at all.
// It is probe-only and must never be reused as a production prompt.
const probeSchemaSystemPrompt = `You answer with one JSON candidate object and nothing else.
No market data is available: no evidence, no prices, no account facts.
Return exactly these keys: strategyVersionId, snapshotId, marketId, action, thesis,
evidenceRefs, invalidation, suggestedEntry, suggestedExit, proposedMaxNotionalUsd,
uncertainty, ttlSeconds.
Rules that are checked automatically:
- strategyVersionId and snapshotId must be copied exactly from the user message.
- With no data you cannot justify any trade, so action must be "HOLD".
- thesis and invalidation must be nonempty strings.
- proposedMaxNotionalUsd must be present and exactly null.
- evidenceRefs must contain the snapshotId as its first entry.
- uncertainty must be one of LOW, MEDIUM, HIGH.
- ttlSeconds must be between 5 and 300.`

// probeSchemaUserPrompt describes the absent data instead of supplying any:
// naming an instrument, an exchange or a timeframe would leak what the
// operator is watching, so the synthetic snapshot names nothing.
const probeSchemaUserPrompt = `{"snapshotId":"00000000-0000-4000-8000-00000000beef","strategyVersionId":"00000000-0000-4000-8000-00000000beef","marketId":"none","facts":{}}`

// Error codes. They form the stable vocabulary the probe writes to the database
// and the webUI renders (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §9), so they
// must not be reworded casually.
const (
	// CodeInvalidBaseURL marks an endpoint that is missing or unusable.
	CodeInvalidBaseURL = "INVALID_BASE_URL"
	// CodeAuthFailed marks an authentication failure.
	CodeAuthFailed = "AUTH_FAILED"
	// CodeRateLimited marks a rate-limit rejection.
	CodeRateLimited = "RATE_LIMITED"
	// CodeUpstreamUnavailable marks a 5xx response.
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	// CodeTimeout marks a request that exceeded its deadline.
	CodeTimeout = "TIMEOUT"
	// CodeNetworkError marks a transport failure.
	CodeNetworkError = "NETWORK_ERROR"
	// CodeResponseTooLarge marks a reply over the byte cap.
	CodeResponseTooLarge = "RESPONSE_TOO_LARGE"
	// CodeInvalidJSON marks a body that is not valid JSON.
	CodeInvalidJSON = "INVALID_JSON_RESPONSE"
	// CodeChatSchemaInvalid marks a reply outside the chat contract.
	CodeChatSchemaInvalid = "CHAT_SCHEMA_INVALID"
	// CodeChatContentMissing marks a reply with no content and no tool calls.
	CodeChatContentMissing = "CHAT_CONTENT_MISSING"
	// CodeTruncated marks a reply cut off by the token budget.
	CodeTruncated = "TRUNCATED"
	// CodeRefused marks a reply the provider withheld or the model declined.
	CodeRefused = "REFUSED"
	// CodeStreamUnsupported marks an endpoint that did not stream.
	CodeStreamUnsupported = "STREAM_UNSUPPORTED"
	// CodeToolCallUnsupported marks an endpoint that did not call the tool.
	CodeToolCallUnsupported = "TOOL_CALL_UNSUPPORTED"
	// CodeToolArgumentsInvalid marks tool arguments that do not decode.
	CodeToolArgumentsInvalid = "TOOL_ARGUMENTS_INVALID"
	// CodeToolRoundIncomplete marks an unfinished tool round.
	CodeToolRoundIncomplete = "TOOL_ROUND_INCOMPLETE"
	// CodeCandidateSchemaInvalid marks a reply that is not a usable candidate.
	CodeCandidateSchemaInvalid = "CANDIDATE_SCHEMA_INVALID"
	// CodeCandidateIncomplete marks a candidate missing required fields.
	CodeCandidateIncomplete = "CANDIDATE_INCOMPLETE"
	// CodeRequestTooLarge marks a request over the byte cap.
	CodeRequestTooLarge = "REQUEST_TOO_LARGE"
)

// ErrorCode maps an error to the stable vocabulary.
//
// A caller that needs to persist or display a failure uses this rather than
// inspecting messages, so the vocabulary stays stable while wording changes.
//
// Ordering rule: a specific protocol fault keeps its own code even when a
// broader wrapper was applied on top. "The second tool round returned an
// unreadable body" is more useful to an operator as INVALID_JSON_RESPONSE than
// as TOOL_ROUND_INCOMPLETE, because it names the fault rather than the stage.
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
	case errors.Is(err, ErrStreamUnsupported):
		return CodeStreamUnsupported
	case errors.Is(err, ErrToolCallUnsupported):
		return CodeToolCallUnsupported
	case errors.Is(err, ErrMalformedArguments), errors.Is(err, ErrEmptyArguments):
		return CodeToolArgumentsInvalid
	case errors.Is(err, ErrToolRoundIncomplete):
		return CodeToolRoundIncomplete
	case errors.Is(err, candidate.ErrCandidateIncomplete):
		// Checked before the generic candidate wrapper so the more specific code
		// wins: "a required field is missing" is a different fix from "the reply
		// is not a candidate at all".
		return CodeCandidateIncomplete
	case errors.Is(err, ErrCandidateInvalid), errors.Is(err, ErrCandidateNotObject),
		errors.Is(err, ErrCandidateNotHold), errors.Is(err, ErrCandidateNotionalNotExplicit):
		return CodeCandidateSchemaInvalid
	case errors.Is(err, ErrTruncated):
		return CodeTruncated
	case errors.Is(err, ErrRefused):
		return CodeRefused
	case errors.Is(err, ErrContentMissing):
		return CodeChatContentMissing
	case errors.Is(err, ErrNoChoices), errors.Is(err, ErrNoMessages), errors.Is(err, ErrInvalidRole),
		errors.Is(err, ErrModelEmpty), errors.Is(err, ErrModelMismatch):
		return CodeChatSchemaInvalid
	case errors.Is(err, httpclient.ErrNilEndpoint), errors.Is(err, httpclient.ErrEmptyHost),
		errors.Is(err, httpclient.ErrInsecureScheme), errors.Is(err, httpclient.ErrEmbeddedCredentials),
		errors.Is(err, httpclient.ErrBlockedAddress):
		return CodeInvalidBaseURL
	case errors.Is(err, ErrUnsupportedLayer):
		return CodeChatSchemaInvalid
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	}
	lowered := strings.ToLower(err.Error())
	if strings.Contains(lowered, "timeout") || strings.Contains(lowered, "deadline exceeded") {
		return CodeTimeout
	}
	return CodeNetworkError
}
