package tui

import (
	"strings"
	"testing"
)

func TestRenderGoalFooter(t *testing.T) {
	if f := renderGoalFooter("", 80); f != "" {
		t.Fatalf("no goal should render empty, got %q", f)
	}
	f := renderGoalFooter("make all tests pass", 80)
	if !strings.Contains(f, "make all tests pass") {
		t.Fatalf("footer missing goal text: %q", f)
	}
	if !strings.Contains(f, "/goal clear") {
		t.Fatalf("footer should hint how to clear: %q", f)
	}
}
