package render

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"unicode"
)

// ActivitySummary is factual presentation data. Fitting and footer placement
// belong to the presenter, not callback adapters or lifecycle registries.
type ActivitySummary struct {
	Primary   string
	Compact   string
	Secondary string
	Counts    string
}

// summaryLine treats labels as plain text, stripping terminal commands before
// measuring cells. No callback-supplied control may move or wrap the pinned row.
func summaryLine(s ActivitySummary, w int) string {
	if w <= 0 {
		return ""
	}
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, ansi.Strip(s))
		return strings.Join(strings.Fields(s), " ")
	}
	primary, compact := clean(s.Primary), clean(s.Compact)
	if primary == "" {
		primary = "Ready"
	}
	parts := []string{primary}
	if secondary := clean(s.Secondary); secondary != "" {
		parts = append(parts, secondary)
	}
	if counts := clean(s.Counts); counts != "" {
		parts = append(parts, counts)
	}
	for len(parts) > 1 {
		line := strings.Join(parts, " · ")
		if ansi.StringWidth(line) <= w {
			return line
		}
		parts = parts[:len(parts)-1]
	}
	if ansi.StringWidth(primary) <= w {
		return primary
	}
	if compact != "" {
		primary = compact
	}
	line := ansi.Truncate(primary, w, "")
	if line == "" {
		return " "
	} // a wide first rune still occupies one row
	return line
}
