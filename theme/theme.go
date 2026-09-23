// Package theme is the single source of truth for nib's visual language:
// a calm, warm-editorial palette of foreground-only inks (no background is
// set, so nib respects the user's terminal theme — the one exception is the
// muted, light/dark-adaptive tint on diff lines, see DiffAddBg), typographic glyphs
// (no emoji), and the lipgloss styles built from them. Both the TUI (tui/)
// and the CLI (cmd/cli.go) render through these styles so the two modes look
// like one product.
package theme

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Inks — 256-color, foreground only. Body text uses the terminal default fg.
var (
	Accent = lipgloss.Color("173") // clay — brand, prompt, affordances
	Sage   = lipgloss.Color("108") // muted green — success / done
	Danger = lipgloss.Color("131") // muted brick — errors / denials
	Dim    = lipgloss.Color("245") // labels, rules, help
	Faint  = lipgloss.Color("240") // ghost hints, metadata
	Code   = lipgloss.Color("137") // inline code — warm tan, legible on light and dark
)

// Diff tints — the only backgrounds nib sets. A diff is read as a band of
// green and red rows, which a foreground-only colour cannot give, so added and
// removed lines get a muted tint picked per terminal background (light or
// dark) and per colour depth. On a 16-colour terminal the tint is dropped
// (ANSI "" is no colour): the basic ANSI greens and reds are too loud as
// backgrounds, and the coloured +/- sign still carries the meaning. The sign is
// also what keeps a diff readable without colour at all.
var (
	DiffAddBg = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#e3f4e6", ANSI256: "194"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#203a29", ANSI256: "22"},
	}
	DiffDelBg = lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: "#fbe6e6", ANSI256: "224"},
		Dark:  lipgloss.CompleteColor{TrueColor: "#422427", ANSI256: "52"},
	}
)

// Glyphs — typographic marks, no emoji. These are vars, not consts, because
// RestrictedGlyphs() swaps the non-Latin-1 marks for ASCII stand-ins at startup
// (see init below). Render through these names rather than hardcoding the rune
// so a single switch covers every call site.
var (
	Sep            = "·"  // separator between label and message / list items
	PromptGlyph    = "›"  // input prompt
	ApprovalGutter = "▏"  // left rule on a tool-approval block
	MsgGutter      = "▏"  // left rule marking a user/assistant message block (full surface)
	SubAgent       = "↳"  // sub-agent line marker
	Cross          = "×"  // error marker
	Arrow          = "→"  // tool-call / edit / mapping arrow
	Loop           = "↻"  // recurring-loop footer marker
	Goal           = "◎"  // active-goal footer marker
	Todo           = "◐"  // todo-list footer marker
	ShellJob       = "▷"  // shell-jobs footer marker
	ScrollKeys     = "↑↓" // up/down navigation hint
	ReasoningGlyph = "✻"  // marks a block of model thinking/reasoning
	NewOutputGlyph = "↓"  // footer marker: new content arrived while scrolled up
	HairlineGlyph  = "─"  // the one-cell rule repeated under the header
	BoxRule        = "│"  // vertical rule down the side of a collapsed trace box
	Check          = "✓"  // tool call succeeded
	DiffGap        = "⋯"  // elided unchanged lines between two diff hunks
	NoticeGlyph    = "∙"  // housekeeping notice (context pruned / compacted)
	StreamCursor   = "▍"  // end of a reply that is still streaming
	RunningDot     = "●"  // pulses on a tool block while its call runs

	// RadioOn/RadioOff mark a single-select ask_user option; CheckOn/CheckOff
	// mark a multi-select one. Cursor marks whichever row is highlighted,
	// regardless of selection mode. All four are geometric shapes, which paint
	// as blank cells on the Linux VT console — see applyGlyphProfile.
	RadioOn  = "◉"
	RadioOff = "○"
	CheckOn  = "◼"
	CheckOff = "◻"
	Cursor   = "▸"
	// Folded and Unfolded mark a folded thought line that ctrl+r expands.
	Folded   = "▸"
	Unfolded = "▾"
)

// spinnerFrames animates the working indicator. Braille cells read as a smooth
// rotation at the 80ms tick; the VT console cannot render them, so
// SpinnerFrames swaps in the classic ASCII barber pole there, at call time
// rather than in applyGlyphProfile like the other swappable glyphs. Every
// frame in a set is the same width so the status line never jitters.
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// SpinnerFrames returns the animation frames for the current terminal profile.
func SpinnerFrames() []string {
	if RestrictedGlyphs() {
		return []string{"-", "\\", "|", "/"}
	}
	return spinnerFrames
}

