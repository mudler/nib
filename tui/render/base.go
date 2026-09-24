package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// Base holds every Presenter method whose implementation genuinely does not
// vary between inline and full: ContentWidth, Reasoning, Dialog, Header,
// HeaderHeight, Footer and FooterHeight. Both presenters embed a Base and
// override only what actually differs — Caps, the role-prefix behaviour,
// Message, and Frame (stack vs overlay) — see each presenter's own doc for
// why those four resist sharing.
//
// This is Task 17's extraction: a Phase 2 ruling deferred it on the
// prediction that Phase 3 would diverge the chrome substantially. It
// diverged exactly one function (contentPrefix's user/assistant arms) and
// added one (gutterLines) — everything below was still byte-identical
// between the two files.
//
// Prefix is the one piece of the divergent chrome Base itself needs: the
// per-role prefix ContentWidth measures (inline's word label, full's
// one-column gutter). Message does NOT move here — the label-vs-gutter
// difference runs deeper than a single substitutable prefix string (inline
// also varies its prefix by the previous role and lays a block out with a
// first-line-only prefix, while full repeats its gutter down every line), so
// each presenter keeps its own Message rather than Base trying to
// parameterise all of that too. Go embedding does not give virtual dispatch —
// Base cannot call back into the embedding presenter's own contentPrefix by
// name — so the embedder passes its function in via this field instead
// (inline/full's New() sets it), rather than Base silently calling some
// default of its own and producing the wrong width for whichever surface
// didn't get looked up.
type Base struct {
	Prefix func(role Role) string
}

// ContentWidth reports how many cells are left for a role's content once this
// surface's chrome (b.Prefix) is accounted for. The result is clamped to at
// least 1: a terminal narrower than the chrome must still give a renderer a
// legal width rather than zero or a negative one.
//
// Panics if Prefix is nil — a Presenter that embeds Base without wiring it up
// (inline/full's New() both do) would otherwise fail with a bare nil-func
// dereference at the call site, naming neither the cause nor the culprit.
// Silently defaulting instead would render the wrong chrome for whichever
// surface forgot to set it, which is worse than a loud failure.
func (b Base) ContentWidth(role Role, w int) int {
	if b.Prefix == nil {
		panic("render: Base.Prefix not set")
	}
	cw := w - lipgloss.Width(b.Prefix(role))
	if cw < 1 {
		cw = 1
	}
	return cw
}

