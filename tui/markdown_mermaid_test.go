package tui

import (
	"strings"
	"testing"
)

func TestStripMermaidFences_ReplacesFence(t *testing.T) {
	md := "```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n"
	out := stripMermaidFences(md)
	if strings.Contains(out, "mermaid") {
		t.Fatalf("mermaid fence not stripped: %q", out)
	}
	if !strings.Contains(out, "Mermaid diagram") {
		t.Fatalf("placeholder missing: %q", out)
	}
	if !strings.Contains(out, "nodes") {
		t.Fatalf("node count missing: %q", out)
	}
}

func TestStripMermaidFences_PreservesOtherFences(t *testing.T) {
	md := "```go\nfmt.Println(\"hi\")\n```\n```mermaid\nA-->B\n```\n"
	out := stripMermaidFences(md)
	if !strings.Contains(out, "fmt.Println") {
		t.Fatalf("go fence should be preserved: %q", out)
	}
	if !strings.Contains(out, "Mermaid diagram") {
		t.Fatalf("mermaid placeholder missing: %q", out)
	}
}

func TestStripMermaidFences_NoFences(t *testing.T) {
	md := "just plain text, no fences"
	out := stripMermaidFences(md)
	if out != md {
		t.Fatalf("content changed without fences: %q", out)
	}
}

func TestStripMermaidFences_NodeCount(t *testing.T) {
	md := "```mermaid\ngraph TD\n  A[Start] --> B[Middle] --> C[End]\n```"
	out := stripMermaidFences(md)
	// Should mention some number of nodes
	if !strings.Contains(out, "nodes") {
		t.Fatalf("node count missing: %q", out)
	}
}
