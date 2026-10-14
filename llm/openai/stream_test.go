package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// sseServer writes the supplied event strings verbatim with an event-stream
// content type, so a test controls exactly where events begin and end.
func sseServer(t *testing.T, events ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, event := range events {
			_, _ = io.WriteString(w, event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatCompletionStreamAssemblesDeltasAndLateUsage(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			return
		}
		if !assert.NoError(t, json.Unmarshal(body, &requestBody)) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keep-alive comment\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"区间\"},\"finish_reason\":\"\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"震荡。\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5,\"total_tokens\":17}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	client := newLocalClient(t, srv.URL)

	var deltas []string
	assembled, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"区间", "震荡。"}, deltas, "deltas should arrive in order and exactly once")

	message, err := assembled.FirstMessage()
	require.NoError(t, err)
	assert.Equal(t, "区间震荡。", message.Content, "the assembled reply is what a non-streaming call would have returned")
	assert.Equal(t, "stop", assembled.Choices[0].FinishReason)
	assert.Equal(t, 17, assembled.Usage.Total(), "usage arrives in the trailing chunk and should settle the counts")

	assert.Equal(t, true, requestBody["stream"], "the stream flag is implied by the method, not left to the caller")
	options, ok := requestBody["stream_options"].(map[string]any)
	require.True(t, ok, "usage must be asked for explicitly or it never arrives")
	assert.Equal(t, true, options["include_usage"])
}

func TestChatCompletionStreamInterruptionIsNotAReply(t *testing.T) {
	srv := sseServer(t,
		"data: {\"choices\":[{\"delta\":{\"content\":\"半句\"}}]}\n\n",
	)
	client := newLocalClient(t, srv.URL)

	var deltas []string
	assembled, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.ErrorIs(t, err, ErrStreamInterrupted, "a stream without [DONE] is not a reply")
	assert.Equal(t, []string{"半句"}, deltas, "what did arrive is still handed to the consumer")
	require.NotNil(t, assembled)
	require.Len(t, assembled.Choices, 1)
	assert.Equal(t, "半句", assembled.Choices[0].Message.Content,
		"the partial assembly is returned for diagnosis, but the error says it is not a reply")
}

func TestChatCompletionStreamStatusError(t *testing.T) {
	srv := replyServer(t, http.StatusUnauthorized, `{"error":{"message":"bad key"}}`)
	client := newLocalClient(t, srv.URL)

	_, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, nil)
	var statusErr *httpclient.StatusError
	require.ErrorAs(t, err, &statusErr, "a non-2xx answer must surface as a StatusError")
	assert.True(t, statusErr.IsAuthentication())
}

func TestChatCompletionStreamErrors(t *testing.T) {
	// A consumer that stops ends the stream at that point.
	stop := errors.New("consumer done")
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n", "data: [DONE]\n\n")
	client := newLocalClient(t, srv.URL)
	_, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, func(string) error { return stop })
	assert.ErrorIs(t, err, stop, "the consumer's error should stop the read and propagate")

	// A malformed chunk is an invalid stream, not a silently skipped fragment.
	bad := sseServer(t, "data: {not json}\n\n", "data: [DONE]\n\n")
	client = newLocalClient(t, bad.URL)
	_, err = client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, nil)
	assert.ErrorIs(t, err, ErrInvalidJSON)

	// A provider error envelope mid-stream is surfaced with its message.
	mid := sseServer(t, "data: {\"error\":{\"message\":\"rate limited\"}}\n\n")
	client = newLocalClient(t, mid.URL)
	_, err = client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

func TestChatCompletionStreamAssemblesToolCallFragments(t *testing.T) {
	// Providers stream a tool call as indexed fragments: the id in the first
	// fragment, the name and argument JSON in pieces. The assembled message
	// must equal what a non-streaming round would have carried, because the
	// tool loop runs over the assembled form.
	srv := sseServer(t,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"getCandle","arguments":"{\"mark"}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"etId\":\"BTC\"}"}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n",
		"data: [DONE]\n\n",
	)
	client := newLocalClient(t, srv.URL)

	var deltas []string
	assembled, err := client.ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []Message{{Role: "user", Content: "?"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, deltas, "tool-call fragments are not display text")

	message, err := assembled.FirstMessage()
	require.NoError(t, err)
	require.Len(t, message.ToolCalls, 1)
	assert.Equal(t, "call-1", message.ToolCalls[0].ID)
	assert.Equal(t, "function", message.ToolCalls[0].Type)
	assert.Equal(t, "getCandle", message.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"marketId":"BTC"}`, message.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", assembled.Choices[0].FinishReason)
}
