package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// Tool-loop limits (docs 05 §5.2, inherited from the original project's
// measured values).
const (
	defaultToolLoopRounds    = 4
	defaultToolsPerRound     = 4
	defaultToolCallsTotal    = 8
	defaultCumulativeTokens  = 24000
	defaultPerRequestTimeout = 30 * time.Second
	defaultFinalContentChars = 12000
	defaultToolResultBytes   = 12 * 1024
)

// Hold reason codes produced by the loop. The decision pipeline stores these
// and converges on HOLD; they are stable strings, not prose.
const (
	// ReasonAnalysisRoundLimit ...
	ReasonAnalysisRoundLimit = "ANALYSIS_ROUND_LIMIT"
	// ReasonToolRoundLimit ...
	ReasonToolRoundLimit = "TOOL_ROUND_LIMIT"
	// ReasonAnalysisTokenBudgetExceeded ...
	ReasonAnalysisTokenBudgetExceeded = "ANALYSIS_TOKEN_BUDGET_EXCEEDED"
	// ReasonPipelineDeadlineExceeded ...
	ReasonPipelineDeadlineExceeded = "PIPELINE_DEADLINE_EXCEEDED"
	// ReasonTruncated ...
	ReasonTruncated = "TRUNCATED"
	// ReasonToolRoundIncomplete ...
	ReasonToolRoundIncomplete = "TOOL_ROUND_INCOMPLETE"
	// ReasonToolCallDuplicate ...
	ReasonToolCallDuplicate = "TOOL_CALL_DUPLICATE"
	// ReasonToolCallUnsupported ...
	ReasonToolCallUnsupported = "TOOL_CALL_UNSUPPORTED"
	// ReasonCandidateContentInvalid ...
	ReasonCandidateContentInvalid = "CANDIDATE_CONTENT_INVALID"
)

// ErrToolArgumentsInvalid is returned by a ToolRunner when a call's arguments
// fail schema validation. The loop feeds the failure back to the model as the
// tool result so it can retry within its remaining rounds (docs 05 §5.1);
// it does not abort the analysis.
var ErrToolArgumentsInvalid = errors.New("llm tool loop: tool arguments invalid")

// ChatCompleter is the narrow transport the loop needs; *openai.Client
// satisfies it.
type ChatCompleter interface {
	ChatCompletion(ctx context.Context, req *openai.ChatRequest) (*openai.ChatResponse, error)
}

// ToolRunner executes one read-only tool call. Supported gates the whitelist;
// Run returns the tool's JSON result.
type ToolRunner interface {
	Supported(name string) bool
	Run(ctx context.Context, callID, name, arguments string) (string, error)
}

// ToolLoopLimits bounds one analysis loop.
type ToolLoopLimits struct {
	MaxRounds            int
	MaxToolsPerRound     int
	MaxToolCalls         int
	MaxCumulativeTokens  int64
	PerRequestTimeout    time.Duration
	MaxFinalContentChars int
	MaxToolResultBytes   int
}

// DefaultToolLoopLimits returns the measured defaults.
func DefaultToolLoopLimits() ToolLoopLimits {
	return ToolLoopLimits{
		MaxRounds:            defaultToolLoopRounds,
		MaxToolsPerRound:     defaultToolsPerRound,
		MaxToolCalls:         defaultToolCallsTotal,
		MaxCumulativeTokens:  defaultCumulativeTokens,
		PerRequestTimeout:    defaultPerRequestTimeout,
		MaxFinalContentChars: defaultFinalContentChars,
		MaxToolResultBytes:   defaultToolResultBytes,
	}
}

