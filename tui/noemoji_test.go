package tui

import (
	"testing"

	"github.com/mudler/nib/chat"
)

// containsEmoji reports whether s contains a rune in the common emoji ranges.
func containsEmoji(s string) bool {
	for _, r := range s {
		if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
			return true
		}
	}
	return false
}

// TestNoEmojiInRenderHelpers guards the calm, no-emoji editorial voice: the
// user-facing render helpers must not emit emoji glyphs.
func TestNoEmojiInRenderHelpers(t *testing.T) {
	// renderAsk header + options.
	ask := renderAsk(chat.AskRequest{
		Question: "Pick one",
		Options:  []string{"alpha", "beta"},
	}, 80)
	if containsEmoji(ask) {
		t.Fatalf("renderAsk output contains emoji: %q", ask)
	}

	// Jobs footer (running/done/failed counts).
	footer := renderJobsFooter([]agentJob{
		{ID: "a1", Type: "explore", Task: "scan", Status: chat.AgentStatusRunning},
		{ID: "b2", Type: "plan", Task: "draft", Status: chat.AgentStatusCompleted},
		{ID: "c3", Type: "edit", Task: "patch", Status: chat.AgentStatusFailed},
	}, 80)
	if containsEmoji(footer) {
		t.Fatalf("renderJobsFooter output contains emoji: %q", footer)
	}

	// Tool-approval labels, both sub-agent and root variants.
	sub := toolApprovalLabel(chat.ToolCallRequest{Name: "echo", AgentID: "a1b2c3d4e5"})
	if containsEmoji(sub) {
		t.Fatalf("toolApprovalLabel (sub-agent) contains emoji: %q", sub)
	}
	root := toolApprovalLabel(chat.ToolCallRequest{Name: "echo"})
	if containsEmoji(root) {
		t.Fatalf("toolApprovalLabel (root) contains emoji: %q", root)
	}
}
