package render

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestActivitySummaryFit(t *testing.T) {
	s := ActivitySummary{Primary: "Approval needed", Compact: "Approval", Secondary: "received 12s ago", Counts: "background: agents 2"}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, "Approval needed · received 12s ago · background: agents 2"},
		{34, "Approval needed · received 12s ago"},
		{20, "Approval needed"}, {10, "Approval"}, {3, "App"},
	} {
		if got := summaryLine(s, tc.width); got != tc.want {
			t.Errorf("width %d: %q want %q", tc.width, got, tc.want)
		}
	}
}
func TestActivitySummarySingleSafeRow(t *testing.T) {
	for _, s := range []ActivitySummary{
		{Primary: "\x1b[31m界界 approval\x1b[0m\n\t\r\x07 needed", Compact: "界界"},
		{Primary: "\x1b[2JReady\u2028now", Secondary: "\x1b]0;title\x07age\n3s", Counts: "shell\r2"},
		{},
	} {
		for w := 1; w < 100; w++ {
			got := summaryLine(s, w)
			if lipgloss.Width(got) > w || strings.ContainsAny(got, "\n\r\t\x07\x1b\u2028\u2029") || lipgloss.Height(got) != 1 {
				t.Fatalf("width %d: %q", w, got)
			}
		}
	}
}
func TestActivitySummaryFooterPositionAndHeight(t *testing.T) {
	b := Base{}
	v := ViewState{Summary: ActivitySummary{Primary: "Parked"}, Footers: []FooterRow{{Text: "logs"}}, Help: "help", Expanded: "telemetry", NewOutput: true, Err: "oops"}
	out := ansi.Strip(b.Footer(v, 80))
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if l == "Parked" {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "logs") {
				t.Fatal(out)
			}
			if b.FooterHeight(v, 80) != len(lines) {
				t.Fatal(out)
			}
			return
		}
	}
	t.Fatal(out)
}
func TestSummaryReplacesGenericLoaderNotReasoning(t *testing.T) {
	b := Base{}
	v := ViewState{Loading: true, Reasoning: Reasoning{Text: "actual received reasoning"}, Speed: "12 tok/s"}
	out := ansi.Strip(b.Reasoning(v, 80))
	if !strings.Contains(out, "actual received reasoning") || !strings.Contains(out, "12 tok/s") {
		t.Fatal(out)
	}
	if got := b.Reasoning(ViewState{Loading: true}, 80); got != "" {
		t.Fatalf("generic loader remains: %q", got)
	}
	v.Status = "Compacting conversation"
	if !strings.Contains(ansi.Strip(b.Reasoning(v, 80)), v.Status) {
		t.Fatal("lost explicit notice")
	}
}
