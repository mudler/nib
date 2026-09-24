// Package inline implements render.Presenter for nib's default surface: the
// fzf-style inline widget that lives in the normal terminal scrollback (no alt
// screen, no mouse reporting). Its output is byte-for-byte what the hand-rolled
// rendering in tui/model.go produced before this package existed — this is a
// pure extraction, not a redesign.
package inline

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// presenter is the inline-widget Presenter. It embeds render.Base for every
// method whose implementation doesn't vary by surface (ContentWidth,
// Reasoning, Dialog, Header/HeaderHeight, Footer/FooterHeight) and overrides
// only Caps, Message and Frame — the pieces that genuinely differ from full
// (Task 17). It holds no other state: every method is a pure function of its
// arguments.
type presenter struct {
	render.Base
}

// New returns the inline Presenter.
func New() render.Presenter {
	return presenter{Base: render.Base{Prefix: contentPrefix}}
}

func (presenter) Caps() render.Caps {
	// OverlayDialogs is false: this surface lives in the normal scrollback, so
	// a dialog is content like any other — updateViewport appends it into the
	// same builder it feeds the viewport, and it scrolls away with history
	// exactly as every other message does. See full.Caps for the surface that
	// overlays instead.
	return render.Caps{AltScreen: false, Mouse: false, OverlayDialogs: false}
}

// contentPrefix returns the chrome this surface puts before a role's content:
// the label or gutter Message writes on the first line and indents the
// continuation lines to. Message and ContentWidth (render.Base, via the
// Prefix field set in New) both read it, so the width the model pre-renders
// markdown at can never drift from the width this surface's chrome actually
// leaves — the model asks (ContentWidth) instead of reconstructing the prefix
// string itself.
func contentPrefix(role render.Role) string {
	switch role {
	case render.RoleUser:
		return theme.LabelYou.Render(theme.LabelYouText) + " " + theme.SepStyle.Render(theme.Sep) + " "
	case render.RoleAssistant:
		return theme.LabelNib.Render(theme.BrandName) + " " + theme.SepStyle.Render(theme.Sep) + " "
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
	sep := " " + theme.Fading(theme.SepStyle, arriving).Render(theme.Sep) + " "
	switch role {
	case render.RoleUser:
		return theme.Fading(theme.LabelYou, arriving).Render(theme.LabelYouText) + sep
	case render.RoleAssistant:
		return theme.Fading(theme.LabelNib, arriving).Render(theme.BrandName) + sep
	case render.RoleError:
		return theme.Fading(theme.Error, arriving).Render(theme.Cross) + " "
	}
	return contentPrefix(role)
}

// messagePrefix returns the prefix Message writes for role, given the
// previously rendered role. Repeating "you ·"/"nib ·" down a run of
// consecutive same-role messages costs six columns on every line and tells
// the reader nothing the blank-line separator didn't already say, so a
// consecutive user/assistant message gets an all-spaces prefix instead of the
// label — but at the SAME width as the label, never narrower. That is what
// keeps this in agreement with ContentWidth (which cannot see prev at all,
// see its doc) and keeps a run's content aligned down every line, not just
// its own: shrinking the prefix would shift content left the moment a run
// starts, and a Message call for a later line in the run never revisits an
// earlier one to re-align it.
//
// Only RoleUser/RoleAssistant branch on prev, matching the brief exactly:
// RoleAgent/RoleTool/RoleError keep their own unconditional chrome (the
// sub-agent marker, the tool body indent, the error cross) unchanged by this
// task.
func messagePrefix(role, prev render.Role, arriving float64) string {
	label := fadedPrefix(role, arriving)
	if prev == role && (role == render.RoleUser || role == render.RoleAssistant) {
		return strings.Repeat(" ", lipgloss.Width(label))
	}
	return label
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
// prev is Phase 3 Task 12's first real consumer: on a run of consecutive
// same-role user/assistant messages, the label is dropped in favour of the
// blank-line separator (see messagePrefix) — the trailing-separator rule
// itself still never depends on prev; it was always "blank after every
// message, except a hugging agent line", and stays that way here.
func (presenter) Message(m render.Message, prev render.Role, w int) string {
	var body string
	switch m.Role {
	case render.RoleUser:
		prefix := messagePrefix(render.RoleUser, prev, m.Arriving)
		wrapped := render.Wrap(m.Content, w-lipgloss.Width(prefix))
		body = render.Prefixed(prefix, wrapped)

	case render.RoleAssistant:
		body = render.Prefixed(messagePrefix(render.RoleAssistant, prev, m.Arriving), m.Content)

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

// Frame composes the whole screen from its four already-rendered pieces. The
// inline widget lives in the normal scrollback, not the alt screen, so it has
// no screen of its own to lay these out on — it simply stacks them in the
// order they always rendered in: header, body, a blank line, the composer
// (completion popup / queue / textarea, whichever are present), a blank line,
// footer. w and h are unused here; this surface never had a frame to budget
// against before Task 10a, and does not gain one now — see full.Frame for the
// surface that does.
func (presenter) Frame(v render.ViewState, header, body, composer, footer string, w, h int) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(body)
	b.WriteString("\n")
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
