package render

import (
	"fmt"
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
	Agents, Shells int
	CountsKnown    bool
	Schedule       string
	Updating       bool
	Primary        string
	Compact        string
	Secondary      string
	Counts         string
	Marker         SummaryMarker
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

// statusLine fits complete fields before reflowing phase words. Unknown counts
// are absent, not zero. Legacy Counts and Compact are not sources of truth.
func statusLine(s ActivitySummary, w int) string {
	if w <= 0 {
		return ""
	}
	phase := cleanSummaryLabel(s.Primary)
	if phase == "" {
		phase = "Working"
		s.Updating = true
	}
	if s.Updating {
		phase += " · status updating"
	}
	schedule := cleanSummaryLabel(s.Schedule)
	count := func(n int, label string) string {
		if n != 1 {
			label += "s"
		}
		return fmt.Sprintf("%d %s", n, label)
	}
	var counts []string
	if s.CountsKnown {
		counts = []string{count(s.Agents, "agent"), count(s.Shells, "shell")}
	}
	join := func() string {
		fields := []string{phase}
		if schedule != "" {
			fields = append(fields, schedule)
		}
		return strings.Join(append(fields, counts...), " · ")
	}
	if ansi.StringWidth(join()) <= w {
		return join()
	}
	if len(counts) > 0 {
		counts = []string{fmt.Sprintf("%d ag", s.Agents), fmt.Sprintf("%d sh", s.Shells)}
	}
	if ansi.StringWidth(join()) <= w {
		return join()
	}
	schedule = ""
	for {
		if ansi.StringWidth(join()) <= w {
			return join()
		}
		if len(counts) == 0 {
			break
		}
		counts = counts[:len(counts)-1]
	}
	return ReflowWords(phase, w)
}

// ReflowWords preserves words even below their display width. Such a word
// wraps physically in the terminal; TerminalRows accounts for those rows.
func ReflowWords(text string, w int) string {
	if w <= 0 {
		return ""
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(cleanSummaryLabel(text)) {
		if line != "" && ansi.StringWidth(line+" "+word) > w {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// TerminalRows measures physical display rows, including oversized words.
// A trailing newline terminates the block without adding an empty row.
func TerminalRows(text string, w int) int {
	if text == "" || w <= 0 {
		return 0
	}
	text = strings.TrimSuffix(text, "\n")
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		rows += max(1, (ansi.StringWidth(line)+w-1)/w)
	}
	return rows
}

func summaryLine(s ActivitySummary, spinner string, w int) string {
	line := statusLine(s, w)
	if line == "" {
		return ""
	}
	marker := cleanSummaryLabel(summaryMarker(s.Marker, spinner))
	if !strings.Contains(line, "\n") && ansi.StringWidth(marker+" "+line) <= w {
		line = marker + " " + line
	}
	if secondary := cleanSummaryLabel(s.Secondary); secondary != "" && !s.Updating && !strings.Contains(line, "\n") && ansi.StringWidth(line+" · "+secondary) <= w {
		line += " · " + secondary
	}
	return line
}

func cleanSummaryLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, ansi.Strip(s))
	return strings.Join(strings.Fields(s), " ")
}

// CompactSummaryWidth reserves the full phase ahead of optional telemetry.
// Decorative markers may yield; phase words never yield to badges.
func CompactSummaryWidth(s ActivitySummary, spinner string, w int) int {
	s.CountsKnown, s.Schedule = false, ""
	return min(max(w, 0), ansi.StringWidth(statusLine(s, 1000000)))
}
