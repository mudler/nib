package render

import (
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
		{16, "Waiting for your\nanswer"}, {8, "Waiting\nfor your\nanswer"}, {1, "Waiting\nfor\nyour\nanswer"},
	} {
		if got := summaryLine(s, theme.SpinnerFrames()[0], tc.width); got != tc.want {
			t.Errorf("width %d: %q want %q", tc.width, got, tc.want)
		}
	}
}
func TestActivitySummarySafeWordReflow(t *testing.T) {
	for _, s := range []ActivitySummary{
		{Primary: "\x1b[31m界界 approval\x1b[0m\n\t\r\x07 needed", Compact: "界界"},
		{Primary: "\x1b[2JReady\u2028now", Secondary: "\x1b]0;title\x07age\n3s", Counts: "shell\r2"},
		{},
	} {
		for w := 1; w < 100; w++ {
			got := summaryLine(s, theme.SpinnerFrames()[0], w)
			if strings.ContainsAny(got, "\r\t\x07\x1b\u2028\u2029") {
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
	v := ViewState{Loading: true, Reasoning: Reasoning{Rendered: "actual received reasoning"}, Speed: "12 tok/s"}
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

func TestStatusTelemetryYieldsToFullPhase(t *testing.T) {
	v := ViewState{Summary: ActivitySummary{Primary: "Ready for input", Compact: "Ready"}, Badges: "12345678"}
	if got := (Base{}).Footer(v, 20); strings.Contains(got, "\n") || !strings.Contains(got, "Ready for input") || strings.Contains(got, v.Badges) {
		t.Fatalf("telemetry displaced phase: %q", got)
	}
}

func TestStatusWholeFieldsAndFitOrder(t *testing.T) {
	s := ActivitySummary{Primary: "Ready for input", Schedule: "next run in 2m", CountsKnown: true, Agents: 123, Shells: 0}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, "Ready for input · next run in 2m · 123 agents · 0 shells"},
		{49, "Ready for input · next run in 2m · 123 ag · 0 sh"},
		{33, "Ready for input · 123 ag · 0 sh"},
		{25, "Ready for input · 123 ag"},
		{20, "Ready for input"},
		{8, "Ready\nfor\ninput"},
		{1, "Ready\nfor\ninput"},
	} {
		if got := statusLine(s, tc.width); got != tc.want {
			t.Errorf("width %d: %q want %q", tc.width, got, tc.want)
		}
	}
}

func TestStatusSanitizedDisplayWidths(t *testing.T) {
	s := ActivitySummary{Primary: "\x1b[31m界界\x1b[0m\r\nWorking\a", Schedule: "\x1b]0;title\a next\trun in 2m", CountsKnown: true, Agents: 123, Shells: 0}
	for w := 1; w <= 100; w++ {
		got := statusLine(s, w)
		if strings.ContainsAny(got, "\x1b\r\t\a") || !strings.Contains(got, "界界") || !strings.Contains(got, "Working") {
			t.Fatalf("width %d unsafe/lost words: %q", w, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > w && len(strings.Fields(line)) != 1 {
				t.Fatalf("width %d failed display-width fit: %q", w, line)
			}
		}
	}
}

func TestStatusTerminalRows(t *testing.T) {
	for _, tc := range []struct {
		text        string
		width, rows int
	}{
		{"Ready\nfor\ninput\n", 1, 13}, {"界界\nWorking\n", 3, 5},
		{"\x1b[31mWorking\x1b[0m\n", 7, 1}, {"Working\n\n", 7, 2}, {"", 1, 0},
	} {
		if got := TerminalRows(tc.text, tc.width); got != tc.rows {
			t.Errorf("%q width %d: %d want %d", tc.text, tc.width, got, tc.rows)
		}
	}
}
