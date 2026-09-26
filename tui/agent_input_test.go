package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
)

func agentLogModel() Model {
	m := newQueueTestModel()
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Task: "scan the LoRA loader", Status: chat.AgentStatusRunning}}
	m.openLogs("agent", "a1")
	return m
}

func typeKeys(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

// A running sub-agent's log takes input: typing fills it, and enter sends it
// to the agent and clears it.
func TestAgentLogTakesInput(t *testing.T) {
	m := agentLogModel()
	if !m.agentInputOpen() {
		t.Fatal("the input should be open on a running agent's log")
	}
	if c := m.renderComposer(80); !strings.Contains(c, "explore") {
		t.Fatalf("composer = %q, want the input addressed to the agent", c)
	}
	if h := m.helpLine(); !strings.Contains(h, "enter send") {
		t.Fatalf("help = %q, want how to send", h)
	}

	m = typeKeys(t, m, "check the scales")
	if got := m.agentInput.Value(); got != "check the scales" {
		t.Fatalf("input = %q", got)
	}
	var sent []string
	m.sendToAgent = func(id, msg string) error { sent = append(sent, id+":"+msg); return nil }
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("enter should send the message")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(sent) != 1 || sent[0] != "a1:check the scales" {
		t.Fatalf("sent = %v", sent)
	}
	if m.agentInput.Value() != "" {
		t.Fatalf("input not cleared: %q", m.agentInput.Value())
	}
	if !strings.Contains(m.agentInputNote, "next step") {
		t.Fatalf("note = %q, want when the agent reads it", m.agentInputNote)
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.Content, "check the scales") || last.AgentID != "a1" {
		t.Fatalf("transcript = %+v, want the message recorded", last)
	}
}

// A failed send keeps the text so the user can retry, and says why.
func TestAgentSendFailureKeepsText(t *testing.T) {
	m := agentLogModel()
	m = typeKeys(t, m, "hello")
	m.sendToAgent = func(string, string) error { return errors.New("the sub-agent has not read your previous messages yet") }
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.agentInput.Value() != "hello" || !strings.Contains(m.agentInputNote, "not read") {
		t.Fatalf("input %q, note %q", m.agentInput.Value(), m.agentInputNote)
	}
}

// Esc clears typed text first, then goes back to the list; arrows still scroll.
func TestAgentInputEscAndScroll(t *testing.T) {
	m := agentLogModel()
	m = typeKeys(t, m, "draft")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.agentInput.Value() != "" || m.logOpenID != "a1" {
		t.Fatalf("first esc: input %q, open %q; want cleared and still open", m.agentInput.Value(), m.logOpenID)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.agentInput.Value() != "" {
		t.Fatal("up typed into the input")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.logOpenID != "" {
		t.Fatal("second esc should go back to the list")
	}
}

// A finished agent's log has no input, and says how to follow up.
func TestFinishedAgentLogHasNoInput(t *testing.T) {
	m := agentLogModel()
	m.jobs[0].Status = chat.AgentStatusCompleted
	if m.agentInputOpen() {
		t.Fatal("input open on a finished agent")
	}
	if v := m.renderLogsViewer(); !strings.Contains(v, "finished") {
		t.Fatalf("log view = %q, want it to say the agent finished", v)
	}
}

// A background sub-agent's events arrive as "sub_agent"; its chip still shows
// the tool it called.
func TestBackgroundAgentStep(t *testing.T) {
	a := &agentMeters{}
	a.step("a1", chat.StreamEvent{Kind: "sub_agent", ToolName: "bash", AgentID: "a1"})
	if got := a.doing("a1"); got != "bash" {
		t.Fatalf("step = %q, want bash", got)
	}
	a.step("a1", chat.StreamEvent{Kind: "sub_agent", Content: "text", AgentID: "a1"})
	if got := a.doing("a1"); got != "working" {
		t.Fatalf("step = %q, want working", got)
	}
}

// An open log fits the terminal, running or finished, with or without the
// input: the viewer's own rows are budgeted for, so the header never scrolls
// off the top.
func TestOpenLogFitsTheTerminal(t *testing.T) {
	for _, st := range []chat.AgentStatus{chat.AgentStatusRunning, chat.AgentStatusCompleted} {
		m := agentLogModel()
		m.width, m.height = 90, 14
		m.jobs[0].Status = st
		m.agentInputNote = "sent"
		m.updateDimensions()
		if got := lipgloss.Height(m.View()); got > m.height {
			t.Errorf("%s: frame is %d rows, terminal is %d", st, got, m.height)
		}
	}
}

// The log's header names the agent by the title the model wrote, once it has
// one, like its chip.
func TestAgentLogHeaderUsesTheTitle(t *testing.T) {
	m := agentLogModel()
	if v := m.renderLogsViewer(); !strings.Contains(v, "explore: scan the LoRA loader") {
		t.Fatalf("header before the title = %q", v)
	}
	m.applyAgentTitle("a1", "Map the LoRA options")
	if v := m.renderLogsViewer(); !strings.Contains(v, "explore: Map the LoRA options") {
		t.Fatalf("header after the title = %q", v)
	}
}
