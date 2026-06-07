package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/loop"
	"github.com/mudler/nib/theme"
)

// durationToCron maps a /loop interval to a 5-field cron expression. Intervals
// under a minute fire every minute (cron's finest granularity); hour-aligned
// intervals use the hour field; other minute intervals use the minute field,
// falling back to hourly when the minute step would exceed 59.
func durationToCron(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "* * * * *"
	case d%time.Hour == 0:
		h := int(d / time.Hour)
		return fmt.Sprintf("0 */%d * * *", h)
	default:
		m := int(d / time.Minute)
		if m > 59 {
			return "0 * * * *" // ≥1h non-aligned → hourly
		}
		return fmt.Sprintf("*/%d * * * *", m)
	}
}

// renderLoopsFooter renders a one-line summary of active cron loops, plus a
// self-paced indicator when selfPaced > 0. Returns "" when nothing is active.
func renderLoopsFooter(r *loop.Registry, selfPaced, width int) string {
	jobs := r.List()
	if len(jobs) == 0 && selfPaced == 0 {
		return ""
	}
	var parts []string
	for _, j := range jobs {
		parts = append(parts, fmt.Sprintf("%s %s%s%s", j.ID, j.Expr, theme.Arrow, truncateRunes(j.Prompt, 24)))
	}
	if selfPaced > 0 {
		parts = append(parts, fmt.Sprintf("%d self-paced", selfPaced))
	}
	line := fmt.Sprintf("%s %d loop(s): %s  (/loop list · /loop stop)", theme.Loop, len(jobs)+selfPaced, strings.Join(parts, " · "))
	return theme.Subtle.Render(line)
}
