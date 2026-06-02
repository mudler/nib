package plugin

import "testing"

func TestAliasClaudeTools(t *testing.T) {
	got := aliasClaudeTools([]string{"Bash", "Read", "Edit", "MultiEdit", "Glob", "Grep", "Write", "Task", "WebFetch"})
	want := map[string]bool{"bash": true, "read": true, "edit": true, "glob": true, "grep": true, "write": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected mapped tool %q (got %v)", g, got)
		}
	}
	if w, ok := aliasClaudeTool("Bash"); !ok || w != "bash" {
		t.Fatalf("Bash -> %q ok=%v", w, ok)
	}
	if _, ok := aliasClaudeTool("Task"); ok {
		t.Fatal("Task should be unmapped")
	}
}
