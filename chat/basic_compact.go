package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// basicCompactMarker opens the user-role message that stands in for the head
// basicCompact drops.
const basicCompactMarker = "Conversation compacted (basic fallback mode). Previous tool outputs and assistant responses were elided to fit the context window."

const (
	basicAssistantChars = 200 // assistant text kept per line
	basicArgsChars      = 80  // tool arguments kept per line when there is no path
)

// basicLine is one rendered line of the dropped head. User lines are the
// user's own words and go last when the result must shrink.
type basicLine struct {
	text string
	user bool
}

// basicCompact compacts the fragment without an LLM call. It is the last
// resort when the summarizer is unavailable or iterativeTrim ran out of steps.
//
// The whole head is saved as an artifact first. The head is then replaced by
// one user-role message: the user messages verbatim, one line per assistant
// text (cut to 200 characters) and one stub line per tool call. The results
// are rendered as text, never as tool messages, so no tool result is left
// without its call. The tail, split by splitForCompaction, keeps its pairing.
//
// When the result does not fit, activity lines go first, oldest first, and
// then user lines, oldest first; they all stay in the artifact. When even the
// tail does not fit, the fragment is left alone and the validation error is
// returned.
func (s *Session) basicCompact(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := s.compactionConfig()
	budget := s.trimBudget(cfg)
	state := s.fragmentSnapshot()
	msgs := state.frag.Messages

	head, tail := splitForCompaction(msgs, max(cfg.KeepRecent, 1))
	if len(head) == 0 {
		return fmt.Errorf("%w: nothing to compact", ErrCompactionNoOp)
	}

	marker := basicCompactMarker
	if uri := s.spillCompactionHead(renderMessages(head)); uri != "" {
		marker += " Full history available at " + uri + "."
	}
	marker += " User messages are preserved verbatim."

	var system []openai.ChatCompletionMessage
	for _, m := range head {
		if m.Role == "system" {
			system = append(system, m)
		}
	}
	lines := basicRender(head)

	build := func() []openai.ChatCompletionMessage {
		var b strings.Builder
		b.WriteString(marker)
		for _, l := range lines {
			b.WriteString("\n")
			b.WriteString(l.text)
		}
		out := append([]openai.ChatCompletionMessage(nil), system...)
		out = append(out, openai.ChatCompletionMessage{Role: "user", Content: b.String()})
		return append(out, tail...)
	}

	candidate := build()
	for _, dropUser := range []bool{false, true} {
		for estimateTokens(candidate) > budget {
			i := oldestLine(lines, dropUser)
			if i < 0 {
				break
			}
			lines = append(lines[:i], lines[i+1:]...)
			candidate = build()
		}
	}
	if err := validateCompaction(msgs, candidate, budget); err != nil {
		return fmt.Errorf("basic compact: %w", err)
	}
	s.installFragment(candidate, compactedDisplay(state.messages, tail))
	return nil
}

// oldestLine is the index of the first line of the kind asked for, or -1.
func oldestLine(lines []basicLine, user bool) int {
	for i, l := range lines {
		if l.user == user {
			return i
		}
	}
	return -1
}

// basicRender renders the head as text lines: user messages verbatim, the
// first 200 characters of assistant text, and one stub per tool call. System
// messages are kept as messages by the caller, and tool results are covered by
// their call's stub.
func basicRender(head []openai.ChatCompletionMessage) []basicLine {
	var lines []basicLine
	for _, m := range head {
		switch m.Role {
		case "user":
			if m.Content != "" {
				lines = append(lines, basicLine{text: "User: " + m.Content, user: true})
			}
		case "assistant":
			if text := strings.TrimSpace(m.Content); text != "" {
				lines = append(lines, basicLine{text: "Assistant: " + cutRunes(text, basicAssistantChars)})
			}
			for _, tc := range m.ToolCalls {
				lines = append(lines, basicLine{text: fmt.Sprintf("[tool %s(%s) ran, result elided]", tc.Function.Name, basicToolTarget(tc.Function.Arguments))})
			}
		}
	}
	return lines
}

// basicToolTarget is what a tool stub names: the path argument when there is
// one, or else the first 80 characters of the arguments.
func basicToolTarget(args string) string {
	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) == nil {
		for _, key := range []string{"path", "file_path"} {
			if p, ok := parsed[key].(string); ok && p != "" {
				return p
			}
		}
	}
	return cutRunes(args, basicArgsChars)
}

// cutRunes returns the first n characters of s.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
