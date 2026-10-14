package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

type fakeLLMSession struct {
	conversationID string
	sent           []string
	responses      []*gctrpc.LLMSendMessageResponse
	sendErr        error
	history        []*gctrpc.LLMChatMessage
	historyErr     error
}

func (s *fakeLLMSession) ConversationID() string { return s.conversationID }

func (s *fakeLLMSession) Send(_ context.Context, content string) (*gctrpc.LLMSendMessageResponse, error) {
	s.sent = append(s.sent, content)
	if s.sendErr != nil {
		return nil, s.sendErr
	}
	if len(s.responses) == 0 {
		return nil, errors.New("no scripted response")
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func (s *fakeLLMSession) History(context.Context) ([]*gctrpc.LLMChatMessage, error) {
	return s.history, s.historyErr
}

func TestChatREPLTurnsAndEvidence(t *testing.T) {
	transcript := `{"entries":[{"CallID":"call-1","Name":"getCandleWindow","Arguments":"{}","Result":"{\"marketId\":\"x\"}","ResultSha256":"abcdef0123456789"}]}`
	session := &fakeLLMSession{
		conversationID: "conv-7",
		responses: []*gctrpc.LLMSendMessageResponse{{
			AssistantMessage: "区间震荡。",
			ToolTranscript:   transcript,
			Evidence: []*gctrpc.LLMEvidenceCard{{
				Ref: "tool:getCandleWindow:call-1", Quality: "EXCHANGE_REST",
				MarketId:   "Binance:usdtmarginedfutures:BTC_USDT",
				SourceTime: "2026-10-11T03:00:00Z", Sha256: "abcdef0123456789",
			}},
			InputTokens: 90, OutputTokens: 30, LatencyMs: 1200,
		}},
		history: []*gctrpc.LLMChatMessage{
			{Seq: 1, Role: "user", Content: "hi"},
			{Seq: 2, Role: "assistant", Content: "区间震荡。"},
		},
	}
	input := strings.Join([]string{
		"BTC 现在什么形态？",
		":evidence tool:getCandleWindow:call-1",
		":evidence nope",
		":history",
		":bogus",
		":save",
		"",
		":quit",
		"never sent",
	}, "\n") + "\n"

	var out strings.Builder
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader(input), &out, session))
	text := out.String()

	assert.Equal(t, []string{"BTC 现在什么形态？"}, session.sent, "nothing after :quit may be sent")
	assert.Contains(t, text, "conversation conv-7")
	assert.Contains(t, text, "assistant: 区间震荡。")
	assert.Contains(t, text, "tool getCandleWindow(arguments={}) -> sha256 abcdef012345",
		"the transcript summary must name what the model asked")
	assert.Contains(t, text, "evidence tool:getCandleWindow:call-1 [EXCHANGE_REST] Binance:usdtmarginedfutures:BTC_USDT")
	assert.Contains(t, text, "(90 in / 30 out tokens, 1200 ms)")
	assert.Contains(t, text, `"ref": "tool:getCandleWindow:call-1"`, "the evidence command prints the full card")
	assert.Contains(t, text, `"sha256": "abcdef0123456789"`)
	assert.Contains(t, text, "no evidence with ref", "an unknown ref is reported, not fatal")
	assert.Contains(t, text, "[1] user: hi")
	assert.Contains(t, text, "[2] assistant: 区间震荡。")
	assert.Contains(t, text, "unknown command")
	assert.Contains(t, text, "conversation conv-7 is stored server-side")
}

func TestChatREPLSurvivesTurnErrorsAndEOFs(t *testing.T) {
	session := &fakeLLMSession{conversationID: "c", sendErr: errors.New("rpc exploded")}
	var out strings.Builder
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader("hello\n:quit\n"), &out, session))
	assert.Contains(t, out.String(), "error: rpc exploded", "a failed turn should not end the session")
	assert.Equal(t, []string{"hello"}, session.sent)

	// EOF without :quit ends the loop quietly; a trailing line without a
	// newline is still a turn.
	session = &fakeLLMSession{conversationID: "c", responses: []*gctrpc.LLMSendMessageResponse{{AssistantMessage: "last"}}}
	out.Reset()
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader("one last question"), &out, session))
	assert.Equal(t, []string{"one last question"}, session.sent, "the final turn without a newline should still be sent")
	assert.Contains(t, out.String(), "assistant: last")

	// :evidence before a turn is a message, not a crash; :help lists the menu.
	session = &fakeLLMSession{conversationID: "c"}
	out.Reset()
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader(":evidence x\n:help\n:quit\n"), &out, session))
	assert.Contains(t, out.String(), "no turn has run")
	assert.Contains(t, out.String(), ":quit", "the help text lists the commands")

	// :history errors are printed, not fatal.
	session = &fakeLLMSession{conversationID: "c", historyErr: errors.New("db down")}
	out.Reset()
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader(":history\n:quit\n"), &out, session))
	assert.Contains(t, out.String(), "error: db down")

	// A malformed transcript degrades to a notice instead of dropping the reply.
	session = &fakeLLMSession{conversationID: "c", responses: []*gctrpc.LLMSendMessageResponse{{AssistantMessage: "ok", ToolTranscript: "not json"}}}
	out.Reset()
	require.NoError(t, runChatREPL(t.Context(), strings.NewReader("q\n:quit\n"), &out, session))
	assert.Contains(t, out.String(), "assistant: ok")
	assert.Contains(t, out.String(), "tool transcript could not be shown")
}

func TestChatREPLHelpers(t *testing.T) {
	command, argument := splitChatCommand(":evidence tool:a:1")
	assert.Equal(t, ":evidence", command)
	assert.Equal(t, "tool:a:1", argument)

	command, argument = splitChatCommand(":quit")
	assert.Equal(t, ":quit", command)
	assert.Empty(t, argument)

	assert.Equal(t, "abcdef012345…", shortHash("abcdef0123456789"))
	assert.Equal(t, "short", shortHash("short"))
}