// Prefixed lays out a block as `prefix + first line`, with continuation
// lines indented to the prefix width. Shared by both presenters' Message for
// the roles whose layout doesn't vary (RoleAgent, RoleError, and inline's
// RoleUser/RoleAssistant) — full's RoleUser/RoleAssistant use GutterLines
// instead, since a gutter repeats down every line rather than heading the
// block once (see full's own doc for why).
func Prefixed(prefix, content string) string {
	pw := lipgloss.Width(prefix)
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, line := range lines {
		if i == 0 {
			b.WriteString(prefix)
		} else {
			b.WriteString(strings.Repeat(" ", pw))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// StyledLines applies style.Render to each line of content independently
// (rather than to the joined block), matching the original per-line styling
// the hand-rolled agent-message loop used to do.
func StyledLines(style lipgloss.Style, content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// Reasoning renders the working indicator (spinner + status verb) and, when
// loading, the collapsible reasoning trace beneath it. Renders nothing when
// !v.Loading. The trace itself is capped through CollapsibleBox: v.Reasoning
// .Collapsed/MaxLines are populated by the model in viewState() from
// Model.reasoningCollapsed and theme.ReasoningMaxLines. Collapsed, the box
// shows the TRAILING lines of the trace (never the leading ones) — see
// CollapsibleBox's doc for why: a head-anchored box freezes on the trace's
// opening words and reads as a hang, while tailing doubles the box as its own
// progress indicator.
func (Base) Reasoning(v ViewState, w int) string {
	if !v.Loading {
		return ""
	}
	var b strings.Builder
	b.WriteString(Loader(v.Spinner, v.Status))
	if v.Speed != "" {
		b.WriteString(" " + theme.SepStyle.Render(theme.Sep) + " " + v.Speed)
	}
	b.WriteString("\n")
	if v.Tip != "" {
		b.WriteString(theme.Hint.Render("  Tip: "+v.Tip) + "\n")
	}
	if strings.TrimSpace(v.Reasoning.Text) != "" {
		r := v.Reasoning
		box := CollapsibleBox{
			Lines:     strings.Split(strings.TrimRight(Wrap(r.Text, w-4), "\n"), "\n"),
			MaxLines:  r.MaxLines,
			Collapsed: r.Collapsed,
		}
		b.WriteString(theme.ReasoningHeader("", r.Elapsed) + "\n")
		for _, line := range box.Visible() {
			b.WriteString("  " + theme.Subtle.Render(theme.BoxRule) + " " + theme.Reasoning.Render(line) + "\n")
		}
		// Default: expanded with nothing hidden — offer to collapse it back.
		// Collapsed with something hidden — offer to expand and say how much
		// is behind the fold. Collapsed with nothing hidden (a short trace
		// that never grew past MaxLines) — no hint at all: there is nothing
		// either affordance would change.
		hint := theme.ReasoningCollapse
		if n := box.Hidden(); n > 0 {
			hint = "… " + strconv.Itoa(n) + theme.ReasoningMore + theme.ReasoningExpand
		} else if r.Collapsed {
			hint = ""
		}
		if hint != "" {
			b.WriteString("  " + theme.Hint.Render(hint) + "\n")
		}
	}
	return b.String()
}

// optionStyle picks the choice-menu style for one DialogOption: the bold
// actionable-key style when Emphasis is set, the dim hint style otherwise.
func optionStyle(o DialogOption) lipgloss.Style {
	if o.Emphasis {
		return theme.ApproveKey
	}
	return theme.Help
}

// dialogAskMarker picks the leading marker for one ask_user option row: a
// checkbox when the dialog is multi-select (d.Checked non-nil), a radio
// otherwise, filled when checked/selected and hollow when not. i indexes
// d.Options; safe even if d.Checked is shorter (defensive, since a Presenter
// never constructs these values itself).
func dialogAskMarker(d Dialog, i int) string {
	if d.Checked != nil {
		if i < len(d.Checked) && d.Checked[i] {
			return theme.CheckOn
		}
		return theme.CheckOff
	}
	if i == d.Selected {
		return theme.RadioOn
	}
	return theme.RadioOff
}

// Dialog renders a modal prompt. DialogAsk lays out the question (Title),
// then one row per option — a cursor mark on the highlighted row, a radio or
// checkbox per dialogAskMarker, the row text in theme.ApproveKey when
// highlighted and theme.Help otherwise (mirroring DialogApproval's Emphasis
// styling) — and Hint beneath, wrapped. An ask with no options (free-text
// only) degrades to placing Title alone. DialogResume (the /resume picker)
// shares this exact branch: buildResumeDialog (tui/resume.go) produces the
// same Title + single-select Options + Hint shape buildAskDialog does, so
// there is nothing for a Presenter to render differently. DialogApproval
// lays out Rows (the argument card, or — when RowsUnstructured — a single
// wrapped prose block), Hint (the captured reasoning, wrapped) and Options
// (the choice menu, one line per option styled per its Emphasis).
func (Base) Dialog(d Dialog, w int) string {
	switch d.Kind {
	case DialogAsk, DialogResume:
		gutter := theme.Gutter.Render(theme.ApprovalGutter) + " "
		var b strings.Builder
		b.WriteString(gutter + theme.LabelNib.Render(d.Title))
		b.WriteString("\n")
		for i, opt := range d.Options {
			cursor := "  "
			style := theme.Help
			if i == d.Selected {
				cursor = theme.Cursor + " "
				style = theme.ApproveKey
			}
			b.WriteString(gutter + cursor + dialogAskMarker(d, i) + " " + style.Render(opt.Text))
			b.WriteString("\n")
		}
		if d.Hint != "" {
			wrapped := Wrap(d.Hint, w-4)
			for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				b.WriteString(gutter + theme.Hint.Render(line) + "\n")
			}
		}
		return b.String()

	case DialogApproval:
		gutter := theme.Gutter.Render(theme.ApprovalGutter) + " "
		var b strings.Builder
		b.WriteString(gutter + theme.ApproveKey.Render(d.Title))
		if d.Meta != "" {
			b.WriteString("  " + theme.Meta.Render(d.Meta))
		}
		b.WriteString("\n")

		if d.RowsUnstructured {
			// Fallback: unstructured args, wrapped and dimmed, no key column.
			if len(d.Rows) > 0 {
				wrapped := Wrap(d.Rows[0][1], w-4)
				for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
					b.WriteString(gutter + theme.Help.Render(line) + "\n")
				}
			}
		} else if len(d.Rows) > 0 {
			maxKey := 0
			for _, row := range d.Rows {
				if kw := lipgloss.Width(row[0]); kw > maxKey {
					maxKey = kw
				}
			}
			for _, row := range d.Rows {
				key := row[0] + strings.Repeat(" ", max(0, maxKey-lipgloss.Width(row[0])))
				val := TruncateLine(row[1], w-8-maxKey)
				b.WriteString(gutter + "  " + theme.Meta.Render(key) + "  " + theme.Help.Render(val) + "\n")
			}
		}

		if d.Diff != nil && len(d.Diff.Lines) > 0 {
			for _, row := range DiffRows(*d.Diff, w-lipgloss.Width(gutter), ApprovalDiffRows) {
				b.WriteString(gutter + row + "\n")
			}
		}

		if d.Hint != "" {
			wrapped := Wrap(d.Hint, w-4)
			for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				b.WriteString(gutter + theme.Reasoning.Render(line) + "\n")
			}
		}

		// The leading blank gutter line separates the choice menu from the
		// card/hint above it. It is keyed on being a genuine multi-option
		// menu, not on a specific option count — a single option is the
		// free-form edit-mode hint (no menu to separate from anything), and
		// any other count (today: 5 — once / always / this turn / this
		// session / deny-edit; tomorrow: possibly more) is a real menu and
		// gets the same treatment. Switching on an exact count here was the
		// original bug: adding a fifth option silently fell through to an
		// "unexpected count" branch that dropped the blank line, and would
		// silently do so again for a sixth.
		if len(d.Options) > 1 {
			b.WriteString(gutter + "\n")
		}
		for _, opt := range d.Options {
			b.WriteString(gutter + optionStyle(opt).Render(opt.Text) + "\n")
		}
		return b.String()

	case DialogModelPicker:
		gutter := theme.Gutter.Render(theme.ApprovalGutter) + " "
		var b strings.Builder
		b.WriteString(gutter + theme.LabelNib.Render(d.Title))
		b.WriteString("\n")
		for i, opt := range d.Options {
			cursor := "  "
			style := theme.Help
			if i == d.Selected {
				cursor = theme.Cursor + " "
				style = theme.ApproveKey
			}
			b.WriteString(gutter + cursor + style.Render(opt.Text))
			b.WriteString("\n")
		}
		if d.Hint != "" {
			wrapped := Wrap(d.Hint, w-4)
			for _, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				b.WriteString(gutter + theme.Hint.Render(line) + "\n")
			}
		}
		return b.String()
	}
	return ""
}

