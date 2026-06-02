package tui

import (
	"fmt"
	"strings"
)

// jobRef is a unified reference to a killable job — either a sub-agent or a
// shell job — used by the numbered detail view and Ctrl+K kill selection.
type jobRef struct {
	Kind   string // "agent" | "shell"
	ID     string
	Status string
	Label  string
}

// unifiedJobs returns sub-agent jobs followed by shell jobs, in the same order
// they appear in the footers, so the numbers shown in the detail view line up
// with what Ctrl+K acts on.
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

// renderUnifiedJobsDetail renders the numbered, unified per-job list (Ctrl+J),
// whose indices Ctrl+K uses for selection.
func renderUnifiedJobsDetail(jobs []jobRef, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, j := range jobs {
		label := strings.ReplaceAll(j.Label, "\n", " ")
		if len(label) > 40 {
			label = label[:37] + "..."
		}
		fmt.Fprintf(&b, "  [%d] %-6s %-8s %-10s %s\n", i+1, j.Kind, shortID(j.ID), j.Status, label)
	}
	return jobsFooterStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
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
	// A job the user killed shouldn't trigger an auto-notify turn.
	if m.notifiedJobs == nil {
		m.notifiedJobs = map[string]bool{}
	}
	m.notifiedJobs[j.ID] = true
	m.status = "Killed " + j.ID
}
