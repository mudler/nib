package cmd

import (
	"strings"
	"testing"
)

func TestPruneNoticeReadsAsPlainProse(t *testing.T) {
	got := pruneNotice(3, 12400)
	for _, want := range []string{"3", "12.4k"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notice %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("notice must stay one line: %q", got)
	}
	for _, r := range got {
		if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
			t.Fatalf("notice contains emoji: %q", got)
		}
	}
}

// "1 stale tool result", not "1 stale tool results".
func TestPruneNoticeSingular(t *testing.T) {
	got := pruneNotice(1, 900)
	if strings.Contains(got, "1 results") || strings.Contains(got, "1 stale tool results") {
		t.Fatalf("notice should be singular for one result: %q", got)
	}
}

// A stale read is stubbed regardless of size, so a pass can free nothing
// measurable. chat.HumanTokens renders 0 as the empty string, which would leave
// the sentence with a hole where its number belongs.
func TestPruneNoticeFillsAZeroSaving(t *testing.T) {
	got := pruneNotice(1, 0)
	if !strings.Contains(got, "0") {
		t.Fatalf("a zero saving dropped its number: %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("notice has a hole where a count should be: %q", got)
	}
}
