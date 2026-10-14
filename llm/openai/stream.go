package openai

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
)

// ErrStreamInterrupted marks a stream that ended before its [DONE] sentinel:
// the reply is incomplete and must not be treated as one (docs/llm-dev 05 §1).
var ErrStreamInterrupted = errors.New("stream ended before [DONE]")

// StreamingCompleter is implemented by clients that can stream tokens; the
// tool loop upgrades to it only when the caller asked for deltas.
type StreamingCompleter interface {
	ChatCompletionStream(ctx context.Context, req *ChatRequest, onDelta func(string) error) (*ChatResponse, error)
}

// maxStreamEventBytes bounds one buffered SSE event. A larger event is a
// runaway provider, not a reply.
const maxStreamEventBytes = 1 << 20

// ChatCompletionStream sends the same request as ChatCompletion with
// stream:true and invokes onDelta for every content fragment as it arrives.
//
// The assembled reply is returned once the stream ends with [DONE], so callers
// that need the whole answer (persistence, the surrounding budget accounting)
// keep working with the same type. Two properties are deliberate:
//
//   - Usage arrives late. It is taken from whatever trailing chunk carries it
//     (IncludeUsage asks for exactly that); a provider that never sends it
//     leaves the usage zero rather than inventing a count.
//   - An interruption is an error. Half a reply is not a reply, and the caller
//     must not persist it as though it were.
//
// Only the chat path streams; a decision call must never use this method,
// because half a JSON document cannot be validated (docs 05 §1, Q14 = C).
func (c *Client) ChatCompletionStream(ctx context.Context, req *ChatRequest, onDelta func(string) error) (*ChatResponse, error) {
	if c == nil {
		return nil, errors.New("nil OpenAI client")
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	if err := c.normaliseRequest(req); err != nil {
		return nil, err
	}
	req.Stream = true
	if req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	body, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	secret, err := c.secretFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint secret: %w", err)
	}
	if secret == "" {
		return nil, fmt.Errorf("%w: empty secret", httpclient.ErrNilSecret)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", httpclient.DefaultUserAgent)
	httpReq.Header.Set("Authorization", "Bearer "+secret)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("posting to endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		body := ""
		if readErr == nil {
			body = strings.TrimSpace(string(payload))
		}
		return nil, &httpclient.StatusError{StatusCode: resp.StatusCode, Body: body}
	}

	assembled := &ChatResponse{Model: req.Model}
	var content strings.Builder
	var usage Usage
	usageSeen := false
	finishReason := ""
	sawDone := false
	toolCalls := map[int]ToolCall{}
	drainToolCallDeltas := func(chunk *streamChunk) {
		for i := range chunk.Choices {
			for j := range chunk.Choices[i].Delta.ToolCalls {
				fragment := chunk.Choices[i].Delta.ToolCalls[j]
				call := toolCalls[fragment.Index]
				call.Type = "function"
				if fragment.ID != "" {
					call.ID = fragment.ID
				}
				call.Function.Name += fragment.Function.Name
				call.Function.Arguments += fragment.Function.Arguments
				toolCalls[fragment.Index] = call
			}
		}
	}

	// finalise fills the assembled reply; it runs on both the clean end and
	// the interruption path so a caller diagnosing an interrupted stream still
	// sees how far the answer got.
	finalise := func() {
		message := Message{Role: "assistant", Content: content.String()}
		if len(toolCalls) > 0 {
			// Tool calls stream as indexed fragments; assembling them restores
			// exactly what a non-streaming round would have returned, which is
			// what lets the tool loop run unchanged over a streamed round.
			ordered := make([]ToolCall, 0, len(toolCalls))
			for i := range len(toolCalls) {
				if call, ok := toolCalls[i]; ok {
					ordered = append(ordered, call)
				}
			}
			message.ToolCalls = ordered
		}
		assembled.Choices = []Choice{{
			Index:        0,
			Message:      message,
			FinishReason: finishReason,
		}}
		if usageSeen {
			assembled.Usage = usage
		}
	}

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var event strings.Builder
	flushEvent := func() error {
		if event.Len() == 0 {
			return nil
		}
		payload := event.String()
		event.Reset()
		if payload == "[DONE]" {
			sawDone = true
			return nil
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("%w: stream chunk: %w", ErrInvalidJSON, err)
		}
		if len(chunk.Error) > 0 {
			return fmt.Errorf("stream error envelope: %s", truncateForError(string(chunk.Error)))
		}
		for i := range chunk.Choices {
			delta := chunk.Choices[i].Delta.Content
			if delta != "" {
				content.WriteString(delta)
				if onDelta != nil {
					if err := onDelta(delta); err != nil {
						return fmt.Errorf("stream consumer stopped: %w", err)
					}
				}
			}
			if chunk.Choices[i].FinishReason != "" {
				finishReason = chunk.Choices[i].FinishReason
			}
		}
		drainToolCallDeltas(&chunk)
		if chunk.Usage != nil {
			usage = *chunk.Usage
			usageSeen = true
		}
		return nil
	}

	for !sawDone {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				// A blank line closes one SSE event; only then is the payload
				// complete. Parsing earlier would read half a JSON document.
				if err := flushEvent(); err != nil {
					return nil, err
				}
			case strings.HasPrefix(trimmed, "data:"):
				if event.Len() > 0 {
					event.WriteString("\n")
				}
				event.WriteString(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
				if event.Len() > maxStreamEventBytes {
					return nil, fmt.Errorf("%w: stream event exceeds %d bytes", httpclient.ErrResponseTooLarge, maxStreamEventBytes)
				}
			default:
				// event:, id:, retry: and comments are not chat payload.
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if !sawDone {
					// Flush what arrived so the assembled content reflects the
					// partial answer, but fail: it is not a reply.
					_ = flushEvent()
					finalise()
					return assembled, fmt.Errorf("%w after %d content characters", ErrStreamInterrupted, content.Len())
				}
				break
			}
			return nil, fmt.Errorf("reading stream: %w", readErr)
		}
	}

	finalise()
	return assembled, nil
}

// streamChunk is one decoded SSE event payload.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage          `json:"usage"`
	Error json.RawMessage `json:"error,omitempty"`
}

// truncateForError bounds an error envelope surfaced in an error string.
func truncateForError(s string) string {
	const limit = 256
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
