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
	return newTestModel(Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		sessionReady: true,
	})
}

func TestEnterQueuesWhileWorking(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true // a run is in flight
	m.textarea.SetValue("follow up please")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(Model)

	if len(nm.queue) != 1 || nm.queue[0].text != "follow up please" {
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
	m := newTestModel(Model{queue: queuedTexts("a", "b", "c"), queueSel: 0})

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
	if removed != "b" || strings.Join(queueTexts(m.queue), ",") != "a,c" {
		t.Fatalf("queueDeleteSel: removed=%q queue=%v", removed, m.queue)
	}
	if m.queueSel != 1 { // c shifted into index 1
		t.Fatalf("queueDeleteSel sel = %d, want 1", m.queueSel)
	}

	m.queueSel = 1 // now points at "c"
	m.queueDeleteSel()
	if m.queueSel != 0 || strings.Join(queueTexts(m.queue), ",") != "a" {
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
	m.queue = queuedTexts("next turn please", "and another")

	next, cmd := m.Update(responseMsg{content: "done"})
	nm := next.(Model)

	// Consecutive plain messages combine into a single turn: the queue
	// drains fully and one sendMessage command starts.
	if len(nm.queue) != 0 {
		t.Fatalf("queue after flush = %v, want empty (both combined into one turn)", nm.queue)
	}
	if !nm.loading {
		t.Fatal("loading should be true: a new turn is starting")
	}
	// Both messages are echoed to the transcript as separate user lines.
	users := 0
	for _, msg := range nm.messages {
		if msg.Role == "user" {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("user transcript lines = %d, want 2 (both echoed)", users)
	}
	if cmd == nil {
		t.Fatal("expected a sendMessage command for the flushed turn")
	}
}

func TestFlushQueueSkipsNonTurnEntries(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	m := newQueueTestModel()
	m.session = s
	m.queue = queuedTexts("/totally-unknown-cmd", "plain follow up")
	cmd := m.flushQueueAsTurn()
	if cmd == nil {
		t.Fatal("expected a turn to start for the plain message")
	}
	if len(m.queue) != 0 {
		t.Fatalf("queue should be drained, got %v", m.queue)
	}
}

func TestQueueNavAndEditKeys(t *testing.T) {
	// Empty composer: Down moves selection through the queue.
	m := newQueueTestModel()
	m.queue = queuedTexts("a", "b", "c")
	m.queueSel = 0
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm := next.(Model)
	if nm.queueSel != 1 {
		t.Fatalf("Down with empty composer: sel = %d, want 1", nm.queueSel)
	}

	// ^x deletes the selected entry.
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	nm = next.(Model)
	if strings.Join(queueTexts(nm.queue), ",") != "a,c" {
		t.Fatalf("^x: queue = %v, want [a c]", nm.queue)
	}

	// ^e pulls the selected entry into the composer and removes it from the queue.
	nm.queueSel = 0
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	nm = next.(Model)
	if nm.textarea.Value() != "a" {
		t.Fatalf("^e: composer = %q, want a", nm.textarea.Value())
	}
	if strings.Join(queueTexts(nm.queue), ",") != "c" {
		t.Fatalf("^e: queue = %v, want [c]", nm.queue)
	}

	// With text in the composer, Down is NOT a queue nav (cursor/history instead).
	m2 := newQueueTestModel()
	m2.queue = queuedTexts("a", "b")
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
	out := renderQueue(queuedTexts("first item", "second item"), 1, 80)
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
	m.queue = queuedTexts("queued follow-up")
	out := m.View()
	if !strings.Contains(out, "queued follow-up") {
		t.Fatalf("View should render the queue, got:\n%s", out)
	}
}

func TestRedispatchGoesFirstWithoutEcho(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	m := newQueueTestModel()
	m.session = s
	m.loading = true
	// An undelivered follow-up (already echoed when it was released) plus a
	// normally queued entry: the follow-up must re-dispatch first, without a
	// second transcript echo.
	m.redispatch = []string{"whats 2+2?"}
	m.queue = queuedTexts("and another")
	m = withMessages(m, ChatMessage{Role: "user", Content: "whats 2+2?"})

	next, cmd := m.Update(responseMsg{content: "done"})
	nm := next.(Model)

	if cmd == nil {
		t.Fatal("expected a sendMessage command for the re-dispatched follow-up")
	}
	if len(nm.redispatch) != 0 {
		t.Fatalf("redispatch should be drained, got %v", nm.redispatch)
	}
	if len(nm.queue) != 0 {
		t.Fatalf("queue = %v, want empty (both combined into one turn)", nm.queue)
	}
	// The redispatched follow-up was already echoed; the queued entry is
	// echoed now. No duplicate echo of the follow-up.
	users := 0
	for _, msg := range nm.messages {
		if msg.Role == "user" {
			users++
		}
	}
	if users != 2 {
		t.Fatalf("user transcript lines = %d, want 2 (original + queued echo, no duplicate)", users)
	}
	if !nm.loading {
		t.Fatal("loading should be true: the re-dispatched turn is starting")
	}
}

func TestReleaseQueueFrontTracksUndelivered(t *testing.T) {
	// releaseQueueFront must inject via InjectUser so an unconsumed follow-up
	// is recoverable. With no live run, injection fails and the entry must
	// stay queued (and nothing is echoed).
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	m := newQueueTestModel()
	m.session = s
	m.queue = queuedTexts("hello")
	if m.releaseQueueFront() {
		t.Fatal("releaseQueueFront should fail with no live run")
	}
	if len(m.queue) != 1 {
		t.Fatalf("entry should stay queued, got %v", m.queue)
	}
}

// A command that only reports or flips guarded state runs as soon as it is
// entered, even mid-run. It used to wait in the queue until the run ended,
// so /help or /settings typed during a long turn showed nothing for minutes.
func TestEnterRunsCommandsAtOnceMidRun(t *testing.T) {
	for _, input := range []string{"/help", "/goal", "/attach list", "/nope-no-such-command"} {
		t.Run(input, func(t *testing.T) {
			m := newQueueTestModel()
			m.session = &chat.Session{}
			m.loading = true
			m.textarea.SetValue(input)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			nm := next.(Model)

			if len(nm.queue) != 0 {
				t.Fatalf("queue = %v, want %s run at once", nm.queue, input)
			}
			n := len(nm.messages)
			if n < 2 || nm.messages[n-2].Role != "user" || nm.messages[n-2].Content != input {
				t.Fatalf("want %s echoed then answered, messages = %+v", input, nm.messages)
			}
			if !nm.loading {
				t.Fatal("the live run must keep loading")
			}
		})
	}
}

// Input that starts a turn, swaps the model or session, or edits the system
// prompt still waits for the live run to end.
func TestEnterQueuesTurnAndSessionCommandsMidRun(t *testing.T) {
	for _, input := range []string{"plain text", "/compact", "/goal ship it", "/model gpt-x", "/endpoint", "/resume"} {
		t.Run(input, func(t *testing.T) {
			m := newQueueTestModel()
			m.session = &chat.Session{}
			m.loading = true
			m.textarea.SetValue(input)

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			nm := next.(Model)

			if len(nm.queue) != 1 || nm.queue[0].text != input {
				t.Fatalf("queue = %v, want %q queued", nm.queue, input)
			}
		})
	}
}

// queuedTexts represents already-accepted human input in queue fixtures.
func queuedTexts(texts ...string) []queuedInput {
	entries := make([]queuedInput, len(texts))
	for i, text := range texts {
		entries[i] = queuedInput{text: text, delivery: chat.InputAccepted}
	}
	return entries
}