// Header renders the brand/badge line with responsive stat segments and the
// rule beneath it. Stats drop by ascending priority on narrow terminals:
// cwd(30) < skills(40) < mcp(50) < tools(60) < model(90); the model
// segment reads "provider · model" and sheds the provider before the model.
func (Base) Header(v ViewState) string {
	var b strings.Builder

	// Left side: brand + yolo badge.
	left := theme.Brand.Render(v.Brand)
	if v.AutoApprove {
		left += "  " + theme.Yolo.Render(theme.YoloBadge)
	} else if m := v.ApprovalMode.OrDefault(); m != types.ApprovalPrompt {
		// strict, allowlist and classify change what prompts; say so.
		left += "  " + theme.Meta.Render(string(m))
	}

	// Middle: stat segments, each with a separator dot. Built left-to-right
	// but dropped right-to-left (lowest priority first) when space is tight.
	// fallback, when set, is a shorter text tried when text does not fit.
	type seg struct {
		text     string
		priority int
		fallback string
	}
	sepDot := theme.SepStyle.Render(theme.Sep)
	var segs []seg
	if v.HeaderStats.Model != "" {
		model := theme.Meta.Render(v.HeaderStats.Model)
		s := seg{text: model, priority: 90}
		if v.HeaderStats.Provider != "" {
			// "provider · model": the provider is dimmer than the model it
			// qualifies, and is what gives way first when space is tight.
			s.text = theme.Help.Render(v.HeaderStats.Provider) + " " + sepDot + " " + model
			s.fallback = model
		}
		segs = append(segs, s)
	}
	if v.HeaderStats.Tools > 0 {
		segs = append(segs, seg{text: fmt.Sprintf("%s %s", theme.Meta.Render(strconv.Itoa(v.HeaderStats.Tools)), theme.Help.Render("tools")), priority: 60})
	}
	if v.HeaderStats.MCP > 0 {
		segs = append(segs, seg{text: fmt.Sprintf("%s %s", theme.Meta.Render(strconv.Itoa(v.HeaderStats.MCP)), theme.Help.Render("mcp")), priority: 50})
	}
	if v.HeaderStats.Skills > 0 {
		segs = append(segs, seg{text: fmt.Sprintf("%s %s", theme.Meta.Render(strconv.Itoa(v.HeaderStats.Skills)), theme.Help.Render("skills")), priority: 40})
	}

	// Right side: cwd, cut from the left when it would take more than two
	// fifths of the line — the trailing directories are the ones that say
	// where you are.
	cwd := theme.Meta.Render(truncateLeft(v.Cwd, max(v.Width*2/5, 12)))

	// Fit segments between left and cwd, dropping lowest-priority first.
	availWidth := v.Width - lipgloss.Width(left) - lipgloss.Width(cwd) - 2 // 2 for gaps

	// Sort segments by priority descending so we keep the highest.
	// (Already in priority order from append above.)

	// Greedily include segments until they don't fit.
	var included []seg
	usedWidth := 0
	for _, s := range segs {
		w := lipgloss.Width(s.text) + 3 // text + sep + spaces
		if usedWidth+w > availWidth && s.fallback != "" {
			s.text = s.fallback
			w = lipgloss.Width(s.text) + 3
		}
		if usedWidth+w <= availWidth {
			included = append(included, s)
			usedWidth += w
		}
	}

	// Build the middle string from included segments.
	var mid string
	for i, s := range included {
		if i > 0 {
			mid += sepDot + " "
		}
		mid += s.text + " "
	}

	// Compose: left + [mid +] cwd, right-aligned.
	// When mid is empty, the header is just left + gap + cwd (same as before
	// the stats were added, so golden tests with zero-value HeaderStats don't drift).
	var line string
	midTrim := strings.TrimRight(mid, " ")
	if midTrim != "" {
		gap := v.Width - lipgloss.Width(left) - lipgloss.Width(midTrim) - lipgloss.Width(cwd) - 1
		if gap < 1 {
			gap = 1
		}
		line = left + " " + midTrim + strings.Repeat(" ", gap) + cwd
	} else {
		gap := v.Width - lipgloss.Width(left) - lipgloss.Width(cwd)
		if gap < 1 {
			gap = 1
		}
		line = left + strings.Repeat(" ", gap) + cwd
	}
	b.WriteString(line)
	b.WriteString("\n")
	b.WriteString(theme.Hairline(v.Width))
	b.WriteString("\n")
	return b.String()
}

