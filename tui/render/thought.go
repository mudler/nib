package render

import (
	"strings"

	"github.com/mudler/nib/theme"
)

// Thought renders a folded reasoning trace in the transcript: one dim line
// with the reasoning glyph and summary, and when expanded the trace beneath
// it, drawn the way the live reasoning box draws it. It ends with the blank
// separator every transcript entry has. Both surfaces draw it the same way,
// the same as the live box (see Base.Reasoning).
func Thought(rendered, summary string, expanded bool, arriving float64, _ int) string {
	var b strings.Builder
	mark := theme.Folded
	if expanded {
		mark = theme.Unfolded
	}
	b.WriteString(theme.Fading(theme.Gutter, arriving).Render(theme.ReasoningGlyph) + " " + theme.Hint.Render(summary+" "+mark) + "\n")
	if expanded {
		for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
			b.WriteString("  " + theme.Subtle.Render(theme.BoxRule) + " " + line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}
