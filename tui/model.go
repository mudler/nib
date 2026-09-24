package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/attachments"
	"github.com/mudler/nib/attachstage"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/internal/textdiff"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/loop"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/termimg"
)

// ChatMessage represents a message in the chat history
type ChatMessage struct {
	Role      string
	Content   string
	Name      string // tool name, for Role == "tool"
	Arguments string // marshaled call args, for Role == "tool"
	AgentID   string // issuing sub-agent, for Role == "tool" (empty = root agent)
	// Transient marks a turn-level error line: it stays in the transcript so
	// the failure is visible, but is dropped as soon as a reply arrives (a
	// successful turn, or a parked reply) so a recovered run doesn't carry
	// the old error for the rest of the session.
	Transient bool
	// Meta, Status and Diff decorate a tool entry's header and body; see
	// render.Message. Set by toolMessage.
	Meta   string
	Status render.ToolStatus
	Diff   *textdiff.Diff
	// Images carries image bytes produced by the tool (computer_use
	// screenshots, browser_vision captures, read_image raw bytes) or
	// user-attached images. The render layer renders them inline when
	// the terminal supports a graphics protocol.
	Images []chat.ToolImage
	// arrived is when the entry joined the transcript; its chrome fades in
	// from it (see arriving). Zero means drawn at full ink.
	arrived time.Time
	// flipped inverts the ctrl+r fold state for this tool entry alone (a
	// click on it). ctrl+r clears it.
	flipped bool
}

type sessionStore interface {
	Save(chat.SessionRecord) error
	Load(string) (chat.SessionRecord, error)
	List(string) ([]chat.SessionRecord, error)
	Delete(string) error
}

// bodyless reports whether a tool entry renders as its header line alone.
func (c ChatMessage) bodyless() bool {
	return c.Diff == nil && strings.TrimSpace(c.Content) == ""
}

// appendMessage appends one or more entries to the transcript. Beyond that it
// claims no invariant over the transcript itself. (It used to be the
// choke-point a now-deleted []render.Message cache keyed its invalidation on;
// that cache fed ViewState.Messages, which no Presenter ever read. Nothing
// stops a caller assigning m.messages directly, and applyResume does exactly
// that when it rebuilds a restored transcript from scratch.)
//
// It does claim one narrow thing of its own: calling it always closes off
// m.streamingActive. appendStreamedContent is the only caller allowed to
// mutate the transcript's tail message in place instead of appending; every
// other append here means that tail is no longer a live streaming target.
func (m *Model) appendMessage(msgs ...ChatMessage) {
	m.streamingActive = false
	now := time.Now()
	for i := range msgs {
		if msgs[i].arrived.IsZero() {
			msgs[i].arrived = now
		}
	}
	m.messages = append(m.messages, msgs...)
}

// bumpTurnGen marks a turn boundary (see turnGen's doc) — called from
// sendMessage/sendWithAttachmentsCmd, synchronously, before either returns
// its Cmd, and from responseMsg/parkMsg when a turn ends. A nil turnGen (a bare
// Model{} literal in a test not exercising generations) makes this a no-op
// rather than a panic.
func (m Model) bumpTurnGen() {
	if m.turnGen != nil {
		m.turnGen.Add(1)
	}
}

// currentTurnGen reads turnGen, defaulting to 0 when nil (see bumpTurnGen) —
// the same default a zero-value reasoningEvent.gen carries, so a test that
// never sets either one up still compares equal and sees unfiltered delivery.
func (m Model) currentTurnGen() int32 {
	if m.turnGen == nil {
		return 0
	}
	return m.turnGen.Load()
}

// startThinking sets the model into a thinking state, picking a random funny
// line and tip for this turn unless ui.no_funny is on. Every site that
// previously assigned m.startThinking() calls this instead.
func (m *Model) startThinking() {
	m.status = "Thinking…"
	if m.cfg.UI.NoFunny {
		m.thinkingLine = ""
		m.tip = ""
		return
	}
	m.thinkingLine = theme.RandomThinkingLine()
	m.tip = theme.RandomTip()
}

// stopThinking clears the funny line and tip set by startThinking, called
// when a turn ends (responseMsg, parkMsg, compactResultMsg).
func (m *Model) stopThinking() {
	m.thinkingLine = ""
	m.tip = ""
}

// appendStreamedContent applies one live "content" delta (Callbacks.OnStream,
// via reasoningEventContentDelta) to the transcript: the FIRST delta of a
// turn starts a new in-progress assistant message, and every delta after that
// — while streamingActive stays true — appends into that SAME message rather
// than appending a new one, so the reply grows in place.
//
// Callers are expected to have already dropped a delta whose gen doesn't
// match currentTurnGen() (see the reasoningEventsMsg case in Update) — that
// closes off a stale delta from a turn that already ended arriving during a
// LATER turn. What's left for the guard here is the narrower window within
// the SAME generation: a delta for the turn that just ended, arriving after
// responseMsg/parkMsg already reconciled and cleared streamingActive, but
// before the NEXT turn has dispatched (so turnGen hasn't moved yet either).
// m.loading is false in exactly that window, so gating on it drops the
// delta instead of fabricating a bubble for a turn that is, from Update's
// perspective, already over.
func (m *Model) appendStreamedContent(delta string) {
	if delta == "" || !m.loading {
		return
	}
	if m.streamingActive && len(m.messages) > 0 {
		m.messages[len(m.messages)-1].Content += delta
		return
	}
	m.appendMessage(ChatMessage{Role: "assistant", Content: delta})
	m.streamingActive = true
	m.streamShown = 0
	m.streamStart = time.Now()
	m.revealIdx, m.revealText = 0, ""
}

// Model represents the TUI state
type Model struct {
	// UI components
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// presenter renders every block. Chosen once at construction from the run
	// mode; the model never branches on mode itself. Every production path
	// (NewModel, always called with a Presenter from app.go) sets it; a test
	// that builds a Model directly must go through newTestModel so this is
	// never nil when updateViewport or View run.
	presenter render.Presenter
	// imageMgr manages inline terminal image rendering (kitty/iTerm2
	// graphics protocols). It tracks kitty transmit-once state and enforces
	// the image budget. nil-safe: when the terminal has no graphics
	// protocol, images degrade to text placeholders.
	imageMgr *termimg.ImageManager
	// imgCounter is the next stable image ID to assign. Used by
	// toImageRefs to give each image a unique ID for kitty transmit
	// tracking.
	imgCounter int
	// Chat state
	messages     []ChatMessage
	session      *chat.Session
	ctx          context.Context
	cancel       context.CancelFunc
	transports   []mcp.Transport
	shellJobs    *wizmcp.ShellJobs
	cfg          types.Config
	sessionReady bool

	// UI state
	width     int
	height    int
	maxHeight int // Configured max height (0 = no limit)
	// footerBudget is how many footer rows the current viewport height was
	// budgeted against (Presenter.FooterHeight at the time). The footer grows
	// and shrinks mid-turn as jobs start, loops run and errors come and go, so
	// syncLayout compares this against the current answer and re-budgets when
	// they differ — a WindowSizeMsg is not the only thing that changes it.
	footerBudget int
	// chromeBudget is the total non-body height (see layoutBudget) the current
	// viewport height was budgeted against. syncLayout re-budgets whenever the
	// live answer differs, so an approval card or a /resume picker appearing —
	// rows full.Frame writes between body and composer, and which nothing
	// reserved before this — takes its rows from the viewport rather than
	// from the top of the screen.
	chromeBudget int
	// footerCache memoizes the last rendered Footer string, so the two call
	// sites that need it for the same frame — syncLayout's height budget
	// (needed before body can be laid out) and View's own frame composition
	// (needed after) — render it once between them instead of twice on every
	// spinner tick. See renderFooter. It lives behind a pointer for the same
	// reason the viewport's own sizes are plain fields: View is a
	// value-receiver method, but every
	// copy of Model shares this pointee. nil on a bare Model{} literal, which
	// renderFooter falls back to an uncached render for.
	footerCache *footerCache
	loading     bool
	// forceFollow makes the next updateViewport scroll to the bottom regardless
	// of where the user had scrolled to. Set only by user-initiated actions
	// (sending a message, answering an approval or a question) — passive
	// re-renders keep respecting the reading position. One-shot: consumed by
	// the render it applies to.
	forceFollow bool
	// interruptArmed is set after a Ctrl+C or Esc interrupts an in-flight turn,
	// so the next Ctrl+C arms the exit instead of re-interrupting. Reset when a
	// new turn starts and when the turn ends.
	interruptArmed bool
	// exitArmed is set by a Ctrl+C with nothing else left to do. Only a second
	// Ctrl+C while it is set quits; any other key, or exitArmWindow passing,
	// disarms it. exitSeq tells the current arm's timeout from an earlier one.
	exitArmed bool
	exitSeq   int
	// hint is a one-shot line shown in place of the help line (the exit
	// warning, "draft cleared"). The next key other than Ctrl+C clears it.
	hint string
	// queueHeld stops the queue from being sent automatically. An interrupt
	// sets it, so pressing stop does not start the next queued message; Enter
	// on an empty composer releases it.
	queueHeld bool
	// parked is true while the live run is parked (the assistant replied but the
	// run is still alive waiting on the injection channel — background work
	// pending, or simply ready for a follow-up). While parked the composer is
	// usable and Enter injects into the SAME run instead of starting a new turn.
	parked bool
	// lastParkedReply is the assistant text most recently surfaced via a park
	// event, so the terminal responseMsg can avoid re-appending an identical
	// final reply.
	lastParkedReply string
	// streamingActive is true while m.messages' tail entry is an in-progress
	// assistant reply still receiving live "content" deltas (see
	// appendStreamedContent). It is the analogue of lastParkedReply for the
	// streaming path: responseMsg/parkMsg check it to RECONCILE that tail
	// message with their own authoritative text instead of appending a
	// second, duplicate copy of the reply.
	//
	// appendMessage clears it unconditionally on every call, so any transcript
	// entry other than a streamed delta mutating its own tail (a tool call, a
	// turn boundary, an error notice, …) closes the streaming target off —
	// otherwise a stray, late-arriving delta (see reasoningChan's ordering
	// doc for why one can race past a turn boundary) could mutate an
	// unrelated message instead of being safely dropped.
	streamingActive bool
	// streamShown is how many bytes of the revealed reply are drawn so far
	// (see typewriter.go). anim is the clock that drives the reveal and the
	// fade-ins; updateViewport starts it when a frame has something moving.
	streamShown int
	anim        *animClock
	// speed measures the model's generation rate for the footer gauge (see
	// speed.go). OnStream records into it from the session's goroutine.
	speed *speedMeter
	// agentSpeed meters each sub-agent's stream for its landing line.
	agentSpeed *agentMeters
	// revealIdx is the index plus one of a reply still being revealed after
	// its turn ended (0 for none), and revealText the text it was revealed
	// with: a transcript rebuild that changed the entry stops the reveal.
	revealIdx  int
	revealText string
	// streamStart is when the streaming reply began; the cursor's pulse is
	// timed from it.
	streamStart time.Time
	// stepThought is the index plus one of the thought entry the current
	// step's reasoning folded into (see thought.go), 0 for none.
	// reasoningSince is when the live trace got its first streamed text,
	// which times the "thought for 4s" summary.
	stepThought    int
	reasoningSince time.Time
	// wakeupGen invalidates pending reminder/self-paced wake-up ticks: a fired
	// tea.Tick is honored only if its captured gen still matches. Bumped by
	// /loop stop to cancel a self-paced loop. Poll wake-ups ride pollGen instead.
	wakeupGen int
	// pollGen invalidates pending *poll* wake-up ticks (those that only watch
	// in-flight background work). Bumped on every park→resume so an orphan poll
	// armed while parked cannot re-dispatch the task once the work it watched
	// completes on its own (cogito resumes us with the result). Reminders are
	// unaffected — they ride wakeupGen — so a real reminder scheduled during
	// background work still fires. See the parkMsg resume branch / wakeupFireMsg.
	pollGen int
	// turnGen is the same invalidate-stale-async-work idiom as wakeupGen/
	// pollGen above, applied to reasoningChan's delta events (reasoningEvent-
	// Delta and reasoningEventContentDelta): each is stamped with the CURRENT
	// generation at the moment OnStream enqueues it, and Update drops one
	// whose gen doesn't match — it belongs to a turn that has already ended,
	// with a new one now in flight.
	//
	// It has to be pointer-backed, unlike wakeupGen/pollGen: those are only
	// ever bumped and compared from inside Update, all on the SAME evolving
	// Model value. OnStream's closure, by contrast, is built once in
	// initSession (session lifetime) and closes over whatever Model snapshot
	// existed then; a plain int field on it would never see a later Update
	// copy's bump. A pointer is the one thing every copy — the frozen
	// initSession snapshot included — still shares, the same reason
	// reasoningChan itself works as a hand-off despite value-receiver Update.
	//
	// Bumped where a new turn dispatches: sendMessage and
	// sendWithAttachmentsCmd (see bumpTurnGen), synchronously, before either
	// returns its Cmd — so the bump happens-before that Cmd's goroutine ever
	// runs, which is happens-before any OnStream call the NEW turn produces.
	// Also bumped where a turn ends (responseMsg, and parkMsg when the run
	// parks): an event the turn sent just before it returned can reach
	// Update after that reset, and with the old generation it would write
	// the ended turn's thinking into the hidden box for the next turn to
	// show. Injecting into a parked run (releaseQueueFront) does not bump
	// it; the resumed run stamps its events after the park's bump, so they
	// carry the current generation.
	turnGen *atomic.Int32
	// selfPaced counts active self-paced loops (for the footer). 0 or 1 in
	// practice. Incremented on /loop <prompt>; reset to 0 by /loop stop. Note: it
	// is NOT auto-cleared when a self-paced loop ends naturally (the TUI has no
	// signal that the model chose not to re-arm), so the footer may show a
	// self-paced loop until /loop stop.
	selfPaced int
	// loops holds active fixed-interval (cron) jobs. Self-paced loops keep no
	// state here — they ride the wake-up timer (see wakeupGen).
	loops     *loop.Registry
	loopsPath string // .nib/loops.json for durable jobs
	status    string
	// thinkingLine is the funny one-liner shown in place of the plain
	// "thinking" verb while the agent works. Picked at random each turn
	// by startThinking(); empty when ui.no_funny is on.
	thinkingLine string
	// tip is the usage hint shown as a dim line beneath the spinner.
	// Picked at random each turn by startThinking(); empty when
	// ui.no_funny is on.
	tip string
	reasoning string
	// reasoningCollapsed caps the live thinking trace to a few trailing lines
	// so it does not flood the transcript. Per-session, persists across
	// turns: it is a Model field (not derived per-frame), toggled only by
	// ctrl+r — and, on a mouse-capable surface, by clicking the box itself
	// (Phase 3 Task 16).
	reasoningCollapsed bool
	// reasoningResetPending marks that the next reasoningEventDelta must start
	// a fresh trace instead of appending to m.reasoning. Set whenever a
	// step-boundary reasoningEventBoundary (Callbacks.OnReasoning, which fires
	// with the COMPLETE block for the step that just ended) is processed:
	// that text is authoritative for the step that just finished, but the
	// next step's streamed deltas are a new trace, not a continuation of it.
	// Without this, the first delta of every step after the first would be
	// appended onto the previous step's complete text and the box would
	// duplicate it.
	reasoningResetPending bool
	// reasoningSpanStart/End record the content-relative row span [start, end)
	// the reasoning box occupies in the viewport's virtualized scrollback for
	// THIS render — recomputed on every updateViewport pass, never cached
	// across frames. The box's height changes between collapsed (~7 rows) and
	// expanded (many more), so a span captured once would go stale the moment
	// the user expands it, making the very next click land on the wrong row.
	// Both are 0 (an empty span, start == end) whenever updateViewport did not
	// render a box this frame (not loading, or no reasoning text yet) — a
	// click can never match an empty span. See the tea.MouseMsg case in
	// Update, which compares a translated click row against this span.
	reasoningSpanStart int
	reasoningSpanEnd   int
	// toolSpans records where each tool block (finished or running) sits in
	// the viewport for THIS render, for click-to-fold; recomputed on every
	// updateViewport pass, like the reasoning span.
	toolSpans []toolSpan
	// running holds the root-agent tool calls that have started and not
	// finished, drawn live below the transcript (see running.go).
	running []runningTool
	// err holds the most recent fatal error, shown as a persistent banner
	// above the composer. It is set only for errors that leave the session
	// unusable (session init failure) — a failed turn already records its
	// error in the transcript, and the banner would otherwise stay on screen
	// for the rest of the session even after the run recovers.
	err      error
	output   string // Command to output to shell on exit
	quitting bool

	// Tool approval state
	pendingTool      *chat.ToolCallRequest
	awaitingApproval bool
	// bell is the terminal ringBell writes BEL to; nil never rings. See
	// WithBell.
	bell io.Writer
	// approvalEditing distinguishes the two approval sub-modes: false is the
	// default key-driven choice mode (single y/a/n/e/A/Esc keypresses, input
	// hidden); true is edit mode where the textarea is shown for a free-form
	// change.
	approvalEditing bool

	// ask_user state
	pendingAsk  *chat.AskRequest
	awaitingAsk bool
	// askList holds the live selection state for a pending ask_user question —
	// which row is highlighted (single-select) or checked (multi-select). Built
	// alongside pendingAsk in the askMsg branch, cleared alongside it once the
	// question is answered (see resolveAsk). nil whenever awaitingAsk is false;
	// every call site guards on it being non-nil rather than assuming
	// awaitingAsk implies it, since a Model built directly (tests, a bare
	// Model{} literal) may set one without the other.
	askList         *render.SelectList
	askRequestChan  chan chat.AskRequest
	askResponseChan chan string
	wakeupChan      chan chat.WakeupRequest
	cronFireChan    chan string    // prompts of cron jobs run now via cron_trigger
	parkChan        chan parkEvent // park/resume signals from the live run
	compactChan     chan [2]int    // {before, after} token counts from auto-compaction
	pruneChan       chan [2]int    // {results, freedTokens} from tool-output pruning

	// /resume picker state (Phase 3 Task 15). Set synchronously by
	// dispatchResolved's KindResume case — unlike ask_user's askMsg, there is
	// no blocking channel here (a session listing is a local file read, not
	// something a live session goroutine has to hand back a channel for).
	// awaitingResume mirrors awaitingAsk's contract: resumeList is nil
	// whenever it is false, and every call site guards on the pointer rather
	// than assuming the bool implies it.
	awaitingResume bool
	// resumeList holds the live selection state, formatted one row per
	// resumeSessions entry (see buildResumeDialog / resumeItems). resumeSessions
	// is the parallel slice of full records resumeList.Selected indexes into —
	// the list only ever carries display strings, never an id a Presenter
	// would have to parse back out.
	resumeList     *render.SelectList
	resumeSessions []chat.SessionRecord
	// resumeDeleteArmed is Task 20's delete confirm: true right after the
	// picker's delete key ('d') has been pressed once, cleared by a second
	// 'd' (which performs the delete) or by any other key (which cancels it
	// instead). See handleResumeDeleteKey (tui/resume.go) for the full
	// rationale.
	resumeDeleteArmed bool

	// store persists the transcript at every turn boundary and on exit (see
	// recordSession) and backs /resume's listing. sessionID and sessionTitle
	// are this conversation's own record key and cached display title;
	// sessionCreated is stamped once at construction (or copied from a
	// resumed record) rather than recomputed, so a session's Created date
	// survives across many autosaves. All three are seeded by NewModel and
	// overwritten by a successful /resume (see applyResume).
	store          sessionStore
	sessionID      string
	sessionTitle   string
	sessionCreated time.Time

	// contextTokens is the current conversation size shown in the footer badge.
	// Updated after each turn and after compaction; 0 hides the badge.
	contextTokens int

	// sessionUsage is what the session has spent so far, shown in the footer
	// beside the context badge. Refreshed wherever contextTokens is.
	sessionUsage chat.SessionUsage

	// Sub-agent jobs state
	jobs           []agentJob
	agentEventChan chan chat.AgentEvent

	// Ctrl+O log viewer state.
	showLogs    bool           // viewer open
	logSel      int            // selected index in the unified jobs list (list mode)
	logOpenID   string         // when non-empty: drilled into this job's full log
	logOpenKind string         // "agent" | "shell" for the open job
	logVP       viewport.Model // scrollable full-log view

	// Ctrl+T todo panel state.
	showTodo bool // panel open

	// Unified `/` completion state
	completion compState

	// pendingSettings maps each config key /settings saved but could not apply
	// to the running session to the saved value, so /settings can show what
	// the next start will use (tui/settings.go).
	pendingSettings map[string]string

	// Model picker state. modelPickerRequest monotonically identifies endpoint
	// lookups so a response from a cancelled picker cannot populate a newer one.
	modelPicker        modelPicker
	modelPickerRequest uint64

	// /login: provider picker, API-key form, and the OAuth/device wait.
	providerPicker providerPicker
	loginForm      loginForm
	loginWait      loginWait

	// /endpoint add: template picker then form.
	endpointForm endpointFormState

	// Pending message queue: text typed while a run is in flight. Entries are
	// editable until they fire (FIFO) into the live run at step boundaries.
	// queueSel is the entry highlighted for ^e/^x when the composer is empty.
	queue    []string
	queueSel int

	// Input history for ↑/↓ recall (shell-style). histPos == len(history) is
	// the "draft" position — the text the user is currently typing, stashed in
	// histDraft once navigation begins.
	history   []string
	histPos   int
	histDraft string

	// pending holds files staged via /attach, awaiting the next message. They
	// combine with inline @path files (via attachstage.BuildSend) on send and
	// clear only after a successful send (mirrors the CLI REPL).
	pending []attachstage.StagedFile

	// redispatch holds follow-ups that were released into a run (and echoed)
	// but never consumed by it — the run ended first. They re-dispatch as
	// fresh turns ahead of the queue, without a second echo, and are not
	// shown in the editable-queue UI (they already read as sent).
	redispatch []string

	// Markdown renderers cached per wrap width (glamour renderers are
	// width-bound and expensive to build). At most a couple of distinct
	// widths exist in practice (one per message-prefix width).
	mdRenderers map[int]*glamour.TermRenderer
	// mdCache holds rendered markdown by width and source (see
	// renderMarkdown). The transcript is redrawn on every spinner and reveal
	// tick, and without it each frame ran glamour on every assistant message.
	mdCache map[mdKey]string

	// Channels for async communication with callbacks
	statusChan       chan string
	toolRequestChan  chan chat.ToolCallRequest
	toolResponseChan chan chat.ToolCallResponse
	toolResultChan   chan chat.ToolResult
	toolStartChan    chan chat.ToolStart
	autoApprovedChan chan autoApprovedMsg
	// suggest is the reply autosuggestion shown in the composer.
	suggest suggestState
	// classifierOverride is the /classifier choice for this session, kept
	// over config.yaml's classifier block across session rebuilds. Nil
	// means none was made.
	classifierOverride *types.ClassifierConfig
	// reasoningChan carries BOTH step-boundary reasoning (Callbacks.OnReasoning,
	// the COMPLETE block for a step) and live streamed reasoning deltas
	// (Callbacks.OnStream's "reasoning" kind), as a single ordered stream of
	// reasoningEvent values distinguished by their kind field.
	//
	// This is deliberately ONE channel rather than two. cogito emits every
	// delta for a step and only then fires the step-boundary callback, all on
	// one goroutine — so the producer's own order is already correct. But
	// bubbletea relays each tea.Cmd's result through its own independently
	// scheduled goroutine (see tea.go's per-Cmd `go func(){ p.Send(cmd()) }`),
	// so splitting boundary and delta onto separate channels/listeners lets
	// Update observe them out of producer order: a buffered delta channel can
	// have a send return (and its listener relay it) without a rendezvous,
	// while a separate listener parked on the boundary channel since session
	// start can relay near-instantly — so the boundary for a step can reach
	// Update before that same step's final delta does, clobbering the
	// authoritative text with a stale trailing fragment. A single channel
	// with a single listener removes the second goroutine entirely: FIFO
	// ordering on one channel preserves the producer's order by construction,
	// with no sequence number or generation counter to keep in sync.
	//
	// Sends are blocking (never select+default): unlike statusChan (a plain
	// status string where dropping a stale one is harmless — only the latest
	// matters), losing either kind of reasoning event here is a real
	// correctness bug: a dropped delta leaves a gap in the accumulated trace,
	// and a dropped boundary means reasoningResetPending never gets armed, so
	// the next step's deltas silently keep appending onto stale text forever.
	// (Update does drop a boundary stamped with an ENDED turn's generation —
	// that one belongs to no live trace and arming anything for it would be
	// arming it for the wrong turn.)
	// The buffer just gives a fast token burst some slack before backpressure
	// kicks in.
	reasoningChan chan reasoningEvent

	// carryAutoApprove holds the /yolo state across a session rebuild
	// (/resume), applied to the new session when it is ready.
	carryAutoApprove *bool

	// boot tracks the startup animation state. See boot.go.
	boot *bootState
	// HUD live telemetry for the footer.
	hudClock string
	// hudCPU is system CPU% over the last tick; hudCPUOK is false until two
	// samples exist, so an idle 0% still renders once it is measured.
	hudCPU       int
	hudCPUOK     bool
	hudPrevBusy  uint64
	hudPrevTotal uint64
	// hudMemUsed and hudMemTotal are system memory in bytes; 0 total where it
	// is unavailable.
	hudMemUsed  int64
	hudMemTotal int64
}

