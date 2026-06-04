package chat

import (
	"fmt"
	"strings"
	"time"
)

// humanTokens renders a token count like "847 tokens" or "12.4k tokens".
// Returns "" for zero/negative so the segment can be omitted.
func humanTokens(n int) string {
	if n <= 0 {
		return ""
	}
	if n < 1000 {
		return fmt.Sprintf("%d tokens", n)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000.0)
	s = strings.TrimSuffix(s, ".0")
	return s + "k tokens"
}

// humanDuration renders a duration like "12s", "1m 03s", or "1h 01m".
// Returns "" for zero/negative so the segment can be omitted.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
}

// StatsSuffix renders the trailing run-stats summary for a completed sub-agent,
// e.g. " · 3 tools · 12.4k tokens · 1m 03s". Segments whose value is zero or
// unknown are omitted; returns "" when nothing is known.
func (ev AgentEvent) StatsSuffix() string {
	var parts []string
	switch {
	case ev.ToolCount == 1:
		parts = append(parts, "1 tool")
	case ev.ToolCount > 1:
		parts = append(parts, fmt.Sprintf("%d tools", ev.ToolCount))
	}
	if t := humanTokens(ev.TotalTokens); t != "" {
		parts = append(parts, t)
	}
	if d := humanDuration(ev.Elapsed); d != "" {
		parts = append(parts, d)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}
