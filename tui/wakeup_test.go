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

// TestParkResumeInvalidatesOrphanPollWakeup reproduces the orphan-tick loop: a
// poll wake-up armed while the run is parked on background work must not
// resurrect the turn after that work completes on its own. cogito wakes the
// parked run by injecting the completion (→ parkMsg{parked:false}); the turn
// then reports and ends. The poll tick armed just before completion fires
// afterwards into an idle session — and used to re-dispatch the finished task
// as a fresh turn because no generation was bumped outside /loop stop.
func TestParkResumeInvalidatesOrphanPollWakeup(t *testing.T) {
	m := newWakeupTestModel()
	m.parkChan = make(chan parkEvent, 1)

	// A poll wake-up was armed while parked, capturing the current poll gen.
	armedGen := m.pollGen

	// Background work completes: the parked run resumes (cogito onResume).
	next, _ := m.Update(parkMsg{parked: false})
	m = next.(Model)

	// The turn finishes and the session goes idle.
	m.loading = false
	m.parked = false

	// The orphan poll tick finally fires with its now-stale generation.
	next, cmd := m.Update(wakeupFireMsg{prompt: "Check on the explore agent", gen: armedGen, poll: true})
	nm := next.(Model)

	if nm.loading || cmd != nil || len(nm.messages) != 0 {
		t.Fatalf("orphan poll wake-up after a background-work resume should be ignored, got loading=%v cmd=%v messages=%v", nm.loading, cmd, nm.messages)
	}
}

// TestParkResumeKeepsReminderWakeup guards the surgical part of the fix: a real
// reminder (poll=false) scheduled while background work happened to be running
// must STILL fire after that work completes — only poll wake-ups are dropped on
// park→resume, so reminders ride wakeupGen and survive.
func TestParkResumeKeepsReminderWakeup(t *testing.T) {
	m := newWakeupTestModel()
	m.parkChan = make(chan parkEvent, 1)

	// A reminder was armed while parked, capturing the current wakeup gen.
	armedGen := m.wakeupGen

	// Background work completes and the parked run resumes.
	next, _ := m.Update(parkMsg{parked: false})
	m = next.(Model)

	// The turn finishes and the session goes idle.
	m.loading = false
	m.parked = false

	// The reminder tick fires; it must still start a turn.
	next, cmd := m.Update(wakeupFireMsg{prompt: "stand-up reminder", gen: armedGen, poll: false})
	nm := next.(Model)

	if !nm.loading || cmd == nil || len(nm.messages) != 1 {
		t.Fatalf("reminder wake-up should survive a background-work resume, got loading=%v cmd=%v messages=%v", nm.loading, cmd, nm.messages)
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
