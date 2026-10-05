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
	wizmcp "github.com/mudler/nib/mcp"
)

var (
	ctrlC = tea.KeyMsg{Type: tea.KeyCtrlC}
	esc   = tea.KeyMsg{Type: tea.KeyEsc}
)

func newCtrlCModel() Model {
	ta := textarea.New()
	ta.Focus()
	return newTestModel(Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		sessionReady: true,
		cancel:       func() {}, // quit() calls this
	})
}

func pressKeys(t *testing.T, m Model, keys ...tea.KeyMsg) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	return m
}

// Idle with an empty composer, one Ctrl+C only arms the exit and says so. It
// used to quit at once.
func TestCtrlCIdleArmsExitThenQuits(t *testing.T) {
	m := pressKeys(t, newCtrlCModel(), ctrlC)
	if m.quitting {
		t.Fatal("the first Ctrl+C quit; it must only arm the exit")
	}
	if !m.exitArmed {
		t.Fatal("the first Ctrl+C did not arm the exit")
	}
	if !strings.Contains(m.helpLine(), "again to exit") {
		t.Fatalf("help line does not warn about the exit: %q", m.helpLine())
	}
	if m = pressKeys(t, m, ctrlC); !m.quitting {
		t.Fatal("the second Ctrl+C did not quit")
	}
}

// The exit warning names the background work that exiting stops.
func TestCtrlCExitWarningNamesBackgroundWork(t *testing.T) {
	m := newCtrlCModel()
	m.jobs = []agentJob{{ID: "a1", Status: chat.AgentStatusRunning}}
	m.selfPaced = 1
	m = pressKeys(t, m, ctrlC)
	hint := m.helpLine()
	for _, want := range []string{"1 sub-agent", "1 loop"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("exit warning %q does not mention %q", hint, want)
		}
	}
}

