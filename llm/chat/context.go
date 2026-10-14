package chat

import (
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"
)

// Context limits (docs/llm-dev 05-LLM接入层-OpenAI兼容.md §8.1).
//
// The limits are on the user instructions alone: they are the part a model
// must not be given a shortened version of, because the dropped lines could be
// exactly the risk constraint that matters. Exceeding either limit is a
// refusal, never a truncation — the user is told to summarise into a new
// conversation instead.
const (
	// MaxContextMessages is the cap on user instructions in one context.
	MaxContextMessages = 256
	// MaxContextChars is the cap on the total characters of those
	// instructions, counted as runes so a Chinese instruction is not billed
	// at three times an ASCII one.
	MaxContextChars = 16000
)

var (
	// ErrContextTooLarge marks a conversation whose user instructions exceed
	// the extraction limits.
	ErrContextTooLarge = errors.New("conversation context exceeds the extraction limits")
	// ErrContextEmpty marks a context with no user instruction at all.
	ErrContextEmpty = errors.New("conversation has no user instructions")
)

// StoredMessage is one row as the store read it: the sealed body plus the
// role and sequence the row carries.
type StoredMessage struct {
	// Seq orders the conversation; the rebuild sorts by it so the caller's
	// slice order cannot scramble the instructions.
	Seq int64
	// Role is the row's role. Only "user" rows are instructions.
	Role string
	// Body is the sealed message, opened with the row id as AAD.
	Body Message
}

// ContextMessage is one opened instruction handed to the model.
type ContextMessage struct {
	// Seq is the stored sequence number, for traceability.
	Seq int64
	// Content is the plaintext instruction.
	Content string
}

// BuildContext opens the user instructions of a conversation, in sequence
// order, refusing when the limits are exceeded.
//
// Assistant and system rows are not part of the extraction context and are not
// opened — a corrupt assistant row must not make the user's own instructions
// unreadable. A corrupt user row, on the other hand, is surfaced rather than
// skipped: silently dropping it would be truncation wearing a different hat.
func (c *ConversationCipher) BuildContext(messages []StoredMessage) ([]ContextMessage, error) {
	ordered := append([]StoredMessage(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })

	instructions := make([]StoredMessage, 0, len(ordered))
	for i := range ordered {
		if ordered[i].Role == "user" {
			instructions = append(instructions, ordered[i])
		}
	}
	if len(instructions) == 0 {
		return nil, ErrContextEmpty
	}
	if len(instructions) > MaxContextMessages {
		return nil, fmt.Errorf("%w: %d user messages exceed %d; summarise into a new conversation",
			ErrContextTooLarge, len(instructions), MaxContextMessages)
	}

	out := make([]ContextMessage, 0, len(instructions))
	chars := 0
	for i := range instructions {
		content, err := c.Open(&instructions[i].Body)
		if err != nil {
			return nil, fmt.Errorf("opening message %q: %w", instructions[i].Body.ID, err)
		}
		chars += utf8.RuneCount(content)
		if chars > MaxContextChars {
			return nil, fmt.Errorf("%w: %d characters exceed %d; summarise into a new conversation",
				ErrContextTooLarge, chars, MaxContextChars)
		}
		out = append(out, ContextMessage{Seq: instructions[i].Seq, Content: string(content)})
	}
	return out, nil
}
