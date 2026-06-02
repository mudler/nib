package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/wiz/chat"

	"github.com/charmbracelet/lipgloss"
)

// renderAsk renders the agent's question (and numbered options, if any). The
// glyph hints the selection mode: ( ) radio for single-select, [ ] checkbox for
// multi-select.
func renderAsk(req chat.AskRequest, width int) string {
	var b strings.Builder
	b.WriteString(askHeaderStyle.Render("❓ " + req.Question))
	b.WriteString("\n")
	marker := "( )"
	if req.MultiSelect {
		marker = "[ ]"
	}
	for i, o := range req.Options {
		fmt.Fprintf(&b, "  %s %d. %s\n", marker, i+1, o)
	}
	switch {
	case len(req.Options) > 0 && req.MultiSelect:
		b.WriteString(dimmedStyle.Render("Type numbers separated by commas (e.g. 1,3), or type your own answer."))
	case len(req.Options) > 0:
		b.WriteString(dimmedStyle.Render("Type a number to pick, or type your own answer."))
	default:
		b.WriteString(dimmedStyle.Render("Type your answer."))
	}
	return askBoxStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// parseAskAnswer maps a typed answer onto req.Options. For single-select a lone
// 1-based index returns that option. For multi-select a list of indices (comma-
// or space-separated, e.g. "1,3" or "1 3") returns the chosen options joined by
// ", ". Anything that doesn't parse cleanly as indices is returned verbatim as a
// free-form answer.
func parseAskAnswer(input string, req chat.AskRequest) string {
	trimmed := strings.TrimSpace(input)
	if len(req.Options) == 0 {
		return input
	}

	if req.MultiSelect {
		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		var chosen []string
		for _, f := range fields {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(req.Options) {
				return input // not a clean index list → treat as free text
			}
			chosen = append(chosen, req.Options[n-1])
		}
		if len(chosen) > 0 {
			return strings.Join(chosen, ", ")
		}
		return input
	}

	if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(req.Options) {
		return req.Options[n-1]
	}
	return input
}

var (
	askBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	askHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
)