// Any other key disarms the exit, and so does the timeout.
func TestCtrlCExitArmExpires(t *testing.T) {
	m := pressKeys(t, newCtrlCModel(), ctrlC, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.exitArmed {
		t.Fatal("typing a key did not disarm the exit")
	}

	m = pressKeys(t, newCtrlCModel(), ctrlC)
	stale := exitDisarmMsg{seq: m.exitSeq - 1}
	next, _ := m.Update(stale)
	if !next.(Model).exitArmed {
		t.Fatal("a timeout from an earlier arm disarmed the current one")
	}
	next, _ = m.Update(exitDisarmMsg{seq: m.exitSeq})
	m = next.(Model)
	if m.exitArmed {
		t.Fatal("the timeout did not disarm the exit")
	}
	if m = pressKeys(t, m, ctrlC); m.quitting {
		t.Fatal("Ctrl+C after the timeout quit; it must arm again")
	}
}

// With text in the composer, Ctrl+C clears it and nothing else; ↑ brings the
// draft back. It used to quit and lose the draft.
func TestCtrlCClearsTheDraftFirst(t *testing.T) {
	m := newCtrlCModel()
	m.textarea.SetValue("half a thought")
	m = pressKeys(t, m, ctrlC)
	if m.quitting || m.exitArmed {
		t.Fatalf("Ctrl+C with a draft quit=%v armed=%v, want only a clear", m.quitting, m.exitArmed)
	}
	if m.textarea.Value() != "" {
		t.Fatalf("draft not cleared: %q", m.textarea.Value())
	}
	if !strings.Contains(m.helpLine(), "↑") {
		t.Fatalf("help line does not say how to restore the draft: %q", m.helpLine())
	}
	m = pressKeys(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.textarea.Value() != "half a thought" {
		t.Fatalf("↑ restored %q, want the cleared draft", m.textarea.Value())
	}
}

// Ctrl+C with a draft during a turn clears the draft and leaves the turn.
func TestCtrlCClearsTheDraftBeforeInterrupting(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m.textarea.SetValue("typing a follow-up")
	m = pressKeys(t, m, ctrlC)
	if m.interruptArmed {
		t.Fatal("Ctrl+C interrupted the turn while there was a draft to clear")
	}
	if m = pressKeys(t, m, ctrlC); !m.interruptArmed {
		t.Fatal("the next Ctrl+C did not interrupt")
	}
}

// During a turn: interrupt, then arm the exit, then quit. A second Ctrl+C
// during the interrupt used to quit straight away.
func TestCtrlCInterruptsThenArmsExit(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m = pressKeys(t, m, ctrlC)
	if !m.interruptArmed || m.quitting {
		t.Fatalf("first Ctrl+C: interrupted=%v quit=%v, want interrupt only", m.interruptArmed, m.quitting)
	}
	m = pressKeys(t, m, ctrlC)
	if m.quitting || !m.exitArmed {
		t.Fatalf("second Ctrl+C: quit=%v armed=%v, want the exit armed", m.quitting, m.exitArmed)
	}
	if m = pressKeys(t, m, ctrlC); !m.quitting {
		t.Fatal("third Ctrl+C did not quit")
	}
}

// Background sub-agents alone are not foreground work: Ctrl+C arms the exit
// instead of sending an interrupt that has nothing to stop.
func TestCtrlCWithOnlyBackgroundWorkArmsExit(t *testing.T) {
	m := newCtrlCModel()
	m.jobs = []agentJob{{ID: "a1", Status: chat.AgentStatusRunning}}
	m = pressKeys(t, m, ctrlC)
	if m.interruptArmed || !m.exitArmed {
		t.Fatalf("interrupted=%v armed=%v, want the exit armed", m.interruptArmed, m.exitArmed)
	}
}

// With a dialog open, Ctrl+C closes the dialog, as Esc does. It used to fall
// through and quit.
func TestCtrlCClosesAnOpenDialog(t *testing.T) {
	m := newCtrlCModel()
	m.showLogs = true
	m = pressKeys(t, m, ctrlC)
	if m.showLogs || m.quitting || m.exitArmed {
		t.Fatalf("logs=%v quit=%v armed=%v, want only the viewer closed", m.showLogs, m.quitting, m.exitArmed)
	}

	m = newCtrlCModel()
	m.showTodo = true
	if m = pressKeys(t, m, ctrlC); m.showTodo || m.quitting {
		t.Fatal("Ctrl+C did not just close the todo panel")
	}
}

// Ctrl+C on a tool approval denies the call, which releases the blocked turn.
// It used to interrupt and leave the dialog open, with the turn still waiting.
func TestCtrlCDeniesAPendingApproval(t *testing.T) {
	ch := make(chan chat.ToolCallResponse, 1)
	m := newCtrlCModel()
	m.loading = true
	m.awaitingApproval = true
	m.pendingTool = &chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"ls"}`}
	m.toolResponseChan = ch

	next, cmd := m.Update(ctrlC)
	m = next.(Model)
	if cmd != nil {
		cmd()
	}
	if m.awaitingApproval {
		t.Fatal("the approval dialog is still open")
	}
	select {
	case resp := <-ch:
		if resp.Approved {
			t.Fatal("Ctrl+C approved the call")
		}
	default:
		t.Fatal("no answer reached the blocked turn")
	}
	if m.interruptArmed || m.quitting {
		t.Fatal("Ctrl+C on an approval must only deny it")
	}
}

// Esc never quits. It used to quit from the plain composer, typing or not.
func TestEscNeverQuits(t *testing.T) {
	m := newCtrlCModel()
	if m = pressKeys(t, m, esc, esc); m.quitting || m.exitArmed {
		t.Fatal("Esc quit or armed the exit")
	}
	m = newCtrlCModel()
	m.textarea.SetValue("draft")
	if m = pressKeys(t, m, esc); m.quitting || m.textarea.Value() != "draft" {
		t.Fatalf("Esc with a draft: quit=%v value=%q", m.quitting, m.textarea.Value())
	}
}

// Esc interrupts foreground work, like Ctrl+C.
func TestEscInterruptsATurn(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	if m = pressKeys(t, m, esc); !m.interruptArmed || m.quitting {
		t.Fatalf("Esc during a turn: interrupted=%v quit=%v", m.interruptArmed, m.quitting)
	}
}

// Esc closes the completion popup before anything else.
func TestEscClosesTheCompletionPopup(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m.completion.active = true
	m = pressKeys(t, m, esc)
	if m.completion.active {
		t.Fatal("Esc did not close the completion popup")
	}
	if m.interruptArmed {
		t.Fatal("Esc interrupted the turn while closing the popup")
	}
}

// An interrupt stops the turn and sends what was queued behind it: the user
// who queued a message and pressed stop wants the queued message next. It
// used to be held until Enter on an empty composer.
func TestInterruptSendsTheQueue(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m.queue = queuedTexts("next task")
	m = pressKeys(t, m, ctrlC)

	next, cmd := m.Update(responseMsg{err: context.Canceled})
	m = next.(Model)
	if cmd == nil || len(m.queue) != 0 {
		t.Fatalf("queue=%v cmd=%v, want the queued message sent as the next turn", m.queue, cmd != nil)
	}
	if !m.loading {
		t.Fatal("the queued turn did not start")
	}
	var notice string
	for _, msg := range m.messages {
		if strings.HasPrefix(msg.Content, "interrupted.") {
			notice = msg.Content
		}
	}
	if !strings.Contains(notice, "sending 1 queued") {
		t.Fatalf("the interrupt notice does not say the queue is sent: %q", notice)
	}
}

// The interrupt notice says what is still running, so the user knows what
// the interrupt did not stop.
func TestInterruptNoticeNamesWhatStillRuns(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m.jobs = []agentJob{{ID: "a1", Status: chat.AgentStatusRunning}}
	m.shellJobs = wizmcp.NewShellJobs()
	m = pressKeys(t, m, ctrlC)
	next, _ := m.Update(responseMsg{err: context.Canceled})
	last := next.(Model).messages[len(next.(Model).messages)-1].Content
	if !strings.HasPrefix(last, "interrupted.") || !strings.Contains(last, "still running: 1 sub-agent") {
		t.Fatalf("interrupt notice = %q", last)
	}
}

// An interrupt stops a self-paced loop: the turn that would re-arm it is gone.
// The footer used to keep counting it.
func TestInterruptResetsTheSelfPacedCount(t *testing.T) {
	m := newCtrlCModel()
	m.loading = true
	m.selfPaced = 1
	if m = pressKeys(t, m, ctrlC); m.selfPaced != 0 {
		t.Fatalf("selfPaced = %d after an interrupt, want 0", m.selfPaced)
	}
}
