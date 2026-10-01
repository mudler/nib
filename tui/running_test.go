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

func TestLifecycleResultBeforeStart(t *testing.T) {
	m := runningTestModel()
	m = update(m, toolResultMsg{Name: "read", Arguments: "{}"})
	m = update(m, toolStartMsg{Name: "read", Arguments: "{}"})
	if len(m.running) != 0 {
		t.Fatal("completed call resurrected")
	}
}

func TestLifecycleLateStartAfterCompletion(t *testing.T) {
	m := runningTestModel()
	m = update(m, responseMsg{content: "done"})
	m = update(m, toolStartMsg{Name: "read"})
	if len(m.running) != 0 {
		t.Fatal("late start survived completion")
	}
}

func TestLifecycleEmptyIDDoesNotMatchDifferentArguments(t *testing.T) {
	m := runningTestModel()
	m.startTool(chat.ToolStart{Name: "read", Arguments: "a"})
	m.finishTool(chat.ToolResult{Name: "read", Arguments: "b"})
	if len(m.running) != 1 {
		t.Fatal("unrelated empty-ID call removed")
	}
}

func TestLifecycleIDsAndDuplicates(t *testing.T) {
	m := runningTestModel()
	m.startTool(chat.ToolStart{ID: "a", Name: "read", Arguments: "{}"})
	m.startTool(chat.ToolStart{ID: "b", Name: "read", Arguments: "{}"})
	m.finishTool(chat.ToolResult{ID: "b", Name: "read", Arguments: "{}"})
	if len(m.running) != 1 || m.running[0].id != "a" {
		t.Fatalf("wrong call remains: %+v", m.running)
	}
	m.startTool(chat.ToolStart{ID: "b", Name: "read", Arguments: "{}"})
	if len(m.running) != 1 {
		t.Fatal("completed ID resurrected")
	}
}

func TestLifecycleQueueBurstAndGeneration(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	for i := 0; i < 500; i++ {
		q.push(toolEvent{start: &chat.ToolStart{ID: fmt.Sprint(i), Name: "read"}})
		q.push(toolEvent{result: &chat.ToolResult{ID: fmt.Sprint(i), Name: "read"}})
	}
	events := q.drain()
	if len(events) != 1000 {
		t.Fatalf("lost events: %d", len(events))
	}
	for i, e := range events {
		if (i%2 == 0) != (e.start != nil) {
			t.Fatal("reordered events")
		}
	}
	m := runningTestModel()
	m.toolEvents = q
	m = update(m, toolEventsMsg(events))
	if len(m.running) != 0 || len(m.messages) != 500 {
		t.Fatalf("burst not delivered: %d running, %d results", len(m.running), len(m.messages))
	}
	q.end()
	q.push(toolEvent{start: &chat.ToolStart{Name: "late"}})
	if len(q.drain()) != 0 {
		t.Fatal("accepted late start")
	}
	q.begin()
	m = update(m, toolEventsMsg(events))
	if len(m.messages) != 500 {
		t.Fatal("accepted stale generation")
	}
}

func TestLifecycleCanceledLateStart(t *testing.T) {
	m := runningTestModel()
	next, _ := m.interrupt()
	m = next.(Model)
	m = update(m, toolStartMsg{Name: "late"})
	if len(m.running) != 0 {
		t.Fatal("accepted canceled start")
	}
}

func TestLifecycleParkResumeOrderedWithTools(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	q := m.toolEvents
	q.push(toolEvent{start: &chat.ToolStart{ID: "first", Name: "read"}})
	q.push(toolEvent{result: &chat.ToolResult{ID: "first", Name: "read"}})
	q.push(toolEvent{park: &parkEvent{parked: true}})
	q.push(toolEvent{start: &chat.ToolStart{ID: "late", Name: "read"}})
	q.push(toolEvent{park: &parkEvent{parked: false}})
	q.push(toolEvent{start: &chat.ToolStart{ID: "second", Name: "read"}})
	m.applyToolEvents(q.drain())
	if len(m.running) != 1 || m.running[0].id != "second" {
		t.Fatalf("park/resume reordered: %+v", m.running)
	}
}

func TestLifecycleOldProducerAfterNewRun(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	q := m.toolEvents
	q.begin()
	old := q.generation()
	q.end()
	q.begin()
	q.pushFor(old, toolEvent{start: &chat.ToolStart{ID: "same", Name: "read"}})
	q.pushFor(old, toolEvent{result: &chat.ToolResult{ID: "same", Name: "read"}})
	q.push(toolEvent{start: &chat.ToolStart{ID: "same", Name: "read"}})
	m.applyToolEvents(q.drain())
	if len(m.running) != 1 || len(m.messages) != 0 {
		t.Fatalf("old producer contaminated new turn: %+v", m.running)
	}
}

func TestLifecycleEmptyIdenticalCallsRemainIndependent(t *testing.T) {
	m := runningTestModel()
	for i := 0; i < 3; i++ {
		m.startTool(chat.ToolStart{Name: "read", Arguments: "{}"})
	}
	for i := 2; i >= 0; i-- {
		m.finishTool(chat.ToolResult{Name: "read", Arguments: "{}"})
		if len(m.running) != i {
			t.Fatalf("want %d running, got %d", i, len(m.running))
		}
	}
	m.startTool(chat.ToolStart{Name: "read", Arguments: "{}"})
	if len(m.running) != 1 {
		t.Fatal("subsequent identical call suppressed")
	}
}

func TestLifecycleCompletionFlushesMailbox(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m.toolEvents.push(toolEvent{start: &chat.ToolStart{ID: "a", Name: "read"}})
	m.toolEvents.push(toolEvent{result: &chat.ToolResult{ID: "a", Name: "read", Result: "finished"}})
	m = update(m, responseMsg{content: "done"})
	if len(m.running) != 0 || len(m.messages) != 2 || m.messages[0].Role != "tool" {
		t.Fatalf("completion lost/reordered tool result: %+v", m.messages)
	}
}
