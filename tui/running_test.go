package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render/full"
)

// runningTestModel builds a model mid-turn that can take tool events.
func runningTestModel() Model {
	return newTestModel(Model{
		ctx:                context.Background(),
		viewport:           viewport.New(80, 20),
		textarea:           textarea.New(),
		width:              80,
		height:             30,
		loading:            true,
		reasoningCollapsed: true,
		toolStartChan:      make(chan chat.ToolStart, 1),
		toolResultChan:     make(chan chat.ToolResult, 1),
	})
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestRunningToolShowsUntilItsResult(t *testing.T) {
	m := runningTestModel()
	m = update(m, toolStartMsg{Name: "read", Arguments: `{"path":"needle.go"}`})
	if len(m.running) != 1 {
		t.Fatalf("a started call should be running; got %d", len(m.running))
	}
	out := ansi.Strip(m.viewport.View())
	if !strings.Contains(out, theme.RunningDot) || !strings.Contains(out, "needle") {
		t.Fatalf("the running call should show with its pulsing mark:\n%s", out)
	}

	m = update(m, toolResultMsg{Name: "read", Arguments: `{"path":"needle.go"}`, Result: `{"content":"x"}`})
	if len(m.running) != 0 {
		t.Fatalf("the result should end the running call; %d still running", len(m.running))
	}
	if last := m.messages[len(m.messages)-1]; last.Role != "tool" {
		t.Fatalf("the finished call should join the transcript; last entry %+v", last)
	}
	if out := ansi.Strip(m.viewport.View()); strings.Contains(out, theme.RunningDot) {
		t.Fatalf("no running mark should remain once the call finished:\n%s", out)
	}
}

func TestParallelResultsEndTheirOwnCalls(t *testing.T) {
	m := runningTestModel()
	m = update(m, toolStartMsg{Name: "grep", Arguments: `{"pattern":"a"}`})
	m = update(m, toolStartMsg{Name: "grep", Arguments: `{"pattern":"b"}`})
	m = update(m, toolResultMsg{Name: "grep", Arguments: `{"pattern":"b"}`, Result: "x"})
	if len(m.running) != 1 || m.running[0].args != `{"pattern":"a"}` {
		t.Fatalf("the result for b should leave only a running: %+v", m.running)
	}
}

func TestTurnEndClearsRunningCalls(t *testing.T) {
	m := runningTestModel()
	m = update(m, toolStartMsg{Name: "bash", Arguments: `{"script":"sleep 9"}`})
	m = update(m, responseMsg{content: "done"})
	if len(m.running) != 0 {
		t.Fatalf("a finished turn should leave nothing running: %+v", m.running)
	}
}

func TestSubAgentResultLeavesRootCallRunning(t *testing.T) {
	m := runningTestModel()
	m = update(m, toolStartMsg{Name: "read", Arguments: `{"path":"a"}`})
	m = update(m, toolResultMsg{Name: "read", Arguments: `{"path":"a"}`, AgentID: "agent-1"})
	if len(m.running) != 1 {
		t.Fatal("a sub-agent's result must not end the root agent's call")
	}
}

func longToolOutput(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("out %d", i+1)
	}
	return strings.Join(lines, "\n")
}

func TestCtrlRExpandsToolOutputAndClearsClicks(t *testing.T) {
	m := runningTestModel()
	m.loading = false
	m = withMessages(m, ChatMessage{Role: "tool", Name: "bash", Content: longToolOutput(30)})
	m.updateViewport()
	if out := ansi.Strip(m.viewport.View()); !strings.Contains(out, "18 more lines") {
		t.Fatalf("long output should start folded:\n%s", out)
	}
	m.messages[0].flipped = true

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.messages[0].flipped {
		t.Fatal("ctrl+r should clear a block's own fold choice")
	}
	if !m.toolsExpanded() {
		t.Fatal("ctrl+r should expand tool output")
	}
	m.viewport.Height = 60
	m.updateViewport()
	if out := ansi.Strip(m.viewport.View()); !strings.Contains(out, "out 30") {
		t.Fatalf("expanded output should show every line:\n%s", out)
	}
}

func TestClickTogglesOneToolBlock(t *testing.T) {
	m := newTestModel(Model{
		viewport:           viewport.New(80, 20),
		textarea:           textarea.New(),
		width:              80,
		height:             40,
		presenter:          full.New(),
		reasoningCollapsed: true,
	})
	m = withMessages(m,
		ChatMessage{Role: "tool", Name: "bash", Content: longToolOutput(30)},
		ChatMessage{Role: "tool", Name: "bash", Content: longToolOutput(30)},
	)
	m.updateDimensions()
	m.updateViewport()
	if len(m.toolSpans) != 2 {
		t.Fatalf("each tool block should record a span; got %d", len(m.toolSpans))
	}
	second := m.toolSpans[1]
	m = clickAt(m, m.rowFor(second.start))
	if m.messages[0].flipped || !m.messages[1].flipped {
		t.Fatalf("a click should flip only the block it lands on: %v %v", m.messages[0].flipped, m.messages[1].flipped)
	}
}

func TestFindForegroundJob(t *testing.T) {
	jobs := []wizmcp.ShellJobInfo{
		{ID: "old", Script: "make", Running: true},
		{ID: "bg", Script: "make", Running: true, Backgrounded: true},
		{ID: "done", Script: "make"},
		{ID: "other", Script: "ls", Running: true},
	}
	if id, ok := findForegroundJob(jobs, `{"script":"make"}`); !ok || id != "old" {
		t.Fatalf("want the running foreground job for the script, got %q %v", id, ok)
	}
	if _, ok := findForegroundJob(jobs, `{"script":"true"}`); ok {
		t.Fatal("no job runs that script")
	}
	if _, ok := findForegroundJob(nil, "not json"); ok {
		t.Fatal("bad arguments find nothing")
	}
}

func TestJoinShellOutput(t *testing.T) {
	if got := joinShellOutput("a\nb\n", "err\n"); got != "a\nb\nerr" {
		t.Fatalf("got %q", got)
	}
	if got := joinShellOutput("", "err"); got != "err" {
		t.Fatalf("got %q", got)
	}
	long := joinShellOutput(longToolOutput(runningToolOutputLines+5), "")
	if n := strings.Count(long, "\n") + 1; n != runningToolOutputLines {
		t.Fatalf("kept %d lines, want %d", n, runningToolOutputLines)
	}
	if !strings.HasSuffix(long, fmt.Sprintf("out %d", runningToolOutputLines+5)) {
		t.Fatal("the cut should keep the newest lines")
	}
}
