package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
)

// newLogsModel builds a session-ready Model with a couple of fake sub-agent
// jobs so unifiedJobs() returns a stable, navigable list.
func newLogsModel() Model {
	return newTestModel(Model{
		textarea:     textarea.New(),
		viewport:     viewport.New(80, 10),
		logVP:        viewport.New(80, 10),
		sessionReady: true,
		cancel:       func() {},
		shellJobs:    wizmcp.NewShellJobs(),
		jobs: []agentJob{
			{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning},
			{ID: "a2", Type: "plan", Status: chat.AgentStatusCompleted},
		},
	})
}

// TestLogsViewerToggleAndNavigate drives the Ctrl+O viewer through list
// navigation, drilling into a job's log, and the two-stage Esc back-out.
func TestLogsViewerToggleAndNavigate(t *testing.T) {
	m := newLogsModel()

	step := func(m Model, key tea.KeyMsg) Model {
		next, _ := m.Update(key)
		return next.(Model)
	}

	// Ctrl+O opens the viewer.
	m = step(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.showLogs {
		t.Fatal("ctrl+o should open the log viewer")
	}

	// Down then up moves the selection.
	m = step(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.logSel != 1 {
		t.Fatalf("down should select index 1, got %d", m.logSel)
	}
	m = step(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logSel != 0 {
		t.Fatalf("up should select index 0, got %d", m.logSel)
	}

	// Enter opens the selected job's log.
	m = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.logOpenID != "a1" {
		t.Fatalf("enter should open job a1, got %q", m.logOpenID)
	}

	// Esc in log mode returns to the list.
	m = step(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.logOpenID != "" {
		t.Fatalf("esc should return to list, logOpenID = %q", m.logOpenID)
	}
	if !m.showLogs {
		t.Fatal("esc from log mode should not close the viewer")
	}

	// Esc in list mode closes the viewer.
	m = step(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showLogs {
		t.Fatal("esc in list mode should close the viewer")
	}
}

// TestLogsViewerRendersList verifies the viewer body lists the jobs.
func TestLogsViewerRendersList(t *testing.T) {
	m := newLogsModel()
	m.showLogs = true

	out := m.renderLogsViewer()
	if !strings.Contains(out, "logs") {
		t.Fatalf("viewer should render the 'logs' heading:\n%s", out)
	}
	if !strings.Contains(out, "explore") || !strings.Contains(out, "a1") {
		t.Fatalf("viewer should list the jobs:\n%s", out)
	}
}

func TestHistoryListKeepsUnifiedOrderAndEnterAccess(t *testing.T) {
	m := newLogsModel()
	m.jobs[0].Status = chat.AgentStatusCompleted
	m.jobs[1].Status = chat.AgentStatusFailed
	m.openActivity(activityItem{kind: chipHistory})

	out := m.renderLogsViewer()
	first, second := strings.Index(out, "a1"), strings.Index(out, "a2")
	if first < 0 || second <= first {
		t.Fatalf("unified History order lost: %q", out)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.logOpenID != "a2" || m.logOpenKind != "agent" {
		t.Fatalf("History Enter opened %q/%q, want agent/a2", m.logOpenKind, m.logOpenID)
	}
}

// TestLogsViewerCtrlCCloses verifies Ctrl+C closes the viewer, as Esc does,
// instead of falling through to quit.
func TestLogsViewerCtrlCCloses(t *testing.T) {
	m := newLogsModel()
	m.showLogs = true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	nm := next.(Model)
	if nm.quitting {
		t.Fatal("ctrl+c quit while the viewer was open")
	}
	if nm.showLogs {
		t.Fatal("ctrl+c did not close the viewer")
	}
}
