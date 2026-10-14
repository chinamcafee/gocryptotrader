package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/types"
)

type scriptedTransport struct {
	responses    []*openai.ChatResponse
	requests     []*openai.ChatRequest
	onCall       func()
	lastDeadline time.Time
}

func (s *scriptedTransport) ChatCompletion(ctx context.Context, req *openai.ChatRequest) (*openai.ChatResponse, error) {
	s.requests = append(s.requests, req)
	s.lastDeadline, _ = ctx.Deadline()
	if s.onCall != nil {
		s.onCall()
	}
	if len(s.responses) == 0 {
		return nil, errors.New("scripted transport: no response left")
	}
	next := s.responses[0]
	s.responses = s.responses[1:]
	return next, nil
}

func chatResponse(finish string, message *openai.Message, tokens int) *openai.ChatResponse {
	return &openai.ChatResponse{
		Choices: []openai.Choice{{Index: 0, Message: *message, FinishReason: finish}},
		Usage:   openai.Usage{TotalTokens: types.Number(tokens)},
	}
}

func toolCall(id, name, args string) openai.ToolCall {
	return openai.ToolCall{ID: id, Type: "function", Function: openai.ToolCallFunction{Name: name, Arguments: args}}
}

type fakeRunner struct {
	supported map[string]bool
	results   map[string]string
	failArgs  map[string]bool
	calls     []string
}

func (f *fakeRunner) Supported(name string) bool { return f.supported[name] }

func (f *fakeRunner) Run(_ context.Context, _, name, _ string) (string, error) {
	f.calls = append(f.calls, name)
	if f.failArgs[name] {
		return "", ErrToolArgumentsInvalid
	}
	return f.results[name], nil
}

func loopRequest() *ToolLoopRequest {
	return &ToolLoopRequest{
		Model:          "model-a",
		Messages:       []openai.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "ctx"}},
		Tools:          []openai.ToolSpec{{Type: "function", Function: openai.ToolFunction{Name: "lookup_market"}}},
		MaxTokens:      1024,
		ResponseFormat: &openai.ResponseFormat{Type: "json_object"},
	}
}

func toolLoopClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestToolLoopFinalAnswerWithoutTools(t *testing.T) {
	transport := &scriptedTransport{responses: []*openai.ChatResponse{
		chatResponse("stop", &openai.Message{Role: "assistant", Content: `{"decision":"HOLD"}`}, 120),
	}}
	runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
	result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
	require.NoError(t, err)
	assert.Empty(t, result.ReasonCode)
	assert.JSONEq(t, `{"decision":"HOLD"}`, result.Content)
	assert.Equal(t, 1, result.Rounds)
	assert.Equal(t, 0, result.ToolCalls)
	assert.Equal(t, int64(120), result.CumulativeTokens)
	require.Len(t, transport.requests, 1)
	assert.Equal(t, openai.ToolChoiceAuto, transport.requests[0].ToolChoice.Mode)
	require.NotNil(t, transport.requests[0].ResponseFormat)
	assert.Equal(t, "json_object", transport.requests[0].ResponseFormat.Type)
	assert.Equal(t, 1024, transport.requests[0].MaxTokens)
}

