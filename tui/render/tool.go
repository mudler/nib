package render

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/internal/textdiff"
	"github.com/mudler/nib/theme"
)

// ToolStatus is the outcome a tool block's header marks.
type ToolStatus int

const (
	ToolStatusUnknown ToolStatus = iota // no outcome to show (a plain separator)
	ToolStatusOK
	ToolStatusFailed
)

// toolIndent is how far a tool block's body sits beneath its header.
const toolIndent = "  "

// ToolFoldLines is how many lines of a finished tool's output show while its
// block is folded; RunningTailLines is how many trailing lines of a running
// call's output show while its block is folded.
const (
	ToolFoldLines    = 12
	RunningTailLines = 5
)

// TranscriptDiffRows caps a diff shown in the transcript; ApprovalDiffRows caps
// one in the approval prompt, where the user decides on it and so gets more.
const (
	TranscriptDiffRows = 16
	ApprovalDiffRows   = 30
)

// ToolBlock renders a RoleTool message: a header line (status mark, the call
// summary with its verb emphasized, and dim metadata), then the body beneath
// it at a two-cell indent — the diff when the message carries one, otherwise
// the text output. It is the same on both surfaces, so it lives here rather
// than in each presenter.
//
// A block that is still arriving (m.Arriving) fades its inks in: the mark,
// the dim detail and meta, and the dim output. The verb and a failure's
// output use the terminal's own foreground, which has no ink to fade from,
// and a diff keeps its tints, which carry its meaning.
func ToolBlock(m Message, w int) string {
	var b strings.Builder
	b.WriteString(toolHeader(m.Label, m.Meta, m.AgentID, m.Status, m.Arriving, w))
	b.WriteString("\n")
	bodyW := w - lipgloss.Width(toolIndent)
	if m.Diff != nil && len(m.Diff.Lines) > 0 {
		for _, row := range DiffRows(*m.Diff, bodyW, TranscriptDiffRows) {
			b.WriteString(toolIndent + row + "\n")
		}
		return b.String()
	}
	if strings.TrimSpace(m.Content) == "" {
		return b.String()
	}
	style := theme.Fading(theme.ToolOutput, m.Arriving)
	if m.Status == ToolStatusFailed {
		// A failure's output is what the user needs to read next; do not dim it.
		style = lipgloss.NewStyle()
	}
	lines := strings.Split(m.Content, "\n")
	hidden := 0
	if !m.Expanded && len(lines) > ToolFoldLines {
		hidden = len(lines) - ToolFoldLines
		lines = lines[:ToolFoldLines]
	}
	writeToolLines(&b, lines, style, bodyW)
	switch {
	case hidden > 0:
		b.WriteString(toolIndent + theme.Hint.Render(fmt.Sprintf(theme.ToolMore, hidden)+theme.ReasoningExpand) + "\n")
	case m.Expanded && len(lines) > ToolFoldLines:
		b.WriteString(toolIndent + theme.Hint.Render(theme.ReasoningCollapse) + "\n")
	}
	return b.String()
}

// writeToolLines writes output lines beneath a tool header: each wrapped to w,
// indented and styled.
func writeToolLines(b *strings.Builder, lines []string, style lipgloss.Style, w int) {
	wrapped := Wrap(strings.Join(lines, "\n"), w)
	for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
		b.WriteString(toolIndent + style.Render(line) + "\n")
	}
}

// RunningTool is a root-agent tool call that has started and not finished.
type RunningTool struct {
	Label   string        // the call summary, as Message.Label
	Elapsed time.Duration // time since the call started
	// Hint is a dim key hint after the elapsed time ("ctrl+b background"),
	// or "" for none.
	Hint string
	// Output is what the call has printed so far; only a shell command has
	// any before it ends.
	Output string
	// Expanded shows all of Output; folded, only its last RunningTailLines.
	Expanded bool
}

// RunningToolBlock renders a running call: a header whose mark pulses, with
// the elapsed time once it reaches a second, then the output so far. Folded,
// it tails the output, with the fold row above the tail so the newest line
// stays last and the block reads as live progress.
func RunningToolBlock(r RunningTool, w int) string {
	var meta []string
	if r.Elapsed >= time.Second {
		meta = append(meta, theme.Elapsed(r.Elapsed))
	}
	if r.Hint != "" {
		meta = append(meta, r.Hint)
	}
	var b strings.Builder
	b.WriteString(toolHeaderMarked(theme.RunningDotAt(r.Elapsed), r.Label, strings.Join(meta, " "+theme.Sep+" "), "", 0, w))
	b.WriteString("\n")
	out := strings.TrimRight(r.Output, "\n")
	if strings.TrimSpace(out) == "" {
		return b.String()
	}
	lines := strings.Split(out, "\n")
	if !r.Expanded && len(lines) > RunningTailLines {
		hidden := len(lines) - RunningTailLines
		lines = lines[hidden:]
		b.WriteString(toolIndent + theme.Hint.Render(fmt.Sprintf(theme.ToolMore, hidden)+theme.ReasoningExpand) + "\n")
	}
	writeToolLines(&b, lines, theme.ToolOutput, w-lipgloss.Width(toolIndent))
	return b.String()
}

