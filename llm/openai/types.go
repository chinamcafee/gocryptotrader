// Package openai adapts the OpenAI-compatible chat completions contract.
//
// The scope is deliberately the intersection that DeepSeek, OpenAI, Azure
// OpenAI, Moonshot, Qwen and local vLLM/Ollama all implement
// (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §1):
//
//   - one endpoint, POST {baseURL}/chat/completions;
//   - messages, tools / tool_choice / tool_calls, response_format json_object;
//   - no Responses API, no Assistants API, no vendor-specific extensions.
//
// Nothing here decides anything. The package builds requests, sends them and
// parses replies; every acceptance rule that matters (schema, scope, evidence)
// lives in the pipeline, so a provider that answers nonsense cannot smuggle a
// decision past the type system.
package openai

import (
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/types"
)

// Endpoint and protocol constants.
const (
	// ChatCompletionsPath is the only endpoint this package talks to. It is
	// appended to the configured base URL, which usually already ends in /v1.
	ChatCompletionsPath = "/chat/completions"
	// MaxRequestBytes caps the serialised request. The cap aborts rather than
	// truncates: a truncated prompt would send the model a different question
	// than the one recorded in the audit trail.
	MaxRequestBytes = 256 * 1024
	// DefaultMaxTokens is the reply budget applied when a profile does not set
	// one. The reference project measured that 512 truncates a real candidate,
	// so the default is higher than a naive guess would be.
	DefaultMaxTokens = 1024
	// maxTimeoutSeconds is the upper bound on a configured request timeout.
	maxTimeoutSeconds = 120
)

// Chat message roles.
const (
	// RoleSystem carries the versioned system prompt.
	RoleSystem = "system"
	// RoleUser carries the snapshot and the task framing.
	RoleUser = "user"
	// RoleAssistant carries model output, including its tool calls.
	RoleAssistant = "assistant"
	// RoleTool carries one tool result back to the model.
	RoleTool = "tool"
)

// Finish reasons.
const (
	// FinishStop marks a reply that ended on its own.
	FinishStop = "stop"
	// FinishLength marks a reply cut off by the token budget. A truncated reply
	// can never be parsed into a candidate, so it is a failure, not a warning.
	FinishLength = "length"
	// FinishToolCalls marks a reply that requests tool execution.
	FinishToolCalls = "tool_calls"
	// FinishContentFilter marks a reply withheld by the provider.
	FinishContentFilter = "content_filter"
)

// Tool choice modes.
const (
	// ToolChoiceAuto lets the model decide whether to call a tool.
	ToolChoiceAuto = "auto"
	// ToolChoiceNone forbids tool calls, used on the final round so the model
	// must answer with the candidate JSON.
	ToolChoiceNone = "none"
	// ToolChoiceRequired forces a tool call.
	ToolChoiceRequired = "required"
)

// Response format types.
const (
	// ResponseFormatJSONObject asks for a JSON object body. It is not a schema
	// guarantee, which is why the reply still passes server-side validation.
	ResponseFormatJSONObject = "json_object"
	// ResponseFormatText is the provider default.
	ResponseFormatText = "text"
)

// ToolTypeFunction is the only tool kind in the contract.
const ToolTypeFunction = "function"

// Message is one entry of the conversation.
//
// Content is always emitted, including as an empty string on an assistant
// message that only carries tool calls: providers disagree about an omitted
// field, and every one of them accepts the empty string.
type Message struct {
	Role string `json:"role"`
	// Content is the message text. For a tool message it is the serialised tool
	// result, already truncated by the caller.
	Content string `json:"content"`
	// Name is the optional participant name.
	Name string `json:"name,omitempty"`
	// ToolCalls is set on an assistant message that requested tools.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool message to the call it answers. Dropping it makes
	// the conversation invalid for every provider.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Refusal is set instead of Content when a provider withheld the answer.
	// Reading it is deterministic, unlike guessing a refusal from prose.
	Refusal string `json:"refusal,omitempty"`
}

// ToolCall is a model request to run one tool.
type ToolCall struct {
	// ID is the provider-assigned call id, echoed back on the tool message.
	ID string `json:"id"`
	// Type is always ToolTypeFunction.
	Type string `json:"type"`
	// Function carries the name and the JSON-encoded arguments.
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the payload of a tool call.
type ToolCallFunction struct {
	// Name is the tool name.
	Name string `json:"name"`
	// Arguments is a JSON document encoded as a string, not a nested object.
	// It is kept as a string so the caller validates it explicitly rather than
	// trusting the provider's shape.
	Arguments string `json:"arguments"`
}

// DecodeArguments decodes the JSON-encoded arguments into v.
//
// The double decode is not incidental: the wire format is a string containing
// JSON, and turning that into a structured value is the first place a hostile
// reply can be caught.
func (f ToolCallFunction) DecodeArguments(v any) error {
	if strings.TrimSpace(f.Arguments) == "" {
		return ErrEmptyArguments
	}
	if err := json.Unmarshal([]byte(f.Arguments), v); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedArguments, err)
	}
	return nil
}

