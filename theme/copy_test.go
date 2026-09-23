package theme

import (
	"strings"
	"testing"
)

func TestCLIApprovePromptScope(t *testing.T) {
	got := CLIApprovePrompt("`git …`")
	want := "y yes · a always (`git …`) · all this turn · n no · or type a change"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReasoningHeaderLabel(t *testing.T) {
	if got := ReasoningHeader(""); !strings.Contains(got, ReasoningLabel) {
		t.Fatalf("empty label: want plain %q in header, got %q", ReasoningLabel, got)
	}
	if got := ReasoningHeader("inner monologue"); !strings.Contains(got, "inner monologue") {
		t.Fatalf("custom label missing from header: %q", got)
	}
	for i := 0; i < 50; i++ {
		l := RandomReasoningLabel()
		if l == "" || strings.Contains(l, "\n") {
			t.Fatalf("RandomReasoningLabel returned unusable label %q", l)
		}
	}
}
