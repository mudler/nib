// Package theme is the single source of truth for nib's visual language:
// a calm, warm-editorial palette of foreground-only inks (no background is
// ever set, so nib respects the user's terminal theme), typographic glyphs
// (no emoji), and the lipgloss styles built from them. Both the TUI (tui/)
// and the CLI (cmd/cli.go) render through these styles so the two modes look
// like one product.
package theme

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Inks — 256-color, foreground only. Body text uses the terminal default fg.
var (
	Accent = lipgloss.Color("173") // clay — brand, prompt, affordances
	Sage   = lipgloss.Color("108") // muted green — success / done
	Danger = lipgloss.Color("131") // muted brick — errors / denials
	Dim    = lipgloss.Color("245") // labels, rules, help
	Faint  = lipgloss.Color("240") // ghost hints, metadata
)

// Glyphs — typographic marks, no emoji. These are vars, not consts, because
// RestrictedGlyphs() swaps the non-Latin-1 marks for ASCII stand-ins at startup
// (see init below). Render through these names rather than hardcoding the rune
// so a single switch covers every call site.
var (
	Sep            = "·"  // separator between label and message / list items
	PromptGlyph    = "›"  // input prompt
	ApprovalGutter = "▏"  // left rule on a tool-approval block
	SubAgent       = "↳"  // sub-agent line marker
	Cross          = "×"  // error marker
	Arrow          = "→"  // tool-call / edit / mapping arrow
	Loop           = "↻"  // recurring-loop footer marker
	Goal           = "◎"  // active-goal footer marker
	ShellJob       = "▷"  // shell-jobs footer marker
	ScrollKeys     = "↑↓" // up/down navigation hint
	ReasoningGlyph = "✻"  // marks a block of model thinking/reasoning
)

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
// the env and call it again). Latin-1 marks (Sep ·, Cross ×) and box-drawing
// (─, used inline for rules) render on the VT console font and are left as-is.
func applyGlyphProfile() {
	if RestrictedGlyphs() {
		PromptGlyph, ApprovalGutter, SubAgent = ">", "|", ">"
		Arrow, ShellJob, ScrollKeys = "->", ">", "up/dn"
		Loop = "~"
		Goal = "*"
		ReasoningGlyph = "*"
		return
	}
	PromptGlyph, ApprovalGutter, SubAgent = "›", "▏", "↳"
	Arrow, ShellJob, ScrollKeys = "→", "▷", "↑↓"
	Loop = "↻"
	Goal = "◎"
	ReasoningGlyph = "✻"
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
)

// ReasoningHeader renders the labeled header that tags a block of model
// thinking, so it reads as a distinct channel from the assistant's answer:
// an accent glyph (✻ / * in restricted mode) and a dim, non-italic label.
// The body beneath is rendered with the Reasoning style by the caller.
func ReasoningHeader() string {
	return Gutter.Render(ReasoningGlyph) + " " + Help.Render("reasoning")
}
