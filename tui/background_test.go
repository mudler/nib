package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
)

// ctrl+b backgrounds the foreground sub-agent, never one already in the
// background: detaching that one fails, and the key used to stop there.
func TestCtrlBBackgroundsTheForegroundAgent(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	m.jobs = []agentJob{
		{ID: "bg", Type: "explore", Status: chat.AgentStatusRunning, Background: true},
		{ID: "fg", Type: "plan", Status: chat.AgentStatusRunning},
	}
	var detached []string
	m.detachAgent = func(id string) error {
		detached = append(detached, id)
		if id == "bg" {
			return errors.New("not detachable")
		}
		return nil
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	m = next.(Model)
	if len(detached) != 1 || detached[0] != "fg" {
		t.Fatalf("detached %v, want only the foreground agent", detached)
	}
	if j, _ := m.jobByID("fg"); !j.Background {
		t.Fatal("the detached agent is still counted as foreground")
	}
	if !strings.Contains(m.status, "plan") {
		t.Fatalf("status = %q, want it to say what was backgrounded", m.status)
	}

	// Nothing in the foreground now: a second press detaches nothing.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	m = next.(Model)
	if len(detached) != 1 {
		t.Fatalf("detached %v after the second press", detached)
	}
}

// The help line says ctrl+b backgrounds while there is foreground work, and
// not otherwise.
func TestHelpLineOffersCtrlB(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	m.jobs = []agentJob{{ID: "bg", Type: "explore", Status: chat.AgentStatusRunning, Background: true}}
	if h := m.helpLine(); strings.Contains(h, "ctrl+b") {
		t.Fatalf("help = %q with only background work", h)
	}
	m.jobs = append(m.jobs, agentJob{ID: "fg", Type: "plan", Status: chat.AgentStatusRunning})
	if h := m.helpLine(); !strings.Contains(h, "ctrl+b background") {
		t.Fatalf("help = %q, want the ctrl+b hint with a foreground agent", h)
	}
}

// A sub-agent spawned in the background arrives marked so.
func TestAgentEventCarriesBackground(t *testing.T) {
	m := newQueueTestModel()
	m.applyAgentEvent(chat.AgentEvent{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning, Background: true})
	if j, _ := m.jobByID("a1"); !j.Background {
		t.Fatal("background flag lost")
	}
}