// ToolHeader renders a tool block's one-line header, truncated to w: the
// status mark, the sub-agent tag when agentID is set, the summary label and
// the dim meta. The label's first word is the verb ("edit", "read", "grep")
// and is shown at full strength; the rest is dim. A shell command ("$ …")
// instead dims the "$" and shows the command itself at full strength, since
// the command is what the user scans for.
func ToolHeader(label, meta, agentID string, status ToolStatus, w int) string {
	return toolHeader(label, meta, agentID, status, 0, w)
}

// toolHeader is ToolHeader for a block that is arriving (see ToolBlock).
func toolHeader(label, meta, agentID string, status ToolStatus, arriving float64, w int) string {
	fade := func(s lipgloss.Style) lipgloss.Style { return theme.Fading(s, arriving) }
	var mark string
	switch status {
	case ToolStatusOK:
		mark = fade(theme.ToolOK).Render(theme.Check)
	case ToolStatusFailed:
		mark = fade(theme.ToolFailed).Render(theme.Cross)
	default:
		mark = fade(theme.Subtle).Render(theme.Sep)
	}
	return toolHeaderMarked(mark, label, meta, agentID, arriving, w)
}

// toolHeaderMarked is toolHeader with the mark already rendered.
func toolHeaderMarked(mark, label, meta, agentID string, arriving float64, w int) string {
	fade := func(s lipgloss.Style) lipgloss.Style { return theme.Fading(s, arriving) }
	head := mark + " "
	if agentID != "" {
		head += fade(theme.Subtle).Render(theme.SubAgent+" "+ShortID(agentID)) + theme.SepStyle.Render(" "+theme.Sep+" ")
	}
	room := w - lipgloss.Width(head)
	if meta != "" {
		room -= lipgloss.Width(meta) + 2
	}
	label = TruncateRunes(label, max(room, 1))

	switch {
	case strings.HasPrefix(label, "$ "):
		head += fade(theme.ToolDetail).Render("$") + " " + theme.ToolVerb.Render(label[2:])
	default:
		verb, rest, _ := strings.Cut(label, " ")
		head += theme.ToolVerb.Render(verb)
		if rest != "" {
			head += " " + fade(theme.ToolDetail).Render(rest)
		}
	}
	if meta != "" {
		head += "  " + fade(theme.Meta).Render(meta)
	}
	return head
}

// DiffStat summarizes a diff as "+N -M" (ASCII, as git prints it), leaving out a side with no lines.
func DiffStat(d textdiff.Diff) string {
	var parts []string
	if d.Added > 0 {
		parts = append(parts, "+"+strconv.Itoa(d.Added))
	}
	if d.Removed > 0 {
		parts = append(parts, "-"+strconv.Itoa(d.Removed))
	}
	return strings.Join(parts, " ")
}

// DiffRows renders a diff as styled rows exactly w cells wide: a dim line
// number gutter (left out for a fragment diff, which has no numbers), then a
// sign column and the line text. Added and removed rows are tinted across the
// full width so the change reads as a band; context rows are dim; a Gap row is
// a dim ellipsis. Tabs expand to four spaces so the tint has no holes, and a
// line too long for w is cut with an ellipsis rather than wrapped, which keeps
// one row per line. At most maxRows rows are shown (maxRows <= 0 shows all);
// past that a fold line says how many were left out.
func DiffRows(d textdiff.Diff, w, maxRows int) []string {
	numW := 0
	for _, l := range d.Lines {
		if n := max(l.OldNo, l.NewNo); n > 0 {
			numW = max(numW, len(strconv.Itoa(n)))
		}
	}
	gutter := 0
	if numW > 0 {
		gutter = numW + 1
	}
	textW := max(w-gutter-2, 1) // sign + space

	lines := d.Lines
	hidden := 0
	if maxRows > 0 && len(lines) > maxRows {
		hidden = len(lines) - (maxRows - 1)
		lines = lines[:maxRows-1]
	}
	rows := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		num := ""
		if numW > 0 {
			n := l.NewNo
			if l.Kind == textdiff.Del {
				n = l.OldNo
			}
			s := ""
			if n > 0 {
				s = strconv.Itoa(n)
			}
			num = theme.DiffLineNo.Render(fmt.Sprintf("%*s", numW, s)) + " "
		}
		if l.Kind == textdiff.Gap {
			rows = append(rows, strings.Repeat(" ", gutter)+theme.DiffLineNo.Render(theme.DiffGap))
			continue
		}
		text := TruncateRunes(strings.ReplaceAll(l.Text, "\t", "    "), textW)
		pad := strings.Repeat(" ", max(textW-lipgloss.Width(text), 0))
		switch l.Kind {
		case textdiff.Add:
			rows = append(rows, num+theme.DiffAddSign.Render("+ ")+theme.DiffAdd.Render(text+pad))
		case textdiff.Del:
			rows = append(rows, num+theme.DiffDelSign.Render("- ")+theme.DiffDel.Render(text+pad))
		default:
			rows = append(rows, num+"  "+theme.DiffContext.Render(text))
		}
	}
	if hidden > 0 {
		rows = append(rows, strings.Repeat(" ", gutter)+theme.Hint.Render(fmt.Sprintf(theme.DiffMore, hidden)))
	}
	return rows
}