// HeaderHeight reports how many terminal rows Header occupies above the body
// — two today (the brand/cwd line and the hairline beneath it). Measured from
// the real string rather than stated as a constant, so a header redesign
// re-budgets the layout instead of silently pushing the frame past the
// terminal's last row. See BlockRows for why this counts newlines rather than
// using lipgloss.Height: Frame writes body straight onto the header.
func (b Base) HeaderHeight(v ViewState) int {
	return BlockRows(b.Header(v))
}

// footerRowStyle renders one FooterRow's text (glyph already prefixed), per
// its Kind. FooterJobs/FooterShell reproduce the original theme.Meta +
// width-fill treatment (Width doesn't just pad — lipgloss wraps content
// exceeding w, so a narrow terminal hard-wraps instead of spilling, exactly
// as before); everything else — FooterLoops/FooterGoal, and the zero-value
// FooterKindUnset (a forgotten Kind on some future fifth row) — gets the
// plain default: theme.Subtle, unfilled.
func footerRowStyle(kind FooterRowKind, text string, w int) string {
	switch kind {
	case FooterJobs, FooterShell:
		return theme.Meta.Width(w).Render(text)
	default:
		return theme.Subtle.Render(text)
	}
}

// Footer renders the new-output marker (when scrolled up with unread content
// below the fold), the help/badges line, the error line, and the job-status
// footer rows — everything that lives between the composer and the bottom of
// the screen.
func (Base) Footer(v ViewState, w int) string {
	var b strings.Builder
	if v.NewOutput {
		b.WriteString(theme.NewOutputMarker())
		b.WriteString("\n")
	}
	if v.Badges != "" {
		gap := w - lipgloss.Width(v.Help) - lipgloss.Width(v.Badges)
		if gap < 1 {
			gap = 1
		}
		b.WriteString(v.Help + strings.Repeat(" ", gap) + v.Badges)
	} else {
		b.WriteString(v.Help)
	}
	if v.Err != "" {
		b.WriteString("\n" + theme.Error.Render(theme.Cross+" "+v.Err))
	}
	for _, row := range v.Footers {
		text := row.Text
		if row.Glyph != "" {
			text = row.Glyph + " " + text
		}
		b.WriteString("\n" + footerRowStyle(row.Kind, text, w))
	}
	return b.String()
}

// FooterHeight reports how many terminal rows Footer occupies for this
// ViewState at this width — 1 for the bare help line, up to 7 once the
// new-output marker, an error line and the four job-status rows are all
// present. The shared core budgets the viewport against it, so an answer that
// disagrees with Footer by even one row makes the composed frame overflow the
// screen. Measuring the real output is the only way the two cannot drift.
func (b Base) FooterHeight(v ViewState, w int) int {
	return lipgloss.Height(b.Footer(v, w))
}

// truncateLeft shortens s to at most w cells by dropping leading runes and
// marking the cut with a leading ellipsis.
func truncateLeft(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}
