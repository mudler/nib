package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func newQueueTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	return Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		sessionReady: true,
	}
}

func TestEnterQueuesWhileWorking(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true // a run is in flight
	m.textarea.SetValue("follow up please")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(Model)

	if len(nm.queue) != 1 || nm.queue[0] != "follow up please" {
		t.Fatalf("queue = %v, want one entry", nm.queue)
	}
	if strings.TrimSpace(nm.textarea.Value()) != "" {
		t.Fatalf("composer should be cleared, got %q", nm.textarea.Value())
	}
	if !nm.loading {
		t.Fatal("run should still be loading after queueing")
	}
}

func TestTypingAllowedWhileWorking(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	nm := next.(Model)
	if nm.textarea.Value() != "x" {
		t.Fatalf("composer should accept keystrokes while loading, got %q", nm.textarea.Value())
	}
}

func TestQueueMutators(t *testing.T) {
	m := Model{queue: []string{"a", "b", "c"}, queueSel: 0}

	m.queueMoveSel(1)
	if m.queueSel != 1 {
		t.Fatalf("queueMoveSel(1): sel = %d, want 1", m.queueSel)
	}
	m.queueMoveSel(-5) // clamp at 0
	if m.queueSel != 0 {
		t.Fatalf("queueMoveSel clamp low: sel = %d, want 0", m.queueSel)
	}
	m.queueSel = 2
	m.queueMoveSel(5) // clamp at last
	if m.queueSel != 2 {
		t.Fatalf("queueMoveSel clamp high: sel = %d, want 2", m.queueSel)
	}

	m.queueSel = 1
	removed := m.queueDeleteSel()
	if removed != "b" || strings.Join(m.queue, ",") != "a,c" {
		t.Fatalf("queueDeleteSel: removed=%q queue=%v", removed, m.queue)
	}
	if m.queueSel != 1 { // c shifted into index 1
		t.Fatalf("queueDeleteSel sel = %d, want 1", m.queueSel)
	}

	m.queueSel = 1 // now points at "c"
	m.queueDeleteSel()
	if m.queueSel != 0 || strings.Join(m.queue, ",") != "a" {
		t.Fatalf("queueDeleteSel last: sel=%d queue=%v", m.queueSel, m.queue)
	}
}

func TestResponseMsgFlushesQueueAsNewTurn(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	m := newQueueTestModel()
	m.session = s
	m.loading = true
	m.queue = []string{"next turn please", "and another"}

	next, cmd := m.Update(responseMsg{content: "done"})
	nm := next.(Model)

	// Front entry becomes the next turn; the rest stay queued.
	if len(nm.queue) != 1 || nm.queue[0] != "and another" {
		t.Fatalf("queue after flush = %v, want [and another]", nm.queue)
	}
	if !nm.loading {
		t.Fatal("loading should be true: a new turn is starting")
	}
	last := nm.messages[len(nm.messages)-1]
	if last.Role != "user" || last.Content != "next turn please" {
		t.Fatalf("last message = %+v, want user/next turn please", last)
	}
	if cmd == nil {
		t.Fatal("expected a sendMessage command for the flushed turn")
	}
}

func TestQueueNavAndEditKeys(t *testing.T) {
	// Empty composer: Down moves selection through the queue.
	m := newQueueTestModel()
	m.queue = []string{"a", "b", "c"}
	m.queueSel = 0
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm := next.(Model)
	if nm.queueSel != 1 {
		t.Fatalf("Down with empty composer: sel = %d, want 1", nm.queueSel)
	}

	// ^x deletes the selected entry.
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	nm = next.(Model)
	if strings.Join(nm.queue, ",") != "a,c" {
		t.Fatalf("^x: queue = %v, want [a c]", nm.queue)
	}

	// ^e pulls the selected entry into the composer and removes it from the queue.
	nm.queueSel = 0
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	nm = next.(Model)
	if nm.textarea.Value() != "a" {
		t.Fatalf("^e: composer = %q, want a", nm.textarea.Value())
	}
	if strings.Join(nm.queue, ",") != "c" {
		t.Fatalf("^e: queue = %v, want [c]", nm.queue)
	}

	// With text in the composer, Down is NOT a queue nav (cursor/history instead).
	m2 := newQueueTestModel()
	m2.queue = []string{"a", "b"}
	m2.queueSel = 0
	m2.textarea.SetValue("typed")
	next, _ = m2.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm2 := next.(Model)
	if nm2.queueSel != 0 {
		t.Fatalf("Down with typed text must not move queue sel: got %d", nm2.queueSel)
	}
}

func TestRenderQueueContent(t *testing.T) {
	if renderQueue(nil, 0, 80) != "" {
		t.Fatal("empty queue should render nothing")
	}
	out := renderQueue([]string{"first item", "second item"}, 1, 80)
	if !strings.Contains(out, "first item") || !strings.Contains(out, "second item") {
		t.Fatalf("renderQueue missing entries: %q", out)
	}
	if containsEmoji(out) {
		t.Fatalf("renderQueue must not contain emoji: %q", out)
	}
}

func TestViewShowsQueue(t *testing.T) {
	m := newQueueTestModel()
	m.width = 80
	m.height = 24
	m.queue = []string{"queued follow-up"}
	out := m.View()
	if !strings.Contains(out, "queued follow-up") {
		t.Fatalf("View should render the queue, got:\n%s", out)
	}
}

func TestTickReconcilesStuckLoading(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	m := newQueueTestModel()
	m.session = s
	m.loading = true // stuck: no live run, but loading never cleared

	next, _ := m.Update(spinner.TickMsg{})
	nm := next.(Model)
	if nm.loading {
		t.Fatal("tick should clear loading when the run is not live")
	}
}
