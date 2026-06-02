package tui

import (
	"strings"

	"github.com/mudler/nib/theme"
)

// renderEmptyState is the first-run view shown before any messages exist. It
// teaches the interface — the value line, a few example prompts, and the `/`
// discovery hint — rather than presenting a blank viewport.
func renderEmptyState(width int) string {
	var b strings.Builder
	indent := "  "

	b.WriteString("\n")
	b.WriteString(indent + theme.Help.Render(theme.EmptyTagline))
	b.WriteString("\n\n")
	b.WriteString(indent + theme.Meta.Render(theme.EmptyTryLead))
	b.WriteString("\n")
	for _, ex := range theme.EmptyExamples {
		b.WriteString(indent + "  " + theme.SepStyle.Render(theme.Sep) + " " + theme.Meta.Render(ex))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(indent + theme.Help.Render(theme.EmptySlash))
	b.WriteString("\n")
	return b.String()
}
