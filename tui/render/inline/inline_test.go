package inline

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR escape sequences so column measurements count only
// visible runes.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

func TestMessageRendersRolePrefixes(t *testing.T) {
	p := New()
	cases := []struct {
		name string
		msg  render.Message
		want string
	}{
		{"user", render.Message{Role: render.RoleUser, Content: "hi"}, "you"},
		{"assistant", render.Message{Role: render.RoleAssistant, Content: "hi"}, theme.BrandName},
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

func TestInlineFooterHistoryAlertFitsNarrowWidths(t *testing.T) {
	p := New()
	v := render.ViewState{Help: "send", HelpRight: "ctrl+g details", Footers: []render.FooterRow{
		{Glyph: "界", Text: "long live agent", State: render.ChipActive},
		{Text: "History 12", Alert: "×3", Selected: true},
	}}
	for w := 1; w < 100; w++ {
		out := p.Footer(v, w)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		activity := stripANSI(lines[len(lines)-2])
		if got := lipgloss.Width(activity); got > w {
			t.Fatalf("width %d rendered %d-cell activity: %q", w, got, activity)
		}
		if w >= 2 && !strings.Contains(activity, "×3") {
			t.Fatalf("width %d cut displayable alert: %q", w, activity)
		}
	}
}

func TestContinuationLinesAreIndentedToPrefixWidth(t *testing.T) {
	p := New()
	out := p.Message(render.Message{
		Role:    render.RoleUser,
		Content: strings.Repeat("word ", 40),
	}, render.RoleNone, 30)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped output, got %d line(s)", len(lines))
	}
	if !strings.HasPrefix(lines[1], "      ") {
		t.Errorf("continuation line not indented to the prefix width: %q", lines[1])
	}
}

// TestInlineOmitsPrefixOnConsecutiveSameRole: repeating "you ·" down a run of
// messages costs six columns on every line and tells the reader nothing new.
func TestInlineOmitsPrefixOnConsecutiveSameRole(t *testing.T) {
	p := New()
	msg := render.Message{Role: render.RoleUser, Content: "second message"}

	first := p.Message(msg, render.RoleNone, 80)
	second := p.Message(msg, render.RoleUser, 80)

	if !strings.Contains(first, "you") {
		t.Error("the first message of a run must carry its label")
	}
	if strings.Contains(second, "you") {
		t.Error("a consecutive same-role message must not repeat the label")
	}
	if !strings.Contains(second, "second message") {
		t.Error("content lost when the prefix was omitted")
	}
}

// TestInlineConsecutiveRunStaysAligned pins the width half of the same rule:
// dropping the label must not shift the content left. Both renders share one
// ContentWidth (which cannot see prev — see the type's doc), so the spaces
// standing in for the label on a consecutive message must be the exact same
// width as the label itself, or a run's content would drift out of column
// the moment it stopped being the first message.
//
// Covers both RoleUser and RoleAssistant: messagePrefix is one shared
// function branching on role, but a table missing either role would leave
// that role's own width-agreement unpinned. This matters more than usual for
// RoleUser specifically — a live multi-turn session could reproduce a
// genuine consecutive-ASSISTANT run (via mid-run message injection) but not a
// genuine consecutive-USER one in the time available (see the report), so
// this case is the only place that path is exercised at all, live or
// otherwise. (A prior version of this test only covered RoleAssistant —
// review finding.)
func TestInlineConsecutiveRunStaysAligned(t *testing.T) {
	p := New()
	cases := []struct {
		name  string
		role  render.Role
		token string
	}{
		{"user", render.RoleUser, "aligned"},
		{"assistant", render.RoleAssistant, "aligned"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := render.Message{Role: c.role, Content: c.token}

			labeled := p.Message(msg, render.RoleNone, 80)
			continued := p.Message(msg, c.role, 80)

			// The column is the RENDERED width up to the token, not a byte
			// offset: the separator (·) is multi-byte, so strings.Index would
			// overstate the column by counting its extra byte.
			col := func(out string) int {
				idx := strings.Index(stripANSI(out), c.token)
				if idx < 0 {
					return -1
				}
				return lipgloss.Width(stripANSI(out)[:idx])
			}
			if labeled == "" || continued == "" {
				t.Fatalf("expected non-empty output, got %q and %q", labeled, continued)
			}
			if got, want := col(continued), col(labeled); got != want {
				t.Errorf("consecutive message content starts at column %d, want %d (same as the labeled run start): %q vs %q", got, want, continued, labeled)
			}
		})
	}
}

func TestCapsAreInlineWidgetCaps(t *testing.T) {
	c := New().Caps()
	if c.AltScreen {
		t.Error("the inline widget must not take the alt screen")
	}
	if c.Mouse {
		t.Error("the inline widget does not enable mouse reporting")
	}
	if c.OverlayDialogs {
		t.Error("the inline widget stacks dialogs into the scrollback, it does not overlay them")
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
		// The summary precedes the strip; the empty help line follows it.
		lines := strings.Split(out, "\n")
		if len(lines) != 3 || lines[0] != theme.ReadyMarker+" Ready" {
			t.Fatalf("summary/strip/help rows: %q", out)
		}
		line := lines[1]
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

// An over-long strip stays on one line: the widest label is shortened, and
// the counts in the other chips and the alerts are kept.
func TestChipsFitOneLine(t *testing.T) {
	p := New()
	const width = 40
	out := p.Footer(render.ViewState{Footers: []render.FooterRow{
		{Glyph: "o", Text: "todo 2/8 Review the existing repository state and clone it"},
		{Glyph: ">", Text: "shell 3 done", Alert: "×1"},
	}}, width)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 || lines[0] != theme.ReadyMarker+" Ready" {
		t.Fatalf("summary/strip/help rows: %q", out)
	}
	line := lines[1]
	if w := lipgloss.Width(line); w > width {
		t.Fatalf("strip is %d cells wide, want at most %d: %q", w, width, line)
	}
	for _, want := range []string{"todo 2/8", "shell 3 done", "×1", "…"} {
		if !strings.Contains(line, want) {
			t.Errorf("strip %q lost %q", line, want)
		}
	}
}