// ToolFunction describes one callable function.
type ToolFunction struct {
	// Name is the identifier the model must use.
	Name string `json:"name"`
	// Description is what the model reads to choose the tool.
	Description string `json:"description"`
	// Parameters is the JSON Schema of the arguments. It is a raw document
	// because schemas are data, not something this package should reinterpret.
	Parameters json.RawMessage `json:"parameters"`
	// Strict requests provider-side schema enforcement where it is supported.
	// It defaults to false because support is uneven and a rejected request is
	// worse than a validated one.
	Strict bool `json:"strict,omitempty"`
}

// ToolSpec is one entry of the tools array.
type ToolSpec struct {
	// Type is always ToolTypeFunction.
	Type string `json:"type"`
	// Function is the callable being described.
	Function ToolFunction `json:"function"`
}

// ToolChoice selects how the model may use tools.
//
// The wire form is either a bare string ("auto" / "none" / "required") or an
// object naming a function. Marshalling keeps those two shapes distinct, because
// a provider that accepts "auto" rejects {"mode": "auto"}.
type ToolChoice struct {
	// Mode is one of the ToolChoice* constants.
	Mode string
	// FunctionName, when set, forces that one function.
	FunctionName string
}

// MarshalJSON emits the bare string or the named-function object.
func (t ToolChoice) MarshalJSON() ([]byte, error) {
	if t.FunctionName == "" {
		return json.Marshal(t.Mode)
	}
	return json.Marshal(map[string]any{
		"type":     ToolTypeFunction,
		"function": map[string]string{"name": t.FunctionName},
	})
}

// UnmarshalJSON accepts either wire shape, so a recorded request replays.
func (t *ToolChoice) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err == nil {
		t.Mode = mode
		t.FunctionName = ""
		return nil
	}
	var obj struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	t.Mode = ""
	t.FunctionName = obj.Function.Name
	return nil
}

// ResponseFormat asks for a JSON-object reply.
type ResponseFormat struct {
	// Type is ResponseFormatJSONObject or ResponseFormatText.
	Type string `json:"type"`
}

// Usage reports token consumption.
//
// The counts are types.Number because providers disagree on whether they arrive
// as bare numbers or quoted strings, and a decision record must not be lost to
// that difference.
type Usage struct {
	PromptTokens     types.Number `json:"prompt_tokens"`
	CompletionTokens types.Number `json:"completion_tokens"`
	TotalTokens      types.Number `json:"total_tokens"`
}

// Total returns the total token count, preferring the provider's own figure and
// falling back to the sum when it is absent.
func (u Usage) Total() int {
	if u.TotalTokens > 0 {
		return int(u.TotalTokens)
	}
	return int(u.PromptTokens + u.CompletionTokens)
}

// Choice is one candidate completion.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatRequest is one chat completion call.
type ChatRequest struct {
	// Model is the provider model identifier. The client fills it from the
	// profile when it is empty and refuses a conflicting value.
	Model string `json:"model"`
	// Messages is the conversation so far, oldest first.
	Messages []Message `json:"messages"`
	// MaxTokens bounds the reply.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Temperature is left nil on purpose for decision calls: determinism is
	// worth more than variety when the output is a trading instruction.
	Temperature *float64 `json:"temperature,omitempty"`
	// ResponseFormat asks for JSON output.
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	// Tools are the callable functions.
	Tools []ToolSpec `json:"tools,omitempty"`
	// ToolChoice selects how tools may be used.
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
	// Stream is set only by the chat path. A decision call must never stream:
	// half a JSON document cannot be validated.
	Stream bool `json:"stream,omitempty"`
	// StreamOptions asks the provider to attach usage to the stream's final
	// chunk; a streamed reply's token counts are only known at its end.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions is the streaming request's options block.
type StreamOptions struct {
	// IncludeUsage asks for a trailing chunk carrying the token counts.
	IncludeUsage bool `json:"include_usage"`
}

// Marshal renders the request body, refusing anything over MaxRequestBytes.
func (r *ChatRequest) Marshal() ([]byte, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxRequestBytes {
		return nil, ErrRequestTooLarge
	}
	return body, nil
}

// ChatResponse is one chat completion reply.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// FirstMessage returns the first choice's message, so callers do not index a
// slice that may be empty.
func (r *ChatResponse) FirstMessage() (*Message, error) {
	if r == nil || len(r.Choices) == 0 {
		return nil, ErrNoChoices
	}
	return &r.Choices[0].Message, nil
}

// FirstFinishReason returns the first choice's finish reason.
func (r *ChatResponse) FirstFinishReason() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
}

// ErrorEnvelope is the provider error shape. Several providers wrap their
// message differently, so only Message and Type are read and the rest is kept
// for the log.
type ErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// ErrorMessage returns the provider's message or an empty string.
func (e *ErrorEnvelope) ErrorMessage() string {
	if e == nil {
		return ""
	}
	return e.Error.Message
}