func TestToolLoopExecutesToolsThenFinal(t *testing.T) {
	transport := &scriptedTransport{responses: []*openai.ChatResponse{
		chatResponse("tool_calls", &openai.Message{
			Role:      "assistant",
			ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{"market":"BTC_USDT"}`)},
		}, 200),
		chatResponse("stop", &openai.Message{Role: "assistant", Content: `{"decision":"HOLD"}`}, 80),
	}}
	runner := &fakeRunner{
		supported: map[string]bool{"lookup_market": true},
		results:   map[string]string{"lookup_market": `{"price":"68000.5"}`},
	}
	result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
	require.NoError(t, err)
	assert.Empty(t, result.ReasonCode)
	assert.Equal(t, 2, result.Rounds)
	assert.Equal(t, 1, result.ToolCalls)
	assert.Equal(t, int64(280), result.CumulativeTokens)
	require.Len(t, result.Transcript, 1)
	entry := result.Transcript[0]
	assert.Equal(t, "call-1", entry.CallID)
	assert.Equal(t, "lookup_market", entry.Name)
	wantSum := sha256.Sum256([]byte(`{"price":"68000.5"}`))
	assert.Equal(t, hex.EncodeToString(wantSum[:]), entry.ResultSha256)

	// The second request must carry the assistant tool-call message and the
	// tool result linked by tool_call_id -- providers reject a conversation
	// where that link is missing.
	require.Len(t, transport.requests, 2)
	second := transport.requests[1].Messages
	require.GreaterOrEqual(t, len(second), 4)
	assistant := second[len(second)-2]
	assert.Equal(t, "assistant", assistant.Role)
	require.Len(t, assistant.ToolCalls, 1)
	toolMsg := second[len(second)-1]
	assert.Equal(t, "tool", toolMsg.Role)
	assert.Equal(t, "call-1", toolMsg.ToolCallID)
	assert.JSONEq(t, `{"price":"68000.5"}`, toolMsg.Content)
	assert.Equal(t, openai.ToolChoiceAuto, transport.requests[1].ToolChoice.Mode)
}

func TestToolLoopFinalRoundForbidsToolsAndIncomplete(t *testing.T) {
	limits := DefaultToolLoopLimits()
	limits.MaxRounds = 2
	transport := &scriptedTransport{responses: []*openai.ChatResponse{
		chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{}`)}}, 100),
		chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-2", "lookup_market", `{}`)}}, 100),
	}}
	runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{}`}}
	result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), limits, toolLoopClock(providerAdminNow))
	require.NoError(t, err)
	assert.Equal(t, ReasonToolRoundIncomplete, result.ReasonCode)
	require.Len(t, transport.requests, 2)
	assert.Equal(t, openai.ToolChoiceAuto, transport.requests[0].ToolChoice.Mode)
	assert.Equal(t, openai.ToolChoiceNone, transport.requests[1].ToolChoice.Mode, "the final round should forbid tool calls")
	assert.Equal(t, 1, result.ToolCalls)
}

func TestToolLoopToolCallGuards(t *testing.T) {
	t.Run("round of calls beyond the total budget", func(t *testing.T) {
		limits := DefaultToolLoopLimits()
		limits.MaxToolCalls = 2
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{
				toolCall("call-1", "lookup_market", `{}`),
				toolCall("call-2", "lookup_market", `{}`),
				toolCall("call-3", "lookup_market", `{}`),
			}}, 100),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{}`}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), limits, toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonToolRoundLimit, result.ReasonCode)
		assert.Empty(t, runner.calls, "no tool may run once the round would overshoot the budget")
		assert.Equal(t, 0, result.ToolCalls)
	})

	t.Run("duplicate call id", func(t *testing.T) {
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{
				toolCall("call-1", "lookup_market", `{}`),
				toolCall("call-1", "lookup_market", `{}`),
			}}, 100),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonToolCallDuplicate, result.ReasonCode)
	})

	t.Run("unsupported tool name", func(t *testing.T) {
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "sudo_rm", `{}`)}}, 100),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonToolCallUnsupported, result.ReasonCode)
		assert.Empty(t, runner.calls)
	})
}

