package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/nib/theme"
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
		out = append(out, jobRef{Kind: "shell", ID: s.ID, Status: s.Status, Label: s.Script})
	}
	return out
}

// jobActivityTail returns recent activity for a job: a sub-agent's captured
// agent_logs, or a shell job's captured output.
func (m Model) jobActivityTail(j jobRef) string {
	switch j.Kind {
	case "agent":
		if m.session != nil {
			return m.session.AgentLog(j.ID)
		}
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

// clipLine truncates a single line to width runes with an ellipsis.
func clipLine(s string, width int) string {
	if width < 8 {
		width = 8
	}
	if len(s) > width {
		return s[:width-1] + "…"
	}
	return s
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
	m.logVP.SetContent(theme.Help.Render(wrapText(content, width-1)))
	m.logVP.GotoBottom()
}

// renderLogsViewer renders the Ctrl+O viewer: a selectable list of sub-agents +
// background jobs, or the scrollable full log of the one the user opened.
func (m Model) renderLogsViewer() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("logs"))
	b.WriteString("\n\n")
	if m.logOpenID != "" {
		// Open one job's full log.
		b.WriteString(theme.Meta.Render(m.logOpenKind + " " + shortID(m.logOpenID)))
		b.WriteString("\n")
		b.WriteString(m.logVP.View())
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
		row := fmt.Sprintf("[%d] %-6s %-8s %-9s %s", i+1, j.Kind, shortID(j.ID), j.Status, label)
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