// ToolLoopRequest starts one loop run.
type ToolLoopRequest struct {
	Model     string
	Messages  []openai.Message
	Tools     []openai.ToolSpec
	MaxTokens int
	// ResponseFormat asks the provider for a JSON document. The decision layer
	// sets json_object; the chat layer leaves it nil, because a conversation
	// reply is free text (docs 05 §6.3).
	ResponseFormat *openai.ResponseFormat
	// OnDelta, when set, streams each round's content fragments to the caller
	// as they arrive; the transport must then implement
	// openai.StreamingCompleter. The decision path leaves it nil -- a streamed
	// reply cannot be schema-validated, so decisions are never streamed.
	OnDelta func(string) error
	// OnProgress, when set, receives live progress from long-running tools
	// (the full-universe screen). Nil is a no-op.
	OnProgress func(ToolProgress)
	// ProfileTimeout is the provider revision's timeout; the per-request
	// timeout is min(profile, 30s) and then clamped by Deadline.
	ProfileTimeout time.Duration
	// Deadline is the shared pipeline deadline. Zero means none (MANUAL_CHAT).
	Deadline time.Time
}

// ToolCallRecord is one transcript entry.
type ToolCallRecord struct {
	CallID       string
	Name         string
	Arguments    string
	Result       string
	ResultSha256 string
}

// ToolLoopResult is either a success (ReasonCode empty, Content set) or a HOLD
// outcome carrying the partial evidence the run produced.
type ToolLoopResult struct {
	Content          string
	Transcript       []ToolCallRecord
	Rounds           int
	ToolCalls        int
	CumulativeTokens int64
	// UsageInputTokens and UsageOutputTokens split the cumulative figure for
	// accounting; they are only known per response, so the loop sums them.
	UsageInputTokens  int64
	UsageOutputTokens int64
	ReasonCode        string
}

func truncateBytes(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit]
}

