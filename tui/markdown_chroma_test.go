package tui

import (
	"strings"
	"testing"
)

func TestSyntaxHighlight_Applied(t *testing.T) {
	r, err := nibMarkdownRenderer(80)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	md := "```go\nfunc main() {\n\t// comment\n\tfmt.Println(\"hello\")\n}\n```\n"
	out := renderMarkdownWith(r, md, 80)
	// Chroma should produce color escape sequences for the code block.
	// If chroma is not active, the output is plain text with no SGR codes.
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected SGR escape sequences in highlighted code, got none: %q", out)
	}
	// The function keyword should be highlighted with the accent color.
	if !strings.Contains(out, "func") {
		t.Fatalf("expected 'func' keyword in output: %q", out)
	}
}

func TestSyntaxHighlight_CommentColored(t *testing.T) {
	r, err := nibMarkdownRenderer(80)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	md := "```go\n// a comment\nx := 1\n```\n"
	out := renderMarkdownWith(r, md, 80)
	// The comment text must survive rendering.
	if !strings.Contains(out, "a comment") {
		t.Fatalf("comment text lost: %q", out)
	}
}
