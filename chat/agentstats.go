package chat

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/nib/theme"
)

// humanTokens renders a token count like "847 tokens" or "12.4k tokens".
// Returns "" for zero/negative so the segment can be omitted.
func humanTokens(n int) string {
	s := formatTokenCount(n)
	if s == "" {
		return ""
	}
	return s + " tokens"
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

// HumanRate formats a rate in tokens per second: whole numbers, with one
// decimal below 10 so a slow model does not read as 0 or 1.
func HumanRate(r float64) string {
	if r < 10 {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	return strconv.Itoa(int(math.Round(r)))
}

// outputTokens renders the generated-token count, "812", or "~812" when it
// is an estimate. Returns "" for zero.
func (ev AgentEvent) outputTokens() string {
	s := formatTokenCount(ev.OutputTokens)
	if s != "" && ev.OutputEstimated {
		s = theme.UsageEstimatedPrefix + s
	}
	return s
}

// StatsSuffix renders the trailing run-stats summary for a completed sub-agent,
// e.g. " · 3 tools · 12.4k tokens (812 out) · 38 tok/s · 1m 03s". Segments
// whose value is zero or unknown are omitted; returns "" when nothing is known.
func (ev AgentEvent) StatsSuffix() string {
	var parts []string
	switch {
	case ev.ToolCount == 1:
		parts = append(parts, "1 tool")
	case ev.ToolCount > 1:
		parts = append(parts, fmt.Sprintf("%d tools", ev.ToolCount))
	}
	out := ev.outputTokens()
	switch t := humanTokens(ev.TotalTokens); {
	case t != "" && out != "":
		parts = append(parts, t+" ("+out+" out)")
	case t != "":
		parts = append(parts, t)
	case out != "":
		parts = append(parts, out+" tokens out")
	}
	if ev.TokensPerSec > 0 {
		parts = append(parts, HumanRate(ev.TokensPerSec)+" tok/s")
	}
	if d := humanDuration(ev.Elapsed); d != "" {
		parts = append(parts, d)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}