// responseMsg is sent when the AI responds
type responseMsg struct {
	content string
	err     error
	blocked []attachments.Blocked
	images  []chat.ToolImage
}

// modelListMsg is the result of an asynchronous model-picker endpoint lookup.
type modelListMsg struct {
	requestID uint64
	models    []string
	partial   bool // the list is a suggestion: typed names are accepted too
	err       error
}

// compactResultMsg is the outcome of a manual /compact run.
type compactResultMsg struct {
	before, after int
	err           error
}

// compactNoticeMsg is an auto-compaction notice pushed from the session goroutine.
type compactNoticeMsg [2]int

// pruneNoticeMsg is a tool-output pruning notice pushed from the session goroutine.
type pruneNoticeMsg [2]int

// parkEvent carries a park/resume signal from the live run. parked=true means
// the run parked (assistant replied, run still alive); parked=false means an
// injected message resumed it.
type parkEvent struct {
	parked bool
	reply  string
}

// parkMsg delivers a parkEvent to the update loop.
type parkMsg parkEvent

// statusMsg is sent for status updates
type statusMsg string

// reasoningEventKind distinguishes the kinds of streamed update carried on
// reasoningChan.
type reasoningEventKind int

const (
	// reasoningEventDelta is one (possibly coalesced) chunk of a live streamed
	// reasoning trace (Callbacks.OnStream's "reasoning" kind).
	reasoningEventDelta reasoningEventKind = iota
	// reasoningEventBoundary carries the COMPLETE reasoning block for a step
	// that just ended (Callbacks.OnReasoning).
	reasoningEventBoundary
	// reasoningEventContentDelta is one (possibly coalesced) chunk of the
	// live streamed assistant REPLY (Callbacks.OnStream's "content" kind —
	// cogito's "answer text delta"). Carried on the SAME reasoningChan as the
	// two reasoning kinds above for the same reason the boundary/delta split
	// was fixed: cogito emits every delta for a step (reasoning or content)
	// from one goroutine, in true order, and a second channel/listener pair
	// for content would let bubbletea's independent per-Cmd goroutine relay
	// reorder it relative to the reasoning events — see reasoningChan's doc.
	// Applied in Update by appendStreamedContent, not by the reasoning-box
	// logic below.
	reasoningEventContentDelta
)

// reasoningEvent is one item read off reasoningChan. The kinds are handled
// with different precedence in Update — see reasoningResetPending (for the
// two reasoning kinds) and streamingActive (for reasoningEventContentDelta).
//
// gen is the turn generation (Model.turnGen) that was current at the moment
// OnStream enqueued this event — see turnGen's doc. Only reasoningEventDelta
// and reasoningEventContentDelta are checked against it; a mismatch means
// this event belongs to a turn that has already ended and a later one is now
// in flight, and Update drops it rather than applying it.
type reasoningEvent struct {
	kind reasoningEventKind
	text string
	gen  int32
}

// reasoningEventsMsg carries one or more reasoningEvent values, in the exact
// order the producer emitted them onto reasoningChan (see listenReasoningEvents
// for why a batch and not always exactly one).
type reasoningEventsMsg []reasoningEvent

// toolCallMsg is sent when a tool call needs approval
type toolCallMsg chat.ToolCallRequest

// toolAnsweredMsg reports that a tool call's approval response was delivered,
// so the next request can be read.
type toolAnsweredMsg struct{}

// askMsg is sent when the agent asks the user a question.
type askMsg chat.AskRequest

// agentEventMsg is sent for sub-agent lifecycle updates.
type agentEventMsg chat.AgentEvent

// toolResultPreviewLines bounds how many lines of a sub-agent's result we
// show inline.
const toolResultPreviewLines = 12

// toolOutputKeepLines bounds how many lines of a tool's output its transcript
// entry keeps. The block shows render.ToolFoldLines of them until it is
// expanded; past this cap even an expanded block is cut short.
const toolOutputKeepLines = 1000

// spinnerFPS matches the 80ms frame advance used by comparable harnesses —
// fast enough to read as motion, slow enough to stay off the CPU.
const spinnerFPS = time.Second / 12

// toolResultMsg carries a finished tool's output to the UI.
type toolResultMsg chat.ToolResult

// sessionReadyMsg is sent when the session is initialized
type sessionReadyMsg struct {
	session *chat.Session
	err     error
}

// NewModel creates a new TUI model
func NewModel(ctx context.Context, cfg types.Config, height int, shellJobs *wizmcp.ShellJobs, p render.Presenter, transports ...mcp.Transport) Model {
	ctx, cancel := context.WithCancel(ctx)

	ta := textarea.New()
	ta.Placeholder = "ask anything…"
	ta.Focus()
	ta.Prompt = theme.PromptGlyph + " "
	ta.FocusedStyle.Prompt = theme.Prompt
	ta.CharLimit = 4096
	ta.SetWidth(80)
	// Single-line input: Enter sends (newline insertion is disabled), so a
	// taller textarea would just repeat the `›` prompt on every empty row.
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends message

	vp := viewport.New(80, 10)
	vp.SetContent("")
	// Update hands every keystroke to the viewport after the composer, so the
	// bubbles default (b/u/k up, space/f/d/j down, ctrl+u/ctrl+d half pages,
	// h/l sideways) scrolled the transcript while the user typed. Keep only
	// the page keys, which the composer does not use.
	vp.KeyMap = viewport.KeyMap{
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
	}

	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: theme.SpinnerFrames(), FPS: spinnerFPS}
	s.Style = theme.Running

	// Calculate max height - negative means percentage, positive means lines
	maxH := height
	if maxH < 0 {
		maxH = 0 // Will be calculated on first WindowSizeMsg
	}

	// Rooted at the per-user BaseDir, not the process's cwd — see the store
	// field's own comment on the Model literal below for why. MaxSessions
	// applies cfg.SessionRetention (0 leaves chat.DefaultMaxSessions, 200, in
	// effect — see SessionStore.maxSessions).
	sessionStore := chat.NewSessionStore(filepath.Join(plugin.BaseDirIn(cfg.BaseDir), "sessions"))
	sessionStore.MaxSessions = cfg.SessionRetention

	m := Model{
		viewport:           vp,
		logVP:              viewport.New(80, 10),
		textarea:           ta,
		spinner:            s,
		presenter:          p,
		imageMgr:           termimg.NewImageManager(),
		reasoningCollapsed: true,
		footerCache:        &footerCache{},
		messages:           []ChatMessage{},
		ctx:                ctx,
		anim:               newAnimClock(ctx),
		speed:              &speedMeter{},
		agentSpeed:         &agentMeters{},
		cancel:             cancel,
		maxHeight:          maxH,
		transports:         transports,
		shellJobs:          shellJobs,
		cfg:                cfg,
		height:             height,
		agentEventChan:     make(chan chat.AgentEvent, 16),
		statusChan:         make(chan string, 10),
		reasoningChan:      make(chan reasoningEvent, 256),
		turnGen:            new(atomic.Int32),
		toolRequestChan:    make(chan chat.ToolCallRequest),
		toolResponseChan:   make(chan chat.ToolCallResponse),
		toolResultChan:     make(chan chat.ToolResult, 64),
		toolStartChan:      make(chan chat.ToolStart, 64),
		autoApprovedChan:   make(chan autoApprovedMsg, 64),
		askRequestChan:     make(chan chat.AskRequest),
		askResponseChan:    make(chan string),
		wakeupChan:         make(chan chat.WakeupRequest, 8),
		cronFireChan:       make(chan string, 8),
		parkChan:           make(chan parkEvent, 16),
		compactChan:        make(chan [2]int, 4),
		pruneChan:          make(chan [2]int, 4),
		mdRenderers:        make(map[int]*glamour.TermRenderer),
		mdCache:            make(map[mdKey]string),
		loops:              loop.NewRegistry(),
		loopsPath:          filepath.Join(".nib", "loops.json"),
		// Rooted at the per-user BaseDir (~/.config/nib by default, the same
		// root config.yaml/plugins/skills already use), NOT at the process's
		// cwd the way loopsPath above is: /resume's cwd filter (chat.Session-
		// Store.List) only means anything — and --all only has anything to
		// widen TO — if every project's sessions land in one shared store
		// that a Cwd field can then filter, rather than each project cwd
		// getting its own separate, mutually invisible .nib/sessions folder.
		store:          sessionStore,
		sessionID:      cfg.ResumeSessionID,
		sessionTitle:   cfg.ResumeSessionTitle,
		sessionCreated: time.Now(),
		boot:           newBootState(),
	}
	m.initHudClock()
	// A fresh (non-resumed) session mints its own id; a --resume'd one
	// (cfg.ResumeSessionID set by app.go before the TUI started) keeps the
	// stored session's own id, so autosaving continues to update that same
	// file instead of forking a new one.
	if m.sessionID == "" {
		m.sessionID = newSessionID()
	}
	// A --resume'd session already seeds the model from cfg.InitialHistory
	// (chat.NewSession); show the user the same conversation, as applyResume
	// does for /resume. Without this the screen started empty and the resumed
	// session looked like a fresh one.
	if len(cfg.InitialHistory) > 0 {
		if !cfg.ResumeSessionCreated.IsZero() {
			m.sessionCreated = cfg.ResumeSessionCreated
		}
		m.appendMessage(restoredTranscript(cfg.InitialHistory)...)
		m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf(theme.ResumeRestored, len(cfg.InitialHistory))})
	}
	m.completion.setRegistries(cfg.Commands, cfg.Skills, cfg.Agents)
	m.completion.setSettingsConfig(cfg)
	return m
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.spinner.Tick,
		m.initSession(),
	}
	if m.boot != nil {
		cmds = append(cmds, m.boot.nextBootCmd())
	}
	cmds = append(cmds, m.hudTick(), m.listenAnim())
	return tea.Batch(cmds...)
}

