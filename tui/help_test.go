package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// /help lists the keys and every command the completion popup offers,
// including the user's own commands, skills and agents.
func TestHelpListsKeysAndCommands(t *testing.T) {
	m := newQueueTestModel()
	m.cfg.Commands = []types.CommandConfig{{Name: "deploy", Description: "ship it"}}
	m.cfg.Skills = []types.Skill{{Name: "review", Description: "review a diff"}}
	m.dispatchResolved("/help")
	got := m.messages[len(m.messages)-1].Content
	for _, want := range []string{
		"ctrl+g", "ctrl+b", "ctrl+o", "ctrl+t", "ctrl+r", "shift+tab", "ctrl+c",
		"/compact", "/model", "/settings", "/help",
		"/deploy", "ship it", "/skill review", "review a diff",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/help lacks %q:\n%s", want, got)
		}
	}
}
