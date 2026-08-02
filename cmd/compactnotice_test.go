package cmd

import (
	"strings"
	"testing"
)

func TestCompactNoticeHasNoEmojiAndDoesNotClaimMeasurement(t *testing.T) {
	got := compactNotice(47200, 12100)
	for _, r := range got {
		if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
			t.Fatalf("compact notice contains emoji: %q", got)
		}
	}
	// These are byte/4 estimates, not the backend's reported usage. Saying so
	// is the difference between a number a user can act on and one they cannot.
	if !strings.Contains(got, "~") && !strings.Contains(strings.ToLower(got), "approx") {
		t.Fatalf("notice presents an estimate as a measurement: %q", got)
	}
	// The figures themselves still have to be there — marking them approximate
	// must not turn the notice into a content-free "compacted something".
	if !strings.Contains(got, "47.2k") || !strings.Contains(got, "12.1k") {
		t.Fatalf("notice dropped its before/after figures: %q", got)
	}
}
