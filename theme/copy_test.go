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
	if got := ReasoningHeader("", 0); !strings.Contains(got, "reasoning") {
		t.Fatalf("empty label: want \"reasoning\" in header, got %q", got)
	}
	if got := ReasoningHeader("inner monologue", 0); !strings.Contains(got, "inner monologue") {
		t.Fatalf("custom label missing from header: %q", got)
	}
}
