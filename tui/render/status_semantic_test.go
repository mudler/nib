package render_test

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/tui/render"
	"strings"
	"testing"
)

func TestStatusSemanticWidth(t *testing.T) {
	for name, p := range presenters() {
		for _, phase := range []string{"Working", "Waiting for jobs", "Reviewing results", "Ready for input", "Approval needed", "Waiting for your answer", "Interrupting"} {
			for _, count := range []int{0, 1, 123} {
				for _, known := range []bool{false, true} {
					for _, schedule := range []string{"", "next run in 2m"} {
						for w := 1; w <= 140; w++ {
							s := render.ActivitySummary{Primary: phase, Agents: count, Shells: count, CountsKnown: known, Schedule: schedule}
							v := render.ViewState{Width: w, Summary: s}
							got := ansi.Strip(p.Header(v))
							if p.HeaderHeight(v) != render.TerminalRows(p.Header(v), w) || p.FooterHeight(v, w) != render.TerminalRows(p.Footer(v, w), w) {
								t.Fatalf("%s width %d dishonest height", name, w)
							}
							for _, word := range strings.Fields(phase) {
								if !strings.Contains(strings.ReplaceAll(got, "\n", ""), word) {
									t.Fatalf("%s width %d loses phase %q: %q", name, w, phase, got)
								}
							}
							if w == 140 && known && count == 0 && !strings.Contains(got, "0 agents · 0 shells") {
								t.Fatalf("missing zeros: %q", got)
							}
							if w == 140 && known && count == 123 && !strings.Contains(got, "123 agents · 123 shells") {
								t.Fatalf("missing counts: %q", got)
							}
							if w == 140 && known && count == 1 && !strings.Contains(got, "1 agent · 1 shell") {
								t.Fatalf("plural: %q", got)
							}
							if !known && strings.Contains(got, "agents") {
								t.Fatalf("invented counts: %q", got)
							}
						}
					}
				}
			}
		}
	}
}

func TestStatusControlsAndFooter(t *testing.T) {
	for name, p := range presenters() {
		for _, phase := range []string{"Approval needed", "Waiting for your answer", "Interrupting"} {
			for w := 1; w <= 120; w++ {
				v := render.ViewState{Width: w, Summary: render.ActivitySummary{Primary: phase}, Help: "enter approve · esc cancel · ctrl+c interrupt"}
				header, footer := ansi.Strip(p.Header(v)), ansi.Strip(p.Footer(v, w))
				for _, word := range strings.Fields(phase) {
					if !strings.Contains(strings.ReplaceAll(header, "\n", ""), word) {
						t.Fatalf("%s width %d lost %q: %q", name, w, word, header)
					}
				}
				for _, word := range []string{"enter", "approve", "esc", "cancel", "ctrl+c", "interrupt"} {
					if !strings.Contains(strings.ReplaceAll(footer, "\n", ""), word) {
						t.Fatalf("%s width %d lost control %q: %q", name, w, word, footer)
					}
				}
				if strings.Contains(footer, "Parked") || strings.Contains(footer, "Working") {
					t.Fatalf("contradictory footer: %q", footer)
				}
			}
		}
	}
}
