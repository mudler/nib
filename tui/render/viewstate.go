package render

import "github.com/mudler/nib/internal/textdiff"

// Role identifies the speaker or origin of a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleAgent     Role = "agent"
	RoleTool      Role = "tool"
	RoleError     Role = "error"
	RoleNone      Role = ""
)

// Message is a single rendered chat entry.
type Message struct {
	Role    Role
	Content string
	// Label is the one-line heading of a RoleTool block: the tool call already
	// rendered as a human summary. It arrives formatted because turning a tool
	// name plus its raw JSON arguments into that summary is domain logic
	// (chat.FormatToolCall) — the same rule that keeps markdown rendering and
	// the ask block model-side. A Presenter places it and styles it; it never
	// imports chat to build it.
	Label   string
	AgentID string
	// Meta, Status and Diff are meaningful for RoleTool only. Meta is dim
	// detail after the label ("+3 -1", "11 lines", "exit 1"); Status is the
	// outcome the header marks; Diff, when set, replaces Content as the body
	// (a write or edit shown as the change it made).
	Meta   string
	Status ToolStatus
	Diff   *textdiff.Diff
	// HugNext, for RoleTool, is true when this block is a lone header line and
	// the next message is another tool block: the Presenter omits the trailing
	// separator so a run of one-line calls stacks. For RoleAgent it is true when the next raw message
	// continues this same agent's thread (a run of agent_tool/agent_result
	// lines rendered separately by the model, never through Message). A
	// Presenter must omit its own trailing separator in that case — the
	// thread run that follows hugs it instead. The model computes this by
	// looking ahead at the next raw message, which a Presenter never sees, so
	// it cannot be derived from prev/Role alone.
	HugNext bool
	// Arriving is how far the entry still is from its full ink: 1 when it
	// has just joined the transcript, 0 (the zero value) once it is fully in.
	// A Presenter fades the entry's chrome by it (see theme.Fading).
	Arriving float64
}

// Reasoning is the model's in-progress reasoning trace: the text beneath the
// working indicator (spinner + status), which lives on ViewState.Spinner and
// ViewState.Status — kept there only, so there is exactly one source of
// truth for them.
//
// Collapsed and MaxLines are populated by the model's viewState() (Phase 3
// Task 10, collapsible reasoning) from Model.reasoningCollapsed and
// theme.ReasoningMaxLines, and read by both presenters' Reasoning to build a
// render.CollapsibleBox.
type Reasoning struct {
	Text      string
	Collapsed bool
	MaxLines  int
}

// DialogKind identifies which modal dialog is being shown.
type DialogKind int

const (
	DialogApproval DialogKind = iota
	DialogAsk
	DialogResume
	DialogModelPicker
)

// DialogOption is one line of a Dialog's choice menu. Emphasis marks it as an
// actionable key (styled as such); a false Emphasis is a dimmer, non-key hint
// line (e.g. the approval block's "[n] no · [e] edit" line, which sits among
// three actionable choices but isn't one itself).
type DialogOption struct {
	Text     string
	Emphasis bool
}

// Dialog is a modal prompt (tool approval, ask, resume) awaiting user input.
//
// DialogAsk (Phase 3 Task 11) carries the question in Title and one
// DialogOption per choice in Options — Emphasis is unused for this kind, left
// false — with Selected the highlighted row (single-select) and Checked the
// per-row checked state (multi-select; nil for single-select, same
// convention DialogApproval never used until now). Hint is the line a
// Presenter renders beneath the rows: the free-text escape-hatch reminder
// (typing instead of picking always works — see tui/ask.go's buildAskDialog
// and tui/model.go's KeyEnter handling). Options is already windowed to what
// should be visible (render.SelectList.Window(), applied by the builder,
// since Dialog itself carries no MaxVisible) — a Presenter renders every row
// it's given. An ask with no options at all (free-text-only) arrives with
// Options empty; a Presenter falls back to placing Title alone, the same
// degrade any other Title-only kind gets for free.
//
// DialogResume (Phase 3 Task 15, the /resume picker) reuses this exact same
// shape — Title, single-select Options, Hint — built by tui/resume.go's
// buildResumeDialog, so both presenters render it through their DialogAsk
// branch with no dedicated DialogResume rendering code at all.
//
// DialogApproval uses the rest of the fields:
//   - Rows holds the argument card: one [key, value] pair per structured
//     argument (chat.ToolArgRows). RowsUnstructured, when true, means Rows
//     instead holds exactly one entry whose second element is a raw prose
//     block (the tool call formatted as text) for a tool chat.ToolArgRows
//     doesn't recognize — a Presenter wraps and dims it instead of laying out
//     a key/value table. This flag exists so that distinction is explicit in
//     the type, rather than inferred from an empty Rows[0][0] key (which is
//     not actually guaranteed unique: a tool whose JSON arguments contain a
//     literal "" key would collide with that convention).
//   - Hint is the tool call's captured reasoning (if any), rendered wrapped
//     beneath the rows.
//   - Options is the choice menu: 4 entries (once / always / this-turn /
//     deny-edit, matching the on-screen approval prompt) in the normal case,
//     or a single entry (the edit-mode hint) while editing. A Presenter
//     should not assume only those two counts occur — render whatever list
//     it's given — but may special-case exactly 4 to reproduce the classic
//     approval layout (a blank gutter line before the menu).
type Dialog struct {
	Kind             DialogKind
	Title            string
	Options          []DialogOption
	Selected         int
	Checked          []bool      // multi-select state; nil for single-select
	Rows             [][2]string // key/value detail rows (approval argument cards)
	RowsUnstructured bool        // true: Rows is a single prose row, not a key/value table
	Hint             string
	// Meta is dim detail after an approval's Title (a diff's "+3 -1", "new
	// file"); Diff, when set, is the change a write or edit approval would
	// make, shown beneath the Rows.
	Meta string
	Diff *textdiff.Diff
}

