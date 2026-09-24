// Package full implements render.Presenter for nib's full-screen surface: the
// alt-screen mode with mouse reporting enabled. It started as a byte-for-byte
// mirror of tui/render/inline, differing only in Caps() — landing the
// alt-screen wiring (tea.WithAltScreen/WithMouseCellMotion, suppressing the
// inline widget's region-clearing escapes) in isolation from the visual
// redesign this surface's chrome gets in Phase 3. Task 12 is the first cut of
// that redesign: RoleUser/RoleAssistant now get a coloured gutter instead of
// inline's word label (see contentPrefix and gutterLines); everything else —
// Reasoning, Dialog, Header, Footer, and the RoleAgent/RoleTool/RoleError
// chrome — still mirrors inline exactly. Task 17 gave that shared remainder
// one implementation (render.Base, which both presenters embed) instead of
// two copies kept in sync by hand; the conformance suite
// (tui/render/conformance_test.go) is what proves the divergence stops at
// what's still overridden here.
package full

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// presenter is the full-screen Presenter. It embeds render.Base for every
// method whose implementation doesn't vary by surface (ContentWidth,
// Reasoning, Dialog, Header/HeaderHeight, Footer/FooterHeight) and overrides
// only Caps, Message and Frame — the pieces that genuinely differ from inline
// (Task 17). It holds no other state: every method is a pure function of its
// arguments.
type presenter struct {
	render.Base
}

// New returns the full-screen Presenter.
func New() render.Presenter {
	return presenter{Base: render.Base{Prefix: contentPrefix}}
}

func (presenter) Caps() render.Caps {
	// OverlayDialogs is true: this surface owns the whole alt screen, so Frame
	// places v.Dialogs itself, freshly, every frame — not baked into body as
	// scrollback content that would scroll away with history (and, since the
	// core still builds body from the same viewport machinery inline uses,
	// would otherwise render twice). See inline.Caps for the surface that
	// stacks instead.
	return render.Caps{AltScreen: true, Mouse: true, OverlayDialogs: true}
}

// contentPrefix returns the chrome this surface puts before a role's content:
// the gutter or label Message writes, and the width it reserves on every
// line. Message and ContentWidth (render.Base, via the Prefix field set in
// New) both read it, so the width the model pre-renders markdown at can never
// drift from the width this surface's chrome actually leaves — the model
// asks (ContentWidth) instead of reconstructing the prefix string itself.
//
// RoleUser/RoleAssistant get a one-column coloured gutter (theme.MsgGutter)
// instead of inline's word label ("you ·"/"nib ·"): this surface owns the
// whole alt screen, so a colour down the left edge of every line of the block
// (see gutterLines) identifies the speaker without spending a word on it,
// where inline's tighter columns keep the word but drop it on a run (Phase 3
// Task 12). RoleAgent/RoleTool/RoleError are unchanged by this task — their
// own chrome (the sub-agent marker, the tool body indent, the error cross)
// stays a label, matching inline.
func contentPrefix(role render.Role) string {
	switch role {
	case render.RoleUser:
		return theme.LabelYou.Render(theme.MsgGutter) + " "
	case render.RoleAssistant:
		return theme.Gutter.Render(theme.MsgGutter) + " "
	case render.RoleAgent:
		return theme.Subtle.Render(theme.SubAgent) + " "
	case render.RoleTool:
		// A tool block's body is indented two cells beneath its own header line.
		return "  "
	case render.RoleError:
		return theme.Error.Render(theme.Cross) + " "
	}
	return ""
}

// fadedPrefix is contentPrefix for an entry that is still arriving: the same
// chrome, at the same width, in inks faded by arriving (see theme.Fading).
func fadedPrefix(role render.Role, arriving float64) string {
	if arriving <= 0 {
		return contentPrefix(role)
	}
	switch role {
	case render.RoleUser:
		return theme.Fading(theme.LabelYou, arriving).Render(theme.MsgGutter) + " "
	case render.RoleAssistant:
		return theme.Fading(theme.Gutter, arriving).Render(theme.MsgGutter) + " "
	case render.RoleError:
		return theme.Fading(theme.Error, arriving).Render(theme.Cross) + " "
	}
	return contentPrefix(role)
}

