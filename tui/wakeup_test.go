package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mudler/nib/chat"
)

// newWakeupTestModel builds an idle, session-ready model suitable for driving
// the wakeupFireMsg branch. The session pointer is non-nil but never used: the
// fired wake-up only returns the sendMessage closure (not invoked here).
func newWakeupTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	return Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		session:      &chat.Session{},
		sessionReady: true,
	}
}

func TestWakeupFireStaleGenIgnored(t *testing.T) {
	m := newWakeupTestModel()
	m.wakeupGen = 5

	next, _ := m.Update(wakeupFireMsg{prompt: "tick", gen: 4})
	nm := next.(Model)

	if nm.loading {
		t.Fatal("stale wake-up tick should not start a turn")
	}
	if len(nm.messages) != 0 {
		t.Fatalf("stale wake-up tick should not append messages, got %v", nm.messages)
	}
}

func TestWakeupFireMatchingGenStartsTurn(t *testing.T) {
	m := newWakeupTestModel()
	m.wakeupGen = 5

	next, cmd := m.Update(wakeupFireMsg{prompt: "tick", gen: 5})
	nm := next.(Model)

	if !nm.loading {
		t.Fatal("matching wake-up tick should start a turn")
	}
	if cmd == nil {
		t.Fatal("matching wake-up tick should return a send command")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "user" || nm.messages[0].Content != "tick" {
		t.Fatalf("expected one user message %q, got %v", "tick", nm.messages)
	}
}

// The parked-inject path (m.parked && session.Inject succeeds) is covered by the
// end-to-end test: Session.Inject returns false without a live run, so a zero-value
// &chat.Session{} cannot exercise it here without a brittle fake/seam.
func TestWakeupFireEmptyPromptDefaultsToContinue(t *testing.T) {
	m := newWakeupTestModel()

	next, _ := m.Update(wakeupFireMsg{prompt: "  ", gen: 0})
	nm := next.(Model)

	if len(nm.messages) != 1 || nm.messages[0].Content != "continue" {
		t.Fatalf("empty prompt should default to %q, got %v", "continue", nm.messages)
	}
}
