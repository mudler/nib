package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// jobRef is a unified reference to a killable job — either a sub-agent or a
// shell job — used by the Ctrl+O log viewer's list and kill selection.
type jobRef struct {
	Kind   string // "agent" | "shell"
	ID     string
	Status string
	Label  string
}

// unifiedJobs returns sub-agent jobs followed by shell jobs, in the same order
// they appear in the footers, so the numbers shown in the Ctrl+O viewer line up
// with what the kill selection acts on.
func (m Model) unifiedJobs() []jobRef {
	var out []jobRef
	for _, j := range m.jobs {
		typ := j.Type
		if typ == "" {
			typ = "agent"
		}
		out = append(out, jobRef{Kind: "agent", ID: j.ID, Status: string(j.Status), Label: typ + " · " + j.Task})
	}
	for _, s := range m.shellJobs.List() {
		if !s.Backgrounded {
			continue
		}
		out = append(out, jobRef{Kind: "shell", ID: s.ID, Status: s.Status, Label: s.Script})
	}
	return out
}

// jobActivityTail returns recent activity for a job: a sub-agent's captured
// agent_logs, or a shell job's captured output.
func (m Model) jobActivityTail(j jobRef) string {
	return m.jobActivityTailAt(j, time.Now())
}

func (m Model) jobActivityTailAt(j jobRef, now time.Time) string {
	switch j.Kind {
	case "agent":
		var b strings.Builder
		b.WriteString(receiptDetails(m.childObservation(j.ID), now))
		b.WriteString("\n")
		if m.phaseStartedAt.IsZero() {
			b.WriteString("current phase start: unavailable")
		} else {
			b.WriteString(fmt.Sprintf("current phase start: %ds ago", receiptSeconds(now, m.phaseStartedAt)))
		}
		b.WriteString("\n\n")
		if job, ok := m.jobByID(j.ID); ok && strings.TrimSpace(job.Task) != "" {
			b.WriteString("task:\n")
			b.WriteString(strings.TrimSpace(job.Task))
			b.WriteString("\n\n")
		}
		if m.session != nil {
			b.WriteString(m.session.AgentLog(j.ID))
		}
		return strings.TrimRight(b.String(), "\n")
	case "shell":
		if so, se, ok := m.shellJobs.Output(j.ID); ok {
			return strings.TrimRight(so+se, "\n")
		}
	}
	return ""
}

// lastLines returns the last n non-empty-trimmed lines of s.
func lastLines(s string, n int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// clipLine truncates a single line to width runes with an ellipsis. It counts
// runes (not bytes) so multibyte characters in tool labels and paths are never
// split into invalid UTF-8.
func clipLine(s string, width int) string {
	if width < 8 {
		width = 8
	}
	if r := []rune(s); len(r) > width {
		return string(r[:width-1]) + "…"
	}
	return s
}

// openJobLog opens one job's full log. A sub-agent's log gets a fresh input
// line addressed to it.
func (m *Model) openJobLog(j jobRef) {
	m.logOpenID, m.logOpenKind = j.ID, j.Kind
	m.agentInputNote = ""
	if j.Kind == "agent" {
		m.agentInput = newAgentInput(m.agentTypeOf(j.ID))
	}
	m.syncLogViewport()
}

// syncLogViewport loads the open job's full log into the log viewport.
func (m *Model) syncLogViewport() {
	if m.logOpenID == "" {
		return
	}
	content := m.jobActivityTail(jobRef{Kind: m.logOpenKind, ID: m.logOpenID})
	if strings.TrimSpace(content) == "" {
		content = "(no activity recorded yet)"
	}
	width := m.logVP.Width
	if width <= 0 {
		width = m.width
	}
	m.logVP.SetContent(theme.Help.Render(render.Wrap(content, width-1)))
	m.logVP.GotoBottom()
}

// logViewerChrome is how many rows an open log's view adds around the log
// itself: the "logs" title, a blank line, the job's header, and the status
// line under the log (blank while there is nothing to say).
const logViewerChrome = 4

// renderLogsViewer renders the Ctrl+O viewer: a selectable list of sub-agents +
// background jobs, or the scrollable full log of the one the user opened.
func (m Model) renderLogsViewer() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("logs"))
	b.WriteString("\n\n")
	if m.logOpenID != "" {
		// Open one job's full log. A sub-agent's header names it and its task,
		// and says whether it still takes messages.
		header := m.logOpenKind + " " + render.ShortID(m.logOpenID)
		var footer string
		if job, ok := m.jobByID(m.logOpenID); ok && m.logOpenKind == "agent" {
			header = m.agentTypeOf(job.ID)
			if title := job.title(); title != "" {
				header += ": " + title
			}
			header += " " + theme.Sep + " " + string(job.Status)
			if job.Status != chat.AgentStatusRunning {
				footer = "finished " + theme.Sep + " ask nib to follow up with it"
			}
		}
		b.WriteString(theme.Meta.Render(header))
		b.WriteString("\n")
		b.WriteString(m.logVP.View())
		// Always a row, so the frame keeps its height when the agent ends.
		b.WriteString("\n" + theme.Help.Render(footer))
		return b.String()
	}
	jobs := m.unifiedJobs()
	if len(jobs) == 0 {
		b.WriteString(theme.Meta.Render("  no sub-agents or background jobs yet."))
		return b.String()
	}
	// Clamp the highlight in case the list shrank since the last keypress.
	sel := m.logSel
	if sel >= len(jobs) {
		sel = len(jobs) - 1
	}
	if sel < 0 {
		sel = 0
	}
	for i, j := range jobs {
		label := strings.ReplaceAll(j.Label, "\n", " ")
		label = clipLine(label, m.width-30)
		row := fmt.Sprintf("[%d] %-6s %-8s %-9s %s", i+1, j.Kind, render.ShortID(j.ID), j.Status, label)
		if i == sel {
			b.WriteString(theme.Prompt.Render(theme.PromptGlyph) + " " + theme.Brand.Render(row))
		} else {
			b.WriteString("  " + theme.Help.Render(row))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// killSelected kills the n-th (1-based) job in the unified list.
func (m *Model) killSelected(n int) {
	jobs := m.unifiedJobs()
	if n < 1 || n > len(jobs) {
		return
	}
	j := jobs[n-1]
	switch j.Kind {
	case "agent":
		if m.session != nil {
			m.session.KillAgent(j.ID)
		}
	case "shell":
		m.shellJobs.Kill(j.ID)
	}
	m.status = "Killed " + j.ID
}
