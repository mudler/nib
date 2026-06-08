package plugin

import "testing"

func TestAliasClaudeTools(t *testing.T) {
	got := aliasClaudeTools([]string{"Bash", "Read", "Edit", "MultiEdit", "Glob", "Grep", "Write", "Task", "TodoWrite"})
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

func TestAliasClaudeWebTools(t *testing.T) {
	cases := map[string]string{
		"WebFetch":  "web_fetch",
		"WebSearch": "web_search",
	}
	for claudeName, want := range cases {
		got, ok := aliasClaudeTool(claudeName)
		if !ok {
			t.Errorf("%s: expected an alias, got dropped", claudeName)
			continue
		}
		if got != want {
			t.Errorf("%s aliased to %q, want %q", claudeName, got, want)
		}
	}
}
