package theme

import "testing"

func TestCLIApprovePromptScope(t *testing.T) {
	got := CLIApprovePrompt("`git …`")
	want := "y yes · a always (`git …`) · all this turn · n no · or type a change"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
