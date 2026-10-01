package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var baseTestSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestFooterNeverClipsMultiDigitFailureIntoAnotherCount(t *testing.T) {
	v := ViewState{Footers: []FooterRow{{Text: "History 123", Alert: "×123", Selected: true}}}
	for w := 1; w < 30; w++ {
		out := baseTestSGR.ReplaceAllString((Base{}).Footer(v, w), "")
		if strings.Contains(out, "×1") && !strings.Contains(out, "×123") {
			t.Fatalf("width %d rendered a false partial failure count: %q", w, out)
		}
		if !strings.Contains(out, "×") {
			t.Fatalf("width %d lost the failure marker: %q", w, out)
		}
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d rendered %d cells: %q", w, got, line)
			}
		}
	}
}

func TestFooterChipFitPreservesSelectedAndAlerts(t *testing.T) {
	rows := []FooterRow{
		{Glyph: "↳", Text: "explore: \x1b[31m非常に長い\x1b[0m\nlabel", State: ChipActive},
		{Text: "History 12", Alert: "×3", Selected: true},
		{Glyph: "○", Text: "idle detail"},
	}
	for w := 18; w < 100; w++ {
		got := fitChips(rows, w)
		var selected bool
		for _, row := range got {
			if strings.Contains(row.Text, "\x1b") || strings.ContainsAny(row.Text, "\r\n") {
				t.Fatalf("width %d retained control input: %+v", w, row)
			}
			if row.Selected {
				selected = true
				if row.Alert != "×3" {
					t.Fatalf("width %d cut failure alert: %+v", w, row)
				}
			}
		}
		if !selected {
			t.Fatalf("width %d dropped selected history: %+v", w, got)
		}
		var parts []string
		for _, row := range got {
			part := chipText(row)
			if row.Alert != "" {
				part += " " + row.Alert
			}
			parts = append(parts, part)
		}
		if gotWidth := lipgloss.Width(strings.Join(parts, chipSep)); gotWidth > w {
			t.Fatalf("width %d produced %d cells: %+v", w, gotWidth, got)
		}
	}
}

func TestFooterSelectedNoAlertChipFitsEveryWidth(t *testing.T) {
	cases := []FooterRow{
		{Glyph: "o", Text: "todo 1/4 trace lifecycle", Kind: FooterTodo, Selected: true},
		{Text: "History 12", Kind: FooterJobs, Selected: true},
		{Glyph: "@", Text: "explore: trace lifecycle · working", Kind: FooterJobs, State: ChipActive, Selected: true},
	}
	for _, row := range cases {
		for width := 1; width <= 99; width++ {
			fitted := fitChips([]FooterRow{row}, width)
			var rendered string
			for i, got := range fitted {
				if i > 0 {
					rendered += chipSep
				}
				rendered += chipText(got)
				if got.Alert != "" {
					rendered += " " + got.Alert
				}
			}
			if got := lipgloss.Width(rendered); got > width {
				t.Errorf("%q at width %d rendered %q (%d cells)", row.Text, width, rendered, got)
			}
		}
	}
}

func TestFooterActivityRowFitsEveryTerminalWidth(t *testing.T) {
	v := ViewState{
		Help:      "enter send",
		HelpRight: "ctrl+g details",
		Footers: []FooterRow{
			{Glyph: "界", Text: "live \x1b[31magent\x1b[0m\ncontrol", State: ChipActive},
			{Glyph: "!", Text: "History 12", Alert: "×3", Selected: true},
			{Glyph: "○", Text: "idle detail"},
		},
	}
	for w := 1; w < 100; w++ {
		out := (Base{}).Footer(v, w)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) < 2 {
			t.Fatalf("width %d footer lost activity row: %q", w, out)
		}
		activity := baseTestSGR.ReplaceAllString(lines[len(lines)-2], "")
		if got := lipgloss.Width(activity); got > w {
			t.Fatalf("width %d rendered %d-cell activity row: %q", w, got, activity)
		}
		if w >= lipgloss.Width("×3") && !strings.Contains(activity, "×3") {
			t.Fatalf("width %d cut displayable failure alert: %q", w, activity)
		}
		if strings.ContainsAny(activity, "\r\n") || strings.Contains(activity, "\x1b") {
			t.Fatalf("width %d retained unsafe input: %q", w, activity)
		}
	}
}

func TestFooterDropsDetailsHintBeforeUsefulHistory(t *testing.T) {
	v := ViewState{
		Help: "enter send", HelpRight: "ctrl+g details",
		Footers: []FooterRow{{Text: "History 12", Alert: "×3", Selected: true}},
	}
	out := baseTestSGR.ReplaceAllString((Base{}).Footer(v, 16), "")
	if strings.Contains(out, v.HelpRight) || !strings.Contains(out, "History") || !strings.Contains(out, "×3") {
		t.Fatalf("hint competed with useful selected history: %q", out)
	}
}