// initSession creates the chat session
func (m Model) initSession() tea.Cmd {
	return func() tea.Msg {
		callbacks := chat.Callbacks{
			OnStatus: func(status string) {
				select {
				case m.statusChan <- status:
				default:
				}
			},
			// Blocking send (no select+default): see reasoningChan's doc for
			// why dropping a boundary event is a real correctness bug here,
			// not a harmless "only the latest matters" case.
			OnReasoning: func(reasoning string) {
				// gen is checked in Update, like the two delta kinds. It used
				// to be carried "for consistency" only, on the argument that a
				// stale boundary merely repaints a complete (if outdated)
				// block that responseMsg clears at turn end anyway — but it
				// can arrive AFTER that reset and after the next turn has
				// dispatched, which is the thinking-box flicker the user sees
				// on every message they send. See the reasoningEventBoundary
				// case in Update.
				m.reasoningChan <- reasoningEvent{kind: reasoningEventBoundary, text: reasoning, gen: m.currentTurnGen()}
			},
			// OnStream opts the session into cogito's streaming path so the
			// thinking box AND the assistant's reply both fill progressively
			// instead of only at step boundaries / turn end (OnReasoning and
			// the terminal responseMsg/parkMsg still fire too — see
			// reasoningResetPending's doc for reasoning, streamingActive's for
			// content).
			//
			// chat.StreamEvent.Kind is string(cogito.StreamEvent.Type); cogito
			// defines exactly these values (cogito's stream.go):
			// "reasoning", "content", "tool_call", "tool_result", "status",
			// "done", "error", "sub_agent". "reasoning" and "content" are the
			// two handled here — the live counterparts to OnReasoning and the
			// final reply, respectively. The rest (tool_call/tool_result/
			// status/done/error/sub_agent) have no handler yet.
			//
			// Both are sent onto the SAME reasoningChan (not separate
			// channels per kind) — see reasoningChan's doc for why: cogito
			// emits every delta for a step and only then fires the boundary,
			// all from one goroutine, so one channel is what makes Update
			// observe them in that same order.
			OnStream: func(ev chat.StreamEvent) {
				// Stamped with whatever generation is CURRENT right now, at
				// enqueue time — not read later by Update, which would be
				// racy against the exact problem this exists to prevent. This
				// read happens-before SendMessage returns (same goroutine),
				// which happens-before responseMsg reaches Update, which is
				// where turnGen next moves — so a mismatch Update
				// later sees is real staleness, not a race on the read
				// itself. See turnGen's doc.
				gen := m.currentTurnGen()
				if ev.Kind == "reasoning" || ev.Kind == "content" {
					// Timed here, as the chunk arrives, not when Update
					// gets to it: a burst drained from reasoningChan in one
					// batch would otherwise read as one instant. A
					// foreground sub-agent's chunks count too: the turn
					// waits on it, and spends its tokens.
					m.speed.recordTurn(gen, len(ev.Content), time.Now())
				}
				// Each sub-agent is also metered on its own, for its
				// landing line. Only text deltas carry Content.
				m.agentSpeed.record(ev.AgentID, len(ev.Content), time.Now())
				switch ev.Kind {
				case "reasoning":
					m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: ev.Content, gen: gen}
				case "content":
					m.reasoningChan <- reasoningEvent{kind: reasoningEventContentDelta, text: ev.Content, gen: gen}
				}
			},
			OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
				// Send tool request and wait for user response
				m.toolRequestChan <- req
				return <-m.toolResponseChan
			},
			OnAskUser: func(req chat.AskRequest) string {
				m.askRequestChan <- req
				return <-m.askResponseChan
			},
			OnScheduleWakeup: func(req chat.WakeupRequest) string {
				// Non-blocking: hand the request to the UI loop and confirm now. The
				// UI arms a timer; when it fires it injects the note into the live
				// run (see wakeupFireMsg).
				select {
				case m.wakeupChan <- req:
					if req.Reason != "" {
						return fmt.Sprintf("Scheduled a wake-up in %ds (%s). You'll be re-invoked then.", req.DelaySeconds, req.Reason)
					}
					return fmt.Sprintf("Scheduled a wake-up in %ds: %q. You'll be re-invoked then.", req.DelaySeconds, req.Prompt)
				default:
					return "Could not schedule wake-up (too many pending)."
				}
			},
			OnCronCreate: func(req chat.CronRequest) string {
				j, err := m.loops.Add(req.Expr, req.Prompt, req.Recurring, req.Durable, loop.MonitorConfig{Script: req.MonitorScript, URL: req.MonitorURL})
				if err != nil {
					return "cron rejected: " + err.Error()
				}
				if req.Durable {
					_ = m.loops.Save(m.loopsPath)
				}
				return fmt.Sprintf("Scheduled %s (%s) → %q", j.ID, j.Expr, j.Prompt)
			},
			OnCronList: func() string {
				jobs := m.loops.List()
				if len(jobs) == 0 {
					return "No active cron loops."
				}
				var b strings.Builder
				for _, j := range jobs {
					b.WriteString(loopLine(j) + "\n")
				}
				return strings.TrimRight(b.String(), "\n")
			},
			OnCronDelete: func(id string) string {
				if m.loops.Delete(id) {
					_ = m.loops.Save(m.loopsPath)
					return "Cancelled " + id
				}
				return "No such loop: " + id
			},
			OnCronPause: func(id string) string {
				if m.loops.Pause(id) {
					_ = m.loops.Save(m.loopsPath)
					return "Paused " + id
				}
				return "No such loop: " + id
			},
			OnCronResume: func(id string) string {
				if m.loops.Resume(id) {
					_ = m.loops.Save(m.loopsPath)
					return "Resumed " + id
				}
				return "No such loop: " + id
			},
			OnCronTrigger: func(id string) string {
				j, ok := m.loops.Get(id)
				if !ok {
					return "No such loop: " + id
				}
				// Hand the prompt to the UI loop, which queues it behind the
				// current turn like any cron fire (see dispatchLoop).
				select {
				case m.cronFireChan <- j.Prompt:
					return "Queued " + id + " to run after this turn."
				default:
					return "Could not run " + id + " now (too many pending)."
				}
			},
			OnParked: func(reply string) {
				select {
				case m.parkChan <- parkEvent{parked: true, reply: reply}:
				default:
				}
			},
			OnResumed: func() {
				select {
				case m.parkChan <- parkEvent{parked: false}:
				default:
				}
			},
			OnAgentEvent: func(ev chat.AgentEvent) {
				select {
				case m.agentEventChan <- ev:
				default:
				}
			},
			OnCompactDone: func(before, after int) {
				select {
				case m.compactChan <- [2]int{before, after}:
				default:
				}
			},
			OnPruneDone: func(results, freed int) {
				select {
				case m.pruneChan <- [2]int{results, freed}:
				default:
				}
			},
			OnAutoApproved: func(req chat.ToolCallRequest, v chat.Verdict) {
				select {
				case m.autoApprovedChan <- autoApprovedMsg{req: req, verdict: v}:
				default:
				}
			},
			OnToolStart: func(ts chat.ToolStart) {
				select {
				case m.toolStartChan <- ts:
				default:
				}
			},
			OnToolResult: func(res chat.ToolResult) {
				select {
				case m.toolResultChan <- res:
				default:
				}
			},
		}

		session, err := chat.NewSession(m.ctx, m.cfg, callbacks, m.transports...)
		if session != nil {
			// Wire the shell-job registry so backgrounded shell jobs keep the run
			// parked and inject a completion notice when they finish.
			session.SetShellJobs(m.shellJobs)
		}
		return sessionReadyMsg{session: session, err: err}
	}
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Any key means the user is replying: no suggestion is fetched for
		// this wait. One that already arrived stays, to match what they type.
		m.suggest.armed = false
		// Any key but Ctrl+C disarms an armed exit and clears a one-shot hint.
		if msg.Type != tea.KeyCtrlC && (m.exitArmed || m.hint != "") {
			m.disarmExit()
		}
		// Ctrl+C with no dialog open goes through the one-step-per-press ladder
		// (handleCtrlC). With a dialog open it is Esc, so it closes or cancels
		// the dialog the way Esc does, and never reaches quit.
		if msg.Type == tea.KeyCtrlC {
			if !m.dialogOpen() {
				return m.handleCtrlC()
			}
			m.disarmExit()
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		}
		// Ctrl+T todo panel: intercepts keys while open.
		if m.showTodo {
			switch msg.Type {
			case tea.KeyEsc, tea.KeyCtrlT:
				m.showTodo = false
				m.reflowLayout()
				return m, nil
			}
			return m, nil // swallow other keys while panel is open
		}
		// Ctrl+O log viewer: intercepts navigation/scroll keys while open.
		if m.showLogs {
			jobs := m.unifiedJobs()
			if m.logOpenID == "" {
				// LIST mode
				switch {
				case msg.Type == tea.KeyEsc, msg.Type == tea.KeyCtrlO:
					m.showLogs = false
					return m, nil
				case msg.Type == tea.KeyUp:
					if m.logSel > 0 {
						m.logSel--
					}
					return m, nil
				case msg.Type == tea.KeyDown:
					if m.logSel < len(jobs)-1 {
						m.logSel++
					}
					return m, nil
				case msg.Type == tea.KeyEnter:
					if m.logSel >= 0 && m.logSel < len(jobs) {
						m.logOpenID = jobs[m.logSel].ID
						m.logOpenKind = jobs[m.logSel].Kind
						m.syncLogViewport()
					}
					return m, nil
				case msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'k' || msg.Runes[0] == 'K'):
					if m.logSel >= 0 && m.logSel < len(jobs) {
						m.killSelected(m.logSel + 1)
					}
					return m, nil
				}
				return m, nil // swallow other keys in list mode
			}
			// LOG mode (drilled into one job): Esc -> back to list; Ctrl+O -> close.
			switch msg.Type {
			case tea.KeyEsc:
				m.logOpenID = ""
				m.logOpenKind = ""
				return m, nil
			case tea.KeyCtrlO:
				m.showLogs = false
				m.logOpenID = ""
				m.logOpenKind = ""
				return m, nil
			}
			// Anything else scrolls the log viewport (up/down/pgup/pgdn/home/end).
			var vpCmd tea.Cmd
			m.logVP, vpCmd = m.logVP.Update(msg)
			return m, vpCmd
		}
		// The /login dialogs own ordinary keys while open, like the model
		// picker below.
		if m.loginWait.active {
			if msg.Type == tea.KeyEsc {
				m.cancelLoginWait()
				m.updateViewport()
			}
			return m, nil
		}
		if m.loginForm.active {
			return m.handleLoginFormKey(msg)
		}
		if m.endpointForm.active {
			return m.handleEndpointFormKey(msg)
		}
		if m.providerPicker.active {
			return m.handleProviderPickerKey(msg)
		}
		// The picker owns ordinary keys while open.
		if m.modelPicker.active {
			switch msg.Type {
			case tea.KeyEsc:
				m.modelPicker.close()
			case tea.KeyUp:
				m.modelPicker.move(-1)
			case tea.KeyDown:
				m.modelPicker.move(1)
			case tea.KeyBackspace:
				m.modelPicker.backspace()
			case tea.KeyEnter:
				m.pickModel(false)
			case tea.KeyCtrlS:
				// Only /model's own picker has a session-only pick to
				// promote: a provider switch is saved already, and a
				// classifier pick has its own save rules.
				if m.modelPicker.target == nil {
					m.pickModel(true)
				}
			case tea.KeySpace:
				m.modelPicker.appendQuery(" ")
			case tea.KeyRunes:
				printable := make([]rune, 0, len(msg.Runes))
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) {
						printable = append(printable, r)
					}
				}
				if len(printable) > 0 {
					m.modelPicker.appendQuery(string(printable))
				}
			}
			m.updateViewport()
			return m, nil
		}
		// Tool approval is a distinct key-driven mode: in choice mode the chat
		// input is hidden and a numbered menu takes single keypresses (1/2/3,
		// with y/a/A as silent legacy aliases, n/Esc deny, e edits); edit mode
		// (entered with `e`) shows the textarea for a free-form change.
		if m.awaitingApproval {
			if !m.approvalEditing {
				switch {
				case msg.Type == tea.KeyEsc:
					return m.resolveApproval(chat.ToolCallResponse{Approved: false})
				case msg.Type == tea.KeyRunes && len(msg.Runes) == 1:
					switch msg.Runes[0] {
					case '1', 'y', 'Y':
						return m.resolveApproval(chat.ToolCallResponse{Approved: true})
					case '2', 'a':
						var prefix string
						if m.pendingTool != nil {
							_, prefix = chat.GrantScope(m.pendingTool.Name, m.pendingTool.Arguments)
						}
						return m.resolveApproval(chat.ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: prefix})
					case '3', 'A':
						return m.resolveApproval(chat.ToolCallResponse{Approved: true, AllowAllTurn: true})
					case '4':
						if m.session != nil {
							m.session.SetAutoApprove(true)
						}
						return m.resolveApproval(chat.ToolCallResponse{Approved: true})
					case 'n', 'N':
						return m.resolveApproval(chat.ToolCallResponse{Approved: false})
					case 'e', 'E':
						m.approvalEditing = true
						m.textarea.Reset()
						m.updateViewport()
						return m, nil
					}
				}
				// Swallow every other key in choice mode.
				return m, nil
			} else if msg.Type == tea.KeyEsc {
				// Edit mode: Esc cancels back to choice mode without denying.
				m.approvalEditing = false
				m.textarea.Reset()
				m.updateViewport()
				return m, nil
			}
		}
		// ask_user is a keyboard-navigable dialog, the same idiom as tool
		// approval above: up/down move the highlighted option, space toggles a
		// multi-select check (enter answers — handled in the KeyEnter case
		// below, since it must also accept the free-text escape hatch). Esc
		// cancels unconditionally via resolveAsk(""). handleListDialogKey (see
		// tui/resume.go) is the generalized form of this navigation, shared
		// with the /resume picker below so the two dialogs don't carry two
		// copies of the same Move/Toggle/Esc wiring.
		if m.awaitingAsk && m.askList != nil {
			if next, cmd, handled := m.handleListDialogKey(msg, m.askList, func(mm Model) (tea.Model, tea.Cmd) { return mm.resolveAsk("") }); handled {
				return next, cmd
			}
		}
		// /resume is the same keyboard-driven list idiom as ask_user above,
		// minus the free-text escape hatch: a session picked from a list has
		// no meaningful typed alternative, so unlike ask_user this fully
		// resolves Enter right here rather than deferring to the KeyEnter
		// case, and swallows every other key while open (the tool-approval
		// choice mode above does the same for the same reason — there is
		// nothing else a keypress could mean while this dialog owns the
		// screen).
		if m.awaitingResume && m.resumeList != nil {
			// 'd' is the picker's delete key (Task 20) — scoped to exactly this
			// branch so it never fires for ask_user's own list dialog below,
			// which shares handleListDialogKey but has nothing to delete. Like
			// every other picker shortcut it only claims the key while the
			// composer is empty (mirroring handleListDialogKey's own guard),
			// so a free-text 'd' elsewhere in the app is unaffected.
			if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == resumeDeleteKey && strings.TrimSpace(m.textarea.Value()) == "" {
				return m.handleResumeDeleteKey()
			}
			// A pending delete confirm is cancelled by anything other than the
			// second 'd' above — but the key still does whatever it would
			// normally do (arrows still move the selection, Esc still cancels
			// the whole picker via cancelResume below): cancelling the arm
			// means "don't also treat this keypress as a delete", not "eat the
			// keypress".
			m.resumeDeleteArmed = false
			if next, cmd, handled := m.handleListDialogKey(msg, m.resumeList, func(mm Model) (tea.Model, tea.Cmd) { return mm.cancelResume() }); handled {
				return next, cmd
			}
			if msg.Type == tea.KeyEnter {
				return m.resolveResumePick()
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyEsc:
			return m.handleEsc()

		case tea.KeyEnd:
			// Only jump when the composer is empty — bubbles' textarea binds End
			// to line-end, and hijacking it unconditionally would break editing.
			if strings.TrimSpace(m.textarea.Value()) == "" {
				m.viewport.GotoBottom()
				return m, nil
			}

		case tea.KeyCtrlY:
			// Yank nib's last suggested command to the shell and exit, so the
			// Ctrl+Space widget inserts it at the prompt. No-op if there's
			// nothing to yank or a turn is still in flight.
			if !m.sessionReady || m.loading {
				return m, nil
			}
			cmd := lastSuggestedCommand(m.messages)
			if cmd == "" {
				m.status = "no command to use yet"
				return m, nil
			}
			m.output = cmd
			return m.quit()

		case tea.KeyCtrlB:
			// Background the running foreground work: a sub-agent first,
			// otherwise a running foreground shell command.
			if m.sessionReady && m.session != nil {
				if id := m.firstRunningJobID(); id != "" {
					// Detach the sub-agent so it keeps running in the background; its
					// completion is auto-injected into the live run by cogito.
					_ = m.session.AgentManager().Detach(id)
					return m, nil
				}
			}
			if id, ok := m.shellJobs.DetachForeground(); ok {
				m.status = "Backgrounded shell job " + id
				m.updateViewport()
			}
			return m, nil

		case tea.KeyCtrlE:
			// Pull the selected queued entry back into the composer to edit; it
			// re-queues (appended) on the next Enter. Only when the composer is
			// empty, so it never clobbers in-progress typing — otherwise fall
			// through so ctrl+e keeps its textarea meaning (move to line end).
			if strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0 {
				entry := m.queueDeleteSel()
				if entry != "" {
					m.textarea.SetValue(entry)
					m.textarea.Focus()
					m.completion.sync(entry)
					m.updateViewport()
				}
				return m, nil
			}

		case tea.KeyCtrlX:
			// Delete the selected queued entry. Only when the composer is empty,
			// so it never interferes with in-progress typing — otherwise fall
			// through to the textarea.
			if strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0 {
				m.queueDeleteSel()
				m.updateViewport()
				return m, nil
			}

		case tea.KeyCtrlO:
			// Toggle the navigable log viewer.
			if !m.sessionReady {
				return m, nil
			}
			m.showLogs = !m.showLogs
			m.logSel = 0
			m.logOpenID = ""
			m.logOpenKind = ""
			// The log viewer owns the keystrokes, so the composer's input line
			// goes away with it: re-budget rather than leave the frame a row
			// short until the next tick (see reflowLayout).
			m.reflowLayout()
			return m, nil

		case tea.KeyCtrlR:
			// Toggle the live reasoning trace between its tailing collapsed
			// box and its full expanded form. Per-session state, so it
			// persists across turns until the user toggles it again. Tool
			// output folds with it, and a block the user clicked open or shut
			// follows the rest again.
			m.reasoningCollapsed = !m.reasoningCollapsed
			m.clearToolFlips()
			m.updateViewport()
			return m, nil

		case tea.KeyShiftTab:
			// Cycle the approval mode: configured → classify → auto.
			// Not while a picker or the completion popup owns the keys.
			if !m.sessionReady || m.completion.active {
				break
			}
			m.cycleApprovalMode()
			// The prompt on screen is one auto would not have raised.
			if m.awaitingApproval && m.session.AutoApprove() {
				return m.resolveApproval(chat.ToolCallResponse{Approved: true})
			}
			m.updateViewportFollow()
			return m, nil

		case tea.KeyCtrlT:
			// Toggle the todo panel.
			if !m.sessionReady {
				return m, nil
			}
			m.showTodo = !m.showTodo
			m.reflowLayout()
			return m, nil

		case tea.KeyTab:
			if m.acceptSuggestion() {
				return m, nil
			}
			if m.completion.active {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				// Accepting narrows the popup to one row, or closes it: either
				// way the composer just changed height (see reflowLayout).
				m.reflowLayout()
				return m, nil
			}

		case tea.KeyUp:
			if m.completion.active {
				m.completion.up()
				// Moving the highlight can add or drop the ghost hint row.
				m.reflowLayout()
				return m, nil
			}
			if strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0 {
				m.queueMoveSel(-1)
				return m, nil
			}
			m.historyUp()
			return m, nil

		case tea.KeyDown:
			if m.completion.active {
				m.completion.down()
				m.reflowLayout()
				return m, nil
			}
			if strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0 {
				m.queueMoveSel(1)
				return m, nil
			}
			m.historyDown()
			return m, nil

		case tea.KeyEnter:
			// Accept an open completion instead of submitting — unless the
			// typed text already equals the sole remaining match exactly
			// ("/yolo" with only "yolo" left to match). At that point there
			// is nothing left to complete, so accepting would just insert a
			// trailing space and eat the keypress; fall through to submit
			// instead. A genuine prefix ("/yo") or a multi-match popup still
			// accepts as before.
			if m.completion.active && !m.completion.exact(m.textarea.Value()) {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				m.reflowLayout()
				return m, nil
			}

			// Answering a pending ask_user question does not require a live
			// session — the session is what's blocked waiting for this very
			// answer — so it is resolved before the sessionReady gate below,
			// which only guards starting a NEW turn. An empty composer answers
			// with the dialog's current pick (or checked set, for multi-select);
			// anything typed is the free-text escape hatch, unchanged from
			// before this dialog existed.
			if m.awaitingAsk && m.pendingAsk != nil {
				if strings.TrimSpace(m.textarea.Value()) == "" {
					if m.askList != nil && len(m.askList.Items) > 0 {
						if answer := m.askList.Answer(); answer != "" {
							return m.resolveAsk(answer)
						}
					}
					// No options to pick from, or (multi-select) nothing checked
					// yet: fall through to the empty-input no-op below, same as
					// the old behaviour.
				} else {
					return m.resolveAsk(parseAskAnswer(m.textarea.Value(), *m.pendingAsk))
				}
			}

			if !m.sessionReady {
				return m, nil
			}

			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				// Enter on an empty composer releases a queue an interrupt
				// held. While a run is live the queue drains at its next
				// boundary; otherwise it is sent now.
				if m.queueHeld && len(m.queue)+len(m.redispatch) > 0 {
					m.queueHeld = false
					if m.session != nil && m.session.RunLive() {
						m.updateViewport()
						return m, nil
					}
					cmd := m.flushQueueAsTurn()
					m.updateViewport()
					return m, cmd
				}
				return m, nil
			}

			// /yolo acts at once, whatever state the run is in. It never starts a
			// turn, and it is typed precisely when a run is prompting: queueing it
			// behind the run (as slash commands otherwise are) left every tool
			// call of that run still asking, and inside the approval prompt it
			// went to the model as an adjustment to the call.
			if k := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents).Kind; k == slash.KindYolo || k == slash.KindApprove {
				m.pushHistory(input)
				m.textarea.Reset()
				m.completion.sync("")
				m.dispatchResolved(input)
				// The prompt on screen is one yolo would not have raised.
				if m.awaitingApproval && m.session.AutoApprove() {
					return m.resolveApproval(chat.ToolCallResponse{Approved: true})
				}
				m.updateViewportFollow()
				return m, nil
			}

			// Check if we're in tool approval mode
			if m.awaitingApproval {
				return m.handleToolApproval(input)
			}

			// Record the input for ↑/↓ recall (shell-style history).
			m.pushHistory(input)

			// While a run is in flight (working or parked), the input does not start
			// a new turn — it queues into the live run. Queued entries are editable
			// until they fire (FIFO) at the next step boundary. A parked run is idle
			// and waiting, so release the front entry immediately to resume it.
			if m.loading || m.parked {
				m.queue = append(m.queue, input)
				m.textarea.Reset()
				m.completion.sync("")
				m.interruptArmed = false
				if m.parked {
					m.releaseQueueFront()
				}
				m.updateViewport()
				return m, nil
			}

			// Echo + resolve + dispatch (command/skill/message).
			m.textarea.Reset()
			m.completion.sync("")
			cmd := m.dispatchInput(input)
			m.updateViewportFollow()
			return m, cmd
		}

	case tea.MouseMsg:
		// Click-to-expand the reasoning box (Phase 3 Task 16). Gated on the
		// presenter's own capability rather than assuming mouse events only
		// arrive when reporting is on: cmd/tui.go only enables
		// tea.WithMouseCellMotion() for a Caps().Mouse surface (full-screen),
		// so a real terminal never sends this to the inline widget — but a
		// test can construct the message directly, and the inline surface
		// must ignore it all the same, by construction, not by accident.
		//
		// A non-hit click (or any other button/action) falls through
		// unchanged to the m.viewport.Update(msg) fallback at the bottom of
		// this function, which is what still gives wheel scrolling — do not
		// return early except on an actual hit.
		if m.presenter.Caps().Mouse && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.reasoningBoxHit(msg.Y) {
				m.reasoningCollapsed = !m.reasoningCollapsed
				m.updateViewport()
				return m, nil
			}
			if span, ok := m.toolBlockHit(msg.Y); ok {
				m.toggleToolBlock(span)
				m.updateViewport()
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		// Captured BEFORE updateDimensions() touches m.viewport.Height: AtBottom()
		// is relative to the current height, so resizing first (in particular
		// shrinking) can make a user who WAS pinned to the bottom read as
		// scrolled-up before their follow state is ever consulted.
		wasAtBottom := m.viewport.AtBottom()
		m.width = msg.Width
		m.height = msg.Height
		if m.modelPicker.active {
			m.modelPicker.scrollSelectionIntoView(modelPickerMaxVisible)
		}
		m.updateDimensions()
		// Content is wrapped to a width that no longer exists, and the offset was
		// clamped against the old height — both have to be recomputed.
		if wasAtBottom {
			m.updateViewportFollow()
		} else {
			m.updateViewport()
		}

	case sessionReadyMsg:
		// A new session: suggestions from the old one answer nothing here.
		m.resetSuggestion()
		if msg.err != nil {
			m.err = msg.err
			// Every other footer-state mutator routes through updateViewport
			// (see e.g. the responseMsg branch below) so the footer budget
			// picks up the new error line immediately; this branch used to
			// return early and leave it one row stale until the next
			// unrelated re-render.
			m.updateViewport()
			return m, nil
		}
		m.session = msg.session
		m.sessionReady = true
		if m.carryAutoApprove != nil {
			m.session.SetAutoApprove(*m.carryAutoApprove)
			m.carryAutoApprove = nil
		}
		m.applyClassifierOverride()
		if m.boot != nil {
			m.boot.markReady(&m)
			// A resumed session already has a conversation to show; the
			// boot log would hide it until the first message is sent.
			if len(m.messages) > 0 {
				m.boot.collapsed = true
				m.updateViewport()
			}
		}
		// A resumed session whose model could not be restored says so in the
		// transcript: the collapsed boot log would hide the note.
		if m.cfg.InitialModel != "" {
			if note := m.session.StartupNote(); note != "" {
				m.appendMessage(ChatMessage{Role: "error", Content: note})
				m.updateViewport()
			}
		}
		// Reload durable cron loops persisted from a previous session.
		if n, err := m.loops.Load(m.loopsPath); err == nil && n > 0 {
			m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf("Reloaded %d durable loop(s).", n)})
		}
		// Start listening for callbacks
		cmds = append(cmds, m.listenStatus(), m.listenReasoningEvents(), m.listenToolRequest(), m.listenToolResult(), m.listenToolStart(), m.listenAutoApproved(), m.listenAskRequest(), m.listenAgentEvents(), m.shellTick(), m.loopTick(), m.listenWakeup(), m.listenCronFire(), m.listenPark(), m.listenCompact(), m.listenPrune())

	case bootTickMsg:
		if m.boot != nil {
			m.boot.tick(&m)
			if cmd := m.boot.nextBootCmd(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.updateViewport()
		}

	case hudTickMsg:
		m.handleHudTick()
		cmds = append(cmds, m.hudTick())

	case modelListMsg:
		if !m.modelPicker.active || !m.modelPicker.loading || msg.requestID != m.modelPicker.requestID {
			return m, nil
		}
		switch {
		case msg.err != nil && (m.modelPicker.target != nil || errors.Is(msg.err, llmprovider.ErrNoModelList)):
			// Switching provider must not dead-end on a missing or failing
			// model list: let the user type the model name instead.
			m.modelPicker.loading = false
			m.modelPicker.typed = true
			name := m.session.ActiveProviderName()
			if m.modelPicker.target != nil {
				name = m.modelPicker.target.Name
			}
			m.modelPicker.listErr = fmt.Sprintf(theme.ModelListFailed, name, msg.err)
		case msg.err != nil:
			m.modelPicker.close()
			m.appendMessage(ChatMessage{Role: "error", Content: msg.err.Error()})
		default:
			m.modelPicker.setModels(msg.models, m.session.Model())
			m.modelPicker.typed = m.modelPicker.target != nil && len(msg.models) == 0
			m.modelPicker.partial = msg.partial
			if m.modelPicker.query != "" {
				m.modelPicker.filter()
			}
		}
		m.updateViewport()
		return m, nil

	case exitDisarmMsg:
		if msg.seq == m.exitSeq && m.exitArmed {
			m.disarmExit()
			m.updateViewport()
		}
		return m, nil

	case responseMsg:
		// The run returned: it is no longer parked (all background work drained).
		m.loading = false
		m.parked = false
		m.interruptArmed = false
		m.status = ""
		m.stopThinking()
		m.endThoughtStep()
		// The turn is over, so a call still marked running will not report.
		m.clearRunning()
		m.reasoningResetPending = false
		// The turn is over: move the generation now, not only at the next
		// dispatch. A boundary or delta this turn sent just before it
		// returned can still be in flight, and with the old generation it
		// would pass the check in Update and write this turn's thinking
		// into the hidden box, where the next turn would show it.
		m.bumpTurnGen()
		// Snapshot the in-progress streamed message's index, if any, BEFORE
		// appendMessage below (blocked-attachment notices) has a chance to
		// clear streamingActive as its own side effect. append only grows
		// m.messages, so this index stays valid however many entries land
		// after it.
		streamIdx := -1
		if m.streamingActive && len(m.messages) > 0 {
			streamIdx = len(m.messages) - 1
		}
		m.streamingActive = false
		// Surface any attachments that couldn't be sent (blocked by model caps
		// or resolution), mirroring the CLI's per-file error lines.
		for _, b := range msg.blocked {
			m.appendMessage(ChatMessage{Role: "error", Content: filepath.Base(b.Path) + " — " + b.Reason})
		}
		// Clear staged attachments only on a successful send (retain on error),
		// matching the CLI REPL.
		if msg.err == nil {
			m.pending = nil
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				// The run was interrupted: drop any stale turn-level error
				// lines. An empty final message is not a reply, so without a
				// cancel the stale error stays visible. The notice itself is
				// appended below, once the undelivered follow-ups are back in
				// the queue it reports on.
				m.dropTransientErrors()
			} else {
				// Record the failure in the transcript (not the persistent
				// banner) and mark it transient: it stays visible until the
				// next reply, so a recovered run doesn't carry the stale
				// error for the rest of the session.
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error(), Transient: true})
			}
		} else if content := strings.TrimSpace(msg.content); content != "" {
			switch {
			case streamIdx >= 0:
				// The reply already streamed into the transcript as it arrived
				// (see appendStreamedContent): reconcile that message with the
				// authoritative final text — self-healing against any dropped
				// delta — instead of appending it a second time.
				m.messages[streamIdx].Content = msg.content
				m.revealAfterTurn(streamIdx, true)
			case content != m.lastParkedReply:
				// Skip the final reply when it duplicates the text already surfaced
				// at the park gate (a run that parked and returned with the same
				// answer, with nothing streamed since).
				m.appendMessage(ChatMessage{Role: "assistant", Content: msg.content})
				m.revealAfterTurn(len(m.messages)-1, false)
			}
			// A reply arrived (even one that duplicates the text already
			// surfaced at the park gate): the run recovered, so drop any
			// stale turn-level error lines after reconciling the final reply.
			m.dropTransientErrors()
		}
		m.lastParkedReply = ""
		if m.session != nil {
			m.contextTokens = m.session.ContextTokens()
			m.sessionUsage = m.session.Usage()
			// Follow-ups released into the ended run that it never consumed:
			// the model never saw them, so re-dispatch them ahead of the queue.
			m.redispatch = append(m.redispatch, m.session.TakeUndelivered()...)
		}
		if msg.err != nil && errors.Is(msg.err, context.Canceled) {
			m.appendMessage(ChatMessage{Role: "agent", Content: m.interruptNotice()})
		}
		// A hold with nothing left to hold is over.
		if len(m.queue)+len(m.redispatch) == 0 {
			m.queueHeld = false
		}
		// Autosave at this turn boundary so /resume never loses more than the
		// turn in flight when the process exits uncleanly. Save failures are
		// logged (see recordSession) and never surface here.
		m.recordSession()
		m.updateViewport()
		// The run ended with messages still queued: dispatch them as fresh turns
		// (resolving slash commands/skills) until one starts a turn or the queue
		// drains; the remainder stay queued and flush on subsequent run ends.
		if cmd := m.flushQueueAsTurn(); cmd != nil {
			m.updateViewport()
			return m, cmd
		}
		// A tail of non-turn entries (skill loads / resolve errors) may have
		// appended notices without starting a turn; re-render so they show now.
		m.updateViewport()
		// Nothing queued started a turn: the composer is the user's again.
		m.ringBell()
		if cmd := m.armSuggestion(msg.err == nil); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case parkMsg:
		if msg.parked {
			// The run parked: the assistant has replied but stays alive (background
			// work pending, or ready for a follow-up). Surface the reply as a
			// durable transcript line and unlock the composer so the user can keep
			// chatting — their input injects into this same run.
			//
			// If the reply already streamed in (appendStreamedContent), reconcile
			// that in-progress message with the authoritative parked text instead
			// of appending it a second time — the same precedence responseMsg
			// applies, for the same reason (see streamingActive's doc).
			streamIdx := -1
			if m.streamingActive && len(m.messages) > 0 {
				streamIdx = len(m.messages) - 1
			}
			m.streamingActive = false
			reply := strings.TrimSpace(msg.reply)
			switch {
			case streamIdx >= 0 && reply != "":
				m.messages[streamIdx].Content = reply
				m.lastParkedReply = reply
				m.revealAfterTurn(streamIdx, true)
			case streamIdx < 0 && reply != "" && reply != m.lastParkedReply:
				m.appendMessage(ChatMessage{Role: "assistant", Content: reply})
				m.lastParkedReply = reply
				m.revealAfterTurn(len(m.messages)-1, false)
			}
			m.parked = true
			m.loading = false
			m.interruptArmed = false
			m.endThoughtStep()
			m.reasoningResetPending = false
			// Same as responseMsg: events this step sent before it parked
			// must not land in the box after this reset.
			m.bumpTurnGen()
			m.stopThinking()
			m.clearRunning()
			if m.isWorking() {
				m.status = "Working in the background — type to add a follow-up"
			} else {
				m.status = ""
			}
			if m.session != nil {
				m.contextTokens = m.session.ContextTokens()
				m.sessionUsage = m.session.Usage()
			}
			m.textarea.Focus()
			// Parked == the agent is idle waiting; release the next queued
			// follow-up now (flips back to loading via releaseQueueFront).
			if m.releaseQueueFront() {
				m.startThinking()
			} else {
				m.ringBell()
			}
		} else {
			// An injected message resumed the run: re-lock the composer and show
			// the working indicator again.
			m.parked = false
			m.loading = true
			m.interruptArmed = false
			m.startThinking()
			// Invalidate any orphan poll wake-up. A poll wake-up only exists to
			// nudge the run if it stays stuck on background work; once that work
			// completes it injects its own result and resumes us here, so a
			// still-pending poll tick would otherwise fire after the turn ends and
			// re-dispatch the finished task as a fresh turn. Each live poll cycle
			// re-arms with the bumped gen, so only the now-moot orphan dies.
			// Reminders ride wakeupGen and are left intact.
			m.pollGen++
		}
		m.updateViewport()
		cmds = append(cmds, m.listenPark())

	case compactResultMsg:
		m.loading = false
		m.status = ""
		m.stopThinking()
		if msg.err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: "compaction failed: " + msg.err.Error()})
		} else if msg.before == msg.after {
			m.appendMessage(ChatMessage{Role: "agent", Content: "Nothing to compact yet."})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: compactNotice(msg.before, msg.after)})
			m.contextTokens = msg.after
			// The context shrank; the spend did not. Re-read the session's own
			// counter rather than deriving anything from msg.
			if m.session != nil {
				m.sessionUsage = m.session.Usage()
			}
		}
		m.updateViewport()
		if cmd := m.flushQueueAsTurn(); cmd != nil {
			m.updateViewport()
			return m, cmd
		}
		m.updateViewport()
		return m, nil

	case loginResultMsg:
		cmd := m.handleLoginResult(msg)
		m.updateViewport()
		return m, cmd

	case compactNoticeMsg:
		m.appendMessage(ChatMessage{Role: "agent", Content: compactNotice(msg[0], msg[1])})
		m.contextTokens = msg[1]
		// Auto-compaction only shrinks the context: the summarising call itself
		// costs tokens, so take the session's total rather than msg's numbers.
		if m.session != nil {
			m.sessionUsage = m.session.Usage()
		}
		// Compaction is done: clear the "Compacting conversation…" status that
		// OnStatus set. If the turn is still in progress (loading), restore the
		// thinking status — a retry that streams a plain answer emits no status
		// of its own, so the compaction line would otherwise stay on screen for
		// the rest of the turn (see retryResumeStatus for the same pattern).
		if m.loading {
			m.startThinking()
		} else {
			m.status = ""
		}
		m.updateViewport()
		return m, m.listenCompact()

	case pruneNoticeMsg:
		m.appendMessage(ChatMessage{Role: "agent", Content: prunedNotice(msg[0], msg[1])})
		m.updateViewport()
		return m, m.listenPrune()

	case shellTickMsg:
		// Periodic refresh so the shell-jobs footer reflects jobs that finish (or
		// are started by the model) while the user is idle. Completion handling no
		// longer lives here: finished background work injects into the live run
		// (shell jobs via SetShellJobs, sub-agents via cogito's auto-injection).
		// It also paces the context gauge: a turn's size changes with every
		// call it makes, and once a second is as often as a gauge ten cells
		// wide can say anything new.
		m.refreshContextTokens()
		m.updateViewport()
		if m.showLogs && m.logOpenID != "" {
			m.syncLogViewport()
		}
		if !m.quitting {
			cmds = append(cmds, m.shellTick())
		}

	case loopTickMsg:
		durableFired := false
		for _, j := range m.loops.Due() {
			durableFired = durableFired || j.Durable
			if c := m.dispatchLoop(j.Prompt); c != nil {
				cmds = append(cmds, c)
			}
		}
		// Due already advanced or removed what fired. Save it now, so a
		// restart cannot fire the same durable slot again.
		if durableFired {
			_ = m.loops.Save(m.loopsPath)
		}
		if !m.quitting {
			cmds = append(cmds, m.loopTick())
		}

	case wakeupScheduledMsg:
		// Arm a timer for the requested delay, then keep listening for more.
		// Poll wake-ups capture pollGen (dropped on park→resume); reminders and
		// self-paced steps capture wakeupGen (dropped only by /loop stop).
		req := chat.WakeupRequest(msg)
		d := time.Duration(req.DelaySeconds) * time.Second
		prompt := req.Prompt
		poll := req.Poll
		gen := m.wakeupGen
		if poll {
			gen = m.pollGen
		}
		cmds = append(cmds,
			tea.Tick(d, func(time.Time) tea.Msg { return wakeupFireMsg{prompt: prompt, gen: gen, poll: poll} }),
			m.listenWakeup(),
		)

	case cronFireMsg:
		if c := m.dispatchLoop(string(msg)); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, m.listenCronFire())

	case wakeupFireMsg:
		// A stale tick — a cancelled self-paced loop, or a poll whose background
		// work already completed (park→resume bumped pollGen): ignore.
		curGen := m.wakeupGen
		if msg.poll {
			curGen = m.pollGen
		}
		if msg.gen != curGen {
			break
		}
		// The delay elapsed: re-run the carried prompt as the next turn. Resolve
		// the payload so a /command re-runs as that command (self-paced loop).
		prompt := strings.TrimSpace(msg.prompt)
		if prompt == "" {
			prompt = "continue"
		}
		action := slash.Resolve(prompt, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
		text := action.Text
		if action.Kind != slash.KindSend || text == "" {
			// Non-KindSend payloads (skill/compact/error) intentionally degrade to literal text — loops carry slash-commands or prompts, not those.
			text = prompt // fall back to the raw text for non-send payloads
		}
		if m.session != nil && m.parked && m.session.Inject(text) {
			m.appendMessage(ChatMessage{Role: "user", Content: prompt})
			m.parked = false
			m.loading = true
			m.interruptArmed = false
			m.startThinking()
			m.updateViewport()
		} else if m.sessionReady && m.session != nil && !m.loading && !m.awaitingApproval && !m.awaitingAsk && !m.awaitingResume {
			m.appendMessage(ChatMessage{Role: "user", Content: prompt})
			m.loading = true
			m.interruptArmed = false
			m.startThinking()
			m.updateViewport()
			cmds = append(cmds, m.sendMessage(text))
		}
		// If a wake-up fires mid-run (loading and not parked) it is dropped: the model drives self-pacing at turn end, so this is rare; cron loops queue instead (see releaseQueueFront).

	case statusMsg:
		m.status = string(msg)
		m.updateViewport()
		// Continue listening for more status updates
		cmds = append(cmds, m.listenStatus())

	case reasoningEventsMsg:
		// Applied in order — the exact order they were read off reasoningChan,
		// which is itself the exact order the producer emitted them in (see
		// reasoningChan's doc). One updateViewport for the whole batch, not
		// one per event: that's the render-cost coalescing: see
		// listenReasoningEvents.
		for _, ev := range msg {
			switch ev.kind {
			case reasoningEventBoundary:
				// Same staleness check as the two delta kinds below. This one
				// was omitted on the argument that a stale boundary only
				// repaints a complete (if outdated) block, which responseMsg
				// clears at every turn end anyway — but the boundary does not
				// have to arrive BEFORE that reset. reasoningChan is buffered
				// and bubbletea relays each listen Cmd on its own goroutine,
				// so turn N's last boundary can land after responseMsg cleared
				// the box AND after turn N+1 dispatched. It then repaints turn
				// N's trace into an empty box, where it stays until N+1's
				// first delta disarms reasoningResetPending and starts over:
				// one frame of the previous answer's thinking, every time the
				// user writes.
				if ev.gen != m.currentTurnGen() {
					continue
				}
				// This step just ended: its complete text is authoritative,
				// so it replaces what streamed, and the step folds into the
				// transcript (see thought.go). The NEXT step's streamed
				// deltas are a fresh trace, not a continuation of this one.
				// Mark the next delta to start over rather than append (see
				// reasoningResetPending's doc).
				if th := m.stepThoughtEntry(); th != nil {
					th.Content = ev.text
					m.reasoning = ""
				} else {
					m.reasoning = ev.text
				}
				m.endThoughtStep()
				m.reasoningResetPending = true
			case reasoningEventDelta:
				// A delta stamped with an older generation than the one
				// currently in flight belongs to a turn that has already
				// ended — a new one is running now. Drop it rather than
				// resuming/appending onto a trace that isn't this turn's.
				// See turnGen's doc.
				if ev.gen != m.currentTurnGen() {
					continue
				}
				if m.reasoningResetPending {
					m.reasoning = ""
					m.reasoningResetPending = false
				}
				// More thinking after this step's answer already started:
				// it belongs to the entry the step folded into.
				if th := m.stepThoughtEntry(); th != nil {
					th.Content += ev.text
					continue
				}
				if m.reasoning == "" {
					m.reasoningSince = time.Now()
				}
				m.reasoning += ev.text
			case reasoningEventContentDelta:
				// Same staleness check as reasoningEventDelta above, and for
				// the same reason — but here a stale delta wouldn't just show
				// wrong text in a box that resets next turn, it would
				// fabricate a whole new transcript entry (see
				// appendStreamedContent's doc and the orphan-bubble test).
				if ev.gen != m.currentTurnGen() {
					continue
				}
				// The answer starts: the thinking that led to it folds
				// into the transcript, above the reply.
				if m.loading && ev.text != "" {
					m.foldReasoning()
				}
				m.appendStreamedContent(ev.text)
			}
		}
		m.updateViewport()
		// Continue listening for more reasoning events
		cmds = append(cmds, m.listenReasoningEvents())

	case toolCallMsg:
		// A request can have been waiting since before yolo was turned on (a
		// parallel call, a sub-agent's). Answer it the way the session would
		// now, instead of raising a prompt yolo would not have raised.
		if m.session != nil && m.session.AutoApprove() {
			cmds = append(cmds, m.answerToolCall(chat.ToolCallResponse{Approved: true}))
			break
		}
		m.pendingTool = (*chat.ToolCallRequest)(&msg)
		m.awaitingApproval = true
		m.approvalEditing = false // every approval starts in key-driven choice mode
		m.loading = false         // Allow user input for approval
		m.textarea.Focus()        // Ensure textarea is focused for input
		m.updateViewport()
		m.ringBell()
		// The next request is read only once this one is answered, in
		// resolveApproval. Both channels are unbuffered, so that keeps exactly
		// one caller waiting on toolResponseChan, and each answer reaches the
		// call it was given for.

	case toolAnsweredMsg:
		cmds = append(cmds, m.listenToolRequest())

	case askMsg:
		req := chat.AskRequest(msg)
		m.pendingAsk = &req
		m.awaitingAsk = true
		m.askList = &render.SelectList{
			Items:       req.Options,
			MultiSelect: req.MultiSelect,
			MaxVisible:  8,
		}
		if req.MultiSelect {
			m.askList.Checked = make([]bool, len(req.Options))
		}
		m.loading = false
		m.textarea.Focus()
		m.updateViewport()
		m.ringBell()
		cmds = append(cmds, m.listenAskRequest())

	case agentEventMsg:
		// Update value-receiver copy via pointer helper, then write back.
		ev := m.withStreamStats(chat.AgentEvent(msg))
		am := m
		(&am).applyAgentEvent(ev)
		m = am
		// On completion, always show the stats marker line (e.g.
		// "sub-agent explore finished · 3 tools · …"); when the agent produced a
		// final result, also surface it inline as one labeled block. Per-tool
		// activity stays in the Ctrl+O log viewer.
		if line := agentTranscriptLine(ev); line != "" {
			m.appendMessage(ChatMessage{Role: "agent", AgentID: ev.ID, Content: line})
		}
		if ev.Status == chat.AgentStatusCompleted && strings.TrimSpace(ev.Result) != "" {
			typ := ev.Type
			if typ == "" {
				typ = "agent"
			}
			m.appendMessage(ChatMessage{
				Role:    "agent_result",
				Name:    typ,
				AgentID: ev.ID,
				// Not a tool result, so no formatter name applies — the agent's
				// own free-text answer passes through as-is (or, on the rare
				// chance it's a JSON object, degrades to rows).
				Content: chat.PreviewResult("", ev.Result, toolResultPreviewLines),
			})
		}
		m.updateViewport()
		if m.showLogs && m.logOpenID != "" {
			m.syncLogViewport()
		}
		// Continue listening for more agent events
		cmds = append(cmds, m.listenAgentEvents())

	case suggestTickMsg:
		return m, m.fetchSuggestion(msg)

	case suggestResultMsg:
		m.takeSuggestion(msg)
		return m, nil

	case autoApprovedMsg:
		m.appendMessage(ChatMessage{Role: "agent", Content: autoApprovedLine(msg)})
		m.updateViewportFollow()
		return m, m.listenAutoApproved()

	case toolStartMsg:
		m.startTool(chat.ToolStart(msg))
		m.updateViewport()
		cmds = append(cmds, m.listenToolStart())

	case toolResultMsg:
		res := chat.ToolResult(msg)
		if res.AgentID == "" {
			// Root agent: the running block gives way to the finished one.
			elapsed := m.finishTool(res)
			m.appendMessage(toolMessage(res, elapsed))
			m.updateViewport()
		} else {
			// Sub-agent: append a compact, body-less line to its inline thread.
			// The output body lives in the Ctrl+O log viewer.
			label := chat.FormatToolCall(res.Name, res.Arguments)
			if nl := strings.IndexByte(label, '\n'); nl >= 0 {
				label = label[:nl]
			}
			if label == "" {
				label = res.Name
			}
			m.appendMessage(ChatMessage{Role: "agent_tool", Name: res.Name, Arguments: res.Arguments, AgentID: res.AgentID, Content: label})
			m.updateViewport()
			if m.showLogs && m.logOpenID != "" {
				m.syncLogViewport()
			}
		}
		// A step just completed: release the next queued follow-up into the run.
		m.releaseQueueFront()
		// Continue listening for more tool results
		cmds = append(cmds, m.listenToolResult())

	case animTickMsg:
		m.advanceAnimation()
		m.updateViewport()
		cmds = append(cmds, m.listenAnim())

	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		if m.loading {
			m.updateViewport()
		}
	}

	// `G` jumps to the newest output, but only when it is not being typed into a
	// message AND there is somewhere to jump back from. vim-style, matching the
	// ↑↓ scroll keys already advertised. Without the AtBottom check, `G` fires
	// whenever the composer is empty — which is exactly the state the user's
	// FIRST keystroke of a new message finds it in, silently eating it.
	if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyRunes && len(k.Runes) == 1 &&
		k.Runes[0] == 'G' && !m.viewport.AtBottom() && strings.TrimSpace(m.textarea.Value()) == "" {
		m.viewport.GotoBottom()
		return m, tea.Batch(cmds...)
	}

	// Update textarea. The composer is always editable — even while a run is in
	// flight — so the user can type follow-ups that queue into the live run.
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	m.completion.sync(m.textarea.Value())
	// The keystroke just changed the composer's height — the `/` completion
	// popup opened, narrowed or closed — so the viewport's budget is stale
	// until something re-runs it. Nothing else on this path does: syncLayout
	// runs from updateViewport, and typing does not touch the transcript.
	m.reflowLayout()

	// Update viewport
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// fencedListing wraps a model listing in a code fence so the transcript leaves
// its columns alone. An "agent" line is rendered as markdown, where a plain
// listing loses its two-space indent and "* current" becomes an ordinary
// bullet: the marker column, which is the only thing the listing has to say,
// is exactly what gets eaten.
func fencedListing(listing string) string {
	return "```\n" + listing + "```"
}

