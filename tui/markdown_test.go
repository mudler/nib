package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownWith_RendersMarkup(t *testing.T) {
	r, err := nibMarkdownRenderer(80)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	out := renderMarkdownWith(r, "# Title\n\nSome **bold** and `code`.", 80)
	if strings.Contains(out, "# Title") {
		t.Fatalf("heading marker not rendered: %q", out)
	}
	if strings.Contains(out, "**bold**") {
		t.Fatalf("bold markers not rendered: %q", out)
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "bold") {
		t.Fatalf("content text missing: %q", out)
	}
}

func TestRenderMarkdownWith_PlainPassthrough(t *testing.T) {
	r, err := nibMarkdownRenderer(80)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	out := renderMarkdownWith(r, "just a plain line", 80)
	if !strings.Contains(out, "just a plain line") {
		t.Fatalf("plain text lost: %q", out)
	}
}

func TestRenderMarkdownWith_NilFallsBack(t *testing.T) {
	// nil renderer → falls back to wrapText, never panics.
	out := renderMarkdownWith(nil, "hello world", 80)
	if !strings.Contains(out, "hello world") {
		t.Fatalf("fallback lost text: %q", out)
	}
}
