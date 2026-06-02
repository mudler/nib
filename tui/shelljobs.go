package tui

import (
	"fmt"
	"strings"

	wizmcp "github.com/mudler/wiz/mcp"
)

// renderShellJobsFooter renders a compact one-line summary of shell jobs
// (background or backgrounded). Returns "" when there are none.
func renderShellJobsFooter(jobs []wizmcp.ShellJobInfo, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var running, done, failed int
	for _, j := range jobs {
		switch j.Status {
		case "running":
			running++
		case "completed":
			done++
		case "failed":
			failed++
		}
	}
	parts := []string{fmt.Sprintf("▷ shell: %d running", running)}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	parts = append(parts, "(ctrl+b background · ctrl+j detail)")
	return jobsFooterStyle.Width(width).Render(strings.Join(parts, "  ·  "))
}

// renderShellJobsDetail renders the expanded per-job list for shell jobs.
func renderShellJobsDetail(jobs []wizmcp.ShellJobInfo, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, j := range jobs {
		script := strings.ReplaceAll(j.Script, "\n", " ")
		if len(script) > 48 {
			script = script[:45] + "..."
		}
		b.WriteString(fmt.Sprintf("  %-7s  %-10s  %s\n", j.ID, j.Status, script))
	}
	return jobsFooterStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}