// dispatchInput echoes the user's literal input to the transcript, resolves it
// as a slash command / skill / message, and starts the appropriate action.
// Returns the command to run (nil for actions that don't start a turn, e.g. a
// skill load or a resolve error). Shared by the Enter handler and the queue
// flush so typed-while-idle and queued-while-busy input behave identically.
func (m *Model) dispatchInput(input string) tea.Cmd {
	if m.boot != nil && !m.boot.collapsed {
		m.boot.collapsed = true
	}
	m.appendMessage(ChatMessage{Role: "user", Content: input})
	return m.dispatchResolved(input)
}

// dispatchResolved resolves and starts input WITHOUT echoing it to the
// transcript — for re-dispatched undelivered follow-ups, whose transcript
// line was already written when they were released into the previous run.
func (m *Model) dispatchResolved(input string) tea.Cmd {
	action := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
	switch action.Kind {
	case slash.KindError:
		m.appendMessage(ChatMessage{Role: "error", Content: action.Err})
		return nil
	case slash.KindLoadSkill:
		notice, err := m.session.LoadSkill(action.Skill)
		if err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: notice})
		}
		return nil
	case slash.KindCompact:
		m.loading = true
		m.interruptArmed = false
		m.status = "Compacting conversation…"
		return m.compactCmd()
	case slash.KindModelPick:
		return m.openModelPicker()
	case slash.KindModelList:
		// Bounded: dispatchResolved runs on the Update goroutine, so an endpoint
		// that accepts the connection and never answers would freeze the TUI.
		lookupCtx, cancel := context.WithTimeout(m.ctx, chat.ModelListTimeout)
		defer cancel()
		// action.Endpoint ("" = the current endpoint) lets /models list ANY
		// endpoint without switching to it, unlike /model's picker which is
		// always the current one.
		models, _, err := m.session.ModelChoices(lookupCtx, action.Endpoint)
		name := m.session.ActiveProviderName()
		if action.Endpoint != "" {
			name = action.Endpoint
			if e, ok := m.providerEntry(action.Endpoint); ok {
				name = e.Name
			}
		}
		if err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		} else {
			listing := fencedListing(chat.FormatProviderModelList(name, models, m.session.Model()))
			m.appendMessage(ChatMessage{Role: "agent", Content: listing})
		}
		return nil
	case slash.KindModelReset:
		model, err := m.session.ResetModel()
		if err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: "model: " + model + " · " + theme.ModelResetNotice})
		}
		return nil
	case slash.KindModelDefault:
		notice, err := m.session.SetDefaultModel(m.ctx, action.Model)
		var unserved *chat.UnservedModelError
		switch {
		case errors.As(err, &unserved):
			m.appendMessage(
				ChatMessage{Role: "error", Content: unserved.Headline()},
				ChatMessage{Role: "agent", Content: fencedListing(unserved.Listing())})
		case err != nil:
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		default:
			m.appendMessage(ChatMessage{Role: "agent", Content: notice + " · " + theme.ProviderSavedDefault})
		}
		m.refreshContextTokens()
		return nil
	case slash.KindModelSet:
		notice, err := m.session.SwitchModel(m.ctx, action.Model)
		var unserved *chat.UnservedModelError
		switch {
		case errors.As(err, &unserved):
			// Two transcript lines, not one. The refusal itself is prose and
			// wraps happily, but the listing must not: an "error" line goes
			// through render.Wrap, which re-flows on word boundaries, drops the
			// leading indent and clips a long model ID, so at a narrow width
			// the marker column stops meaning anything.
			m.appendMessage(
				ChatMessage{Role: "error", Content: unserved.Headline()},
				ChatMessage{Role: "agent", Content: fencedListing(unserved.Listing())})
		case err != nil:
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		default:
			m.appendMessage(ChatMessage{Role: "agent", Content: notice + " · " + theme.ModelSessionOnly})
		}
		m.refreshContextTokens()
		return nil
	case slash.KindLoopStart:
		return m.startLoop(action)
	case slash.KindLoopStop:
		m.appendMessage(ChatMessage{Role: "agent", Content: m.stopLoop(action.LoopID)})
		return nil
	case slash.KindLoopList:
		m.appendMessage(ChatMessage{Role: "agent", Content: m.listLoops()})
		return nil
	case slash.KindGoalSet:
		m.session.SetGoal(action.Text)
		m.appendMessage(ChatMessage{Role: "agent", Content: theme.Goal + " Goal set: " + action.Text + "\nWorking on it now, re-checking until it's met. Ctrl+C pauses it; /goal clear drops it."})
		return m.startGoalTurn(chat.GoalKickoff(action.Text))
	case slash.KindGoalResume:
		if m.session.ResumeGoal() {
			goal := m.session.Goal()
			m.appendMessage(ChatMessage{Role: "agent", Content: theme.Goal + " Goal resumed: " + goal + "\nWorking on it now."})
			return m.startGoalTurn(chat.GoalResumeKickoff(goal))
		} else if m.session.Goal() != "" {
			m.appendMessage(ChatMessage{Role: "agent", Content: "The goal is not paused."})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: "No goal to resume. Use /goal <text> to set one."})
		}
		return nil
	case slash.KindGoalShow:
		if g := m.session.Goal(); g != "" {
			if m.session.GoalPaused() {
				g += " (paused · /goal resume)"
			}
			m.appendMessage(ChatMessage{Role: "agent", Content: theme.Goal + " Current goal: " + g})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: "No goal set. Use /goal <text> to set one."})
		}
		return nil
	case slash.KindGoalClear:
		if m.session.Goal() != "" {
			m.session.ClearGoal()
			// No turn follows to autosave it, so save now.
			m.recordSession()
			m.appendMessage(ChatMessage{Role: "agent", Content: "Goal cleared."})
		} else {
			m.appendMessage(ChatMessage{Role: "agent", Content: "No goal to clear."})
		}
		return nil
	case slash.KindApprove:
		m.setApprovalMode(action.Mode)
		return nil
	case slash.KindClassifier:
		switch {
		case action.ClassifierOff:
			m.useClassifier("", "", true)
		case action.Endpoint != "":
			m.useClassifier(action.Endpoint, action.Model, false)
		default:
			m.openProviderPicker(pickerClassifier)
		}
		return nil
	case slash.KindYolo:
		on := !m.session.AutoApprove()
		if action.YoloOn != nil {
			on = *action.YoloOn
		}
		m.session.SetAutoApprove(on)
		notice := theme.YoloOff
		if on {
			notice = theme.YoloOn
		}
		m.appendMessage(ChatMessage{Role: "agent", Content: notice})
		return nil
	case slash.KindLogin:
		if action.Provider == "" {
			m.openProviderPicker(pickerLogin)
			return nil
		}
		e, ok := m.providerEntry(action.Provider)
		if !ok {
			m.appendMessage(ChatMessage{Role: "error", Content: fmt.Sprintf("unknown provider %q · /login lists them", action.Provider)})
			return nil
		}
		if e.Kind != endpoint.KindProvider {
			// A config.yaml default or named entry: /login authenticates
			// registry providers only now that /endpoint lists (and
			// switches to) everything, including these. Without this gate,
			// e.LoginKind's zero value equals provider.LoginNone for a
			// yaml entry (whose Def is the zero Definition), so useProvider
			// would happily open a model picker for it and switch — the
			// exact bypass /endpoint's split was meant to close.
			m.appendMessage(ChatMessage{Role: "error", Content: fmt.Sprintf(theme.LoginNotAProvider, e.ID, e.ID)})
			return nil
		}
		return m.useProvider(e, true)
	case slash.KindLogout:
		if action.Provider == "" {
			m.openProviderPicker(pickerLogout)
			return nil
		}
		m.logout(action.Provider)
		return nil
	case slash.KindEndpoint:
		if action.Endpoint == "" {
			m.openEndpointPicker()
			return nil
		}
		e, ok := m.providerEntry(action.Endpoint)
		if !ok {
			m.appendMessage(ChatMessage{Role: "error", Content: fmt.Sprintf(theme.EndpointUnknown, action.Endpoint)})
			return nil
		}
		return m.useEndpoint(e)
	case slash.KindEndpointAdd:
		m.openEndpointForm()
		return nil
	case slash.KindResume:
		return m.startResume(action.ResumeAll, action.ResumeID)
	case slash.KindSettings:
		m.runSettings(action)
		return nil
	case slash.KindAttach:
		switch action.AttachOp {
		case slash.AttachStage:
			m.pending = append(m.pending, attachstage.StagedFile{Path: action.AttachPath, Transcribe: action.Transcribe})
			mode := "default"
			if action.Transcribe {
				mode = "transcribe"
			}
			m.appendMessage(ChatMessage{Role: "agent", Content: "attached: " + filepath.Base(action.AttachPath) + " (" + mode + ") — sends with your next message"})
		case slash.AttachList:
			if len(m.pending) == 0 {
				m.appendMessage(ChatMessage{Role: "agent", Content: "nothing staged"})
			} else {
				var b strings.Builder
				for i, s := range m.pending {
					if i > 0 {
						b.WriteString("\n")
					}
					b.WriteString(filepath.Base(s.Path))
				}
				m.appendMessage(ChatMessage{Role: "agent", Content: b.String()})
			}
		case slash.AttachClear:
			n := len(m.pending)
			m.pending = nil
			m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf("cleared %d staged attachment(s)", n)})
		}
		return nil
	case slash.KindAbout:
		m.appendMessage(ChatMessage{Role: "agent", Content: aboutText(m.cfg)})
		return nil
	default: // slash.KindSend
		files, overrides := attachstage.BuildSend(m.pending, action)
		m.loading = true
		m.interruptArmed = false
		m.status = ""
		// Attach user-supplied image files to the ChatMessage so the
		// viewport can render them inline alongside the user's text.
		if imgs := attachmentImages(files); len(imgs) > 0 {
			m.messages[len(m.messages)-1].Images = imgs
		}
		if len(files) == 0 {
			return m.sendMessage(action.Text)
		}
		return m.sendWithAttachmentsCmd(action.Text, files, overrides)
	}
}

