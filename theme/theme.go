// Package theme is the single source of truth for nib's visual language:
// a calm, warm-editorial palette of foreground-only inks (no background is
// ever set, so nib respects the user's terminal theme), typographic glyphs
// (no emoji), and the lipgloss styles built from them. Both the TUI (tui/)
// and the CLI (cmd/cli.go) render through these styles so the two modes look
// like one product.
package theme

import "github.com/charmbracelet/lipgloss"

// Inks — 256-color, foreground only. Body text uses the terminal default fg.
var (
	Accent = lipgloss.Color("173") // clay — brand, prompt, affordances
	Sage   = lipgloss.Color("108") // muted green — success / done
	Danger = lipgloss.Color("131") // muted brick — errors / denials
	Dim    = lipgloss.Color("245") // labels, rules, help
	Faint  = lipgloss.Color("240") // ghost hints, metadata
)

// Glyphs — typographic marks, no emoji.
const (
	Sep            = "·" // separator between label and message / list items
	PromptGlyph    = "›" // input prompt
	ApprovalGutter = "▏" // left rule on a tool-approval block
	SubAgent       = "↳" // sub-agent line marker
	Cross          = "×" // error marker
)

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
