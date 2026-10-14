package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/urfave/cli/v2"
)

// The conversation REPL (docs/llm-dev 08 §4.3).
//
// gctcli's other commands are one-shot calls; this one keeps a conversation
// open. The menu lives in colon commands, every turn goes through
// LLMSendMessage, and the tool transcript that comes back is printed so the
// operator sees which evidence the answer rests on.

var llmChatCommand = &cli.Command{
	Name:  "llmchat",
	Usage: "starts an interactive LLM conversation (read-only analysis; :help for commands)",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "conversation",
			Usage: "existing conversation id; a new conversation is created when empty",
		},
		&cli.StringFlag{
			Name:  "title",
			Value: "gctcli chat",
			Usage: "title for a newly created conversation",
		},
		&cli.Int64Flag{
			Name:  "turn-timeout",
			Value: 30,
			Usage: "per-turn timeout in seconds",
		},
		&cli.BoolFlag{
			Name:  "it",
			Usage: "widen the per-turn timeout to 120s for long analyses",
		},
	},
	Action: llmChat,
}

// llmChatSession is the narrow client seam the REPL drives; the gRPC client
// satisfies it through grpcLLMChatSession, and tests script it directly.
type llmChatSession interface {
	ConversationID() string
	Send(ctx context.Context, content string) (*gctrpc.LLMSendMessageResponse, error)
	History(ctx context.Context) ([]*gctrpc.LLMChatMessage, error)
}

// grpcLLMChatSession sends turns over the RPC connection.
type grpcLLMChatSession struct {
	client         gctrpc.GoCryptoTraderServiceClient
	conversationID string
	turnTimeout    time.Duration
}

// ConversationID returns the session's conversation.
func (s *grpcLLMChatSession) ConversationID() string { return s.conversationID }

// Send runs one turn with the session's per-turn timeout.
func (s *grpcLLMChatSession) Send(ctx context.Context, content string) (*gctrpc.LLMSendMessageResponse, error) {
	turnCtx, cancel := context.WithTimeout(ctx, s.turnTimeout)
	defer cancel()
	return s.client.LLMSendMessage(turnCtx, &gctrpc.LLMSendMessageRequest{
		ConversationId: s.conversationID,
		Content:        content,
	})
}

// History reads the conversation's stored messages.
func (s *grpcLLMChatSession) History(ctx context.Context) ([]*gctrpc.LLMChatMessage, error) {
	response, err := s.client.LLMListMessages(ctx, &gctrpc.LLMListMessagesRequest{ConversationId: s.conversationID})
	if err != nil {
		return nil, err
	}
	return response.Messages, nil
}

// llmChat is the command action: connect, pick or create a conversation,
// and run the REPL.
func llmChat(c *cli.Context) error {
	conn, cancel, err := setupClient(c)
	if err != nil {
		return err
	}
	defer closeConn(conn, cancel)

	client := gctrpc.NewGoCryptoTraderServiceClient(conn)
	conversationID := c.String("conversation")
	if conversationID == "" {
		created, createErr := client.LLMCreateConversation(c.Context, &gctrpc.LLMCreateConversationRequest{Title: c.String("title")})
		if createErr != nil {
			return createErr
		}
		conversationID = created.Id
		fmt.Fprintf(c.App.Writer, "conversation %s created (--conversation %s to return to it)\n", conversationID, conversationID)
	}

	turnTimeout := time.Duration(c.Int64("turn-timeout")) * time.Second
	if c.Bool("it") {
		turnTimeout = 120 * time.Second
	}
	session := &grpcLLMChatSession{client: client, conversationID: conversationID, turnTimeout: turnTimeout}
	return runChatREPL(c.Context, c.App.Reader, c.App.Writer, session)
}

const chatREPLHelp = `commands:
  :help             show this help
  :history          print the stored conversation
  :evidence <ref>   print one tool response of the last turn in full
  :save             print the conversation id (every turn is already persisted)
  :quit             leave the conversation (also :exit)
any other line is sent to the analyst; the reply is read-only analysis, never an order.`

