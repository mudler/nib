package tui

import (
	"fmt"
	"strings"

	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/theme"
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
	parts := []string{fmt.Sprintf("%s shell: %d running", theme.ShellJob, running)}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	parts = append(parts, "(ctrl+b background · ctrl+o logs)")
	return jobsFooterStyle.Width(width).Render(strings.Join(parts, "  ·  "))
}
