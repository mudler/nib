package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mudler/nib/theme"
)

func roles(m Model) []string {
	var out []string
	for _, msg := range m.messages {
		out = append(out, msg.Role)
	}
	return out
}

// When the answer starts, the live thinking folds into a thought entry above
// it, the box empties, and the reply keeps streaming into the tail.
func TestThinkingFoldsAboveTheStreamingReply(t *testing.T) {
	m := streamModel()
	m.messages = []ChatMessage{{Role: "user", Content: "q"}}
	for _, ev := range []reasoningEventsMsg{delta("let me "), delta("think"), content("The answer"), content(" is 42")} {
		n, _ := m.Update(ev)
		m = n.(Model)
	}
	if got := strings.Join(roles(m), ","); got != "user,thought,assistant" {
		t.Fatalf("transcript roles = %s, want user,thought,assistant", got)
	}
	if m.reasoning != "" {
		t.Fatalf("live box = %q, want empty once the answer started", m.reasoning)
	}
	if !m.streamingActive || m.messages[2].Content != "The answer is 42" {
		t.Fatalf("reply no longer streaming into the tail: %+v", m.messages[2])
	}
	if th := m.messages[1]; th.Content != "let me think" || !strings.HasPrefix(th.Meta, theme.ThoughtFor) {
		t.Fatalf("thought entry = %+v, want the trace with a timed summary", th)
	}

	// The step's boundary arrives after its answer: it corrects the same
	// entry rather than adding a second one or refilling the box.
	n, _ := m.Update(boundary("let me think hard"))
	m = n.(Model)
	if got := thoughts(m); len(got) != 1 || got[0] != "let me think hard" {
		t.Fatalf("thoughts after boundary = %q, want the one entry corrected", got)
	}
	if m.reasoning != "" || !m.streamingActive {
		t.Fatalf("boundary refilled the box (%q) or ended the stream (%v)", m.reasoning, m.streamingActive)
	}

	n, _ = m.Update(responseMsg{content: "The answer is 42"})
	m = n.(Model)
	if got := strings.Join(roles(m), ","); got != "user,thought,assistant" {
		t.Fatalf("after turn end roles = %s, want user,thought,assistant", got)
	}
}

// A reply that did not stream still gets its thinking folded above it.
func TestThinkingFoldsBeforeANonStreamedReply(t *testing.T) {
	m := streamModel()
	n, _ := m.Update(boundary("pondering"))
	n, _ = n.(Model).Update(responseMsg{content: "done"})
	m = n.(Model)
	if got := strings.Join(roles(m), ","); got != "thought,assistant" {
		t.Fatalf("roles = %s, want thought,assistant", got)
	}
	if got := m.messages[0].Meta; got != theme.ThoughtLabel {
		t.Fatalf("summary = %q, want %q for a trace with no streamed start", got, theme.ThoughtLabel)
	}
}

// Folded, a thought is one summary line; ctrl+r shows the trace under it.
func TestThoughtLineExpandsWithCtrlR(t *testing.T) {
	m := Model{
		viewport:           viewport.New(80, 20),
		width:              80,
		presenter:          testPresenter(),
		reasoningCollapsed: true,
		messages:           []ChatMessage{{Role: "thought", Content: "secret plan", Meta: "thought for 3s"}},
	}
	m.updateViewport()
	out := m.viewport.View()
	if !strings.Contains(out, "thought for 3s") || strings.Contains(out, "secret plan") {
		t.Fatalf("folded thought = %q, want the summary alone", out)
	}
	m.reasoningCollapsed = false
	m.updateViewport()
	if out := m.viewport.View(); !strings.Contains(out, "secret plan") {
		t.Fatalf("expanded thought = %q, want the trace shown", out)
	}
}

func TestThoughtSummary(t *testing.T) {
	for d, want := range map[string]string{"0s": "thought", "400ms": "thought for 1s", "4.4s": "thought for 4s", "75s": "thought for 1m 15s"} {
		if got := theme.ThoughtSummary(mustDuration(t, d)); got != want {
			t.Errorf("ThoughtSummary(%s) = %q, want %q", d, got, want)
		}
	}
}

func mustDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A step that folded on its answer but never got a boundary (cogito only
// fires one when the step had reasoning, and a sub-agent's steps fire none)
// must not swallow the next step's thinking. The tool starting is the step
// end: the next trace is a new entry at the bottom, not text appended to the
// old one above the tool calls.
func TestNextStepThinkingAfterToolStartsNewEntry(t *testing.T) {
	m := streamModel()
	m.messages = []ChatMessage{{Role: "user", Content: "q"}}
	for _, ev := range []reasoningEventsMsg{delta("first"), content("I'll look"), {{kind: reasoningEventStepEnd, gen: m.currentTurnGen()}}, delta("second")} {
		n, _ := m.Update(ev)
		m = n.(Model)
	}
	got := thoughts(m)
	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("thoughts = %q, want the first entry untouched", got)
	}
	if m.reasoning != "second" {
		t.Fatalf("live box = %q, want the new step's trace", m.reasoning)
	}
}
