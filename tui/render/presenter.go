package render

// Caps declares what a Presenter's surface supports, so the shared core can
// adapt behaviour (e.g. mouse-driven scrolling) without a presenter needing
// to know about the other surface.
type Caps struct {
	AltScreen bool
	Mouse     bool
	// OverlayDialogs is true for a surface whose Frame places v.Dialogs itself
	// (as a real overlay composed fresh every frame) rather than relying on
	// them having been baked into body as scrollback content. It is a
	// separate concern from AltScreen — "owns the screen" and "overlays
	// dialogs" are not the same thing, and a future alt-screen surface could
	// still choose to stack — so the shared core must not infer one from the
	// other: it uses OverlayDialogs to decide whether to also append Dialog
	// output into the scrollback it feeds the viewport, which would otherwise
	// render every pending dialog twice on a surface that overlays.
	OverlayDialogs bool
}

// Presenter renders a ViewState (and its parts) into strings for one surface
// — the inline fzf-style widget or the full-screen alt-screen mode. The
// shared core drives a Presenter; a Presenter holds no state of its own.
//
// Reasoning takes the full ViewState (not just its Reasoning field) because
// the working indicator it renders — the spinner frame and status verb —
// lives on ViewState.Spinner/Status, the single source of truth for both
// (rather than duplicating them onto the narrower Reasoning type). A
// Presenter should render nothing when !v.Loading.
//
// ContentWidth exists because some content is rendered before it reaches a
// Presenter: markdown is glamour output, and glamour is width-cached state the
// model owns, so the model must know at what width to render. That width is
// whatever this surface's chrome leaves, which only the Presenter knows. The
// model asks for the number rather than for the prefix string: a surface whose
// chrome is not a literal per-line prefix (a frame, a hanging gutter) can still
// answer a width, and the model never has to measure chrome it did not compose.
//
// HeaderHeight and FooterHeight exist for the same reason in the other
// direction: the core budgets the viewport's height by subtracting the chrome
// that surrounds it, and neither piece is a constant. HeaderHeight was a
// hardcoded 2 in the core's layout budget for three phases — correct only for
// as long as both surfaces drew the same plain brand line plus hairline; a
// bordered full-screen header would have mis-budgeted silently, exactly as the
// composer used to before it was measured. It reports the number of rows the
// header occupies ABOVE the body (see BlockRows): Frame concatenates body
// straight onto header, so a header ending in "\n" occupies one row per
// newline.
//
// FooterHeight is the same query at the bottom: the core
// budgets the viewport's height by subtracting the footer, and Footer emits
// a permanent status row plus optional contextual help, telemetry, activity,
// new-output and error rows. A fixed guess makes an over-tall frame,
// which on the alt screen scrolls the header off the top. It must agree with
// Footer exactly, for the same ViewState and width.
//
// Frame is the whole-screen composition point (Phase 3 Task 10a). Before it
// existed, the core drove Header/Footer around the viewport and
// Message/Reasoning/Dialog INTO the viewport's scrollback — two call sites
// that never met, and neither received a height. That made a full-screen
// layout impossible to express: a presenter that owns the whole screen
// (alt-screen `full`) has no way to place a dialog as a real overlay rather
// than transcript content that scrolls away with history, and no way to know
// how tall the screen even is.
//
// Frame takes header, body, composer and footer already rendered — the core
// still calls Header/Message/Reasoning/Dialog/Footer to build them, exactly
// as before — because two of them are needed earlier than Frame can run:
// body's own content depends on the viewport's width and height, which the
// core must budget (from the footer's height) before it can lay out a single
// line of transcript, and footer is that same budget's input. Passing footer
// through as an already-rendered string (rather than Frame calling
// Footer(v, w) itself) is what lets the core measure it once per frame and
// reuse the string for the frame's own footer row, rather than rendering it
// twice (Task 10a's third carried defect). composer bundles whatever sits
// between the body and the footer this frame — the `/` completion popup, the
// queued-message block, the textarea, in whatever combination is present —
// since none of those has a Presenter method of its own (they are tui-side
// concerns: completion state, queue state, textarea state) and Frame does not
// need to distinguish them to place them.
//
// header and footer are both pure functions of v (Header(v) and Footer(v,
// w)), so v carries a second source of truth for the same two pieces —
// this states which one wins. header MAY be discarded and re-derived from v:
// nothing depends on measuring it before Frame runs, so a presenter that
// draws it as part of a box border (rather than a plain top row) is free to
// call Header(v) itself instead of placing the given string. footer MUST NOT
// be re-derived — it is the exact string the layout budget upstream was
// measured against (via FooterHeight, before body was ever laid out), and
// calling Footer(v, w) again inside Frame reintroduces the double render
// this shape exists to prevent.
//
// v is carried alongside the four strings (rather than Frame taking only
// them) for what it carries that isn't captured in any one piece: v.Dialogs,
// so a surface that owns the whole screen can place a dialog as a real
// overlay on top of body instead of it being baked into body as scrollback
// content. Phase 3 Task 11 (the ask_user dialog) is the first to exercise
// this: full.Frame now places v.Dialogs itself (docked above the composer —
// see its doc comment for why not centred), and Caps.OverlayDialogs tells the
// shared core to stop also baking them into body's scrollback for that
// surface, so they render exactly once. A later /resume picker follows the
// same shape.
//
// A presenter that stacks (inline) concatenates the four pieces in the order
// they always rendered in, and relies on the core having already rendered
// v.Dialogs into body via its per-message loop. A presenter that owns the
// screen and declares Caps.OverlayDialogs (full) instead renders v.Dialogs
// itself inside Frame, since the core skips that append for it.
type Presenter interface {
	Caps() Caps
	Header(v ViewState) string
	HeaderHeight(v ViewState) int
	Message(m Message, prev Role, w int) string
	ContentWidth(role Role, w int) int
	Reasoning(v ViewState, w int) string
	Dialog(d Dialog, w int) string
	Footer(v ViewState, w int) string
	FooterHeight(v ViewState, w int) int
	Frame(v ViewState, header, body, composer, footer string, w, h int) string
}
