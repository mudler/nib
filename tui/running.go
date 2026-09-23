package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// A root-agent tool call shows in the live area below the transcript from the
// moment it starts (chat.Callbacks.OnToolStart) until its result arrives: a
// header whose mark pulses, the time it has run, and, for a shell command,
// the tail of its output so far. When the result arrives the running block
// goes away and the finished block joins the transcript as before, so the
// transcript keeps the order in which calls finished.

// runningTool is a started root-agent tool call with no result yet.
type runningTool struct {
	name, args string
	started    time.Time
	// flipped inverts the ctrl+r fold state for this block alone (a click).
	flipped bool
}

// toolStartMsg carries a started tool call to the UI.
type toolStartMsg chat.ToolStart

// runningToolOutputLines caps how much of a running command's output a block
// keeps, even expanded: the tail is what is changing.
const runningToolOutputLines = 1000

// startTool records a call that just started.
func (m *Model) startTool(ts chat.ToolStart) {
	m.running = append(m.running, runningTool{name: ts.Name, args: ts.Arguments, started: time.Now()})
}

// finishTool drops the running entry a result belongs to. There is no call id
// to match on, so it takes the oldest entry with the same name and arguments,
// or failing that the oldest with the same name.
func (m *Model) finishTool(res chat.ToolResult) {
	at := -1
	for i, r := range m.running {
		if r.name == res.Name && r.args == res.Arguments {
			at = i
			break
		}
	}
	if at < 0 {
		for i, r := range m.running {
			if r.name == res.Name {
				at = i
				break
			}
		}
	}
	if at >= 0 {
		m.running = append(m.running[:at:at], m.running[at+1:]...)
	}
}

// clearRunning forgets every running call: the turn ended, so none of them
// will report back.
func (m *Model) clearRunning() {
	m.running = nil
}

// toolsExpanded is the ctrl+r fold state that tool blocks share with the
// thinking: expanded when the reasoning is.
func (m Model) toolsExpanded() bool {
	return !m.reasoningCollapsed
}

// runningBlock builds the render data for one running call.
func (m Model) runningBlock(r runningTool) render.RunningTool {
	rt := render.RunningTool{
		Label:    toolLabel(r.name, r.args),
		Elapsed:  time.Since(r.started),
		Expanded: m.toolsExpanded() != r.flipped,
	}
	if r.name == "bash" {
		if id, ok := m.foregroundShellJob(r.args); ok {
			rt.Hint = theme.ToolBackgroundHint
			rt.Output = m.shellOutput(id)
		}
	}
	return rt
}

// foregroundShellJob finds the running foreground shell job a bash call
// started. That job is what ctrl+b backgrounds.
func (m Model) foregroundShellJob(args string) (string, bool) {
	return findForegroundJob(m.shellJobs.List(), args)
}

// findForegroundJob returns the newest running, not backgrounded job whose
// script is the bash call's (args is the call's JSON arguments).
func findForegroundJob(jobs []wizmcp.ShellJobInfo, args string) (string, bool) {
	var a struct {
		Script string `json:"script"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return "", false
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.Running && !j.Backgrounded && j.Script == a.Script {
			return j.ID, true
		}
	}
	return "", false
}

// shellOutput returns a job's output so far (see joinShellOutput).
func (m Model) shellOutput(id string) string {
	stdout, stderr, ok := m.shellJobs.Output(id)
	if !ok {
		return ""
	}
	return joinShellOutput(stdout, stderr)
}

// joinShellOutput puts stderr after stdout and keeps the last
// runningToolOutputLines lines.
func joinShellOutput(stdout, stderr string) string {
	out := strings.TrimRight(stdout, "\n")
	if e := strings.TrimRight(stderr, "\n"); e != "" {
		if out != "" {
			out += "\n"
		}
		out += e
	}
	lines := strings.Split(out, "\n")
	if len(lines) > runningToolOutputLines {
		lines = lines[len(lines)-runningToolOutputLines:]
	}
	return strings.Join(lines, "\n")
}

// toolSpan is the content-relative row span [start, end) one tool block takes
// in the viewport this render, and which block it is: an index into
// m.messages, or into m.running when running is set.
type toolSpan struct {
	start, end int
	index      int
	running    bool
}

// toolBlockHit returns the tool block a terminal-relative mouse Y lands in.
// The translation from Y to a content row is reasoningBoxHit's.
func (m Model) toolBlockHit(y int) (toolSpan, bool) {
	if !m.showingViewport() {
		return toolSpan{}, false
	}
	row := y - m.presenter.HeaderHeight(m.viewState()) + m.viewport.YOffset
	for _, s := range m.toolSpans {
		if row >= s.start && row < s.end {
			return s, true
		}
	}
	return toolSpan{}, false
}

// toggleToolBlock flips the fold of the block s names.
func (m *Model) toggleToolBlock(s toolSpan) {
	if s.running {
		if s.index < len(m.running) {
			m.running[s.index].flipped = !m.running[s.index].flipped
		}
		return
	}
	if s.index < len(m.messages) && m.messages[s.index].Role == "tool" {
		m.messages[s.index].flipped = !m.messages[s.index].flipped
	}
}

// clearToolFlips drops every per-block fold choice, so the blocks follow
// ctrl+r together again.
func (m *Model) clearToolFlips() {
	for i := range m.messages {
		m.messages[i].flipped = false
	}
	for i := range m.running {
		m.running[i].flipped = false
	}
}