// runChatREPL reads lines until :quit, :exit or EOF.
//
// bufio.Reader rather than bufio.Scanner: a scanner's line limit would silently
// split a long instruction, and an instruction that reaches the model in two
// pieces is a different instruction.
func runChatREPL(ctx context.Context, in io.Reader, out io.Writer, session llmChatSession) error {
	reader := bufio.NewReader(in)
	fmt.Fprintf(out, "conversation %s; type :help for commands\n", session.ConversationID())
	var last *gctrpc.LLMSendMessageResponse
	for {
		fmt.Fprint(out, "> ")
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, ":"):
			if quit := runChatREPLCommand(ctx, out, session, last, trimmed); quit {
				return nil
			}
		case trimmed != "":
			response, sendErr := session.Send(ctx, trimmed)
			if sendErr != nil {
				// A failed turn is not a failed session: the operator sees the
				// error and may rephrase or retry.
				fmt.Fprintf(out, "error: %v\n", sendErr)
			} else {
				printChatTurn(out, response)
				last = response
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}

// runChatREPLCommand executes one colon command; it reports whether the REPL
// should end.
func runChatREPLCommand(ctx context.Context, out io.Writer, session llmChatSession, last *gctrpc.LLMSendMessageResponse, line string) bool {
	command, argument := splitChatCommand(line)
	switch command {
	case ":quit", ":exit":
		return true
	case ":help":
		fmt.Fprintln(out, chatREPLHelp)
	case ":save":
		fmt.Fprintf(out, "conversation %s is stored server-side; every turn is already persisted\n", session.ConversationID())
	case ":history":
		messages, err := session.History(ctx)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			break
		}
		if len(messages) == 0 {
			fmt.Fprintln(out, "(no messages yet)")
			break
		}
		for i := range messages {
			fmt.Fprintf(out, "[%d] %s: %s\n", messages[i].Seq, messages[i].Role, messages[i].Content)
		}
	case ":evidence":
		if last == nil {
			fmt.Fprintln(out, "no turn has run in this session yet")
			break
		}
		card := findEvidenceCard(last.Evidence, argument)
		if card == nil {
			fmt.Fprintf(out, "the last turn has no evidence with ref %q\n", argument)
			break
		}
		printEvidenceCard(out, card)
	default:
		fmt.Fprintf(out, "unknown command %q; try :help\n", command)
	}
	return false
}

// splitChatCommand splits ":evidence ref" into the command and its argument.
func splitChatCommand(line string) (command, rest string) {
	if index := strings.IndexAny(line, " \t"); index >= 0 {
		return line[:index], strings.TrimSpace(line[index+1:])
	}
	return line, ""
}

// findEvidenceCard locates a card by ref in the last turn's response.
func findEvidenceCard(cards []*gctrpc.LLMEvidenceCard, ref string) *gctrpc.LLMEvidenceCard {
	for i := range cards {
		if cards[i].Ref == ref {
			return cards[i]
		}
	}
	return nil
}

// llmTranscriptEntry mirrors the stored transcript's entry shape
// (engine.ToolCallRecord's field names are its JSON keys).
type llmTranscriptEntry struct {
	CallID       string `json:"CallID"`
	Name         string `json:"Name"`
	Arguments    string `json:"Arguments"`
	Result       string `json:"Result"`
	ResultSha256 string `json:"ResultSha256"`
}

// llmTranscript mirrors the retained transcript document.
type llmTranscript struct {
	Entries []llmTranscriptEntry `json:"entries"`
	Dropped int                  `json:"dropped,omitempty"`
}

// printChatTurn prints the reply, the tool exchanges behind it, the evidence
// cards and the usage line.
func printChatTurn(out io.Writer, response *gctrpc.LLMSendMessageResponse) {
	fmt.Fprintf(out, "assistant: %s\n", response.AssistantMessage)
	if response.ToolTranscript != "" {
		var transcript llmTranscript
		if err := json.Unmarshal([]byte(response.ToolTranscript), &transcript); err != nil {
			fmt.Fprintf(out, "  (tool transcript could not be shown: %v)\n", err)
		} else {
			for i := range transcript.Entries {
				entry := &transcript.Entries[i]
				fmt.Fprintf(out, "  tool %s(arguments=%s) -> sha256 %s\n", entry.Name, entry.Arguments, shortHash(entry.ResultSha256))
			}
			if transcript.Dropped > 0 {
				fmt.Fprintf(out, "  (%d older tool exchanges were dropped by the retention budget)\n", transcript.Dropped)
			}
		}
	}
	for i := range response.Evidence {
		card := response.Evidence[i]
		fmt.Fprintf(out, "  evidence %s [%s] %s %s\n", card.Ref, card.Quality, card.MarketId, card.SourceTime)
	}
	if response.ErrorCode != "" {
		fmt.Fprintf(out, "  reason: %s\n", response.ErrorCode)
	}
	fmt.Fprintf(out, "  (%d in / %d out tokens, %d ms)\n", response.InputTokens, response.OutputTokens, response.LatencyMs)
}

// printEvidenceCard prints one card in full: every field plus the hash, so the
// operator can check what the model actually saw.
func printEvidenceCard(out io.Writer, card *gctrpc.LLMEvidenceCard) {
	encoded, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		fmt.Fprintf(out, "evidence %s (could not be rendered: %v)\n", card.Ref, err)
		return
	}
	fmt.Fprintf(out, "evidence %s\n%s\n", card.Ref, encoded)
}

// shortHash trims a hash for one-line display; the full value is in :evidence.
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "…"
}
