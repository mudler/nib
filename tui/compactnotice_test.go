package tui

import (
	"strings"
	"testing"
)

// The TUI keeps its own copy of the compaction notice because it cannot import
// cmd, so the same two rules have to be pinned twice: no emoji, and no estimate
// dressed up as a measurement.
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
}
