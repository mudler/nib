package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/nib/theme"
)

// helpKeys are the TUI's keys, in the order /help lists them. Keep it in step
// with the key handling in Update.
var helpKeys = [][2]string{
	{"enter", "send; while a turn runs, queue it for after (most commands run at once)"},
	{"tab", "accept the suggestion or completion"},
	{"shift+tab", "cycle the approval mode"},
	{"↑ ↓", "history; with queued messages, pick one"},
	{"ctrl+e / ctrl+x", "edit / delete the picked queued message (empty composer)"},
	{"esc", "interrupt the turn, or close what is open"},
	{"ctrl+c", "interrupt the turn (the queue goes next), clear the draft, twice to exit"},
	{"ctrl+b", "move the foreground sub-agent or shell command to the background"},
	{"ctrl+g", "activity strip: ←→ pick a chip, enter open it"},
	{"ctrl+o", "logs of sub-agents and shell jobs; a running sub-agent's log takes a message"},
	{"ctrl+t", "the todo list"},
	{"ctrl+r", "expand or fold reasoning and tool output"},
	{"ctrl+y", "quit and hand the suggested command to your shell"},
	{"pgup / pgdn, G / end", "scroll; jump to the newest"},
}

// helpText is /help: the keys, then every command the completion popup
// offers (built-ins, the user's commands, skills and agents).
func helpText(items []compItem) string {
	var b strings.Builder
	b.WriteString("keys\n")
	width := 0
	for _, k := range helpKeys {
		width = max(width, len([]rune(k[0])))
	}
	for _, k := range helpKeys {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, k[0], k[1])
	}

	b.WriteString("\ncommands\n")
	names := make([]string, len(items))
	width = 0
	for i, it := range items {
		names[i] = strings.TrimSpace(it.Insert)
		width = max(width, len([]rune(names[i])))
	}
	for i, it := range items {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, names[i], it.Desc)
	}
	b.WriteString("\n" + theme.HelpMore)
	return b.String()
}
