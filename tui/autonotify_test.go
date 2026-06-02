package tui

import (
	"strings"
	"testing"
)

func TestTailOf(t *testing.T) {
	if got := tailOf("  hi there  "); got != "hi there" {
		t.Fatalf("trim: %q", got)
	}
	long := strings.Repeat("x", noticeTailLimit+50)
	got := tailOf(long)
	if !strings.HasPrefix(got, "…") {
		t.Fatal("over-limit output should be marked with a leading ellipsis")
	}
	if len(got) != noticeTailLimit+len("…") {
		t.Fatalf("tail length = %d, want %d", len(got), noticeTailLimit+len("…"))
	}
}

func TestCollectCompletionsNilSafe(t *testing.T) {
	// nil shellJobs (List has a nil guard) and nil session must not panic.
	m := Model{}
	m.collectCompletions()
	if len(m.pendingNotices) != 0 {
		t.Fatalf("expected no notices, got %v", m.pendingNotices)
	}
}

func TestCanAutoNotifyGating(t *testing.T) {
	// Not ready / no session → never auto-notify.
	if (Model{}).canAutoNotify() {
		t.Fatal("a model with no session should not auto-notify")
	}
}