func contentSha256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// RunToolLoop executes the tool-call loop from docs 05 §5: bounded rounds,
// bounded tool calls, a cumulative token budget, a shared deadline, and a
// transcript of everything the model saw. tool_choice is "auto" for all but
// the final round ("none"), so the last round must produce the final answer.
//
// Candidate parsing and scope/evidence validation are NOT here: they need the
// pipeline's snapshot context and belong to the decision layer (M4), which
// consumes Content.
func RunToolLoop(ctx context.Context, transport ChatCompleter, runner ToolRunner, req *ToolLoopRequest, limits ToolLoopLimits, clock func() time.Time) (*ToolLoopResult, error) {
	if transport == nil || runner == nil {
		return nil, errors.New("llm tool loop: transport and runner are required")
	}
	if clock == nil {
		clock = time.Now
	}

	result := &ToolLoopResult{Transcript: make([]ToolCallRecord, 0)}
	messages := slices.Clone(req.Messages)
	seenCallIDs := make(map[string]struct{})

	round := 0
	for {
		if round >= limits.MaxRounds {
			result.ReasonCode = ReasonAnalysisRoundLimit
			return result, nil
		}
		if result.ToolCalls >= limits.MaxToolCalls {
			result.ReasonCode = ReasonToolRoundLimit
			return result, nil
		}
		if result.CumulativeTokens >= limits.MaxCumulativeTokens {
			result.ReasonCode = ReasonAnalysisTokenBudgetExceeded
			return result, nil
		}
		now := clock()
		if !req.Deadline.IsZero() && now.After(req.Deadline) {
			result.ReasonCode = ReasonPipelineDeadlineExceeded
			return result, nil
		}

		timeout := limits.PerRequestTimeout
		if req.ProfileTimeout > 0 && req.ProfileTimeout < timeout {
			timeout = req.ProfileTimeout
		}
		if !req.Deadline.IsZero() {
			remaining := req.Deadline.Sub(now)
			if remaining < timeout {
				timeout = remaining
			}
		}
		if timeout <= 0 {
			result.ReasonCode = ReasonPipelineDeadlineExceeded
			return result, nil
		}

		choice := openai.ToolChoice{Mode: openai.ToolChoiceAuto}
		if round >= limits.MaxRounds-1 {
			choice = openai.ToolChoice{Mode: openai.ToolChoiceNone}
		}
		request := &openai.ChatRequest{
			Model:          req.Model,
			Messages:       append([]openai.Message(nil), messages...),
			MaxTokens:      req.MaxTokens,
			ResponseFormat: req.ResponseFormat,
			Tools:          req.Tools,
			ToolChoice:     &choice,
		}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		var response *openai.ChatResponse
		var err error
		if req.OnDelta != nil {
			streamer, ok := transport.(openai.StreamingCompleter)
			if !ok {
				cancel()
				return nil, errors.New("llm tool loop: streaming requested but the transport cannot stream")
			}
			response, err = streamer.ChatCompletionStream(requestCtx, request, req.OnDelta)
		} else {
			response, err = transport.ChatCompletion(requestCtx, request)
		}
		cancel()
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("llm tool loop: nil chat response")
		}
		result.CumulativeTokens += int64(response.Usage.Total())
		result.UsageInputTokens += int64(response.Usage.PromptTokens)
		result.UsageOutputTokens += int64(response.Usage.CompletionTokens)
		result.Rounds = round + 1

		message, err := response.FirstMessage()
		if err != nil {
			return nil, err
		}
		// Some OpenAI-compatible endpoints (observed on deepseek-flash) emit
		// tool calls as DSML text instead of structured tool_calls. Convert
		// them before the content can be mistaken for the final answer.
		if len(message.ToolCalls) == 0 {
			message.ToolCalls, message.Content, _ = parseDSMLToolCalls(message.Content)
		}
		messages = append(messages, *message)

		if response.FirstFinishReason() == "length" {
			result.ReasonCode = ReasonTruncated
			return result, nil
		}

		if len(message.ToolCalls) == 0 {
			if len(message.Content) > limits.MaxFinalContentChars {
				result.ReasonCode = ReasonCandidateContentInvalid
				return result, nil
			}
			result.Content = message.Content
			return result, nil
		}

		// Tool round.
		if round >= limits.MaxRounds-1 {
			result.ReasonCode = ReasonToolRoundIncomplete
			return result, nil
		}
		if limits.MaxToolsPerRound > 0 && len(message.ToolCalls) > limits.MaxToolsPerRound {
			result.ReasonCode = ReasonToolRoundLimit
			return result, nil
		}
		if result.ToolCalls+len(message.ToolCalls) > limits.MaxToolCalls {
			result.ReasonCode = ReasonToolRoundLimit
			return result, nil
		}
		for _, call := range message.ToolCalls {
			if _, dup := seenCallIDs[call.ID]; dup {
				result.ReasonCode = ReasonToolCallDuplicate
				return result, nil
			}
			seenCallIDs[call.ID] = struct{}{}
			if !runner.Supported(call.Function.Name) {
				result.ReasonCode = ReasonToolCallUnsupported
				return result, nil
			}
		}

		for _, call := range message.ToolCalls {
			runCtx := withToolProgress(ctx, req.OnProgress)
			reportToolProgress(runCtx, ToolProgress{Tool: call.Function.Name, Phase: "START", Message: "调用工具 " + call.Function.Name})
			toolResult, runErr := runner.Run(runCtx, call.ID, call.Function.Name, call.Function.Arguments)
			if runErr != nil {
				if !errors.Is(runErr, ErrToolArgumentsInvalid) {
					toolResult = fmt.Sprintf(`{"error":%q}`, runErr.Error())
				} else {
					toolResult = fmt.Sprintf(`{"error":%q,"retry":true}`, runErr.Error())
				}
			}
			toolResult = truncateBytes(toolResult, limits.MaxToolResultBytes)
			record := ToolCallRecord{
				CallID:       call.ID,
				Name:         call.Function.Name,
				Arguments:    call.Function.Arguments,
				Result:       toolResult,
				ResultSha256: contentSha256(toolResult),
			}
			result.Transcript = append(result.Transcript, record)
			messages = append(messages, openai.Message{
				Role:       "tool",
				Content:    record.Result,
				ToolCallID: call.ID,
			})
			result.ToolCalls++
		}
		round++
	}
}
