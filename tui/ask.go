package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/wiz/chat"

	"github.com/charmbracelet/lipgloss"
)

// renderAsk renders the agent's question (and numbered options, if any).
func renderAsk(req chat.AskRequest, width int) string {
	var b strings.Builder
	b.WriteString(askHeaderStyle.Render("❓ " + req.Question))
	b.WriteString("\n")
	for i, o := range req.Options {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, o)
	}
	if len(req.Options) > 0 {
		b.WriteString(dimmedStyle.Render("Type a number to pick, or type your own answer."))
	} else {
		b.WriteString(dimmedStyle.Render("Type your answer."))
	}
	return askBoxStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// parseAskAnswer maps a typed answer to an option when it is a valid 1-based
// index into req.Options; otherwise it returns the raw text (free-form answer).
func parseAskAnswer(input string, req chat.AskRequest) string {
	trimmed := strings.TrimSpace(input)
	if len(req.Options) > 0 {
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(req.Options) {
			return req.Options[n-1]
		}
	}
	return input
}

var (
	askBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	askHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
)
