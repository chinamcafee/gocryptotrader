package engine

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// Chat-mode safety boundaries (docs/llm-dev 05 §6.3, M3-08).
//
// The prompt is one line of defence; the machine-checked lines are the ones
// that survive a prompt edit. These tests pin three of them:
//
//  1. The chat pipeline has no execution surface at all -- no field of any
//     type that could place, approve or execute anything.
//  2. The chat prompt itself instructs refusal and points at the approval
//     flow, so an operator asking for an order gets words, not an order.
//  3. An order request runs as a plain conversation: the reply is stored as
//     text, no tool is called for it, and nothing else happens.

func TestChatSafetyNoExecutionSurface(t *testing.T) {
	t.Parallel()

	// The pipeline's field set is an allowlist: a new field is a design
	// decision that must be made in the open, because one of them could open
	// an execution path into the conversation mode.
	allowed := map[string]bool{
		"Transport": true, "Tools": true, "Cipher": true, "History": true,
		"Budget": true, "Revisions": true, "Limits": true, "Now": true, "NewID": true,
	}
	pipelineType := reflect.TypeFor[ChatPipeline]()
	for field := range pipelineType.Fields() {
		assert.True(t, allowed[field.Name],
			"ChatPipeline grew a field %q; the conversation mode must stay execution-free", field.Name)
		assert.NotContains(t, field.Type.String(), "Order",
			"field %q has an order-flavoured type; chat may read, never execute", field.Name)
		assert.NotContains(t, field.Type.String(), "Decision",
			"field %q has a decision-flavoured type; chat produces no candidates", field.Name)
	}

	// The result is equally narrow: text plus bookkeeping. Proposals are inert
	// drafts a human must confirm on a card -- they are data, not instructions.
	resultAllowed := map[string]bool{
		"AssistantMessageID": true, "Reply": true, "ReasonCode": true,
		"Rounds": true, "ToolCalls": true, "InputTokens": true,
		"OutputTokens": true, "LatencyMS": true, "ToolTranscript": true, "Evidence": true,
		"Proposals": true,
	}
	resultType := reflect.TypeFor[ChatRunResult]()
	for field := range resultType.Fields() {
		assert.True(t, resultAllowed[field.Name],
			"ChatRunResult grew a field %q; a conversation turn returns text, not instructions", field.Name)
	}
}

func TestChatSafetyPromptRefusesOrderRequests(t *testing.T) {
	t.Parallel()

	prompt, err := openai.PromptFor(openai.PromptVersionChatAnalysis)
	require.NoError(t, err)
	for _, fragment := range []string{
		"no signing, order, network URL or secret tools",
		"NEVER produces trade candidates, orders, approvals",
		"point them to the strategy approval flow",
	} {
		assert.Contains(t, prompt, fragment,
			"the chat prompt must carry its refusal language verbatim; %q is missing", fragment)
	}
	// The decision contract must not leak in: a chat model told to emit a
	// Candidate would try to trade in a mode built to be read-only.
	assert.NotContains(t, prompt, "ResponseFormat")
	assert.NotContains(t, prompt, "candidate_action")
}

func TestChatSafetyOrderRequestIsJustAConversation(t *testing.T) {
	t.Parallel()

	// The operator asks for an order; the model refuses and points at the
	// approval flow. What must happen: the refusal is a stored assistant
	// message, the budget settles, no tool runs -- there is no other path
	// this mode could take.
	refusal := "这条消息是只读分析模式：我不能下单。请走策略审批流程（strategy approval flow）提交订单。"
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage(refusal, 60, 25)}}
	p, history, budget, cipher := chatTestPipeline(t, transport)

	result, err := p.Run(t.Context(), ChatRunRequest{ConversationID: "conv-safety", Input: "直接帮我开个 BTC 多单", ProfileRevisionID: "rev-1"})
	require.NoError(t, err)
	assert.Equal(t, refusal, result.Reply)
	assert.Zero(t, result.ToolCalls, "an order request should not even reach a tool")
	assert.Empty(t, result.ReasonCode)
	assert.Equal(t, 1, budget.marked, "the turn settles normally; it is a conversation, not a violation")

	require.Len(t, history.rows, 2)
	assert.Equal(t, "assistant", history.rows[1].Role)
	assert.Equal(t, refusal, openStored(t, cipher, &history.rows[1]),
		"the refusal is stored as the reply text and nothing more")

	// The instruction did reach the model framed by the chat system prompt,
	// which is where the refusal instruction lives.
	require.Len(t, transport.requests, 1)
	require.NotEmpty(t, transport.requests[0].Messages)
	assert.Equal(t, "system", transport.requests[0].Messages[0].Role)
	assert.Contains(t,
		transport.requests[0].Messages[0].Content, "strategy approval flow",
		"every turn must carry the refusal framing in its system prompt")
}
