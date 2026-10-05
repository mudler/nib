package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateKey(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestInputHistoryRecall(t *testing.T) {
	m := newQueueTestModel()
	m.history = []string{"first", "second", "third"}
	m.histPos = len(m.history)
	m.textarea.SetValue("draft text")

	// Up stashes the draft and recalls the newest entry.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "third" {
		t.Fatalf("first Up: got %q, want %q", got, "third")
	}
	// Up walks back through older entries.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("second Up: got %q, want %q", got, "second")
	}
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("third Up: got %q, want %q", got, "first")
	}
	// Up at the oldest entry stays put.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("Up at oldest: got %q, want %q", got, "first")
	}
	// Down walks forward to newer entries.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("Down: got %q, want %q", got, "second")
	}
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.textarea.Value(); got != "third" {
		t.Fatalf("Down: got %q, want %q", got, "third")
	}
	// Down past the newest restores the stashed draft.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.textarea.Value(); got != "draft text" {
		t.Fatalf("Down to draft: got %q, want %q", got, "draft text")
	}
}

func TestInputHistoryEmptyNoOp(t *testing.T) {
	m := newQueueTestModel()
	m.textarea.SetValue("hello")

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "hello" {
		t.Fatalf("Up with no history changed text: %q", got)
	}
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.textarea.Value(); got != "hello" {
		t.Fatalf("Down with no history changed text: %q", got)
	}
}

func TestPushHistorySkipsConsecutiveDuplicate(t *testing.T) {
	m := newQueueTestModel()
	m.pushHistory("hello")
	m.pushHistory("hello")
	if len(m.history) != 1 {
		t.Fatalf("consecutive duplicate not skipped: history = %v", m.history)
	}
	m.pushHistory("world")
	if len(m.history) != 2 {
		t.Fatalf("distinct input not recorded: history = %v", m.history)
	}
}

func TestPushHistoryResetsToDraft(t *testing.T) {
	m := newQueueTestModel()
	m.history = []string{"old"}
	m.histPos = 0 // mid-navigation
	m.pushHistory("new")
	if m.histPos != len(m.history) {
		t.Fatalf("pushHistory did not reset histPos: got %d want %d", m.histPos, len(m.history))
	}
	if m.histDraft != "" {
		t.Fatalf("pushHistory did not clear draft: got %q", m.histDraft)
	}
}

func TestHistoryDoesNotInterfereWithQueueNav(t *testing.T) {
	// With an empty composer and a pending queue, Up/Down navigate the queue,
	// not the input history.
	m := newQueueTestModel()
	m.history = []string{"old prompt"}
	m.histPos = len(m.history)
	m.queue = queuedTexts("a", "b")
	m.queueSel = 0

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.queueSel != 1 {
		t.Fatalf("Down should move queue sel: got %d want 1", m.queueSel)
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("Down should not recall history into composer: got %q", got)
	}
	if m.histPos != len(m.history) {
		t.Fatalf("Down should not start history navigation: histPos = %d", m.histPos)
	}
}
