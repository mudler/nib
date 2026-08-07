package tui

import (
	"strings"
	"testing"
)

// The TUI keeps its own copy of the compaction notice because it cannot import
// cmd, so the same rules have to be pinned twice: no emoji, no estimate dressed
// up as a measurement, and no figure that disappears when it is zero.
func TestCompactNoticeHasNoEmojiAndDoesNotClaimMeasurement(t *testing.T) {
	got := compactNotice(47200, 12100)
	if containsEmoji(got) {
		t.Fatalf("compact notice contains emoji: %q", got)
	}
	if !strings.Contains(got, "~") && !strings.Contains(strings.ToLower(got), "approx") {
		t.Fatalf("notice presents an estimate as a measurement: %q", got)
	}
	if !strings.Contains(got, "47.2k") || !strings.Contains(got, "12.1k") {
		t.Fatalf("notice dropped its before/after figures: %q", got)
	}
	// And the third: a zero renders as "0" rather than vanishing, the same rule
	// the prune notice next to it follows. chat.HumanTokens renders 0 as "",
	// which would print "~ → ~ tokens".
	if z := compactNotice(0, 0); !strings.Contains(z, "~0 → ~0 tokens") {
		t.Fatalf("a zero figure vanished from the notice: %q", z)
	}
}
