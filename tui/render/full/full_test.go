package full

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR escape sequences so a prefix check measures only
// visible runes — mirrors tui/render/conformance_test.go's helper of the same
// name.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// TestMessageRendersRolePrefixes pins each role's chrome. RoleUser/
// RoleAssistant were both a word label before Phase 3 Task 12 ("you"/
// theme.BrandName); this surface now marks them with theme.MsgGutter instead
// (see TestFullUsesGutterNotLabels for the negative half — that the label is
// gone). RoleError is untouched by that task, so it keeps its label check.
//
// user and assistant assert against the fully STYLED prefix (theme.LabelYou
// vs theme.Gutter around the same glyph), not merely the bare glyph's
// presence: the spec requires the gutter be "coloured per role", and a bare
// substring check on the shared glyph would still pass if the two roles'
// styles were swapped by mistake (review finding — a prior version of this
// test asserted plain theme.MsgGutter for both cases, which could not tell
// the two branches of full.go's contentPrefix apart).
//
// The color profile is forced to termenv.TrueColor for the duration of this
// test: go test's stdout is not a tty, so lipgloss's default profile
// detection strips all SGR codes, and EVERY style's Render degrades to the
// same plain glyph — a swapped LabelYou/Gutter would then produce identical
// "want" strings and pass unnoticed. This was caught only by deliberately
// swapping the two styles in full.go and observing this test stay green
// (see the task report's RED evidence) before this fix was added.
func TestMessageRendersRolePrefixes(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	p := New()
	cases := []struct {
		name string
		msg  render.Message
		want string
	}{
		{"user", render.Message{Role: render.RoleUser, Content: "hi"}, theme.LabelYou.Render(theme.MsgGutter) + " "},
		{"assistant", render.Message{Role: render.RoleAssistant, Content: "hi"}, theme.Gutter.Render(theme.MsgGutter) + " "},
		{"error", render.Message{Role: render.RoleError, Content: "boom"}, theme.Cross},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := p.Message(c.msg, render.RoleNone, 80)
			if !strings.Contains(got, c.want) {
				t.Errorf("Message(%v) = %q, want it to contain %q", c.msg, got, c.want)
			}
		})
	}
}

// TestFullUsesGutterNotLabels: the full-screen surface has room for a colored
// gutter, which identifies the speaker without spending a word on it.
func TestFullUsesGutterNotLabels(t *testing.T) {
	p := New()
	out := p.Message(render.Message{Role: render.RoleUser, Content: "hello"}, render.RoleNone, 80)
	if strings.Contains(out, "you") {
		t.Error("the full-screen presenter should not print a 'you' label")
	}
	if !strings.Contains(out, theme.MsgGutter) {
		t.Errorf("expected the gutter glyph %q in %q", theme.MsgGutter, out)
	}
}

// TestContinuationLinesCarryTheGutter pins the shape that replaced
// prefix-then-spaces-indent for RoleUser/RoleAssistant: unlike inline (which
// indents continuation lines to the label's width with blank spaces), this
// surface's gutter is a colour bar that must mark EVERY line of the block, or
// wrapped content past the first line would read as unattributed.
//
// Asserted with HasPrefix on the ANSI-stripped line, not Contains: a
// left-edge gutter must sit at the left edge. A bare Contains check would
// also pass a regression that pushed the gutter mid-line (review finding).
func TestContinuationLinesCarryTheGutter(t *testing.T) {
	p := New()
	out := p.Message(render.Message{
		Role:    render.RoleUser,
		Content: strings.Repeat("word ", 40),
	}, render.RoleNone, 30)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped output, got %d line(s)", len(lines))
	}
	if !strings.HasPrefix(stripANSI(lines[1]), theme.MsgGutter) {
		t.Errorf("continuation line does not lead with the gutter: %q", lines[1])
	}
}

func TestCapsAreFullScreenCaps(t *testing.T) {
	c := New().Caps()
	if !c.AltScreen {
		t.Error("the full-screen presenter must take the alt screen")
	}
	if !c.Mouse {
		t.Error("the full-screen presenter must enable mouse reporting")
	}
	if !c.OverlayDialogs {
		t.Error("the full-screen presenter must overlay dialogs from Frame, not rely on them being baked into body")
	}
}

// TestChipsStyledByState pins how the activity strip draws a chip: dim when
// idle, plain when something runs, in the accent color with a cursor when the
// strip's focus is on it, and an alert in the error color after the text.
func TestChipsStyledByState(t *testing.T) {
	p := New()
	const width = 60

	chip := func(row render.FooterRow) string {
		out := p.Footer(render.ViewState{Footers: []render.FooterRow{row}}, width)
		// The activity strip precedes the permanent combined status row.
		lines := strings.Split(out, "\n")
		if len(lines) != 2 || lines[1] != theme.ReadyMarker+" Ready" {
			t.Fatalf("strip/status rows: %q", out)
		}
		line := lines[0]
		return line
	}

	if got, want := chip(render.FooterRow{Text: "shell –"}), theme.Help.Render("shell –"); got != want {
		t.Errorf("idle chip = %q, want %q", got, want)
	}
	if got, want := chip(render.FooterRow{Text: "shell 1 running", State: render.ChipActive}), theme.Meta.Render("shell 1 running"); got != want {
		t.Errorf("active chip = %q, want %q", got, want)
	}
	if got, want := chip(render.FooterRow{Text: "todo 1/3", Selected: true}), theme.Brand.Render(theme.Cursor+" todo 1/3"); got != want {
		t.Errorf("selected chip = %q, want %q", got, want)
	}
	if got, want := chip(render.FooterRow{Text: "shell 3 done", Alert: "×1"}), theme.Help.Render("shell 3 done")+" "+theme.Error.Render("×1"); got != want {
		t.Errorf("chip with an alert = %q, want %q", got, want)
	}
}
