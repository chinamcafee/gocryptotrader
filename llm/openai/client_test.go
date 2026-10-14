package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

const testModel = "deepseek-chat"

func staticSecret(token string) httpclient.SecretFn {
	return func(context.Context) (string, error) { return token, nil }
}

// newLocalClient builds a client allowed to reach an httptest server.
func newLocalClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	c, err := NewClient(&Profile{
		BaseURL:               serverURL,
		Model:                 testModel,
		TimeoutSeconds:        5,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, staticSecret("test-token"))
	require.NoError(t, err, "a loopback profile with private access must be accepted")
	return c
}

// replyServer answers every request with the supplied body and status.
func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func chatReply(content, finish string) string {
	msg := map[string]any{"role": "assistant", "content": content}
	payload := map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   testModel,
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func userTurn(text string) []Message {
	return []Message{{Role: RoleUser, Content: text}}
}

func TestNewClientRejectsBadProfiles(t *testing.T) {
	t.Parallel()

	_, err := NewClient(&Profile{BaseURL: "https://api.example.com", Model: testModel}, nil)
	require.ErrorIs(t, err, httpclient.ErrNilSecret, "a nil secret callback must be refused")

	_, err = NewClient(&Profile{BaseURL: "https://api.example.com"}, staticSecret("x"))
	require.ErrorIs(t, err, ErrModelEmpty, "a profile without a model must be refused")

	_, err = NewClient(&Profile{BaseURL: "", Model: testModel}, staticSecret("x"))
	require.Error(t, err, "an empty endpoint must be refused")

	_, err = NewClient(&Profile{BaseURL: "http://api.example.com", Model: testModel}, staticSecret("x"))
	require.Error(t, err, "plain http to a public host must be refused")
}

func TestNewClientResolvesTheChatPath(t *testing.T) {
	t.Parallel()

	c, err := NewClient(&Profile{BaseURL: "https://api.deepseek.com/v1/", Model: testModel}, staticSecret("x"))
	require.NoError(t, err, "a well-formed profile must be accepted")
	assert.Equal(t, "https://api.deepseek.com/v1/chat/completions", c.Endpoint(), "the contract path should be appended once")
	assert.Equal(t, testModel, c.Model(), "the configured model should be exposed")
	assert.Equal(t, DefaultMaxTokens, c.MaxTokens(), "a profile without a token budget should use the measured default")
}

func TestChatCompletionHappyPath(t *testing.T) {
	t.Parallel()

	var seenAuthorization, seenContentType, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuthorization = r.Header.Get("Authorization")
		seenContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatReply(`{"action":"HOLD"}`, FinishStop))
	}))
	t.Cleanup(srv.Close)

	c := newLocalClient(t, srv.URL)
	resp, err := c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hello")})
	require.NoError(t, err, "a well-formed reply must be accepted")

	assert.Equal(t, "Bearer test-token", seenAuthorization, "the secret should be sent as a bearer token")
	assert.Equal(t, "application/json", seenContentType, "the request should declare its content type")
	assert.Contains(t, seenBody, `"model":"`+testModel+`"`, "the profile model should be filled in")
	assert.Contains(t, seenBody, `"max_tokens":`+strconv.Itoa(DefaultMaxTokens), "the default token budget should be applied")
	assert.NotContains(t, seenBody, "test-token", "the secret should never appear in the body")

	content, err := ContentOf(resp)
	require.NoError(t, err, "the reply must carry content")
	assert.JSONEq(t, `{"action":"HOLD"}`, content, "the content should round-trip unchanged")
	assert.Equal(t, 18, resp.Usage.Total(), "the provider's total should be preferred")
	assert.Equal(t, FinishStop, resp.FirstFinishReason(), "the finish reason should be readable")
	assert.Equal(t, testModel, resp.Model, "the served model should be reported for drift checks")
}

func TestChatCompletionAcceptsQuotedTokenCounts(t *testing.T) {
	t.Parallel()

	// Providers disagree on whether usage counts are bare numbers or strings, so
	// both must decode. A decision must not be lost to the quoting difference.
	body := `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":"1204","completion_tokens":"12","total_tokens":"1216"}}`
	srv := replyServer(t, http.StatusOK, body)

	c := newLocalClient(t, srv.URL)
	resp, err := c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.NoError(t, err, "a reply with quoted counts must be accepted")
	assert.Equal(t, 1216, resp.Usage.Total(), "a quoted total should be read like a bare one")
	assert.Equal(t, 1204, int(resp.Usage.PromptTokens), "a quoted prompt count should be read")
}

