package render

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/theme"
	"strings"
	"testing"
)

func TestActivitySummaryFit(t *testing.T) {
	s := ActivitySummary{Primary: "Waiting for your answer", Compact: "Answer needed", Secondary: "12s", Marker: SummaryMarkerWaiting}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, theme.WaitingMarker + " Waiting for your answer · 12s"},
		{26, theme.WaitingMarker + " Waiting for your answer"},
		{16, theme.WaitingMarker + " Answer needed"}, {8, theme.WaitingMarker + " Answer"}, {1, theme.WaitingMarker},
	} {
		if got := summaryLine(s, theme.SpinnerFrames()[0], tc.width); got != tc.want {
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
			got := summaryLine(s, theme.SpinnerFrames()[0], w)
			if lipgloss.Width(got) > w || strings.ContainsAny(got, "\n\r\t\x07\x1b\u2028\u2029") || lipgloss.Height(got) != 1 {
				t.Fatalf("width %d: %q", w, got)
			}
		}
	}
}

func TestActivitySummaryMarkersAnimateOnlyActiveStates(t *testing.T) {
	frames := theme.SpinnerFrames()
	for _, marker := range []SummaryMarker{SummaryMarkerWorking, SummaryMarkerRunning} {
		a := summaryLine(ActivitySummary{Primary: "Working", Marker: marker}, frames[0], 80)
		b := summaryLine(ActivitySummary{Primary: "Working", Marker: marker}, frames[1], 80)
		if a == b {
			t.Fatalf("active marker %v did not animate: %q", marker, a)
		}
	}
	for _, marker := range []SummaryMarker{SummaryMarkerReady, SummaryMarkerWaiting, SummaryMarkerApproval, SummaryMarkerParked, SummaryMarkerInterrupting} {
		a := summaryLine(ActivitySummary{Primary: "state", Marker: marker}, frames[0], 80)
		b := summaryLine(ActivitySummary{Primary: "state", Marker: marker}, frames[1], 80)
		if a != b {
			t.Fatalf("stable marker %v animated: %q != %q", marker, a, b)
		}
	}
}

func TestActivitySummaryStableMarkerCopy(t *testing.T) {
	for _, tc := range []struct {
		mode SummaryMarker
		want string
	}{
		{SummaryMarkerReady, theme.ReadyMarker},
		{SummaryMarkerWaiting, theme.WaitingMarker},
		{SummaryMarkerApproval, theme.ApprovalMarker},
		{SummaryMarkerParked, theme.ParkedMarker},
		{SummaryMarkerInterrupting, theme.InterruptingMarker},
	} {
		got := summaryLine(ActivitySummary{Primary: "state", Marker: tc.mode}, "different-frame", 80)
		if got != tc.want+" state" {
			t.Errorf("marker %v = %q, want %q", tc.mode, got, tc.want+" state")
		}
	}
}
func TestActivitySummaryFooterPositionAndHeight(t *testing.T) {
	b := Base{}
	v := ViewState{Summary: ActivitySummary{Primary: "Parked"}, Footers: []FooterRow{{Text: "logs"}}, Help: "help", Expanded: "telemetry", NewOutput: true, Err: "oops"}
	out := ansi.Strip(b.Footer(v, 80))
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.HasSuffix(l, "Parked") {
			if i < 2 || lines[i-1] != "help" || !strings.Contains(lines[i-2], "logs") {
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