// RestrictedGlyphs reports whether glyphs must fall back to ASCII because the
// terminal can only render a fixed bitmap font with no arrows, geometric
// shapes, or eighth-block glyphs. The Linux VT console (TERM=linux) is the
// canonical case — there, the unmapped runes paint as blank cells. NIB_ASCII
// overrides the autodetection: "1"/"true"/"yes" forces the stand-ins on any
// terminal, "0"/"false"/"no" forces the full set.
func RestrictedGlyphs() bool {
	switch os.Getenv("NIB_ASCII") {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	return os.Getenv("TERM") == "linux"
}

func init() { applyGlyphProfile() }

// applyGlyphProfile (re)assigns the swappable glyphs for the current terminal.
// On restricted terminals the non-Latin-1 marks become ASCII stand-ins so they
// never paint as blank cells; otherwise the full typographic set is used. It
// sets both branches explicitly so it is idempotent and reversible (tests flip
// the env and call it again). Latin-1 marks (Sep ·, Cross ×) render on the VT
// console font and are left as-is.
func applyGlyphProfile() {
	if RestrictedGlyphs() {
		PromptGlyph, ApprovalGutter, SubAgent = ">", "|", ">"
		MsgGutter = "|"
		Arrow, ShellJob, ScrollKeys = "->", ">", "up/dn"
		Loop = "~"
		Goal = "*"
		Todo = "o"
		ReasoningGlyph = "*"
		NewOutputGlyph = "v"
		HairlineGlyph = "-"
		BoxRule = "|"
		Check = "ok"
		DiffGap = "..."
		NoticeGlyph = "-"
		StreamCursor = "_"
		RunningDot = "*"
		RadioOn, RadioOff = "(*)", "( )"
		CheckOn, CheckOff = "[x]", "[ ]"
		Cursor = ">"
		Folded, Unfolded = ">", "v"
		return
	}
	PromptGlyph, ApprovalGutter, SubAgent = "›", "▏", "↳"
	MsgGutter = "▏"
	Arrow, ShellJob, ScrollKeys = "→", "▷", "↑↓"
	Loop = "↻"
	Goal = "◎"
	Todo = "◐"
	ReasoningGlyph = "✻"
	NewOutputGlyph = "↓"
	HairlineGlyph = "─"
	BoxRule = "│"
	Check = "✓"
	DiffGap = "⋯"
	NoticeGlyph = "∙"
	StreamCursor = "▍"
	RunningDot = "●"
	RadioOn, RadioOff = "◉", "○"
	CheckOn, CheckOff = "◼", "◻"
	Cursor = "▸"
	Folded, Unfolded = "▸", "▾"
}

// Styles. Bold is reserved for the brand mark and the active approval keys.
var (
	Brand      = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	Rule       = lipgloss.NewStyle().Foreground(Dim)
	LabelYou   = lipgloss.NewStyle().Foreground(Dim)
	LabelNib   = lipgloss.NewStyle().Foreground(Accent)
	SepStyle   = lipgloss.NewStyle().Foreground(Faint)
	Prompt     = lipgloss.NewStyle().Foreground(Accent)
	Hint       = lipgloss.NewStyle().Foreground(Faint)
	Help       = lipgloss.NewStyle().Foreground(Dim)
	Meta       = lipgloss.NewStyle().Foreground(Faint)
	Reasoning  = lipgloss.NewStyle().Foreground(Dim).Italic(true)
	Subtle     = lipgloss.NewStyle().Foreground(Dim).Italic(true)
	Error      = lipgloss.NewStyle().Foreground(Danger)
	Gutter     = lipgloss.NewStyle().Foreground(Accent)
	ApproveKey = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	Running    = lipgloss.NewStyle().Foreground(Accent)
	Done       = lipgloss.NewStyle().Foreground(Sage)
	// Tool blocks: the verb (first word of the call summary) in the terminal's
	// own foreground so a run of calls can be scanned by what they did; the
	// rest of the summary and the output stay dim.
	ToolVerb   = lipgloss.NewStyle()
	ToolDetail = lipgloss.NewStyle().Foreground(Dim)
	ToolOutput = lipgloss.NewStyle().Foreground(Dim)
	ToolOK     = lipgloss.NewStyle().Foreground(Sage)
	ToolFailed = lipgloss.NewStyle().Foreground(Danger)
	// Diff rows. Changed lines keep the terminal foreground on their tint (the
	// text is what the user must read); context lines are dim; the sign takes
	// the line's colour.
	DiffAdd     = lipgloss.NewStyle().Background(DiffAddBg)
	DiffDel     = lipgloss.NewStyle().Background(DiffDelBg)
	DiffAddSign = lipgloss.NewStyle().Background(DiffAddBg).Foreground(Sage)
	DiffDelSign = lipgloss.NewStyle().Background(DiffDelBg).Foreground(Danger)
	DiffContext = lipgloss.NewStyle().Foreground(Dim)
	DiffLineNo  = lipgloss.NewStyle().Foreground(Faint)
	// Yolo flags the auto-approve-everything mode — bold brick so it reads as a
	// standing warning that the approval gate is off.
	Yolo = lipgloss.NewStyle().Bold(true).Foreground(Danger)
)

// ReasoningHeader renders the labeled header that tags a block of model
// thinking, so it reads as a distinct channel from the assistant's answer:
// an accent glyph (✻ / * in restricted mode) and a dim, non-italic label.
// The body beneath is rendered with the Reasoning style by the caller.
func ReasoningHeader() string {
	return Gutter.Render(ReasoningGlyph) + " " + Help.Render("reasoning")
}

// Hairline renders the dim horizontal rule that closes the header: the
// swappable HairlineGlyph (─ / - in restricted mode) repeated to width, in the
// Rule style. It lives here rather than in a presenter because both surfaces
// draw the same rule, and repeating the rune inline in each of them put a
// non-Latin-1 glyph outside RestrictedGlyphs()'s reach. A width below 1 still
// yields one cell, so the rule never renders as the empty string.
func Hairline(width int) string {
	if width < 1 {
		width = 1
	}
	return Rule.Render(strings.Repeat(HairlineGlyph, width))
}

// NewOutputMarker renders the dim footer marker shown when the user is
// scrolled up in the transcript and content has arrived below the fold — the
// swappable NewOutputGlyph (↓ / v in restricted mode) plus NewOutputText, both
// in the same dim Help style as the rest of the footer.
func NewOutputMarker() string {
	return Help.Render(NewOutputGlyph + " " + NewOutputText)
}