func TestChatCompletionFallsBackToTheSummedTotal(t *testing.T) {
	t.Parallel()

	body := `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":4}}`
	body = strings.ReplaceAll(body, "\n", "")
	srv := replyServer(t, http.StatusOK, body)

	c := newLocalClient(t, srv.URL)
	resp, err := c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.NoError(t, err, "a reply with a partial usage block must be accepted")
	assert.Equal(t, 14, resp.Usage.Total(), "the total should fall back to the sum when the provider omits it")
}

func TestChatCompletionRejectsAConflictingModel(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	c := newLocalClient(t, srv.URL)
	_, err := c.ChatCompletion(context.Background(), &ChatRequest{
		Model:    "some-other-model",
		Messages: userTurn("hi"),
	})
	require.ErrorIs(t, err, ErrModelMismatch, "a request must not silently run a different model")
	assert.False(t, called, "a rejected request should not reach the network")
}

func TestChatCompletionRejectsMalformedRequests(t *testing.T) {
	t.Parallel()

	c := newLocalClient(t, "http://127.0.0.1:1")

	_, err := c.ChatCompletion(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilRequest, "a nil request must be refused")

	_, err = c.ChatCompletion(context.Background(), &ChatRequest{})
	require.ErrorIs(t, err, ErrNoMessages, "a request with no messages must be refused")

	_, err = c.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []Message{{Role: "wizard", Content: "hi"}},
	})
	require.ErrorIs(t, err, ErrInvalidRole, "an unknown role must be refused before sending")

	var nilClient *Client
	_, err = nilClient.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.Error(t, err, "a nil client must refuse rather than panic")
}

func TestChatCompletionRejectsAnOversizedRequest(t *testing.T) {
	t.Parallel()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(srv.Close)

	c := newLocalClient(t, srv.URL)
	_, err := c.ChatCompletion(context.Background(), &ChatRequest{
		Messages: userTurn(strings.Repeat("x", MaxRequestBytes+1)),
	})
	require.ErrorIs(t, err, ErrRequestTooLarge, "a request over the cap must be refused")
	assert.False(t, called, "an oversized request should not reach the network")
}