// gutterLines lays out a block with the gutter prefix repeated on every
// non-blank line, rather than only on the first line the way render.Prefixed's
// label layout does — a gutter's whole point is to mark the block as it
// scrolls by, not to head it once. A blank line inside the content (a
// markdown paragraph break) is left bare: painting the bar across it would
// read as the message continuing through the gap rather than pausing for one,
// and would silently merge what must stay two visually separate paragraphs
// (and, cross-surface, two separate blocks — see
// TestMessageStructuralEquivalence's "multiline content" case, which requires
// both presenters to agree on block count).
func gutterLines(prefix, content string) string {
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for _, line := range lines {
		// Measured without escapes: glamour pads a code block's closing row
		// with styled spaces, which is still a blank line to the reader.
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			b.WriteString(prefix)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Message renders one chat entry, including the trailing blank-line separator
// before the next entry. assistant/agent content arrives already
// markdown-rendered (and wrapped) by the model — glamour is width-cached state
// the model owns, not something behind this interface. user/error content
// arrives raw and is wrapped here.
//
// Every role gets an unconditional trailing separator except RoleAgent with
// HugNext set: an agent lifecycle line whose thread run (rendered separately
// by the model, never through Message) immediately follows omits it, so the
// header visually hugs its own run instead of floating a blank line above it.
// That decision depends on the next RAW message (including agent_tool/
// agent_result, which Message never sees as a Message value), so the model
// computes HugNext and carries it on the value rather than Message re-deriving
// it from prev/Role.
//
// prev is accepted for signature symmetry with the Presenter interface, but
// this surface's chrome never reads it: the gutter (see contentPrefix) marks
// every message regardless of what rendered before it, since a one-column
// colour bar costs nothing to repeat the way a word label does — inline is
// the surface with columns tight enough to make that trade (Phase 3 Task 12).
// The trailing-separator rule likewise never depended on the previous role —
// it was always "blank after every message, except a hugging agent line".
func (presenter) Message(m render.Message, prev render.Role, w int) string {
	var body string
	switch m.Role {
	case render.RoleUser:
		prefix := fadedPrefix(render.RoleUser, m.Arriving)
		wrapped := render.Wrap(m.Content, w-lipgloss.Width(prefix))
		body = gutterLines(prefix, wrapped)

	case render.RoleAssistant:
		body = gutterLines(fadedPrefix(render.RoleAssistant, m.Arriving), m.Content)

	case render.RoleAgent:
		body = render.Prefixed(agentPrefix(m.AgentID), render.StyledLines(theme.Subtle, m.Content))
		if m.HugNext {
			return body
		}

	case render.RoleTool:
		// The label arrives already formatted (Message.Label): turning a tool
		// name and its raw JSON arguments into a human summary is domain logic
		// that stays model-side, same as markdown and the ask block.
		body = render.ToolBlock(m, w)
		body += m.ImageOut
		if m.HugNext {
			return body
		}

	case render.RoleError:
		prefix := fadedPrefix(render.RoleError, m.Arriving)
		wrapped := render.Wrap(m.Content, w-lipgloss.Width(prefix))
		body = render.Prefixed(prefix, wrapped)

	default:
		return ""
	}
	return body + "\n"
}

// Frame composes the whole screen from its four already-rendered pieces:
// header, body, then every pending v.Dialogs overlay, then composer
// (completion popup / queue / textarea, whichever are present), then footer —
// header/body/composer/footer stacked in the same order the core always
// concatenated them in, with dialogs docked directly above the composer
// (Phase 3 Task 11).
//
// Docked above the composer, not centred over body: this is where the
// tool-approval block has always visually sat on the inline surface (the last
// thing updateViewport pushes before the composer), so keeping it there on
// this surface too means the two never disagree about WHERE a pending
// question appears, only about HOW it gets there. A centred floating box
// would need real (row, col) placement — lipgloss.Place or manual line
// splicing over body's own lines — machinery nothing else in either presenter
// uses, for a payoff (visual centring) this phase's design doesn't call for.
//
// "Overlay" names what changed, not where: OverlayDialogs (see Caps) means
// Frame places v.Dialogs itself, fresh every frame from the ViewState, rather
// than the core having baked them into body's scrollback (inline's approach —
// see its Caps and updateViewport in tui/model.go). That is what stops a
// dialog from scrolling away with history when the user scrolls the
// transcript, and from rendering twice now that the core skips its own
// scrollback append for a surface that declares OverlayDialogs.
//
// p.Dialog resolves to render.Base's Dialog (promoted through the embedded
// Base, since presenter does not override it) — the exact same
// implementation inline relies on for its own baked-into-body dialogs, so the
// two surfaces can never render a dialog's own content differently, only
// place it differently.
func (p presenter) Frame(v render.ViewState, header, body, composer, footer string, w, h int) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(body)
	b.WriteString("\n")
	for _, d := range v.Dialogs {
		b.WriteString(p.Dialog(d, w))
	}
	b.WriteString(composer)
	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}

// agentPrefix is RoleAgent's prefix for one message: the sub-agent marker for
// a sub-agent's line, or the notice marker for a housekeeping line nib itself
// writes (context pruned, compacted, yolo toggled), which has no agent id.
// Both glyphs are one cell, so ContentWidth's measure of contentPrefix holds.
func agentPrefix(agentID string) string {
	if agentID == "" {
		return theme.Subtle.Render(theme.NoticeGlyph) + " "
	}
	return contentPrefix(render.RoleAgent)
}