// pushHistory records a submitted input for ↑/↓ recall, skipping an exact
// repeat of the most recent entry (shell-style). Navigation resets to the
// draft position so the next ↑ recalls the newest entry.
func (m *Model) pushHistory(s string) {
	if s == "" {
		return
	}
	if n := len(m.history); n > 0 && m.history[n-1] == s {
		m.histPos = len(m.history)
		m.histDraft = ""
		return
	}
	m.history = append(m.history, s)
	m.histPos = len(m.history)
	m.histDraft = ""
}

// historyUp recalls the previous (older) submitted input into the composer.
// The current draft is stashed on the first press so historyDown can restore it.
func (m *Model) historyUp() {
	n := len(m.history)
	if n == 0 {
		return
	}
	if m.histPos >= n {
		m.histDraft = m.textarea.Value()
		m.histPos = n - 1
	} else if m.histPos > 0 {
		m.histPos--
	} else {
		return
	}
	m.textarea.SetValue(m.history[m.histPos])
}

// historyDown recalls the next (newer) submitted input, restoring the stashed
// draft once navigation runs past the newest entry.
func (m *Model) historyDown() {
	n := len(m.history)
	if n == 0 || m.histPos >= n {
		return
	}
	m.histPos++
	if m.histPos >= n {
		m.textarea.SetValue(m.histDraft)
	} else {
		m.textarea.SetValue(m.history[m.histPos])
	}
}

// openModelPicker resets the picker and starts a separately identifiable
// asynchronous endpoint lookup.
func (m *Model) openModelPicker() tea.Cmd {
	m.modelPickerRequest++
	m.modelPicker.open(m.modelPickerRequest)
	return m.loadModelsCmd(m.modelPickerRequest)
}

// openProviderModelPicker is the model picker for a provider the session is
// about to switch to: the switch happens on Enter, so Esc leaves the session
// exactly as it was (the login, if any, is kept).
func (m *Model) openProviderModelPicker(e chat.ProviderEntry) tea.Cmd {
	m.modelPickerRequest++
	m.modelPicker.open(m.modelPickerRequest)
	m.modelPicker.target = &e
	return m.loadModelsCmd(m.modelPickerRequest)
}

// loadModelsCmd bounds model discovery without blocking Bubble Tea's Update
// goroutine.
func (m Model) loadModelsCmd(requestID uint64) tea.Cmd {
	target := m.modelPicker.target
	return func() tea.Msg {
		lookupCtx, cancel := context.WithTimeout(m.ctx, chat.ModelListTimeout)
		defer cancel()
		id := ""
		if target != nil {
			id = target.ID
		}
		models, partial, err := m.session.ModelChoices(lookupCtx, id)
		return modelListMsg{requestID: requestID, models: models, partial: partial, err: err}
	}
}

// startGoalTurn starts the turn /goal and /goal resume send, so the agent
// reacts to the goal at once. The notice already shows the goal, so the
// kickoff text is not echoed to the transcript.
func (m *Model) startGoalTurn(kickoff string) tea.Cmd {
	m.loading = true
	m.interruptArmed = false
	m.status = ""
	return m.sendMessage(kickoff)
}

// sendMessage sends a message to the AI. bumpTurnGen runs synchronously here
// — before the Cmd below is ever executed by bubbletea — marking this as a
// genuinely NEW turn (see turnGen's doc) so any reasoningChan event a LATER
// turn's OnStream stamps is distinguishable from a straggler out of this one.
func (m Model) sendMessage(text string) tea.Cmd {
	m.bumpTurnGen()
	return func() tea.Msg {
		response, err := m.session.SendMessage(text)
		return responseMsg{content: response, err: err}
	}
}

// sendWithAttachmentsCmd sends a message with staged + inline @path attachments.
// Blocked entries and clear-on-success are handled in the responseMsg handler.
// bumpTurnGen: see sendMessage's doc.
func (m Model) sendWithAttachmentsCmd(text string, files []string, overrides map[string]attachments.Override) tea.Cmd {
	m.bumpTurnGen()
	return func() tea.Msg {
		reply, blocked, err := m.session.SendWithAttachments(m.ctx, text, files, overrides)
		return responseMsg{content: reply, err: err, blocked: blocked, images: attachmentImages(files)}
	}
}

// attachmentImages reads image files from disk so the TUI can display them
// inline alongside the user's message. Non-image files and unreadable files
// are silently skipped.
func attachmentImages(files []string) []chat.ToolImage {
	var imgs []chat.ToolImage
	id := 0
	for _, f := range files {
		if attachments.Sniff(f) != attachments.KindImage {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		mime := "image/png"
		switch ext := filepath.Ext(f); ext {
		case ".jpg", ".jpeg":
			mime = "image/jpeg"
		case ".gif":
			mime = "image/gif"
		case ".webp":
			mime = "image/webp"
		case ".bmp":
			mime = "image/bmp"
		}
		id++
		imgs = append(imgs, chat.ToolImage{ID: id, Data: data, MIME: mime})
	}
	return imgs
}

// shellTickMsg drives a periodic refresh of the shell-jobs footer.
type shellTickMsg struct{}

// shellTick schedules the next shell-jobs footer refresh.
func (m Model) shellTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return shellTickMsg{} })
}

// loopTickMsg drives the cron scheduler poll.
type loopTickMsg struct{}

// loopTick schedules the next scheduler poll.
func (m Model) loopTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return loopTickMsg{} })
}

// wakeupScheduledMsg is emitted when the agent schedules an in-session wake-up.
type wakeupScheduledMsg chat.WakeupRequest

// wakeupFireMsg is emitted when a scheduled wake-up's delay elapses. poll marks
// a tick that watches background work; it is validated against pollGen rather
// than wakeupGen so only poll ticks are dropped on park→resume.
type wakeupFireMsg struct {
	prompt string
	gen    int
	poll   bool
}

// listenWakeup waits for the agent to schedule a wake-up.
// cronFireMsg carries the prompt of a cron job run now via cron_trigger.
type cronFireMsg string