func TestChatCompletionSurfacesProviderErrors(t *testing.T) {
	t.Parallel()

	srv := replyServer(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`)
	c := newLocalClient(t, srv.URL)
	_, err := c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})

	var status *httpclient.StatusError
	require.ErrorAs(t, err, &status, "a non-2xx reply must be reported as a status error")
	assert.True(t, status.IsAuthentication(), "a 401 should be recognisable as an authentication failure")
	assert.Contains(t, err.Error(), "invalid api key", "the provider's reason should reach the caller")
}

func TestChatCompletionRejectsAnEmptyBody(t *testing.T) {
	t.Parallel()

	srv := replyServer(t, http.StatusOK, "")
	c := newLocalClient(t, srv.URL)
	_, err := c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.ErrorIs(t, err, ErrInvalidJSON, "an empty body must be refused, not treated as an empty answer")
}

func TestParseChatResponse(t *testing.T) {
	t.Parallel()

	_, err := ParseChatResponse([]byte("not json"))
	require.ErrorIs(t, err, ErrInvalidJSON, "a non-JSON body must be refused")

	_, err = ParseChatResponse([]byte(`{"id":"x"}`))
	require.ErrorIs(t, err, ErrNoChoices, "a reply with no choices must be refused")

	// A provider that reports failure inside a 200 must not look like success.
	_, err = ParseChatResponse([]byte(`{"error":{"message":"model overloaded"}}`))
	require.ErrorIs(t, err, ErrNoChoices, "an error envelope must be refused")
	assert.Contains(t, err.Error(), "model overloaded", "the provider's message should be preserved")

	resp, err := ParseChatResponse([]byte(chatReply("{}", FinishStop)))
	require.NoError(t, err, "a well-formed reply must parse")
	require.Len(t, resp.Choices, 1, "the single choice must be preserved")
}

func TestContentOf(t *testing.T) {
	t.Parallel()

	_, err := ContentOf(nil)
	require.ErrorIs(t, err, ErrNoChoices, "a nil response must be refused")

	empty, err := ParseChatResponse([]byte(chatReply("   ", FinishStop)))
	require.NoError(t, err, "an empty-content reply is still a well-formed response")
	_, err = ContentOf(empty)
	require.ErrorIs(t, err, ErrContentMissing, "an empty answer must be an error, not an empty string")

	withTools, err := ParseChatResponse([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"getIndicators","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	require.NoError(t, err, "a tool-call reply must parse")
	content, err := ContentOf(withTools)
	require.NoError(t, err, "a tool-call reply with no text must not be a content error")
	assert.Empty(t, content, "there is no text to return")
}

func TestToolCallRoundTrip(t *testing.T) {
	t.Parallel()

	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"getIndicators","arguments":"{\"interval\":\"1h\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	t.Cleanup(srv.Close)

	c := newLocalClient(t, srv.URL)
	req := &ChatRequest{
		Messages:   userTurn("analyse"),
		Tools:      []ToolSpec{{Type: ToolTypeFunction, Function: ToolFunction{Name: "getIndicators", Description: "reads facts", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		ToolChoice: &ToolChoice{Mode: ToolChoiceAuto},
	}
	resp, err := c.ChatCompletion(context.Background(), req)
	require.NoError(t, err, "a tool-call reply must be accepted")

	assert.Contains(t, seenBody, `"tool_choice":"auto"`, "the tool choice should go out as a bare string")
	assert.Contains(t, seenBody, `"parameters":{"type":"object"}`, "the schema should be embedded as a document, not a string")

	msg, err := resp.FirstMessage()
	require.NoError(t, err, "the assistant message must be readable")
	require.Len(t, msg.ToolCalls, 1, "the requested call must round-trip")
	call := msg.ToolCalls[0]
	assert.Equal(t, "call_1", call.ID, "the call id should be preserved for the tool message")
	assert.Equal(t, "getIndicators", call.Function.Name, "the tool name should be preserved")

	var args struct {
		Interval string `json:"interval"`
	}
	require.NoError(t, call.Function.DecodeArguments(&args), "the stringified arguments must decode")
	assert.Equal(t, "1h", args.Interval, "the argument value should survive the double decode")

	// The tool message must carry the id the model asked about, or the follow-up
	// turn is rejected by every provider.
	follow := &ChatRequest{Messages: []Message{
		{Role: RoleAssistant, ToolCalls: msg.ToolCalls},
		{Role: RoleTool, ToolCallID: call.ID, Content: `{"ok":true}`},
	}}
	body, err := follow.Marshal()
	require.NoError(t, err, "the follow-up request must marshal")
	assert.Contains(t, string(body), `"tool_call_id":"call_1"`, "the tool result should reference the call it answers")
	assert.Contains(t, string(body), `"role":"assistant","content":""`, "an assistant turn with only tool calls should still carry a content field")
}

func TestToolCallArgumentsErrors(t *testing.T) {
	t.Parallel()

	var args map[string]any
	require.ErrorIs(t, ToolCallFunction{}.DecodeArguments(&args), ErrEmptyArguments, "missing arguments must be an error")
	require.ErrorIs(t, ToolCallFunction{Arguments: "   "}.DecodeArguments(&args), ErrEmptyArguments, "blank arguments must be an error")
	require.ErrorIs(t, ToolCallFunction{Arguments: "{oops"}.DecodeArguments(&args), ErrMalformedArguments, "unparsable arguments must be an error")
}

func TestToolChoiceWireForms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   ToolChoice
		want string
	}{
		{"auto", ToolChoice{Mode: ToolChoiceAuto}, `"auto"`},
		{"none", ToolChoice{Mode: ToolChoiceNone}, `"none"`},
		{"required", ToolChoice{Mode: ToolChoiceRequired}, `"required"`},
		{"named", ToolChoice{FunctionName: "getIndicators"}, `{"function":{"name":"getIndicators"},"type":"function"}`},
	} {
		got, err := json.Marshal(tc.in)
		require.NoErrorf(t, err, "%s must marshal", tc.name)
		assert.JSONEqf(t, tc.want, string(got), "%s should use its own wire form", tc.name)

		var back ToolChoice
		require.NoErrorf(t, json.Unmarshal(got, &back), "%s must unmarshal", tc.name)
		assert.Equalf(t, tc.in, back, "%s should round-trip so a recorded request replays", tc.name)
	}
}

func TestChatRequestMarshalRejectsOversize(t *testing.T) {
	t.Parallel()

	req := &ChatRequest{Messages: userTurn(strings.Repeat("y", MaxRequestBytes+1))}
	_, err := req.Marshal()
	require.ErrorIs(t, err, ErrRequestTooLarge, "the cap must be enforced at marshal time")

	req = &ChatRequest{Messages: userTurn("small")}
	body, err := req.Marshal()
	require.NoError(t, err, "a normal request must marshal")
	assert.NotEmpty(t, body, "the body should be produced")
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	t.Parallel()

	body, err := (&ChatRequest{Messages: userTurn("hi")}).Marshal()
	require.NoError(t, err, "a minimal request must marshal")

	// A nil temperature is the point: a decision call must not ask for variety.
	assert.NotContains(t, string(body), "temperature", "temperature should be omitted unless set")
	assert.NotContains(t, string(body), "tools", "tools should be omitted when there are none")
	assert.NotContains(t, string(body), "stream", "streaming should be off unless asked for")
	assert.NotContains(t, string(body), "response_format", "the response format should be omitted when unset")
}

func TestResponseFormatAndStreamFlags(t *testing.T) {
	t.Parallel()

	req := &ChatRequest{
		Messages:       userTurn("hi"),
		ResponseFormat: &ResponseFormat{Type: ResponseFormatJSONObject},
		Stream:         true,
	}
	body, err := req.Marshal()
	require.NoError(t, err, "the request must marshal")
	assert.Contains(t, string(body), `"response_format":{"type":"json_object"}`, "the JSON mode should be requested explicitly")
	assert.Contains(t, string(body), `"stream":true`, "the stream flag should be transmitted when set")
}

func TestTimeoutClamping(t *testing.T) {
	t.Parallel()

	assert.Equal(t, httpclient.DefaultTimeout, defaultTimeout(0), "an unset timeout should take the default")
	assert.Equal(t, httpclient.DefaultTimeout, defaultTimeout(-5), "a negative timeout should take the default")
	assert.Equal(t, httpclient.DefaultTimeout, defaultTimeout(maxTimeoutSeconds+1), "an excessive timeout should be clamped")
	assert.Equal(t, 5*time.Second, defaultTimeout(5), "a sane timeout should be honoured")
}

func TestSecretIsFetchedPerCall(t *testing.T) {
	t.Parallel()

	calls := 0
	secretFn := func(context.Context) (string, error) {
		calls++
		return "token-" + strconv.Itoa(calls), nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatReply("{}", FinishStop))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(&Profile{
		BaseURL:               srv.URL,
		Model:                 testModel,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, secretFn)
	require.NoError(t, err, "the profile must be accepted")

	for range 2 {
		_, err = c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
		require.NoError(t, err, "the call must succeed")
	}
	assert.Equal(t, 2, calls, "the secret should be resolved per request rather than cached on the client")
}

func TestSecretFailureStopsTheCall(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("a request must not be sent when the secret cannot be resolved")
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(&Profile{
		BaseURL:               srv.URL,
		Model:                 testModel,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, func(context.Context) (string, error) { return "", assert.AnError })
	require.NoError(t, err, "the profile itself is fine")

	_, err = c.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.Error(t, err, "a secret failure must fail the call")
	assert.Contains(t, err.Error(), "resolving endpoint secret", "the failure should say where it came from")

	empty, err := NewClient(&Profile{
		BaseURL:               srv.URL,
		Model:                 testModel,
		AllowInsecureLoopback: true,
		AllowPrivateNetwork:   true,
	}, staticSecret(""))
	require.NoError(t, err, "the profile itself is fine")
	_, err = empty.ChatCompletion(context.Background(), &ChatRequest{Messages: userTurn("hi")})
	require.ErrorIs(t, err, httpclient.ErrNilSecret, "an empty secret must be refused rather than sent as a blank token")
}

func TestChatResponseHelpersAreNilSafe(t *testing.T) {
	t.Parallel()

	var resp *ChatResponse
	_, err := resp.FirstMessage()
	require.ErrorIs(t, err, ErrNoChoices, "a nil response must not panic")
	assert.Empty(t, resp.FirstFinishReason(), "a nil response has no finish reason")

	empty := &ChatResponse{}
	_, err = empty.FirstMessage()
	require.ErrorIs(t, err, ErrNoChoices, "an empty response has no message")
	assert.Empty(t, empty.FirstFinishReason(), "an empty response has no finish reason")

	var envelope *ErrorEnvelope
	assert.Empty(t, envelope.ErrorMessage(), "a nil envelope should not panic")
}
