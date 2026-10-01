package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/internal/textdiff"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

const (
	// agentThreadInlineCap bounds how many sub-agent tool lines render inline in
	// the transcript thread before older ones collapse to a "+N earlier" note.
	agentThreadInlineCap = 8
	// compactTaskWidth bounds the sub-agent task shown in the transcript header.
	compactTaskWidth = 72
)

// compactTask returns the first line of s, trimmed and ellipsized to maxRunes
// (the ellipsis counts toward maxRunes). Returns "" for blank input.
func compactTask(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = strings.TrimSpace(s[:nl])
	}
	r := []rune(s)
	if maxRunes > 0 && len(r) > maxRunes {
		if maxRunes == 1 {
			return "…"
		}
		return string(r[:maxRunes-1]) + "…"
	}
	return s
}

// capThreadLines returns the lines to render for one sub-agent thread run: when
// len(lines) exceeds limit, a leading "… +N earlier" marker followed by the
// last `limit` lines; otherwise lines unchanged.
func capThreadLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	hidden := len(lines) - limit
	out := make([]string, 0, limit+1)
	out = append(out, fmt.Sprintf("… +%d earlier", hidden))
	out = append(out, lines[len(lines)-limit:]...)
	return out
}

// agentTranscriptLine renders a durable one-line transcript marker for a
// sub-agent lifecycle event, or "" for statuses that should not be logged.
func agentTranscriptLine(ev chat.AgentEvent) string {
	typ := ev.Type
	if typ == "" {
		typ = "agent"
	}
	switch ev.Status {
	case chat.AgentStatusRunning:
		if t := compactTask(ev.Task, compactTaskWidth); t != "" {
			return fmt.Sprintf("sub-agent %s started: %s", typ, t)
		}
		return fmt.Sprintf("sub-agent %s started", typ)
	case chat.AgentStatusCompleted:
		return fmt.Sprintf("sub-agent %s finished%s", typ, ev.StatsSuffix())
	case chat.AgentStatusFailed:
		if ev.Err != nil {
			return fmt.Sprintf("sub-agent %s failed: %v", typ, ev.Err)
		}
		return fmt.Sprintf("sub-agent %s failed", typ)
	default:
		return ""
	}
}

// agentJob is the UI view of a sub-agent for the jobs footer.
type agentJob struct {
	ID     string
	Type   string
	Task   string
	Status chat.AgentStatus
	// Title is the short title the model wrote for the task, "" until it
	// arrives (see chat.Callbacks.OnAgentTitle).
	Title string
	// Background is true for a sub-agent that no longer holds up the turn:
	// spawned in the background, or detached with ctrl+b.
	Background bool
}

// approvalContent is what a tool-approval prompt shows above its menu.
type approvalContent struct {
	title        string
	meta         string
	rows         [][2]string
	unstructured bool
	diff         *textdiff.Diff
}

// buildApprovalContent lays out a tool-approval prompt. The title is the
// call's one-line summary ("edit main.go", "$ go test ./..."), tagged with
// the sub-agent that asked; a tool the summary formatters do not know (an MCP
// tool) is titled by name with its arguments as a key/value card beneath. A
// write or edit with a predicted change shows that change as a diff, with its
// "+N -M" as the title's meta. Any further lines of the summary (a multi-line
// script) follow as one prose row; the summary's first line is never repeated
// under the title.
func buildApprovalContent(req chat.ToolCallRequest) approvalContent {
	var c approvalContent
	if rows, ok := chat.ToolArgRows(req.Name, req.Arguments); ok {
		c.title = req.Name
		c.rows = make([][2]string, len(rows))
		for i, r := range rows {
			c.rows[i] = [2]string{r.Key, r.ValueDisplay()}
		}
	} else {
		summary := chat.FormatToolCall(req.Name, req.Arguments)
		first, rest, _ := strings.Cut(summary, "\n")
		c.title = first
		if first == "" {
			c.title = req.Name
		}
		if req.Change != nil {
			if d := req.Change.Diff(); !d.Empty() {
				c.diff = &d
				c.meta = changeMeta(req.Change, render.DiffStat(d))
				rest = "" // the diff replaces the old -> new summary
			}
		}
		if strings.TrimSpace(rest) != "" {
			c.rows = [][2]string{{"", rest}}
			c.unstructured = true
		}
	}
	if req.Verdict != "" {
		v := theme.ClassifierVerdict + req.Verdict
		if c.meta != "" {
			v = c.meta + "  " + theme.Sep + "  " + v
		}
		c.meta = v
	}
	if req.AgentID != "" {
		c.title = theme.SubAgent + " " + render.ShortID(req.AgentID) + " " + theme.Sep + " " + c.title
	}
	return c
}

// hasForegroundWork reports whether something holds up the turn that ctrl+b
// can background: a foreground sub-agent or a foreground shell command.
func (m Model) hasForegroundWork() bool {
	for _, j := range m.jobs {
		if j.Status == chat.AgentStatusRunning && !j.Background {
			return true
		}
	}
	if m.shellJobs != nil {
		for _, j := range m.shellJobs.List() {
			if j.Running && !j.Backgrounded {
				return true
			}
		}
	}
	return false
}

// backgroundForeground backgrounds the work holding up the turn: the first
// foreground sub-agent, otherwise the foreground shell command. It returns
// what it backgrounded, or "" when there was nothing to.
//
// A sub-agent is marked background once detached: cogito accepts a second
// detach of the same agent without error, so without the mark ctrl+b would
// keep picking it. One that cannot be detached runs in the background
// already, and is marked so too.
func (m *Model) backgroundForeground() string {
	detach := m.detachAgent
	if detach == nil && m.session != nil && m.session.AgentManager() != nil {
		detach = m.session.AgentManager().Detach
	}
	for i := range m.jobs {
		j := &m.jobs[i]
		if j.Status != chat.AgentStatusRunning || j.Background || detach == nil {
			continue
		}
		err := detach(j.ID)
		j.Background = true
		if err == nil {
			return "sub-agent " + m.agentTypeOf(j.ID)
		}
	}
	if m.shellJobs != nil {
		if id, ok := m.shellJobs.DetachForeground(); ok {
			return "shell job " + id
		}
	}
	return ""
}

// applyAgentEvent upserts a job by ID and refreshes status.
func (m *Model) applyAgentEvent(ev chat.AgentEvent) {
	defer func() {
		m.toolEvents.syncChildRetention(m.jobs)
		m.toolEvents.retainChild(ev)
	}()
	for i := range m.jobs {
		if m.jobs[i].ID == ev.ID {
			m.jobs[i].Status = ev.Status
			if ev.Type != "" {
				m.jobs[i].Type = ev.Type
			}
			return
		}
	}
	m.jobs = append(m.jobs, agentJob{ID: ev.ID, Type: ev.Type, Task: ev.Task, Status: ev.Status, Title: m.earlyTitles[ev.ID], Background: ev.Background})
	delete(m.earlyTitles, ev.ID)
}

// applyAgentTitle records the title the model wrote for sub-agent id. It can
// arrive before the agent's own event reaches the UI: the two travel on
// separate channels. Then it is kept until the agent shows up.
func (m *Model) applyAgentTitle(id, title string) {
	for i := range m.jobs {
		if m.jobs[i].ID == id {
			m.jobs[i].Title = title
			return
		}
	}
	if m.earlyTitles == nil {
		m.earlyTitles = map[string]string{}
	}
	m.earlyTitles[id] = title
}

// jobByID returns the tracked sub-agent job with the given id.
func (m Model) jobByID(id string) (agentJob, bool) {
	for _, j := range m.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return agentJob{}, false
}
