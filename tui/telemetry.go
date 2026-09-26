package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/theme"
)

// Footer telemetry items, as ui.footer_front and ui.footer_expanded name them.
const (
	telemetryContext = "context"
	telemetrySpeed   = "speed"
	telemetryUsage   = "usage"
	telemetryAge     = "age"
	telemetryClock   = "clock"
	telemetryCPU     = "cpu"
	telemetryMem     = "mem"
)

// Defaults for the two telemetry lines: what predicts compaction and how fast
// the model is, up front; the rest one ctrl+g away.
const (
	defaultFooterFront    = "context,speed"
	defaultFooterExpanded = "usage,age,clock,cpu,mem"
)

// telemetryItems parses a ui.footer_* value: item names separated by commas
// or spaces, "" for the default, and "none" for no items. Unknown names are
// dropped.
func telemetryItems(value, def string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = def
	}
	if value == "none" {
		return nil
	}
	var out []string
	for _, f := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' }) {
		switch f {
		case telemetryContext, telemetrySpeed, telemetryUsage, telemetryAge, telemetryClock, telemetryCPU, telemetryMem:
			out = append(out, f)
		}
	}
	return out
}

// telemetryForms renders one telemetry item in each form it has, widest first,
// or nil when it has nothing to show yet. ui.hide_hud hides the machine items
// (clock, cpu, mem) wherever they are placed.
func (m Model) telemetryForms(item string) []string {
	hud := !m.cfg.UI.HideHUD
	switch item {
	case telemetryContext:
		return m.contextBadges()
	case telemetrySpeed:
		if full, narrow := m.speedBadges(); full != "" {
			return []string{full, narrow}
		}
	case telemetryUsage:
		if u := m.usageBadge(); u != "" {
			return []string{u}
		}
	case telemetryAge:
		if !m.sessionCreated.IsZero() {
			return []string{theme.Help.Render("age ") + theme.Meta.Render(humanAge(time.Since(m.sessionCreated)))}
		}
	case telemetryClock:
		if hud && m.hudClock != "" {
			return []string{theme.Meta.Render(m.hudClock)}
		}
	case telemetryCPU:
		if hud && m.hudCPUOK {
			return []string{theme.Help.Render("cpu ") + theme.Meta.Render(strconv.Itoa(m.hudCPU)+"%")}
		}
	case telemetryMem:
		if hud && m.hudMemTotal > 0 {
			return []string{theme.Help.Render("mem ") + theme.Meta.Render(humanGiB(m.hudMemUsed)+"/"+humanGiB(m.hudMemTotal)+"G")}
		}
	}
	return nil
}

// telemetryLine renders items on one line of width cells, in order, which is
// also their priority. Each item takes its widest form that still fits, an
// item with no form that fits is left out, and the first item is always kept
// in its narrowest form, since the context gauge predicts compaction.
func (m Model) telemetryLine(items []string, width int) string {
	const sep = "  "
	var parts []string
	used := 0
	for _, item := range items {
		forms := m.telemetryForms(item)
		if len(forms) == 0 {
			continue
		}
		gap := 0
		if len(parts) > 0 {
			gap = len(sep)
		}
		pick := ""
		for _, f := range forms {
			if used+gap+lipgloss.Width(f) <= width {
				pick = f
				break
			}
		}
		if pick == "" && len(parts) == 0 {
			pick = forms[len(forms)-1]
		}
		if pick == "" {
			continue
		}
		parts = append(parts, pick)
		used += gap + lipgloss.Width(pick)
	}
	return strings.Join(parts, sep)
}

// footerBadges renders the front telemetry line (ui.footer_front) for a
// line that already holds helpWidth cells.
func (m Model) footerBadges(helpWidth int) string {
	return m.telemetryLine(telemetryItems(m.cfg.UI.FooterFront, defaultFooterFront), m.width-helpWidth)
}

// expandedBadges renders the expanded telemetry line (ui.footer_expanded).
func (m Model) expandedBadges() string {
	return m.telemetryLine(telemetryItems(m.cfg.UI.FooterExpanded, defaultFooterExpanded), m.width)
}

// humanAge renders a session's age in its two largest units: "7d 5h",
// "5h 12m", "12m", "30s".
func humanAge(d time.Duration) string {
	d = d.Round(time.Second)
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
