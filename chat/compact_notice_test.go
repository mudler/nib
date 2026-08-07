package chat

import (
	"strings"
	"testing"
)

// containsEmoji reports whether s holds a rune in the common emoji ranges. It is
// a copy of the tui test helper of the same name on purpose: the no-emoji rule
// covers what a user sees, and package chat writes user-visible strings of its
// own, so it carries its own guard rather than exporting its notices for
// tui/noemoji_test.go to reach across the package boundary.
func containsEmoji(s string) bool {
	for _, r := range s {
		if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
			return true
		}
	}
	return false
}

// The compaction notice goes into s.messages, which the TUI renders — so it is
// a user-facing render helper in every sense except the one
// tui.TestNoEmojiInRenderHelpers can see.
func TestCompactionTranscriptNoticeHasNoEmoji(t *testing.T) {
	got := compactedNotice(12)
	if containsEmoji(got) {
		t.Fatalf("compaction notice contains emoji: %q", got)
	}
	if !strings.Contains(got, "12") {
		t.Fatalf("notice should say how many messages were compacted: %q", got)
	}
}
