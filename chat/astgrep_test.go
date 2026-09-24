package chat

import (
	"os/exec"
	"strings"
	"testing"
)

func TestASTGrepToolDefinition(t *testing.T) {
	td := astGrepToolDefinition(func(p string) string { return p }).Tool()
	if td.Function.Name != "ast_grep" {
		t.Fatalf("name = %q, want ast_grep", td.Function.Name)
	}
}

func TestASTGrepNotInstalled(t *testing.T) {
	if _, err := exec.LookPath("ast-grep"); err == nil {
		t.Skip("ast-grep installed; not-installed path untestable")
	}
	tool := &astGrepTool{resolvePath: func(p string) string { return p }}
	out, _, _ := tool.Run(map[string]any{"pat": "func $F($_) { $$$ }", "path": "."})
	if !strings.Contains(out, "ast-grep binary not found") {
		t.Fatalf("expected not-found message, got: %s", out)
	}
}
