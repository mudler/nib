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

func TestRenderMarkdownWith_NoBackgroundSGR(t *testing.T) {
	r, err := nibMarkdownRenderer(80)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	// A doc exercising the styled blocks most likely to carry a background:
	// fenced code, inline code, blockquote, list, heading, link.
	md := "# Title\n\n> a quote\n\n- item one\n- item two\n\nInline `code` and a [link](https://example.com).\n\n```go\nfunc main() {}\n```\n"
	out := renderMarkdownWith(r, md, 80)
	// Background SGR introducers we must never emit (terminal must show through):
	//   \x1b[48;...  -> 256-color / truecolor background
	//   \x1b[40m..47m, \x1b[100m..107m -> standard / bright background
	if strings.Contains(out, "\x1b[48") || strings.Contains(out, ";48;") {
		t.Fatalf("found extended background SGR in rendered markdown: %q", out)
	}
	for _, code := range []string{
		"\x1b[40m", "\x1b[41m", "\x1b[42m", "\x1b[43m", "\x1b[44m", "\x1b[45m", "\x1b[46m", "\x1b[47m",
		"\x1b[100m", "\x1b[101m", "\x1b[102m", "\x1b[103m", "\x1b[104m", "\x1b[105m", "\x1b[106m", "\x1b[107m",
	} {
		if strings.Contains(out, code) {
			t.Fatalf("found background SGR %q in rendered markdown: %q", code, out)
		}
	}
}
