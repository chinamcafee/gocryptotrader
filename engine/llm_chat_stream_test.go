package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
	"github.com/thrasher-corp/gocryptotrader/types"
)

// chatStreamTransport streams scripted fragments; the non-streaming method
// fails loudly so a test proves which path the loop took.
type chatStreamTransport struct {
	chunks   []string
	err      error
	requests int
}

func (s *chatStreamTransport) ChatCompletion(context.Context, *openai.ChatRequest) (*openai.ChatResponse, error) {
	s.requests++
	return nil, errors.New("non-streaming path was taken")
}

func (s *chatStreamTransport) ChatCompletionStream(_ context.Context, _ *openai.ChatRequest, onDelta func(string) error) (*openai.ChatResponse, error) {
	s.requests++
	if s.err != nil {
		return nil, s.err
	}
	var content strings.Builder
	for _, chunk := range s.chunks {
		content.WriteString(chunk)
		if onDelta != nil {
			if err := onDelta(chunk); err != nil {
				return nil, err
			}
		}
	}
	return &openai.ChatResponse{
		Choices: []openai.Choice{{Index: 0, FinishReason: "stop", Message: openai.Message{Role: "assistant", Content: content.String()}}},
		Usage: openai.Usage{
			PromptTokens:     types.Number(100),
			CompletionTokens: types.Number(25),
			TotalTokens:      types.Number(125),
		},
	}, nil
}

func TestChatStreamDeliversDeltasAndStoresTheSameReply(t *testing.T) {
	transport := &chatStreamTransport{chunks: []string{"看", "在", "区间。"}}
	p, history, budget, cipher := chatTestPipeline(t, transport)

	var deltas []string
	result, err := p.RunStream(t.Context(), chatRunRequest(), func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"看", "在", "区间。"}, deltas, "fragments should arrive in order")
	assert.Equal(t, "看在区间。", result.Reply)
	assert.Equal(t, 1, transport.requests, "the streaming path should be the one taken")
	assert.Equal(t, int64(100), result.InputTokens, "usage that arrives late still settles the turn")
	assert.Equal(t, int64(25), result.OutputTokens)
	assert.Equal(t, 1, budget.marked)
	assert.Zero(t, budget.released)

	require.Len(t, history.rows, 2)
	assert.Equal(t, "看在区间。", openStored(t, cipher, &history.rows[1]),
		"the stored reply is exactly the streamed text: one turn, one version")
}

func TestChatStreamInterruptionPersistsNothing(t *testing.T) {
	transport := &chatStreamTransport{err: fmt.Errorf("%w: cut after two chunks", openai.ErrStreamInterrupted)}
	p, history, budget, _ := chatTestPipeline(t, transport)

	_, err := p.RunStream(t.Context(), chatRunRequest(), func(string) error { return nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, openai.ErrStreamInterrupted, "an interrupted stream should surface as such")
	assert.Equal(t, 1, budget.released, "the slot is released: nothing billable completed")
	assert.Zero(t, budget.marked)
	require.Len(t, history.rows, 1, "no half reply may be stored (docs 05 §1)")
	assert.Equal(t, "user", history.rows[0].Role)
}

func TestChatStreamWithoutAStreamingTransport(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage("x", 1, 1)}}
	p, history, budget, _ := chatTestPipeline(t, transport)

	_, err := p.RunStream(t.Context(), chatRunRequest(), func(string) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot stream", "asking for deltas from a non-streaming transport should fail loudly")
	assert.Equal(t, 1, budget.released)
	assert.Len(t, history.rows, 1)

	// ...while the same pipeline still serves plain turns.
	result, err := p.Run(t.Context(), chatRunRequest())
	require.NoError(t, err)
	assert.Equal(t, "x", result.Reply)
}

func TestChatStreamConsumerErrorReleasesAndStoresNothing(t *testing.T) {
	consumerErr := errors.New("the display is gone")
	transport := &chatStreamTransport{chunks: []string{"first"}}
	p, history, budget, _ := chatTestPipeline(t, transport)

	_, err := p.RunStream(t.Context(), chatRunRequest(), func(string) error { return consumerErr })
	assert.ErrorIs(t, err, consumerErr)
	assert.Equal(t, 1, budget.released)
	assert.Len(t, history.rows, 1, "a consumer that stops leaves no reply either")
}
