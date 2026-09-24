package tui

import (
	"strings"
	"time"

	"github.com/mudler/nib/theme"
)

// A step's reasoning shows live in the reasoning box under the spinner. When
// the step moves on (its answer starts to stream, its boundary arrives, or
// the turn ends) the trace folds into a "thought" transcript entry: one dim
// line, "✻ thought for 4s ▸", that ctrl+r expands. So the box does not
// vanish with the thinking in it, and the transcript keeps what the model
// thought, in order, before what it did.
//
// A thought entry is UI-only, like the rest of m.messages: resume rebuilds
// the transcript from the session history, which has no thought entries.

// foldReasoning moves the live trace (m.reasoning) into a thought entry.
// foldReasoning folds the accumulated reasoning text into a transcript
// entry (a "thought for Xs" line), so the live reasoning box closes and
// the trace is preserved in the scrollback.
//
// While a reply is streaming the entry goes before the streaming message, so
// that message stays the tail and streamingActive stays true.
func (m *Model) foldReasoning() {
	text := m.reasoning
	m.reasoning = ""
	if strings.TrimSpace(text) == "" {
		return
	}
	var took time.Duration
	if !m.reasoningSince.IsZero() {
		took = time.Since(m.reasoningSince)
	}
	m.reasoningSince = time.Time{}
	entry := ChatMessage{Role: "thought", Content: text, Meta: theme.ThoughtSummary(took), arrived: time.Now()}
	if m.streamingActive && len(m.messages) > 0 {
		tail := len(m.messages) - 1
		m.messages = append(m.messages[:tail], entry, m.messages[tail])
		m.stepThought = tail + 1
		return
	}
	m.appendMessage(entry)
	m.stepThought = len(m.messages)
}

// reasoningElapsed returns the time the live reasoning trace has been
// running so far, or zero when reasoning has not started.
func (m Model) reasoningElapsed() time.Duration {
	if m.reasoningSince.IsZero() {
		return 0
	}
	return time.Since(m.reasoningSince)
}

// stepThoughtEntry returns the thought entry this step already folded into,
// or nil. stepThought is its index plus one, so the zero value means none;
// the role check guards an index that a transcript rebuild made stale.
func (m *Model) stepThoughtEntry() *ChatMessage {
	i := m.stepThought - 1
	if i < 0 || i >= len(m.messages) || m.messages[i].Role != "thought" {
		return nil
	}
	return &m.messages[i]
}

// endThoughtStep folds what is left of the live trace and forgets the step's
// entry, so the next step folds into a new one.
func (m *Model) endThoughtStep() {
	m.foldReasoning()
	m.stepThought = 0
}
