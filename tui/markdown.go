package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/mudler/nib/theme"
)

// nibMarkdownRenderer builds a glamour renderer styled to match nib's
// foreground-only, no-emoji aesthetic, word-wrapped at width columns.
func nibMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	if width < 1 {
		width = 1
	}
	return glamour.NewTermRenderer(
		glamour.WithStyles(nibMarkdownStyle()),
		glamour.WithWordWrap(width),
	)
}

// renderMarkdownWith renders content with r, trimming surrounding blank lines.
// If r is nil or rendering fails it falls back to plain wrapText so output is
// always shown.
func renderMarkdownWith(r *glamour.TermRenderer, content string, width int) string {
	if r == nil {
		return wrapText(content, width)
	}
	out, err := r.Render(content)
	if err != nil {
		return wrapText(content, width)
	}
	return strings.Trim(out, "\n")
}

// nibMarkdownStyle is a minimal glamour style: no background fills, no document
// margin (the TUI owns indentation), theme inks for headings/code/links.
func nibMarkdownStyle() ansi.StyleConfig {
	accent := string(theme.Accent)
	dim := string(theme.Dim)
	faint := string(theme.Faint)
	zero := uint(0)
	yes := true

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
			Margin:         &zero,
		},
		Paragraph: ansi.StyleBlock{},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:  &dim,
				Italic: &yes,
				Prefix: theme.ApprovalGutter + " ",
			},
		},
		List: ansi.StyleList{
			StyleBlock:  ansi.StyleBlock{},
			LevelIndent: 2,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: &accent, Bold: &yes},
		},
		Text:        ansi.StylePrimitive{},
		Strong:      ansi.StylePrimitive{Bold: &yes},
		Emph:        ansi.StylePrimitive{Italic: &yes},
		Item:        ansi.StylePrimitive{},
		Enumeration: ansi.StylePrimitive{},
		Link:        ansi.StylePrimitive{Color: &accent, Underline: &yes},
		LinkText:    ansi.StylePrimitive{Color: &accent},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: &faint},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{Color: &dim},
				Margin:         &zero,
			},
		},
		HorizontalRule: ansi.StylePrimitive{Color: &faint, Format: "\n──────\n"},
	}
}