func (m Model) listenCronFire() tea.Cmd {
	return func() tea.Msg {
		select {
		case p := <-m.cronFireChan:
			return cronFireMsg(p)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m Model) listenWakeup() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.wakeupChan:
			return wakeupScheduledMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenPark waits for a park/resume signal from the live run.
func (m Model) listenPark() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-m.parkChan:
			return parkMsg(ev)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenCompact waits for an auto-compaction notice from the session.
func (m Model) listenCompact() tea.Cmd {
	return func() tea.Msg {
		select {
		case v := <-m.compactChan:
			return compactNoticeMsg(v)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenPrune waits for a tool-output pruning notice from the session.
func (m Model) listenPrune() tea.Cmd {
	return func() tea.Msg {
		select {
		case v := <-m.pruneChan:
			return pruneNoticeMsg(v)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// compactCmd runs a manual /compact (an LLM call) off the event loop.
func (m Model) compactCmd() tea.Cmd {
	return func() tea.Msg {
		before, after, err := m.session.CompactHistory()
		return compactResultMsg{before: before, after: after, err: err}
	}
}

// listenStatus listens for status updates from the session
func (m Model) listenStatus() tea.Cmd {
	return func() tea.Msg {
		select {
		case status := <-m.statusChan:
			return statusMsg(status)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenReasoningEvents listens for reasoning updates — both step-boundary
// (Callbacks.OnReasoning) and live streamed deltas (Callbacks.OnStream) — on
// the single reasoningChan, and re-arms itself after every delivery: a
// listener that does not re-arm delivers exactly one event/batch and then
// goes silent.
//
// Ordering note: reasoningChan carries both kinds precisely so there is only
// ONE listener goroutine for reasoning updates. Two separate channels (one
// per kind, each with its own listener) would let bubbletea's independent
// per-Cmd goroutines relay them to Update out of the producer's own order —
// see reasoningChan's doc for the failure mode that caused. One channel, one
// listener, means Update sees exactly the order they were sent in.
//
// Render-cost note: a token stream can deliver far faster than a terminal
// should repaint (an updateViewport per token would be one render per
// keystroke-equivalent). Rather than invent a timer, this drains whatever is
// already queued on the channel — accumulated in order, boundary and delta
// events alike — into a single reasoningEventsMsg before returning. Because
// bubbletea does not call this again until it has processed the previous
// message (and re-armed via the cmds append below), the render rate is
// naturally bounded by how fast Update can process a frame, not by the token
// rate: a burst that arrives while one frame is still rendering collapses
// into the next frame instead of queuing one render per token. The final
// drain is unconditional (it runs once at least, then loops only while more
// is already buffered), so the last event of a turn is always included in
// the message it returns, never left for a call that never comes.
func (m Model) listenReasoningEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case first := <-m.reasoningChan:
			events := []reasoningEvent{first}
		drain:
			for {
				select {
				case more := <-m.reasoningChan:
					events = append(events, more)
				default:
					break drain
				}
			}
			return reasoningEventsMsg(events)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenAgentEvents listens for sub-agent lifecycle events from the session
func (m Model) listenAgentEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-m.agentEventChan:
			return agentEventMsg(ev)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenToolRequest listens for tool call requests from the session
func (m Model) listenToolRequest() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.toolRequestChan:
			return toolCallMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenToolStart listens for tool calls that start running.
func (m Model) listenToolStart() tea.Cmd {
	return func() tea.Msg {
		select {
		case ts := <-m.toolStartChan:
			return toolStartMsg(ts)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenToolResult listens for finished tool results from the session.
func (m Model) listenToolResult() tea.Cmd {
	return func() tea.Msg {
		select {
		case res := <-m.toolResultChan:
			return toolResultMsg(res)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// listenAskRequest listens for ask_user requests from the session.
func (m Model) listenAskRequest() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.askRequestChan:
			return askMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}

// handleToolApproval handles a free-form adjustment typed in edit mode. An empty
// input is treated as a plain approval. (Choice-mode keypresses are handled by
// the interception block in Update and never reach here.)
func (m Model) handleToolApproval(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	if input == "" {
		return m.resolveApproval(chat.ToolCallResponse{Approved: true})
	}
	return m.resolveApproval(chat.ToolCallResponse{Approved: true, Adjustment: input})
}

// resolveApproval finalizes a tool-approval decision: tear down approval state,
// resume the spinner, and hand the response back to the waiting callback.
func (m Model) resolveApproval(resp chat.ToolCallResponse) (tea.Model, tea.Cmd) {
	m.awaitingApproval = false
	m.approvalEditing = false
	m.pendingTool = nil
	m.textarea.Reset()
	// The trace that led to this call is answered now; leaving it up reads as
	// the model re-thinking a step the user already decided.
	m.reasoning = ""
	m.reasoningResetPending = false
	m.loading = true
	m.status = theme.StatusRunning
	m.updateViewportFollow()
	return m, m.answerToolCall(resp)
}

// answerToolCall hands resp to the call waiting on toolResponseChan. The
// toolAnsweredMsg it returns re-arms the request listener only after the send
// completed, which is what serializes approvals: see the toolCallMsg case.
func (m Model) answerToolCall(resp chat.ToolCallResponse) tea.Cmd {
	return func() tea.Msg {
		m.toolResponseChan <- resp
		return toolAnsweredMsg{}
	}
}

// resolveAsk finalizes an ask_user answer — echoing it to the transcript,
// tearing down the pending-ask state (including the dialog's selection list),
// resuming the spinner, and handing the answer to the blocked
// askResponseChan reader. Shared by the dialog-pick and free-text branches of
// the KeyEnter case, which used to each duplicate this teardown inline.
func (m Model) resolveAsk(answer string) (tea.Model, tea.Cmd) {
	m.appendMessage(ChatMessage{Role: "user", Content: answer})
	m.textarea.Reset()
	m.awaitingAsk = false
	m.pendingAsk = nil
	m.askList = nil
	m.loading = true
	m.startThinking()
	m.updateViewportFollow()
	m.askResponseChan <- answer
	return m, nil
}

// updateDimensions re-budgets every component against the current terminal
// size. The footer's share of that budget is not a constant: Presenter.Footer
// emits between one row (the help line alone) and seven (new-output marker,
// error line, and four job-status rows), so it is measured against the very
// ViewState View will render from rather than guessed.
func (m *Model) updateDimensions() {
	vs := m.viewState()
	m.applyDimensions(vs, m.footerHeight(vs))
}

// footerHeight asks how tall the footer is for this frame, via renderFooter
// so this and the frame's eventual Footer text (rendered later, in View) come
// from the same cached render rather than each paying for their own.
func (m Model) footerHeight(vs render.ViewState) int {
	_, h := m.renderFooter(vs, m.width)
	return h
}

// footerCache is renderFooter's memo: the fields Footer actually reads from a
// ViewState (see render.Presenter.Footer), plus the width, and the rendered
// string and height that produced. A ViewState carries plenty Footer never
// looks at — Messages, Reasoning, Dialogs — so keying on those exact fields
// means an unrelated change (a streamed token, a reasoning trace update)
// leaves the cache valid, while anything that would actually change Footer's
// output invalidates it correctly. Footers is flattened to a string because
// []render.FooterRow isn't comparable with ==; FooterRow's own fields are all
// plain strings/ints, so joining them with NUL separators can't collide two
// distinct row lists onto the same key.
type footerCache struct {
	valid             bool
	width             int
	help, badges, err string
	newOutput         bool
	footers           string
	rendered          string
	height            int
}

// footerCacheKey builds the comparable snapshot of v's Footer-relevant
// fields at width w.
func footerCacheKey(v render.ViewState, w int) footerCache {
	var rows strings.Builder
	for _, r := range v.Footers {
		fmt.Fprintf(&rows, "%d\x00%s\x00%s\x00", r.Kind, r.Glyph, r.Text)
	}
	return footerCache{
		width:     w,
		help:      v.Help,
		badges:    v.Badges,
		err:       v.Err,
		newOutput: v.NewOutput,
		footers:   rows.String(),
	}
}

// renderFooter returns Presenter.Footer(v, w) and Presenter.FooterHeight(v,
// w), reusing the model's cached pair when v's footer-relevant fields and w
// exactly match what produced it. Both are pure functions of exactly those
// fields, so a key match proves the cached pair is still exactly what a
// fresh call of each would return. Without this, syncLayout's height budget
// (which must run before body can be laid out, since the viewport's height
// depends on it) and View's own frame composition (which needs the footer's
// actual text) each rendered Footer themselves — twice per frame, on every
// spinner tick while loading (~80ms) — this is Task 10a's third carried
// defect.
//
// The height still goes through Presenter.FooterHeight rather than
// lipgloss.Height(rendered): the model asks the presenter, the same rule
// ContentWidth documents, rather than measuring chrome it did not compose.
// That does mean a genuine cache MISS (footer content actually changed —
// a job row appearing, an error arriving) renders Footer twice, once
// directly and once inside FooterHeight; a HIT — the common case, since most
// re-renders (spinner ticks, streamed tokens) don't touch Footer's inputs —
// renders it zero times, reusing both cached values.
//
// footerCache lives behind a pointer (see the Model field comment), so this
// value-receiver method can still update it in place; on a bare Model{}
// literal (footerCache nil, as in tests that skip newTestModel) it falls back
// to an uncached render, matching msgViewCache's nil-safety precedent.
func (m Model) renderFooter(v render.ViewState, w int) (string, int) {
	key := footerCacheKey(v, w)
	if m.footerCache != nil && m.footerCache.valid &&
		key.width == m.footerCache.width &&
		key.help == m.footerCache.help &&
		key.badges == m.footerCache.badges &&
		key.err == m.footerCache.err &&
		key.newOutput == m.footerCache.newOutput &&
		key.footers == m.footerCache.footers {
		return m.footerCache.rendered, m.footerCache.height
	}
	rendered := m.presenter.Footer(v, w)
	height := m.presenter.FooterHeight(v, w)
	if m.footerCache != nil {
		key.rendered = rendered
		key.height = height
		key.valid = true
		*m.footerCache = key
	}
	return rendered, height
}

// syncLayout re-budgets the viewport when the chrome around it has changed
// since the last budget — a sub-agent job row appearing mid-turn, a loop
// starting, an error line arriving, an approval card or a /resume picker
// docking above the composer on a surface that overlays dialogs. Without it
// the budget only ever moved on a WindowSizeMsg, so chrome that grew by two
// rows produced a frame taller than the screen: harmless spill into
// scrollback on the inline widget, but on the alt screen bubbletea truncates
// the composed frame from the TOP and the header walks off screen.
//
// It compares the whole layoutBudget, not just the footer's share: the footer
// was the only piece that moved when this was written, and a dialog block
// (which can be a dozen rows) moved without the budget noticing at all.
func (m *Model) syncLayout(vs render.ViewState) {
	// Before the first WindowSizeMsg there is no real terminal size to budget
	// against; updateDimensions owns that first pass.
	if m.height == 0 {
		return
	}
	fh := m.footerHeight(vs)
	if m.layoutBudget(vs, fh) != m.chromeBudget {
		m.applyDimensions(vs, fh)
	}
}

// reflowLayout re-budgets the viewport when the chrome around it changed but
// the transcript did not — the composer growing or shrinking under the user's
// own keystrokes, which is the `/` completion popup opening (up to a dozen
// rows), narrowing as the verb is typed, and closing again.
//
// syncLayout's only other caller is updateViewport, which the keystroke path
// never reaches, so the viewport kept the height it was budgeted for BEFORE
// the popup existed: the composed frame ran over the terminal while the popup
// was open and, once a tick had re-budgeted it, came up short the moment it
// closed. Bubble Tea's inline renderer drops lines from the TOP of an
// over-tall frame and erases the screen below a short one, so the transcript
// jumped away and snapped back on the next one-second shellTick — the flicker
// a user sees while typing.
//
// The content is untouched, so this re-budgets and re-pins the scroll position
// rather than re-rendering the whole transcript (glamour included) on every
// keystroke.
func (m *Model) reflowLayout() {
	if m.height == 0 {
		return
	}
	// Captured before syncLayout can change the height AtBottom() is relative
	// to — the same ordering hazard updateViewport guards against.
	wasAtBottom := m.viewport.AtBottom()
	vs := m.viewState()
	vs.NewOutput = m.showingViewport() && !wasAtBottom
	before := m.viewport.Height
	m.syncLayout(vs)
	// A taller viewport can leave YOffset past the last line, which reads as
	// blank rows below the newest output for a user who was pinned there.
	if m.viewport.Height != before && wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// effectiveHeight is m.height clamped to maxHeight (0 = no limit) — the
// actual number of rows the frame is budgeted against. applyDimensions uses
// it to size the viewport; View passes the same value to Presenter.Frame as
// its height budget, so a presenter that centres an overlay against h (Task
// 11) sizes against the terminal rows this session actually uses, not the
// raw (possibly larger) terminal height a maxHeight flag deliberately caps.
func (m Model) effectiveHeight() int {
	if m.maxHeight > 0 && m.height > m.maxHeight {
		return m.maxHeight
	}
	return m.height
}

// renderComposer builds the composer block: the `/` completion popup, the
// pending-message queue, and the input line (or the not-ready notice, or
// nothing at all in the modes where the viewport's own dialog block carries
// the choice row). Shared by View (which places the string via Frame) and
// applyDimensions (which must measure it before the viewport's own height can
// be budgeted) — the single call site Task 10a's Frame composition point
// made possible, so the two can no longer drift the way a guessed constant
// invited.
func (m Model) renderComposer(w int) string {
	var composer strings.Builder
	if comp := renderCompletion(m.completion, strings.TrimSpace(m.textarea.Value()), w); comp != "" {
		composer.WriteString(comp)
		composer.WriteString("\n")
	}
	// Selection only matters when the composer is empty (that's when up/down
	// navigate the queue).
	if q := renderQueue(m.queue, m.queueSel, w); q != "" {
		composer.WriteString(q)
		composer.WriteString("\n")
	}
	switch {
	case !m.sessionReady:
		composer.WriteString(theme.Help.Render(theme.Starting))
	case m.showLogs:
		// no input: the log viewer owns the body and the keystrokes
	case m.showTodo:
		// no input: the todo panel owns the body and the keystrokes
	case m.awaitingApproval && !m.approvalEditing:
		// no input: choice row lives in the viewport approval block
	case m.awaitingResume:
		// no input: unlike ask_user, /resume has no free-text fallback — the
		// picker lives in the viewport dialog block and swallows every key.
	case m.modelPicker.active, m.providerPicker.active, m.loginForm.active, m.loginWait.active, m.endpointForm.active:
		// no input: the picker/login dialog handles all keys.
	default:
		composer.WriteString(m.composerView())
	}
	return composer.String()
}

// dialogsHeight is how many rows the pending dialogs cost the frame: zero on a
// surface that does not overlay them (inline bakes them into the viewport's
// own scrollback, where they are body content and already inside its height),
// and the measured height of the real rendered strings on a surface that does
// (full.Frame writes every v.Dialogs entry between body and composer).
//
// This is the defect that blocked the branch: bubbles/viewport.View() pads the
// body to exactly Height, so the arithmetic is exact — a frame of
// header + vpHeight + dialogs + composer + footer with nothing reserved for
// the dialogs runs over the terminal by precisely their height, and
// bubbletea's renderer truncates the TOP. A five-option approval card is
// 10-14 rows, so the header walked off screen on every approval on the
// default surface.
//
// Measured, not guessed, for the same reason composerHeight is: an approval
// card's height depends on its argument rows, its captured reasoning and the
// terminal's width, and the /resume picker's on how many sessions fit its
// window.
func (m Model) dialogsHeight(vs render.ViewState) int {
	if !m.presenter.Caps().OverlayDialogs {
		return 0
	}
	rows := 0
	for _, d := range vs.Dialogs {
		rows += render.BlockRows(m.presenter.Dialog(d, m.width))
	}
	return rows
}

// layoutBudget is how many rows of this frame are NOT the body: the header,
// the overlaid dialogs, the composer block and the footer. The viewport gets
// whatever is left. syncLayout compares this against what the current sizes
// were budgeted for, so any of the four changing mid-turn — a job row
// appearing, an approval arriving, the `/` completion popup opening —
// re-budgets rather than pushing the frame over the terminal's height.
func (m Model) layoutBudget(vs render.ViewState, footerHeight int) int {
	// The composer block: its own rendered height (renderComposer — usually
	// one line, but a visible `/` completion popup or queued-message block can
	// be taller). Frame writes the composer between two '\n' separators, but
	// those are line terminators, not blank lines: the body and the composer
	// string do not end in '\n', so each '\n' only ends the preceding line and
	// adds no row of its own. Counting them as two rows here (as this used to)
	// shrank the viewport by two and left a two-row gap below the footer every
	// frame. Measuring the real string instead of guessing is what Task 10a's
	// single composer call site made cheap.
	composerHeight := lipgloss.Height(m.renderComposer(m.width))
	return m.presenter.HeaderHeight(vs) + m.dialogsHeight(vs) + composerHeight + footerHeight
}

// applyDimensions sizes the components against the chrome vs implies for this
// frame, with a footer of footerHeight rows, and records what it budgeted for
// so syncLayout can tell when that answer goes stale.
func (m *Model) applyDimensions(vs render.ViewState, footerHeight int) {
	budget := m.layoutBudget(vs, footerHeight)

	vpHeight := m.effectiveHeight() - budget
	minimumViewportHeight := 5
	if vpHeight < minimumViewportHeight {
		vpHeight = minimumViewportHeight
	}

	m.footerBudget = footerHeight
	m.chromeBudget = budget
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	m.logVP.Width = m.width
	m.logVP.Height = vpHeight
	m.textarea.SetWidth(m.width - 2)
}

// updateViewport updates the viewport content with chat messages
// markdownFor returns a glamour renderer for the given wrap width, building and
// caching one per distinct width. Returns nil on construction error (callers
// fall back to plain render.Wrap).
func (m *Model) markdownFor(width int) *glamour.TermRenderer {
	if width < 1 {
		width = 1
	}
	if m.mdRenderers == nil {
		m.mdRenderers = make(map[int]*glamour.TermRenderer)
	}
	if r, ok := m.mdRenderers[width]; ok {
		return r
	}
	r, err := nibMarkdownRenderer(width)
	if err != nil {
		return nil
	}
	m.mdRenderers[width] = r
	return r
}

// renderAgentThreadRun renders one contiguous run of a sub-agent's thread
// messages (agent_tool labels and/or agent_result blocks, all same AgentID) as
// an indented block. It prints a short continuation header (↳ <type>) when
// reprint is true (i.e. the previous rendered line did not belong to this
// agent), caps the tool lines, and renders each result with a → marker. The
// trailing blank separator is omitted when hugNext is true (the next message
// still belongs to this agent's run), keeping the whole thread visually tight.
func (m *Model) renderAgentThreadRun(sb *strings.Builder, run []ChatMessage, contentWidth int, reprint, hugNext bool) {
	if len(run) == 0 {
		return
	}
	if reprint {
		typ := "agent"
		if j, ok := m.jobByID(run[0].AgentID); ok && j.Type != "" {
			typ = j.Type
		}
		sb.WriteString(theme.Subtle.Render(theme.SubAgent + " " + typ))
		sb.WriteString("\n")
	}
	// Tool labels are capped; results render in full after them.
	var toolLines []string
	var tools, results []ChatMessage
	for _, msg := range run {
		if msg.Role == "agent_result" {
			results = append(results, msg)
			continue
		}
		tools = append(tools, msg)
		toolLines = append(toolLines, msg.Content)
	}
	lines := capThreadLines(toolLines, agentThreadInlineCap)
	capped := len(lines) != len(tools)
	for j, line := range lines {
		// Each line fades in with its own entry. The capped lines are the
		// newest ones; the "… +N earlier" header on top has no entry of its own.
		var arriving float64
		if k := len(tools) - (len(lines) - j); k >= 0 && !(capped && j == 0) {
			arriving = m.arriving(tools[k])
		}
		sb.WriteString("   " + theme.Fading(theme.Help, arriving).Render(clipLine(line, contentWidth-3)))
		sb.WriteString("\n")
	}
	for _, r := range results {
		style := theme.Fading(theme.Subtle, m.arriving(r))
		wrapped := render.Wrap(r.Content, contentWidth-5)
		for i, line := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
			if i == 0 {
				sb.WriteString("   " + style.Render(theme.Arrow+" "+line))
			} else {
				sb.WriteString("     " + style.Render(line))
			}
			sb.WriteString("\n")
		}
	}
	if !hugNext {
		sb.WriteString("\n")
	}
}

// sameAgentMsg reports whether the message at idx is part of agentID's run —
// a lifecycle line, tool line, or result tagged with that id. Used to decide
// whether a thread item should hug the next one (omit the blank separator).
func (m *Model) sameAgentMsg(idx int, agentID string) bool {
	if agentID == "" || idx < 0 || idx >= len(m.messages) {
		return false
	}
	x := m.messages[idx]
	if x.AgentID != agentID {
		return false
	}
	return x.Role == "agent" || x.Role == "agent_tool" || x.Role == "agent_result"
}

// dropTransientErrors removes stale turn-level error lines from the
// transcript. Called when a reply arrives: the run recovered, so a failure
// recorded earlier in the session should not keep showing.

// toImageRefs converts chat.ToolImage values to render.ImageRef values,
// assigning each a stable ID for kitty transmit tracking.
func (m *Model) toImageRefs(imgs []chat.ToolImage) []render.ImageRef {
	if len(imgs) == 0 {
		return nil
	}
	refs := make([]render.ImageRef, len(imgs))
	for i, img := range imgs {
		m.imgCounter++
		refs[i] = render.ImageRef{
			ID:     m.imgCounter,
			Data:   img.Data,
			MIME:   img.MIME,
			Source: "tool",
		}
	}
	return refs
}

// renderImageOut produces the terminal escape sequences for inline image
// display. Returns "" when the terminal has no graphics protocol or when
// there are no images. The model calls this once per tool block per render
// pass and passes the result via Message.ImageOut to the Presenter, keeping
// the render layer stateless.
func (m *Model) renderImageOut(imgs []chat.ToolImage, width int) string {
	if m.imageMgr == nil || len(imgs) == 0 || m.imageMgr.Protocol() == termimg.ProtocolNone {
		return ""
	}
	m.imageMgr.BeginPass()
	var b strings.Builder
	for _, img := range imgs {
		// Prepare (decode, resize, PNG-encode) the image data. For
		// kitty this is required (PNG only); for iTerm2 it's a no-op
		// passthrough if already PNG, and a best-effort conversion
		// otherwise.
		data, mime := termimg.PrepareImage(img.Data, img.MIME)
		// Use a content-hash as the stable kitty image ID so the
		// transmit-once optimization works across render passes. A
		// new ID every frame would re-transmit the full base64 data
		// each time, defeating the purpose.
		id := imageIDFromData(data)
		ref := termimg.ImageRef{
			ID:     id,
			Data:   data,
			MIME:   mime,
			Source: "tool",
		}
		// Approximate cell dimensions: assume ~2:1 pixel-to-cell ratio
		// and cap width to terminal width. The terminal handles final
		// scaling.
		cols := width
		rows := max(cols/3, 4)
		seq, ok := m.imageMgr.RenderImage(ref, cols, rows)
		if !ok {
			b.WriteString("  " + termimg.TextPlaceholder(0, 0) + "\n")
		} else {
			b.WriteString(seq)
		}
	}
	// Evict images beyond the budget.
	for _, id := range m.imageMgr.EvictedIDs() {
		b.WriteString(termimg.EncodeKittyDelete(id))
	}
	return b.String()
}

// imageIDFromData derives a stable positive integer from image bytes.
// The hash is folded into the positive int32 range to fit kitty's image
// ID space.
func imageIDFromData(data []byte) int {
	var h uint32
	for _, b := range data {
		h = h*31 + uint32(b)
	}
	id := int(h%0x7FFFFFFF) + 1 // 1-based, avoid 0
	if id <= 0 {
		id = 1
	}
	return id
}

func (m *Model) dropTransientErrors() {
	kept := m.messages[:0]
	reveal := m.revealIdx
	for i, msg := range m.messages {
		if msg.Role == "error" && msg.Transient {
			if i < m.revealIdx-1 {
				reveal-- // the revealed reply moves up with the rest
			}
			continue
		}
		kept = append(kept, msg)
	}
	m.messages = kept
	m.revealIdx = reveal
}

// updateViewportFollow re-renders and pins the viewport to the bottom. Use it
// for user-initiated updates, where the user is waiting on new output and being
// left in scrollback reads as nothing having happened.
func (m *Model) updateViewportFollow() {
	m.forceFollow = true
	m.updateViewport()
}

// toolLabel renders a tool call as the one-line heading a tool block carries:
// the first line of the friendly summary, falling back to the bare tool name
// when the call has no arguments or the summary comes back empty. It lives
// model-side because turning a name plus raw JSON arguments into prose is
// domain logic — the same rule that keeps markdown rendering and the ask block
// out of the presenters. A Presenter places Message.Label; it never imports
// chat to build it.
func toolLabel(name, arguments string) string {
	if arguments == "" {
		return name
	}
	summary := chat.FormatToolCall(name, arguments)
	if nl := strings.IndexByte(summary, '\n'); nl >= 0 {
		summary = summary[:nl]
	}
	if summary == "" {
		return name
	}
	return summary
}

// currentDialogs returns the render.Dialog for every prompt currently
// pending, in the same order the original hand-rolled code rendered them
// (approval block, then ask block). Both a background sub-agent's gated tool
// approval and a foreground ask_user question can be pending at once — cogito
// propagates the tool-call callback into spawned sub-agents (chat/session.go),
// which run in the background while the root agent can independently be
// blocked on ask_user — so this must not assume they're mutually exclusive:
// an earlier version of this method returned only one and the other silently
// vanished from the screen. Shared by updateViewport (which renders each) and
// viewState (which projects them onto ViewState.Dialogs so the field is never
// silently nil for a Presenter reading the whole frame).
func (m Model) currentDialogs() []render.Dialog {
	var dialogs []render.Dialog
	if m.awaitingApproval && m.pendingTool != nil {
		content := buildApprovalContent(*m.pendingTool)
		var options []render.DialogOption
		if m.approvalEditing {
			options = []render.DialogOption{{Text: theme.ApproveEditHint, Emphasis: true}}
		} else {
			scope, _ := chat.GrantScope(m.pendingTool.Name, m.pendingTool.Arguments)
			options = []render.DialogOption{
				{Text: theme.ApproveOnce, Emphasis: true},
				{Text: theme.ApproveAlwaysPrefix + scope + theme.ApproveAlwaysSuffix, Emphasis: true},
				{Text: theme.ApproveTurn, Emphasis: true},
				{Text: theme.ApproveSession, Emphasis: true},
				{Text: theme.ApproveDenyEdit, Emphasis: false},
			}
		}
		dialogs = append(dialogs, render.Dialog{
			Kind:             render.DialogApproval,
			Title:            content.title,
			Meta:             content.meta,
			Rows:             content.rows,
			RowsUnstructured: content.unstructured,
			Diff:             content.diff,
			Hint:             m.pendingTool.Reasoning,
			Options:          options,
		})
	}
	// Guarded the same way as the approval branch above: buildAskDialog
	// actually runs only when a question is pending, not on every frame.
	if m.awaitingAsk && m.pendingAsk != nil {
		dialogs = append(dialogs, buildAskDialog(*m.pendingAsk, m.askList, m.awaitingApproval))
	}
	if m.awaitingResume && m.resumeList != nil {
		dialogs = append(dialogs, buildResumeDialog(m.resumeList, m.resumeDeleteArmed))
	}
	if m.modelPicker.active {
		dialogs = append(dialogs, m.buildModelPickerDialog())
	}
	if m.providerPicker.active {
		dialogs = append(dialogs, m.providerPicker.dialog())
	}
	if m.loginForm.active {
		dialogs = append(dialogs, m.loginForm.dialog())
	}
	if m.endpointForm.active {
		dialogs = append(dialogs, m.endpointForm.dialog())
	}
	if m.loginWait.active {
		dialogs = append(dialogs, m.loginWait.dialog())
	}
	return dialogs
}

// showingViewport reports whether the body area is the conversation viewport,
// rather than the log viewer or the first-run empty state. View reads it to
// pick the body; viewState reads it to resolve NewOutput (which is only
// meaningful when the viewport is on screen at all). One definition, so the
// two can never disagree about what the body is.
func (m Model) showingViewport() bool {
	if m.showLogs {
		return false
	}
	if m.showTodo {
		return false
	}
	return len(m.messages) > 0 || m.loading || m.awaitingApproval || m.awaitingAsk || m.awaitingResume || m.modelPicker.active ||
		m.providerPicker.active || m.loginForm.active || m.loginWait.active || m.endpointForm.active
}

// reasoningBoxHit reports whether a terminal-relative mouse Y lands inside
// the reasoning box's last-recorded row span (reasoningSpanStart/End, see its
// doc comment — recomputed every updateViewport pass, so this always tests
// against the box's CURRENT height, collapsed or expanded).
//
// Y is translated to a content-relative viewport row by subtracting the
// chrome the Presenter's own Header renders above the body, then adding the
// viewport's scroll offset. The chrome height comes from
// Presenter.HeaderHeight — the same query the layout budget subtracts (see
// layoutBudget), so the hit-test and the budget can never disagree about how
// tall the header is. It used to count the newlines in presenter.Header(vs)
// here while applyDimensions hardcoded 2 a few hundred lines away: two
// measurements of one thing, and the hardcoded one was already wrong in
// principle.
func (m Model) reasoningBoxHit(y int) bool {
	if m.reasoningSpanStart >= m.reasoningSpanEnd {
		return false // nothing rendered as a box this frame
	}
	if !m.showingViewport() {
		return false // body isn't the transcript viewport this frame
	}
	chrome := m.presenter.HeaderHeight(m.viewState())
	row := y - chrome + m.viewport.YOffset
	return row >= m.reasoningSpanStart && row < m.reasoningSpanEnd
}

// footerRows builds the job-status footer rows — active sub-agent jobs, shell
// jobs, cron loops, the active goal — in the order they are rendered. Empty
// while the log viewer owns the body, which hides the footer entirely.
func (m Model) footerRows() []render.FooterRow {
	if m.showLogs {
		return nil
	}
	if m.showTodo {
		return nil
	}
	var rows []render.FooterRow
	if row, ok := jobsFooterRow(m.jobs); ok {
		rows = append(rows, row)
	}
	if row, ok := shellJobsFooterRow(m.shellJobs.List()); ok {
		rows = append(rows, row)
	}
	if row, ok := loopsFooterRow(m.loops, m.selfPaced); ok {
		rows = append(rows, row)
	}
	if m.session != nil {
		if row, ok := goalFooterRow(m.session.Goal(), m.session.GoalPaused()); ok {
			rows = append(rows, row)
		}
		if row, ok := todoFooterRow(m.session.TodoList()); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// viewState builds the complete ViewState for the current frame: every field
// populated from Model state, so a Presenter driven from one ViewState — an
// alt-screen full-frame compositor, for instance — never finds a field it
// needs left at its zero value, and so Header and Footer are never handed two
// different projections of the same frame. View builds one of these and
// mutates nothing; updateDimensions budgets the layout against the same value
// View will render from.
func (m Model) viewState() render.ViewState {
	status := m.status
	tip := ""
	if status == "" || status == "Thinking…" {
		if m.thinkingLine != "" {
			status = m.thinkingLine
		} else {
			status = theme.VerbThinking
		}
		// Tip only shows while thinking
		tip = m.tip
	}

	help := theme.Help.Render(m.helpLine())
	errText := ""
	if m.err != nil {
		errText = m.err.Error()
	}

	return render.ViewState{
		Width:       m.width,
		Cwd:         shortenPath(currentDir()),
		Brand:       theme.BrandName,
		AutoApprove: m.session != nil && m.session.AutoApprove(),
		ApprovalMode: func() types.ApprovalMode {
			if m.session == nil {
				return ""
			}
			return m.session.ApprovalMode()
		}(),
		Loading: m.loading,
		Status:  status,
		Spinner: m.spinner.View(),
		Speed:   m.liveSpeed(),
		Reasoning: render.Reasoning{
			Text:      m.reasoning,
			Collapsed: m.reasoningCollapsed,
			MaxLines:  theme.ReasoningMaxLines,
			Elapsed:   m.reasoningElapsed(),
		},
		Dialogs: m.currentDialogs(),
		Help:    help,
		Tip:     tip,
		Badges:  m.footerBadges(lipgloss.Width(help)),
		Clock:   m.hudClock,
		CPU:     m.hudCPU,
		RAM:     int(m.hudMemUsed / (1 << 20)),
		HeaderStats: render.HeaderStats{
			Provider: m.headerProvider(),
			Model:    m.headerModel(),
			Tools:    m.headerToolCount(),
			MCP:      len(m.transports),
			Skills:   len(m.cfg.Skills),
		},

		// New content arrived below the fold while the user was scrolled up.
		NewOutput: m.showingViewport() && !m.viewport.AtBottom(),
		Err:       errText,
		Footers:   m.footerRows(),
	}
}

func (m *Model) updateViewport() {
	var sb strings.Builder

	presenter := m.presenter

	// One ViewState for this pass, shared by the layout budget below and the
	// Reasoning/Dialog blocks at the end — building it twice would re-run
	// currentDialogs, the footer-row builders and the message projection for
	// the same frame.
	vs := m.viewState()

	// Captured BEFORE syncLayout can shrink m.viewport.Height: AtBottom() is
	// relative to the current height, so re-budgeting first would make a user
	// who WAS pinned to the bottom read as scrolled-up (the same ordering
	// hazard the WindowSizeMsg handler guards against).
	wasAtBottom := m.viewport.AtBottom() || m.forceFollow
	m.forceFollow = false
	// vs.NewOutput, as viewState() computed it above, read the viewport's
	// scroll position before this pass has moved it — and before forceFollow,
	// which the viewport hasn't been told about yet, is folded in. wasAtBottom
	// already answers "will this pass leave the viewport at the bottom",
	// forceFollow included; recompute NewOutput from that same answer so
	// syncLayout budgets against the marker row Footer will actually draw for
	// the frame this pass produces, not the one a stale pre-move read implied.
	// Without this, a forced follow while scrolled up reserved a row for the
	// marker that the eventual View() (built from a fresh, post-move
	// ViewState) never draws.
	vs.NewOutput = m.showingViewport() && !wasAtBottom
	m.syncLayout(vs)

	// Calculate available width for content (use viewport width, not terminal width)
	contentWidth := m.viewport.Width
	if contentWidth <= 0 {
		contentWidth = m.width
	}
	if contentWidth <= 0 {
		contentWidth = 80 // fallback
	}

	prevRole := render.RoleNone
	lastAgent := "" // id of the agent whose line was rendered last, "" for non-agent
	m.toolSpans = nil
	for i := 0; i < len(m.messages); i++ {
		msg := m.messages[i]

		// Group a contiguous run of one sub-agent's thread messages.
		if msg.Role == "agent_tool" || msg.Role == "agent_result" {
			j := i
			for j < len(m.messages) &&
				(m.messages[j].Role == "agent_tool" || m.messages[j].Role == "agent_result") &&
				m.messages[j].AgentID == msg.AgentID {
				j++
			}
			m.renderAgentThreadRun(&sb, m.messages[i:j], contentWidth, lastAgent != msg.AgentID, m.sameAgentMsg(j, msg.AgentID))
			lastAgent = msg.AgentID
			i = j - 1
			// A thread run isn't rendered through Message, so nothing sets
			// prevRole for it above — but a following Message call still needs
			// an accurate "what rendered last" answer (Phase 3 Task 12 reads
			// prev to drop labels on consecutive same-role messages). RoleAgent
			// is the closest fit for these raw agent_tool/agent_result roles.
			prevRole = render.RoleAgent
			continue
		}

		// Track agent context so a following thread run knows whether to reprint.
		if msg.Role == "agent" && msg.AgentID != "" {
			lastAgent = msg.AgentID
		} else {
			lastAgent = ""
		}

		switch msg.Role {
		case "user":
			sb.WriteString(presenter.Message(render.Message{Role: render.RoleUser, Content: msg.Content, Arriving: m.arriving(msg)}, prevRole, contentWidth))
			prevRole = render.RoleUser
		case "assistant":
			// Markdown is width-cached model state (glamour), not something a
			// Presenter owns — pre-render it here at the width the presenter's
			// prefix will leave for content, matching its own prefix exactly.
			mdWidth := presenter.ContentWidth(render.RoleAssistant, contentWidth)
			var rendered string
			if target, ok := m.revealTarget(); ok && i == target {
				// This message is still receiving live content deltas:
				// render it block by block, so it looks the same as the
				// final glamour pass below and nothing jumps when the turn
				// ends (see renderStreaming).
				rendered = m.renderStreaming(m.visibleStreamContent(msg.Content), mdWidth)
			} else {
				rendered = m.renderMarkdown(msg.Content, mdWidth)
			}
			sb.WriteString(presenter.Message(render.Message{Role: render.RoleAssistant, Content: rendered, Arriving: m.arriving(msg)}, prevRole, contentWidth))
			prevRole = render.RoleAssistant
		case "agent":
			mdWidth := presenter.ContentWidth(render.RoleAgent, contentWidth)
			rendered := m.renderMarkdown(msg.Content, mdWidth)
			sb.WriteString(presenter.Message(render.Message{
				Role:    render.RoleAgent,
				Content: rendered,
				AgentID: msg.AgentID,
				// Tighten: a sub-agent lifecycle header hugs its own thread run
				// that follows (tool lines / result) — the Presenter must omit
				// the blank separator in that case. This depends on the NEXT raw
				// message, which a Presenter never sees, so the model resolves
				// it here and carries the answer on the Message value.
				HugNext: m.sameAgentMsg(i+1, msg.AgentID),
			}, prevRole, contentWidth))
			prevRole = render.RoleAgent
		case "tool":
			toolStart := strings.Count(sb.String(), "\n")
			sb.WriteString(presenter.Message(render.Message{
				Role:    render.RoleTool,
				Content: msg.Content,
				Label:   toolLabel(msg.Name, msg.Arguments),
				AgentID: msg.AgentID,
				Meta:    msg.Meta,
				Status:  msg.Status,
				Diff:    msg.Diff,
				// ctrl+r folds tool output with the thinking; a click flips
				// one block.
				Expanded: m.toolsExpanded() != msg.flipped,
				// Fades in like every other entry (see render.ToolBlock).
				Arriving: m.arriving(msg),
				// A one-line tool block (a collapsed read, a bare write) hugs
				// the tool block after it, so a run of them reads as a list
				// rather than a column of blank-separated lines.
				HugNext: msg.bodyless() && i+1 < len(m.messages) && m.messages[i+1].Role == "tool",
				Images:  m.toImageRefs(msg.Images),
				ImageOut: m.renderImageOut(msg.Images, contentWidth),
			}, prevRole, contentWidth))
			m.toolSpans = append(m.toolSpans, toolSpan{start: toolStart, end: strings.Count(sb.String(), "\n"), index: i})
			prevRole = render.RoleTool
		case "error":
			sb.WriteString(presenter.Message(render.Message{Role: render.RoleError, Content: msg.Content, Arriving: m.arriving(msg)}, prevRole, contentWidth))
			prevRole = render.RoleError
		case "thought":
			// ctrl+r expands the live box and the folded thoughts together:
			// one switch for "show the thinking in full".
			sb.WriteString(render.Thought(msg.Content, msg.Meta, !m.reasoningCollapsed, m.arriving(msg), contentWidth))
			// Not a user or assistant entry: the reply after it keeps its
			// label on inline, as after a tool block.
			prevRole = render.RoleAgent
		}
	}

	// Reasoning renders nothing when !vs.Loading, so the call is
	// unconditional; Dialogs renders each pending dialog in turn (ordinarily
	// zero or one, but a background sub-agent's tool approval and a
	// foreground ask_user question can both be pending at once — see
	// currentDialogs — and the original hand-rolled code rendered both).
	//
	// A surface that declares Caps.OverlayDialogs (full) places v.Dialogs
	// itself, fresh every frame, from Frame — see full.Frame's doc comment.
	// Appending it here too would render every pending dialog twice: once
	// baked into this scrollback (which View's Frame call passes through as
	// body, indistinguishable from any other transcript text by the time
	// Frame runs) and once more as that surface's own overlay. A surface that
	// does not declare it (inline) has no other place a dialog reaches the
	// screen, so this append is still its only path.
	// Record the box's content-relative row span for this render: the
	// newline count immediately before and after the write. Recomputed every
	// pass (never cached across frames) — see reasoningSpanStart/End's doc
	// comment on Model. An empty Reasoning() write (not loading, or no trace
	// yet) leaves start == end, an empty span nothing can click.
	// Calls still running sit between the transcript and the working
	// indicator. A block with no output yet hugs the next one, so parallel
	// calls stack as a list.
	for i, r := range m.running {
		start := strings.Count(sb.String(), "\n")
		rb := m.runningBlock(r)
		sb.WriteString(render.RunningToolBlock(rb, contentWidth))
		if strings.TrimSpace(rb.Output) != "" || i == len(m.running)-1 {
			sb.WriteString("\n")
		}
		m.toolSpans = append(m.toolSpans, toolSpan{start: start, end: strings.Count(sb.String(), "\n"), index: i, running: true})
	}

	// Live sub-agent stats: one indented line per running agent that has
	// streamed, showing its token count and generation rate. Collapsed
	// by default (ctrl+o opens the full log viewer); the line keeps the
	// user oriented without filling the transcript.
	if line := m.agentLiveStatsLine(); line != "" {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	reasoningStart := strings.Count(sb.String(), "\n")
	reasoningOut := presenter.Reasoning(vs, contentWidth)
	sb.WriteString(reasoningOut)
	m.reasoningSpanStart = reasoningStart
	m.reasoningSpanEnd = reasoningStart + strings.Count(reasoningOut, "\n")
	if !presenter.Caps().OverlayDialogs {
		for _, d := range vs.Dialogs {
			sb.WriteString(presenter.Dialog(d, contentWidth))
		}
	}

	// Preserve the user's scroll position: only follow to the bottom when they
	// were already there (captured at the top of this function). Otherwise a
	// re-render (spinner tick, status update, streamed token) would yank them
	// back down while they're reading history.
	offset := m.viewport.YOffset
	m.viewport.SetContent(sb.String())
	if wasAtBottom {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(offset)
	}
	// This frame has a reveal or a fade in progress: keep the clock running
	// for the next one (it stops itself once a frame has nothing moving).
	m.anim.want(m.animating())
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	presenter := m.presenter
	// One complete ViewState for the whole frame. Nothing below mutates it:
	// Header and Footer must see the same projection, or a full-frame
	// Presenter that composes from a single ViewState gets zero-valued
	// footers.
	//
	// updateViewport builds its own during Update rather than sharing this
	// one, and deliberately so: NewOutput is resolved from the viewport's
	// scroll position, which updateViewport itself moves (SetContent, then
	// GotoBottom or SetYOffset) AFTER it has built its ViewState, and which
	// the tail of Update can move again. A value cached across the two would
	// be stale by construction. Each phase builds one and shares it within
	// itself.
	vs := m.viewState()

	// Header: brand (plus a yolo badge when the approval gate is off) left,
	// cwd right, one dim hairline beneath.
	header := presenter.Header(vs)

	// Body: log viewer, first-run empty state, otherwise the conversation viewport.
	var body string
	switch {
	case m.showTodo:
		body = m.renderTodoPanel()
	case m.showLogs:
		body = m.renderLogsViewer()
	case m.boot != nil && !m.boot.collapsed:
		body = m.boot.render(m.width)
	case !m.showingViewport():
		body = renderEmptyState(m.width)
	default:
		body = m.viewport.View()
	}

	// Composer: everything that sits between the body and the footer this
	// frame — the `/` completion popup, the pending-message queue, and the
	// input line (or the not-ready notice, or nothing at all in the modes
	// where the viewport's own approval block carries the choice row).
	composer := m.renderComposer(m.width)

	// Footer: new-output marker (scroll-position signal — content arrived below
	// the fold while the user was reading history), help/badges line, error
	// line, and the job-status footer rows. All of it already on vs — the same
	// value Header rendered from, and the same one updateDimensions budgeted
	// the viewport's height against. renderFooter reuses syncLayout's render
	// of this same vs when nothing footer-relevant changed since, rather than
	// paying for a second Footer call every frame.
	footer, _ := m.renderFooter(vs, m.width)

	// Frame is the whole-screen composition point: it places header, body,
	// composer and footer relative to one another. Inline (and full, for now)
	// simply stack them in this same order; a surface that owns the whole
	// screen can do more once there's a dialog worth overlaying (Task 11).
	//
	// Fitting the frame to the terminal is the core's job, not the presenter's
	// (see TestFrameContainsEveryPiece): Frame stacks and never clamps. The
	// viewport self-pads to its allotted height, so the common case already
	// fills the screen. But the first-run empty state and the log-viewer job
	// list do not self-pad, so on a surface that owns the whole terminal the
	// frame comes up short and the footer floats above the bottom. Measuring
	// the deficit and padding body with that many newlines fills it exactly:
	// appending N newlines to body adds exactly N rows to the frame (the first
	// newline either terminates body's last line or extends the separator's
	// blank line, and each subsequent newline is one blank row — the two cases
	// net out to N rows either way).
	out := presenter.Frame(vs, header, body, composer, footer, m.width, m.effectiveHeight())
	if presenter.Caps().AltScreen {
		if deficit := m.effectiveHeight() - lipgloss.Height(out); deficit > 0 {
			body += strings.Repeat("\n", deficit)
			out = presenter.Frame(vs, header, body, composer, footer, m.width, m.effectiveHeight())
		}
	}
	return out
}

// helpLine returns the context-appropriate help string.
func (m Model) helpLine() string {
	switch {
	case m.hint != "":
		return m.hint
	case m.showTodo:
		return theme.ScrollKeys + " scroll · esc/ctrl+t close"
	case m.showLogs && m.logOpenID != "":
		return theme.ScrollKeys + " scroll · esc back · ctrl+o close"
	case m.showLogs:
		return theme.ScrollKeys + " select · enter open · k kill · esc close"
	case m.awaitingApproval && m.approvalEditing:
		return theme.HelpApprovalEdit
	case m.awaitingApproval:
		return theme.HelpApproval
	case m.awaitingAsk:
		return theme.HelpAsk
	case m.awaitingResume:
		// theme.ResumeDeleteConfirm (while armed) lives in the dialog's own
		// Hint, adjacent to the row being deleted (see buildResumeDialog) —
		// the footer keeps showing the key list unconditionally so the user
		// never loses sight of which key cancels the arm.
		return theme.HelpResume
	case m.modelPicker.active && m.modelPicker.typed:
		return theme.ModelPickerTypeName
	case m.modelPicker.active:
		return theme.ModelPickerKeyHint
	case m.providerPicker.active && m.providerPicker.mode == pickerLogout:
		return theme.ProviderPickerLogoutHint
	case m.providerPicker.active && m.providerPicker.mode == pickerEndpoint:
		return theme.EndpointPickerKeyHint
	case m.providerPicker.active:
		return theme.ProviderPickerKeyHint
	case m.loginForm.active:
		return theme.LoginFormHint
	case m.endpointForm.active:
		return theme.EndpointFormHint
	case m.loginWait.active:
		return theme.LoginWaitHint
	case m.parked:
		return "enter add a follow-up · ctrl+c interrupt · ctrl+o logs"
	case strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0 && m.queueHeld:
		return theme.HintQueueHeld
	case strings.TrimSpace(m.textarea.Value()) == "" && len(m.queue) > 0:
		return "↑↓ pick · ^e edit · ^x delete"
	default:
		return theme.HelpDefault
	}
}

func currentDir() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// shortenPath replaces the home-dir prefix with ~ for a compact header.
func shortenPath(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

// Output returns any command that should be output to the shell
func (m Model) Output() string {
	return m.output
}

// SessionUsage reports what the session spent, for the exit summary RunTUI
// prints once the program has stopped rendering.
func (m Model) SessionUsage() chat.SessionUsage { return m.sessionUsage }

// isWorking reports whether a turn or sub-agent is currently running, or the
// run is parked (alive, waiting on the injection channel). A parked run is still
// in flight, so the first Ctrl+C should interrupt it rather than quit.
func (m Model) isWorking() bool {
	if m.loading || m.parked {
		return true
	}
	if m.session != nil && m.session.AgentManager() != nil {
		return m.session.AgentManager().HasRunning()
	}
	return false
}

// prunedNotice formats a one-line tool-output pruning summary for the
// transcript. It repeats cmd.pruneNotice's wording because tui cannot import
// cmd (cmd builds the TUI) — including its silence about staleness, since the
// high-water sweep prunes on size alone and calling those results stale would
// be untrue. The saving uses HumanTokensOrZero: a stale read is stubbed however
// small it was, so a pass can free nothing measurable and HumanTokens renders
// 0 as "".
//
// It carries the same "~" and "(estimated)" markers as compactNotice below,
// because the figure is the same byte/4 estimate: an unmarked number reads as
// measured, and a user cannot tell the difference. The count is exact and stays
// unmarked.
func prunedNotice(results, freed int) string {
	noun := "results"
	if results == 1 {
		noun = "result"
	}
	return fmt.Sprintf("pruned %d tool %s — freed ~%s tokens (estimated)", results, noun, chat.HumanTokensOrZero(freed))
}

// compactNotice formats a one-line compaction summary for the transcript. It
// repeats cmd.compactNotice's wording — tui cannot import cmd, because cmd
// builds the TUI — including the "~" and "(estimated)" markers: the figures are
// byte/4 estimates, not the backend's reported usage, and an unmarked number
// reads as measured. Both figures use HumanTokensOrZero for the same reason
// pruneNotice above does: HumanTokens renders 0 as "", which would print
// "~ → ~ tokens" — a hole where the number belongs.
func compactNotice(before, after int) string {
	return fmt.Sprintf("Compacted conversation — ~%s → ~%s tokens (estimated)",
		chat.HumanTokensOrZero(before), chat.HumanTokensOrZero(after))
}

// aboutText renders the /about output for the TUI. It duplicates
// cmd.AboutText — tui cannot import cmd, because cmd builds the TUI.
func aboutText(cfg types.Config) string {
	prog := types.ProgramNameOr(cfg.ProgramName)
	v := strings.TrimSpace(internal.PrintableVersion())
	if v == "()" || v == "" {
		v = "dev (local build)"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s\n", prog, v))
	b.WriteString("\n")

	b.WriteString("Configuration paths (first existing wins):\n")
	for _, p := range config.ConfigPaths() {
		b.WriteString(fmt.Sprintf("  %s\n", p))
	}
	b.WriteString(fmt.Sprintf("Writable config: %s\n", config.WritablePath()))
	b.WriteString("\n")

	model := cfg.Model
	if model == "" {
		model = "(unset)"
	}
	provider := cfg.Provider
	if provider == "" {
		provider = "(unset)"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "(default)"
	}
	b.WriteString(fmt.Sprintf("Provider: %s\n", provider))
	b.WriteString(fmt.Sprintf("Model: %s\n", model))
	b.WriteString(fmt.Sprintf("Base URL: %s\n", baseURL))
	b.WriteString("\n")

	b.WriteString("Built-in tools:\n")
	if len(cfg.BuiltinTools) == 0 {
		b.WriteString("  (all enabled)\n")
	} else {
		for _, t := range cfg.BuiltinTools {
			b.WriteString(fmt.Sprintf("  %s\n", t))
		}
	}
	b.WriteString("\n")

	if len(cfg.Skills) > 0 {
		b.WriteString("Skills:\n")
		for _, s := range cfg.Skills {
			b.WriteString(fmt.Sprintf("  %s: %s\n", s.Name, s.Description))
		}
		b.WriteString("\n")
	}

	if len(cfg.Agents) > 0 {
		b.WriteString("Agents:\n")
		for _, a := range cfg.Agents {
			b.WriteString(fmt.Sprintf("  %s: %s\n", a.Name, a.Description))
		}
		b.WriteString("\n")
	}

	if len(cfg.MCPServers) > 0 {
		b.WriteString("MCP servers:\n")
		for name := range cfg.MCPServers {
			b.WriteString(fmt.Sprintf("  %s\n", name))
		}
		b.WriteString("\n")
	}

	b.WriteString("The agent can read its own documentation by loading the \"about-nib\" skill.\n")
	b.WriteString("Documentation is embedded in the binary — no network required.\n")

	return b.String()
}

// ctxBadgeWarn highlights the context badge once usage nears the auto-compaction
// threshold (clay — the palette's warmest attention color).
var ctxBadgeWarn = lipgloss.NewStyle().Foreground(theme.Accent)

// contextBudget is the number the compaction point is taken from: the window
// the session is really using, less the reserve held back for the response.
// It is what chat.shouldAutoCompact takes its threshold of, so the gauge's tick
// sits exactly where compaction fires — when compaction is on at all.
//
// Both halves used to be wrong. The raw cfg.Compaction.MaxContextTokens ignores
// the reserve (a few percent at 128k) and, worse, ignores a window learned from
// a backend overflow error: a session configured for 400k against a model that
// really serves 262k drew a calm badge while compaction ran. The session is the
// authority whenever there is one; the config is the fallback for a model with
// no session yet.
func (m Model) contextBudget() int {
	return chat.ContextBudget(m.cfg.Compaction, m.contextWindow())
}

// contextNearFraction is how close to the compaction point the badge starts
// warning, as a fraction of that point. Warning a little early is the point:
// a badge that lights up only as compaction fires tells the user nothing they
// can act on.
const contextNearFraction = 0.9

// contextGaugeCells is the width of the context gauge, in cells.
const contextGaugeCells = 10

// contextCompactAt returns the token count at which auto-compaction fires, or 0
// when it cannot fire. It mirrors chat.shouldAutoCompact: Threshold × budget,
// with Disabled rejected first and the same 0.8 default.
func (m Model) contextCompactAt(budget int) int {
	if m.cfg.Compaction.Disabled || budget <= 0 {
		return 0
	}
	threshold := m.cfg.Compaction.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	return int(float64(budget) * threshold)
}

// contextBadgeWarns reports whether the badge should highlight: usage is
// within contextNearFraction of the compaction point, AND compaction can
// actually fire.
//
// The Disabled half is the part that is easy to forget. The highlight is a
// warning that something is about to happen; with compaction off nothing is,
// and lighting the badge predicts a compaction that cannot come.
//
// Split out of contextBadge because it is the only part of the badge a test can
// assert on: lipgloss renders both styles as plain text when tests run without
// a TTY, so a rendered badge cannot say whether it was highlighted.
func (m Model) contextBadgeWarns(used, budget int) bool {
	at := m.contextCompactAt(budget)
	return at > 0 && float64(used) >= float64(at)*contextNearFraction
}

// contextWindow is the full window the gauge is drawn against: the one the
// session really uses, falling back to the config before a session exists.
func (m Model) contextWindow() int {
	if m.session != nil {
		return m.session.ContextWindow()
	}
	return m.cfg.Compaction.MaxContextTokens
}

// refreshContextTokens re-reads the session's context size mid-turn.
//
// m.contextTokens is otherwise only assigned at turn boundaries (responseMsg,
// parkMsg and the two compaction notices), so the gauge sat still for the
// whole turn and jumped once at the end — the "used" figure frozen while the
// window beside it stayed correct, because the window does not depend on the
// run in flight. The session reports the live figure for the duration of a
// turn (see chat.liveUsage), so a periodic re-read is all this needs; between
// turns the session answers from the fragment, which is the same number the
// boundary handlers already wrote.
func (m *Model) refreshContextTokens() {
	if m.session == nil || !m.loading {
		return
	}
	if n := m.session.ContextTokens(); n > 0 {
		m.contextTokens = n
	}
}

// contextGauge draws used/window as contextGaugeCells cells, with a tick at
// the compaction point when compaction can fire, e.g. "▰▰▰▰▱▱▱▱│▱▱".
func contextGauge(used, window, compactAt int, warn bool) string {
	cell := func(n int) int {
		c := (n*contextGaugeCells + window/2) / window
		return min(max(c, 0), contextGaugeCells)
	}
	filled := cell(used)
	if used > 0 && filled == 0 {
		filled = 1 // something is in the window; never draw it empty
	}
	tick := -1
	if compactAt > 0 {
		tick = cell(compactAt)
	}
	fill := theme.Help
	if warn {
		fill = ctxBadgeWarn
	}
	var b strings.Builder
	for i := range contextGaugeCells {
		if i == tick {
			b.WriteString(theme.Help.Render("│"))
		}
		if i < filled {
			b.WriteString(fill.Render("▰"))
		} else {
			b.WriteString(theme.Meta.Render("▱"))
		}
	}
	return b.String()
}

// contextBadges returns the context indicator for the bottom bar at each
// width it can take, widest first, e.g.
//
//	ctx ▰▰▰▰▰▱▱▱│▱▱ 48k/200k · 152k left
//	ctx 48k/200k · 152k left
//	ctx 48k/200k
//
// The figures are against the full window, which is what "how much room is
// there" means to a reader. The compaction point is the tick on the gauge, and
// once usage nears it the trailing text names it instead of the headroom,
// because at that point it is the number that matters.
//
// Returns nil when there's nothing to show yet.
func (m Model) contextBadges() []string {
	used := m.contextTokens
	if used <= 0 {
		return nil
	}
	window := m.contextWindow()
	if window <= 0 {
		// No window at all: show the bare size.
		return []string{theme.Meta.Render("ctx " + chat.HumanTokens(used))}
	}
	budget := m.contextBudget()
	compactAt := m.contextCompactAt(budget)
	warn := m.contextBadgeWarns(used, budget)

	text := theme.Meta
	if warn {
		text = ctxBadgeWarn
	}
	size := fmt.Sprintf("%s/%s", chat.HumanTokensOrZero(used), chat.HumanTokens(window))
	tail := fmt.Sprintf("%s left", chat.HumanTokensOrZero(max(window-used, 0)))
	if warn {
		tail = "compacts at " + chat.HumanTokens(compactAt)
	}
	label := theme.Meta.Render("ctx ")
	return []string{
		label + contextGauge(used, window, compactAt, warn) + " " + text.Render(size+" · "+tail),
		label + text.Render(size+" · "+tail),
		label + text.Render(size),
	}
}

// contextBadge renders the widest form of the context indicator, or "" when
// there's nothing to show yet.
func (m Model) contextBadge() string {
	if b := m.contextBadges(); len(b) > 0 {
		return b[0]
	}
	return ""
}

// usageBadge renders the session's cumulative token spend for the bottom bar,
// e.g. "session 312k in / 18.4k out". Returns "" before anything is spent, the
// same way contextBadge does.
//
// Plain words rather than arrows: it matches contextBadge's phrasing and the
// calm editorial voice TestNoEmojiInRenderHelpers guards.
//
// chat.HumanTokensOrZero, not chat.HumanTokens: the badge names both directions,
// so a zero one has to render "0" rather than the empty string HumanTokens
// returns, or the badge shows "session 1.2k in /  out". The exit summary prints
// the same fixed shape and shares the same formatter.
//
// When both counts are <= 0 AND a session is attached, this falls back to
// chat.Session.EstimatedUsage — the byte/4 conversation estimate — rather than
// hiding the badge outright. That fallback exists for a streamed session:
// cogito's bundled clients never populate StreamEvent.Usage, so Usage() stays
// zero for every streamed turn (chat/usage.go's doc comment), and without this
// the badge would simply vanish the moment streaming turns on. The estimate is
// marked with theme.UsageEstimatedPrefix so it never reads as measured spend —
// real usage.json and the exit summary still carry the true (possibly zero)
// figure; only this footer badge borrows the estimate for display.
func (m Model) usageBadge() string {
	in, out := m.sessionUsage.PromptTokens, m.sessionUsage.CompletionTokens
	estimated := false
	if in <= 0 && out <= 0 {
		if m.session == nil {
			return ""
		}
		est := m.session.EstimatedUsage()
		in, out = est.PromptTokens, est.CompletionTokens
		if in <= 0 && out <= 0 {
			return ""
		}
		estimated = true
	}
	label := fmt.Sprintf("session %s in / %s out",
		chat.HumanTokensOrZero(in), chat.HumanTokensOrZero(out))
	if estimated {
		label = theme.UsageEstimatedPrefix + label
	}
	return theme.Meta.Render(label)
}

// footerBadges renders the right-aligned bottom-bar badges for a help line of
// helpWidth columns. Badges are priority-based: the lowest-priority badge drops
// first when space is tight.
//
// Priority (lowest drops first): mem(20), cpu(40), clock(60), usage(80),
// speed(90), context(100). The context badge earns the highest priority because it
// predicts auto-compaction and is therefore actionable.
func (m Model) footerBadges(helpWidth int) string {
	type badge struct {
		text     string
		priority int
	}
	var badges []badge

	// The context badge takes the widest form that fits beside the help line,
	// falling back to its narrowest when none does; the rest share what is left.
	usage := m.usageBadge()
	ctxWidth := 0
	if forms := m.contextBadges(); len(forms) > 0 {
		ctx := forms[len(forms)-1]
		for _, f := range forms {
			if helpWidth+1+lipgloss.Width(f) <= m.width {
				ctx = f
				break
			}
		}
		badges = append(badges, badge{ctx, 100})
		ctxWidth = lipgloss.Width(ctx)
	}
	// The speed badge ranks just under the context badge. It takes its full
	// form when that fits beside the help line and the context badge, and
	// its narrow form (the session average alone) otherwise.
	if speed, narrow := m.speedBadges(); speed != "" {
		if helpWidth+1+ctxWidth+2+lipgloss.Width(speed) > m.width {
			speed = narrow
		}
		badges = append(badges, badge{speed, 90})
	}
	if usage != "" {
		badges = append(badges, badge{usage, 80})
	}
	// Aggregate sub-agent token spend while one or more are running. It
	// complements the session usage badge (root agent) so the user can see
	// what sub-agents are costing live.
	if total, active := m.agentSpeed.aggregateTokens(time.Now()); total > 0 && active > 0 {
		agentBadge := theme.Help.Render("agents ") + theme.Meta.Render(chat.HumanTokens(int(math.Round(total))))
		badges = append(badges, badge{agentBadge, 85})
	}
	// ui.hide_hud drops the machine badges (clock, cpu, mem) and keeps the
	// session ones above, which predict compaction and spend.
	hud := !m.cfg.UI.HideHUD
	if hud && m.hudClock != "" {
		badges = append(badges, badge{theme.Meta.Render(m.hudClock), 60})
	}
	if hud && m.hudCPUOK {
		badges = append(badges, badge{theme.Help.Render("cpu ") + theme.Meta.Render(strconv.Itoa(m.hudCPU)+"%"), 40})
	}
	if hud && m.hudMemTotal > 0 {
		badges = append(badges, badge{theme.Help.Render("mem ") + theme.Meta.Render(humanGiB(m.hudMemUsed)+"/"+humanGiB(m.hudMemTotal)+"G"), 20})
	}

	if len(badges) == 0 {
		return ""
	}

	// Greedily include from highest to lowest priority. The first (highest-
	// priority) badge is always included even if it overflows — it carries the
	// most actionable information (context predicts compaction).
	sep := "  "
	var included []string
	used := helpWidth + 1
	for i, b := range badges {
		w := lipgloss.Width(b.text)
		if len(included) > 0 {
			w += len(sep)
		}
		if i == 0 || used+w <= m.width {
			included = append(included, b.text)
			used += w
		}
	}

	return strings.Join(included, sep)
}

// quit tears down the session and exits.
//
// The usage refresh comes BEFORE Close, and is not decoration. Close writes
// usage.json from the session's live counter, while RunTUI prints the exit
// summary from this cached field, so skipping the refresh lets the two disagree
// about the same run: a second Ctrl+C during the first turn left usage.json
// holding the real spend and the terminal printing nothing at all, because
// FormatSessionSummary renders "" for zero. Reading first makes both surfaces
// quote the same snapshot.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.session != nil {
		m.sessionUsage = m.session.Usage()
		// Before Close, for the same reason the usage refresh is: recordSession
		// reads through m.session (ExportHistory), so it must run while the
		// session is still live. A save failure here is logged and never blocks
		// exit — see recordSession's doc comment.
		m.recordSession()
		m.session.Close()
	}
	m.cancel()
	if m.imageMgr != nil {
		if purge := m.imageMgr.PurgeAll(); purge != "" {
			fmt.Fprint(os.Stdout, purge)
		}
	}
	return m, tea.Quit
}

// openBrowser attempts to open url in the user's default browser. Failure is
// non-fatal — the URL is already displayed for manual entry.
func openBrowser(url string) {
	var c string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		c, args = "open", []string{url}
	case "windows":
		c, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		c, args = "xdg-open", []string{url}
	}
	_ = exec.Command(c, args...).Start()
}