// FooterRowKind identifies which footer row a FooterRow is, so a Presenter can
// map it to the right style. The four rows are not all styled alike (the
// original hand-rolled footer gave jobs/shell one treatment and loops/goal
// another), so this is domain data the tui-side builders supply — not
// something a Presenter can infer from Glyph/Text alone.
//
// FooterKindUnset is deliberately the zero value: a FooterRow built without
// setting Kind (a forgotten case in a future fifth row) must degrade to a
// Presenter's plain default styling, not silently adopt FooterJobs's
// treatment just because it happens to be int 0.
type FooterRowKind int

const (
	FooterKindUnset FooterRowKind = iota
	FooterJobs
	FooterShell
	FooterLoops
	FooterGoal
	FooterTodo
)

// FooterRow is one line of the footer's job-status area (active sub-agent
// jobs, shell jobs, cron loops, the active goal). It carries data, not
// pixels: Glyph is the marker rune (e.g. theme.Loop), Text is the already-
// composed but UNSTYLED line, and Kind says which of the four rows this is so
// a Presenter can apply the right style (FooterJobs/FooterShell get the
// original theme.Meta + width-fill treatment; FooterLoops/FooterGoal get
// theme.Subtle, unfilled — see inline.Footer). The tui-side callers that
// build these (tui/agents.go, tui/shelljobs.go, tui/loops.go, tui/goal.go)
// must not depend on any Presenter or style types, only produce plain data,
// since a Presenter must never import their argument types (agentJob,
// *loop.Registry, wizmcp.ShellJobInfo).
type FooterRow struct {
	Glyph string
	Text  string
	Kind  FooterRowKind
}

// ViewState is the read-only projection of Model state a Presenter renders
// from. It carries no behaviour — presenters read it and produce strings.
//
// The model builds one ViewState per frame (see tui/model.go's viewState
// method) with every field populated — Reasoning/Dialogs included, even though
// the inline Presenter's Header/Footer never read them — so nothing here is
// silently nil for a Presenter that composes a whole alt-screen frame from one
// ViewState rather than being driven block-by-block.
//
// It carries no transcript projection: a []Message field lived here for two
// phases with a producer and no consumer (updateViewport builds its own
// render.Message values inline, from the raw transcript, because it also has
// to route sub-agent thread runs and pre-render markdown), and the two
// projections had already drifted apart. Height went the same way — Frame
// receives the frame's height as an argument.
//
// Dialogs is a slice, not a single *Dialog, because more than one prompt can
// be pending at once: a background sub-agent's gated tool approval and a
// foreground ask_user question are independent and not mutually exclusive
// (cogito propagates the tool-call callback into spawned sub-agents, which
// run in the background — see chat/session.go). A Presenter renders each in
// order; ordinarily the slice holds zero or one entry.
//
// Fields from NewOutput onward were added by Task 6, which is the first to
// actually build a ViewState (Task 5 declared the struct with no call sites).
// The original Task 5 contract had no way to reach the model's viewport or
// its job-registry state from a Presenter, both of which the pre-existing
// hand-rolled Footer rendering needs:
//
//   - NewOutput: the presenter cannot see m.viewport, so the model resolves
//     "is there unread content below the fold" (showingViewport &&
//     !m.viewport.AtBottom()) itself and passes the answer through.
//   - Err: the plain (unstyled) text of the model's last error, or "" for
//     none. The presenter applies the error glyph and style.
//   - Footers: plain {Glyph, Text, Kind} data for the active-jobs/shell-jobs/
//     loops/goal footer rows (see FooterRow) — the presenter styles (per
//     Kind) and joins whichever are present, in order.
//
// HeaderStats carries the header's stat segments for responsive rendering.
// Lower-priority fields drop first on narrow terminals.
type HeaderStats struct {
	// Provider names where Model runs ("regolo", "config.yaml"). It rides in
	// the model segment as "provider · model", because a model name alone
	// cannot tell a /login pick from config.yaml's endpoint; on a terminal too
	// narrow for both it drops before the model does.
	Provider string
	Model    string // priority 90
	Tools    int    // priority 60
	MCP      int    // priority 50
	Skills   int    // priority 40
}

type ViewState struct {
	Width       int
	Cwd         string
	Brand       string
	AutoApprove bool
	// ApprovalMode is the session's approval_mode, for the header badge.
	// Every mode but prompt draws one; yolo has AutoApprove.
	ApprovalMode string
	Loading      bool
	Status       string
	Spinner      string
	// Speed is the live generation rate, already rendered, shown after the
	// status on the working indicator line; "" when the model is not
	// generating right now.
	Speed     string
	Reasoning Reasoning
	Dialogs   []Dialog
	Help      string
	Badges    string

	// HUD live telemetry for the footer.
	Clock string
	CPU   int
	RAM   int

	HeaderStats HeaderStats

	NewOutput bool
	Err       string
	Footers   []FooterRow
}