func TestToolLoopTokenBudgetAndRoundLimit(t *testing.T) {
	t.Run("cumulative token budget", func(t *testing.T) {
		limits := DefaultToolLoopLimits()
		limits.MaxCumulativeTokens = 100
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{}`)}}, 150),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{}`}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), limits, toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonAnalysisTokenBudgetExceeded, result.ReasonCode)
		assert.Equal(t, int64(150), result.CumulativeTokens)
	})

	t.Run("round limit when the model keeps asking", func(t *testing.T) {
		limits := DefaultToolLoopLimits()
		limits.MaxRounds = 2
		limits.MaxToolCalls = 100
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{}`)}}, 10),
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-2", "lookup_market", `{}`)}}, 10),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{}`}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), limits, toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		// Round 1 is the last round: a tool request there is incomplete.
		assert.Equal(t, ReasonToolRoundIncomplete, result.ReasonCode)

		limits.MaxRounds = 4
		transport = &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{}`)}}, 10),
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-2", "lookup_market", `{}`)}}, 10),
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-3", "lookup_market", `{}`)}}, 10),
			chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-4", "lookup_market", `{}`)}}, 10),
		}}
		result, err = RunToolLoop(t.Context(), transport, runner, loopRequest(), limits, toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonToolRoundIncomplete, result.ReasonCode, "round 3 is the final round and forbade tools")
		assert.Equal(t, 3, result.ToolCalls)
	})
}

func TestToolLoopDeadline(t *testing.T) {
	t.Run("deadline already passed", func(t *testing.T) {
		transport := &scriptedTransport{}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		req := loopRequest()
		req.Deadline = providerAdminNow.Add(-time.Second)
		result, err := RunToolLoop(t.Context(), transport, runner, req, DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonPipelineDeadlineExceeded, result.ReasonCode)
		assert.Empty(t, transport.requests)
	})

	t.Run("per request timeout is clamped by the deadline", func(t *testing.T) {
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("stop", &openai.Message{Role: "assistant", Content: `{}`}, 10),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		req := loopRequest()
		req.Deadline = time.Now().Add(10 * time.Second)
		before := time.Now()
		// Real clock on both sides: the injected clock is for deterministic
		// tests, but the deadline clamp must compare like with like.
		_, err := RunToolLoop(t.Context(), transport, runner, req, DefaultToolLoopLimits(), nil)
		require.NoError(t, err)
		require.Len(t, transport.requests, 1)
		require.False(t, transport.lastDeadline.IsZero(), "the request context must carry a timeout")
		remaining := transport.lastDeadline.Sub(before)
		assert.Greater(t, remaining, 8*time.Second)
		assert.LessOrEqual(t, remaining, 11*time.Second, "min(30s, deadline-now) should win over the 30s default")
	})

	t.Run("deadline reached between rounds", func(t *testing.T) {
		now := providerAdminNow
		transport := &scriptedTransport{
			responses: []*openai.ChatResponse{
				chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{}`)}}, 10),
			},
			onCall: func() { now = now.Add(time.Minute) },
		}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}, results: map[string]string{"lookup_market": `{}`}}
		req := loopRequest()
		req.Deadline = providerAdminNow.Add(30 * time.Second)
		result, err := RunToolLoop(t.Context(), transport, runner, req, DefaultToolLoopLimits(), func() time.Time { return now })
		require.NoError(t, err)
		assert.Equal(t, ReasonPipelineDeadlineExceeded, result.ReasonCode)
		require.Len(t, transport.requests, 1, "the second model call must not happen after the deadline")
	})
}

func TestToolLoopTruncatedAndOversizedFinal(t *testing.T) {
	t.Run("finish reason length", func(t *testing.T) {
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("length", &openai.Message{Role: "assistant", Content: `{"partial":`}, 500),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonTruncated, result.ReasonCode)
	})

	t.Run("final content beyond the cap", func(t *testing.T) {
		transport := &scriptedTransport{responses: []*openai.ChatResponse{
			chatResponse("stop", &openai.Message{Role: "assistant", Content: strings.Repeat("x", defaultFinalContentChars+1)}, 500),
		}}
		runner := &fakeRunner{supported: map[string]bool{"lookup_market": true}}
		result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
		require.NoError(t, err)
		assert.Equal(t, ReasonCandidateContentInvalid, result.ReasonCode)
	})
}

func TestToolLoopArgumentsInvalidIsFedBack(t *testing.T) {
	transport := &scriptedTransport{responses: []*openai.ChatResponse{
		chatResponse("tool_calls", &openai.Message{Role: "assistant", ToolCalls: []openai.ToolCall{toolCall("call-1", "lookup_market", `{"market":42}`)}}, 90),
		chatResponse("stop", &openai.Message{Role: "assistant", Content: `{"decision":"HOLD"}`}, 60),
	}}
	runner := &fakeRunner{
		supported: map[string]bool{"lookup_market": true},
		failArgs:  map[string]bool{"lookup_market": true},
	}
	result, err := RunToolLoop(t.Context(), transport, runner, loopRequest(), DefaultToolLoopLimits(), toolLoopClock(providerAdminNow))
	require.NoError(t, err)
	assert.Empty(t, result.ReasonCode)
	assert.Equal(t, 1, result.ToolCalls, "an invalid-arguments call still consumes a tool slot")
	require.Len(t, result.Transcript, 1)
	assert.Contains(t, result.Transcript[0].Result, "retry")
	require.Len(t, transport.requests, 2)
	second := transport.requests[1].Messages
	toolMsg := second[len(second)-1]
	assert.Equal(t, "tool", toolMsg.Role)
	assert.Contains(t, toolMsg.Content, "retry")
}
