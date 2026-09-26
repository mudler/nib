package tui

import (
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/tui/render"
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
	// The ask_user dialog: question + options, rendered through the presenter
	// (buildAskDialog replaced the old renderAsk plain-text block).
	req := chat.AskRequest{Question: "Pick one", Options: []string{"alpha", "beta"}}
	ask := testPresenter().Dialog(buildAskDialog(req, &render.SelectList{Items: req.Options}, false), 80)
	if containsEmoji(ask) {
		t.Fatalf("ask dialog output contains emoji: %q", ask)
	}

	// Activity strip chips (sub-agents running, done, failed).
	jm := newTestModel(Model{jobs: []agentJob{
		{ID: "a1", Type: "explore", Task: "scan", Status: chat.AgentStatusRunning},
		{ID: "b2", Type: "plan", Task: "draft", Status: chat.AgentStatusCompleted},
		{ID: "c3", Type: "edit", Task: "patch", Status: chat.AgentStatusFailed},
	}})
	for _, row := range jm.footerRows() {
		if containsEmoji(row.Glyph + row.Text + row.Alert) {
			t.Fatalf("activity chip contains emoji: %q", row.Glyph+row.Text+row.Alert)
		}
	}

	// Tool-approval labels, both sub-agent and root variants.
	sub := buildApprovalContent(chat.ToolCallRequest{Name: "echo", AgentID: "a1b2c3d4e5"}).title
	if containsEmoji(sub) {
		t.Fatalf("approval title (sub-agent) contains emoji: %q", sub)
	}
	root := buildApprovalContent(chat.ToolCallRequest{Name: "echo"}).title
	if containsEmoji(root) {
		t.Fatalf("approval title (root) contains emoji: %q", root)
	}

	// Ctrl+O log viewer list.
	lv := newLogsModel()
	lv.showLogs = true
	if out := lv.renderLogsViewer(); containsEmoji(out) {
		t.Fatalf("renderLogsViewer output contains emoji: %q", out)
	}

	// Completion popup (tags, names, descriptions, ghost hint).
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)
	c.sync("/rev")
	comp := renderCompletion(c, "/rev", 80)
	if containsEmoji(comp) {
		t.Fatalf("renderCompletion output contains emoji: %q", comp)
	}

	// Tool-output pruning notice, both the plural and the singular reading.
	for _, n := range []string{prunedNotice(3, 12400), prunedNotice(1, 900)} {
		if containsEmoji(n) {
			t.Fatalf("prunedNotice output contains emoji: %q", n)
		}
	}

	// Compaction notice. It sat outside this sweep for months and carried an
	// emoji the whole time, which is what a guard listing its subjects one by
	// one costs when a helper is added and nobody remembers to list it.
	if n := compactNotice(47200, 12100); containsEmoji(n) {
		t.Fatalf("compactNotice output contains emoji: %q", n)
	}

	// The sibling notice that package chat writes into the transcript — which
	// this model renders verbatim — is guarded on its own side, by
	// chat.TestCompactionTranscriptNoticeHasNoEmoji. The rule is about what a
	// user sees, and a user sees both; reaching that one from here would mean
	// exporting a chat function that exists only for this test, so each package
	// guards the strings it writes.
}
