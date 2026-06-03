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
	return Model{
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
	}
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

// TestLogsViewerCtrlCFallsThrough verifies Ctrl+C is not swallowed by the
// viewer (so it can still interrupt/quit).
func TestLogsViewerCtrlCFallsThrough(t *testing.T) {
	m := newLogsModel()
	m.showLogs = true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	nm := next.(Model)
	if !nm.quitting {
		t.Fatal("ctrl+c should fall through to quit while the viewer is open")
	}
}
