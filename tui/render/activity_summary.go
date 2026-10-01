package render

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/theme"
)

// SummaryMarker selects the semantic lifecycle marker. Active modes consume
// the already-selected ViewState spinner frame; every other mode is stable.
type SummaryMarker uint8

const (
	SummaryMarkerReady SummaryMarker = iota + 1
	SummaryMarkerWorking
	SummaryMarkerRunning
	SummaryMarkerWaiting
	SummaryMarkerApproval
	SummaryMarkerParked
	SummaryMarkerInterrupting
)

// ActivitySummary is factual presentation data. Fitting and footer placement
// belong to the presenter, not callback adapters or lifecycle registries.
type ActivitySummary struct {
	Primary   string
	Compact   string
	Secondary string
	Counts    string
	Marker    SummaryMarker
}

func summaryMarker(mode SummaryMarker, spinner string) string {
	switch mode {
	case SummaryMarkerWorking, SummaryMarkerRunning:
		if spinner == "" {
			return theme.SpinnerFrames()[0]
		}
		return spinner
	case SummaryMarkerWaiting:
		return theme.WaitingMarker
	case SummaryMarkerApproval:
		return theme.ApprovalMarker
	case SummaryMarkerParked:
		return theme.ParkedMarker
	case SummaryMarkerInterrupting:
		return theme.InterruptingMarker
	default:
		return theme.ReadyMarker
	}
}

// summaryLine treats labels as plain text, stripping terminal commands before
// measuring cells. No callback-supplied control may move or wrap the pinned row.
func summaryLine(s ActivitySummary, spinner string, w int) string {
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
	marker := clean(summaryMarker(s.Marker, spinner))
	if marker == "" {
		marker = theme.ReadyMarker
	}
	prefix := marker + " "
	full := prefix + primary
	if secondary := clean(s.Secondary); secondary != "" {
		withDuration := full + " · " + secondary
		if ansi.StringWidth(withDuration) <= w {
			return withDuration
		}
	}
	if ansi.StringWidth(full) <= w {
		return full
	}
	if compact != "" {
		primary = compact
	}
	compactLine := prefix + primary
	if ansi.StringWidth(compactLine) <= w {
		return compactLine
	}
	if ansi.StringWidth(marker) >= w {
		return ansi.Truncate(marker, w, "")
	}
	line := marker + " " + ansi.Truncate(primary, w-ansi.StringWidth(prefix), "")
	return strings.TrimRight(line, " ")
}