// TestBaseContentWidthUsesInjectedPrefix pins the embedding fix Task 17
// depends on: Base cannot call back into an embedder's own contentPrefix (Go
// embedding has no virtual dispatch), so ContentWidth must measure whatever
// function was actually stored in the Prefix field, not some default of its
// own.
func TestBaseContentWidthUsesInjectedPrefix(t *testing.T) {
	b := Base{Prefix: func(Role) string { return "XXXX" }} // 4 cells
	if got, want := b.ContentWidth(RoleUser, 10), 6; got != want {
		t.Errorf("ContentWidth = %d, want %d (10 - 4-cell prefix)", got, want)
	}
}

// TestBaseContentWidthClampsToAtLeastOne: a terminal narrower than the chrome
// must still yield a legal width for a renderer (glamour, Wrap) to use.
func TestBaseContentWidthClampsToAtLeastOne(t *testing.T) {
	b := Base{Prefix: func(Role) string { return "far too wide a prefix" }}
	if got := b.ContentWidth(RoleUser, 5); got != 1 {
		t.Errorf("ContentWidth = %d, want 1 (clamped)", got)
	}
}

// TestBaseContentWidthPanicsWithoutPrefix pins the loud-failure contract: a
// future third presenter that embeds Base and forgets to wire up Prefix must
// get a clear panic naming the cause, not a bare nil-func dereference and
// never a silent default that would render the wrong chrome.
func TestBaseContentWidthPanicsWithoutPrefix(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ContentWidth with a nil Prefix did not panic")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "Prefix") {
			t.Errorf("panic value = %v, want a message naming Prefix", r)
		}
	}()
	Base{}.ContentWidth(RoleUser, 10)
}

// TestBaseHeaderHeightMatchesHeader ties HeaderHeight to Header's own output
// directly on Base, independent of either presenter: the layout budget
// subtracts HeaderHeight from the frame, so an answer that disagrees with the
// real string over- or under-budgets the viewport.
func TestBaseHeaderHeightMatchesHeader(t *testing.T) {
	b := Base{}
	v := ViewState{Width: 60, Brand: "nib", Cwd: "~/src/project"}
	if got, want := b.HeaderHeight(v), BlockRows(b.Header(v)); got != want {
		t.Errorf("HeaderHeight = %d, want %d (BlockRows of the real Header output)", got, want)
	}
}

// TestBaseHeaderNamesTheProvider: the header shows "provider · model" so a
// /login pick is never mistaken for config.yaml's model, and on a terminal
// too narrow for both the provider gives way before the model does.
func TestBaseHeaderNamesTheProvider(t *testing.T) {
	b := Base{}
	v := ViewState{Width: 80, Brand: "nib", Cwd: "~/p",
		HeaderStats: HeaderStats{Provider: "regolo", Model: "glm5.2"}}
	line := baseTestSGR.ReplaceAllString(strings.SplitN(b.Header(v), "\n", 2)[0], "")
	if !strings.Contains(line, "regolo · glm5.2") {
		t.Fatalf("header = %q, want %q", line, "regolo · glm5.2")
	}

	v.Width = 22 // room for brand, model and cwd, not the provider
	line = baseTestSGR.ReplaceAllString(strings.SplitN(b.Header(v), "\n", 2)[0], "")
	if strings.Contains(line, "regolo") || !strings.Contains(line, "glm5.2") {
		t.Fatalf("narrow header = %q, want the model without the provider", line)
	}
}

// TestBaseFooterHeightMatchesFooter is the same guard for the footer: the
// core measures FooterHeight before body is laid out, so it must count
// exactly the rows Footer itself renders (lipgloss.Height, since — unlike
// Header — nothing is written directly onto Footer's own trailing content).
func TestBaseFooterHeightMatchesFooter(t *testing.T) {
	b := Base{}
	v := ViewState{
		Help: "tab complete", Badges: "12k ctx", NewOutput: true, Err: "boom",
		Footers: []FooterRow{{Glyph: "*", Text: "1 job running", Kind: FooterJobs}},
	}
	const w = 60
	got := b.FooterHeight(v, w)
	want := 5 // summary + marker + activity strip + help (with the badges) + error
	if got != want {
		t.Errorf("FooterHeight = %d, want %d", got, want)
	}
}

// TestBaseDialogUnhandledKindRendersNothing pins the shared contract
// TestUnknownDialogKindRendersNothing (conformance_test.go) exercises through
// both presenters: a Kind neither ask/resume nor approval must render as
// nothing, not a half-drawn card.
func TestBaseDialogUnhandledKindRendersNothing(t *testing.T) {
	b := Base{}
	if out := b.Dialog(Dialog{Kind: DialogKind(99), Title: "resume?"}, 60); out != "" {
		t.Errorf("Dialog(unhandled kind) = %q, want empty", out)
	}
}

// TestBaseReasoningRendersNothingWhenIdle pins the same not-loading contract
// directly on Base, since it is the sole implementation both presenters now
// share.
func TestBaseReasoningRendersNothingWhenIdle(t *testing.T) {
	b := Base{}
	v := ViewState{Loading: false, Spinner: "|", Status: "Working", Reasoning: Reasoning{Text: "thinking"}}
	if out := b.Reasoning(v, 50); out != "" {
		t.Errorf("Reasoning(idle) = %q, want empty", out)
	}
}
