package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/theme"
)

func TestStreamingMarkdownUsesProvidedCursor(t *testing.T) {
	m := streamModel()
	out := ansi.Strip(m.renderStreamingMarkdown("# Heading", 60, "CURSOR"))
	if strings.Contains(out, "# Heading") || !strings.Contains(out, "Heading") {
		t.Fatalf("streaming markdown was not rendered: %q", out)
	}
	if strings.Count(out, "CURSOR") != 1 {
		t.Fatalf("cursor count = %d, want 1 in %q", strings.Count(out, "CURSOR"), out)
	}
}

func TestLiveReasoningRendersStreamingMarkdown(t *testing.T) {
	m := streamModel()
	m.reasoning = "# Plan\n\nUse **shared** rendering.\n\n```go\nfunc main() {"
	m.reasoningShown = len(m.reasoning)
	m.reasoningSince = time.Now().Add(-time.Second)
	m.reasoningCollapsed = false
	m.updateViewport()

	out := ansi.Strip(m.viewport.View())
	for _, raw := range []string{"# Plan", "**shared**", "```"} {
		if strings.Contains(out, raw) {
			t.Fatalf("reasoning contains raw markdown %q: %q", raw, out)
		}
	}
	for _, rendered := range []string{"Plan", "shared", "func main() {"} {
		if !strings.Contains(out, rendered) {
			t.Fatalf("reasoning is missing rendered content %q: %q", rendered, out)
		}
	}
	if strings.Count(out, theme.StreamCursor) != 1 {
		t.Fatalf("streaming cursor count = %d, want 1 in %q", strings.Count(out, theme.StreamCursor), out)
	}
}

func TestExpandedThoughtRendersFinalMarkdownWithoutCursor(t *testing.T) {
	m := streamModel()
	m.loading = false
	m.reasoningCollapsed = false
	m.appendMessage(ChatMessage{
		Role:    "thought",
		Content: "# Plan\n\nUse **shared** rendering.\n\n```go\nfunc main() {}\n```",
		Meta:    "thought for 1s",
	})
	m.updateViewport()

	out := ansi.Strip(m.viewport.View())
	for _, raw := range []string{"# Plan", "**shared**", "```"} {
		if strings.Contains(out, raw) {
			t.Fatalf("expanded thought contains raw markdown %q: %q", raw, out)
		}
	}
	for _, rendered := range []string{"Plan", "shared", "func main() {}"} {
		if !strings.Contains(out, rendered) {
			t.Fatalf("expanded thought is missing rendered content %q: %q", rendered, out)
		}
	}
	if strings.Contains(out, theme.StreamCursor) {
		t.Fatalf("completed thought has a streaming cursor: %q", out)
	}
}

func TestCollapsedReasoningTailsRenderedRows(t *testing.T) {
	m := streamModel()
	m.reasoning = "# Old heading\n\nold paragraph\n\n- one\n- two\n- newest"
	m.reasoningShown = len(m.reasoning)
	m.reasoningSince = time.Now().Add(-time.Second)
	m.reasoningCollapsed = true
	m.updateViewport()

	out := ansi.Strip(m.viewport.View())
	if strings.Contains(out, "Old heading") {
		t.Fatalf("collapsed reasoning did not hide old rendered rows: %q", out)
	}
	if !strings.Contains(out, "newest") || !strings.Contains(out, theme.StreamCursor) {
		t.Fatalf("collapsed reasoning did not retain rendered tail and cursor: %q", out)
	}
}
