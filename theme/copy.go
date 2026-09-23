package theme

// Microcopy — calm, lowercase, no wizard metaphor, no emoji.
const (
	BrandName = "nib"

	// LabelYouText labels the user's own chat messages (paired with LabelYou,
	// the style, in theme.go).
	LabelYouText = "you"

	HelpDefault      = "enter send · ctrl+y use command · G/end newest · ctrl+c twice exit"
	HelpApproval     = "pick an option above · esc deny"
	HelpApprovalEdit = "enter submit · esc cancel"
	ApproveEditHint  = "describe the change · enter submit · esc cancel"

	// YoloOn/YoloOff are the transcript notices the /yolo toggle appends.
	YoloOn  = "yolo on — every tool call is auto-approved"
	YoloOff = "yolo off — tool calls need approval again"

	// NewOutputText is the footer marker shown when the viewport is scrolled up
	// and content has arrived below the fold. Composed with NewOutputGlyph by
	// NewOutputMarker() in theme.go — the glyph is swappable, this text isn't.
	NewOutputText = "new output"

	// The numbered approval menu. Line 2 is dynamic — the TUI composes
	// ApproveAlwaysPrefix + chat.GrantScope(...) + ApproveAlwaysSuffix.
	ApproveOnce         = "[1] run it once"
	ApproveAlwaysPrefix = "[2] always allow "
	ApproveAlwaysSuffix = "  (this session)"
	ApproveTurn         = "[3] yes to everything this turn"
	ApproveSession      = "[4] yes to everything this session"
	ApproveDenyEdit     = "[n] no · [e] edit"

	EmptyTagline = "a calm assistant for your terminal."
	EmptyTryLead = "try:"
	EmptySlash   = "type /  for skills, agents & commands"
	SlashHint    = "/ for skills"
	Starting     = "starting…"

	ModelPickerLoading     = "loading models…"
	ModelPickerEmpty       = "no models available."
	ModelPickerNoMatches   = "no matching models."
	ModelPickerSearchLabel = "search:"
	ModelPickerKeyHint     = "type to filter · ↑↓ move · enter select · esc cancel"
	// ModelPickerTypeName replaces the key hint when the provider being
	// switched to offers no model list: the typed query itself is the model.
	ModelPickerTypeName  = "type the model name · enter use it · esc cancel"
	ModelPickerNameLabel = "name:"
	ModelPickerUseTyped  = "enter uses the typed name."
	// ModelPickerLoginHint follows the key hint in /model's picker, which
	// only lists the current provider's models: switching provider is /login.
	ModelPickerLoginHint = "/login to switch provider"
	// ModelListFailed is the picker's listErr when an endpoint could not be
	// reached to list its models (%s the endpoint's name, %s the error). It
	// precedes ModelPickerTypeName in the hint, so the dialog both explains
	// the failure and says a model name can still be typed — it never closes
	// silently on a listing failure.
	ModelListFailed = "could not list models for %s: %v"

	// ModelResetNotice follows "model: <name>" in /model reset's success
	// notice: the sticky per-endpoint override is gone and the endpoint's own
	// model applies again.
	ModelResetNotice = "reset to the endpoint's own model"

	// ModelOverridesConfigNotice follows "model: <name>" in /model's success
	// notice on config.yaml's DEFAULT endpoint, when the pick differs from
	// what config.yaml itself declares (%s). SetModel persists a pick
	// uniformly on every endpoint, including the default one, so the pick
	// does outlive the session and shadow config.yaml on the next start —
	// this says so now, instead of leaving the user to discover it only at
	// the next boot's note. Only shown when /model reset would actually
	// undo it (see SettingsModelOverrideNoReset for the case it would not).
	ModelOverridesConfigNotice = "overrides config.yaml's %s until /model reset"

	// BootModelOverride is the dim note on the boot log's model line when the
	// running model diverges from config.yaml's own (a %s for config.yaml's
	// model): both are configured, and only one is used, so the log says
	// which. The cause is always the same shape — a pick saved in
	// provider.json shadowing config.yaml — whether it came from a /login
	// provider, a named endpoint, or a /model pick on config.yaml's own
	// default endpoint, so one wording covers all three instead of naming
	// /login specifically and being wrong for the other two.
	BootModelOverride = "(config.yaml: %s, overridden by a saved pick)"

	// /login provider picker, API-key form and OAuth wait dialog
	// (tui/providerpicker.go).
	ProviderPickerTitle       = "provider"
	ProviderPickerLogoutTitle = "log out of"
	ProviderPickerKeyHint     = "type to filter · ↑↓ move · enter use or log in · esc cancel"
	ProviderPickerLogoutHint  = "type to filter · ↑↓ move · enter log out · esc cancel"
	ProviderPickerNoMatches   = "no matching providers."
	ProviderNoneLoggedIn      = "not logged in to any provider · /login to add one"
	ProviderCurrentSuffix     = " (current)"
	ProviderSavedDefault      = "saved as default"
	LoginFormTitle            = "log in to %s"
	LoginFormKeyLabel         = "API key"
	LoginFormURLLabel         = "Base URL"
	LoginFormHint             = "paste or type · enter save · esc cancel"
	LoginFormFieldsHint       = "paste or type · tab next field · enter save · esc cancel"
	LoginFormEnvHint          = "or set $%s instead"
	LoginWaitTitle            = "waiting for %s login…"
	LoginWaitHint             = "finish in the browser · esc cancel"
	LoginCancelled            = "login cancelled"

	// /endpoint picker (tui/endpointpicker.go): the same dialog as /login's,
	// but listing every endpoint — the config.yaml default, named endpoints,
	// and the registry — not just the registry.
	EndpointPickerTitle   = "endpoint"
	EndpointPickerKeyHint = "type to filter · ↑↓ move · enter use or log in · esc cancel"
	// EndpointSwitched is the transcript notice after /endpoint switches
	// straight to the default or a named endpoint (a %s for its name, a %s
	// for the model now in use).
	EndpointSwitched = "endpoint: %s · model: %s"
	// EndpointUnknown is `/endpoint <id>`'s refusal for an ID that matches no
	// entry (a %s for the typed ID).
	EndpointUnknown = "unknown endpoint %q · /endpoint lists them"
	// LoginNotAProvider is `/login <id>`'s refusal when id names config.yaml's
	// default endpoint or one of its named endpoints rather than a registry
	// provider: /login only authenticates and switches registry providers now
	// that /endpoint lists (and switches to) everything, so a config.yaml
	// entry is pointed at /endpoint instead of being silently accepted (both
	// %s are the typed ID).
	LoginNotAProvider = "%s is a config.yaml endpoint, not a login provider · use /endpoint %s instead"

	CLIWelcome = "a calm assistant for your terminal."
	CLIExit    = "ctrl+c or 'exit' to leave · 'help' for commands"

	// CLIHelp is cmd/cli.go's help() output — the CLI's own command list, kept
	// separate from the TUI's slash-completion popup. /yolo works in CLI mode
	// (cmd/cli.go's KindYolo case) and belongs here alongside exit/clear/help.
	CLIHelp = "commands:  exit  ·  clear  ·  help  ·  /yolo  ·  /approve  ·  /about"

	// CLINotAvailable is the CLI dispatch loop's catch-all for a resolved
	// slash.Action whose Kind has no explicit case there — a %s format string
	// naming the command. It exists so a Kind with no CLI meaning (no picker,
	// no popup, nothing to wire up) is refused with a clear message instead of
	// silently falling through to KindSend and reaching the model as chat
	// text — see cmd/cli.go's default arm.
	CLINotAvailable = "%s is not available in CLI mode."

	// CLIResumeHint is appended to CLINotAvailable for /resume specifically:
	// unlike /loop or /goal, it has a real non-interactive equivalent already
	// wired up (app.applyResumeFlag), so the refusal can point at it instead
	// of just saying no.
	CLIResumeHint = "restart with `nib --resume` (or `nib --resume <id>`) to load a recorded session."

	// Shown when a CLI approval prompt gets no answer at all. A closed stdin
	// (the piped one-shot idiom) and a cancelled run are both "nobody
	// decided", which is not a yes, so the call is denied.
	CLIDeniedNoInput  = "denied: stdin closed, nobody left to approve this"
	CLIDeniedNoAnswer = "denied: no answer (the run was cancelled)"

	// Shown when --yolo / NIB_YOLO auto-approves every tool call. The header
	// carries the compact badge; the CLI prints the fuller notice at startup.
	YoloBadge  = "yolo"
	YoloNotice = "yolo — auto-approving every tool call (no prompts)"

	// StatusRunning is shown between an approved tool call and its result.
	StatusRunning = "running…"

	// Reasoning box copy. A collapsed box shows the trailing
	// ReasoningMaxLines lines of the live trace; the TUI composes the hint
	// line as "… " + n + ReasoningMore + ReasoningExpand.
	ReasoningMore     = " more · "
	ReasoningExpand   = "ctrl+r expand"
	ReasoningCollapse = "ctrl+r collapse"

	// Folded thought copy: the dim line the live reasoning box leaves in the
	// transcript once the answer starts (see ThoughtSummary).
	ThoughtLabel = "thought"
	ThoughtFor   = "thought for "

	// ask_user dialog copy (Phase 3 Task 11). HelpAsk is the footer help line
	// while a question is pending; the AskHint* lines sit beneath the option
	// list itself and, unlike HelpAsk, always mention the free-text escape
	// hatch (typing instead of picking), since that's the one thing every ask
	// dialog offers regardless of how it's answered.
	HelpAsk             = "up/down move · pgup/pgdn page · enter pick · esc cancel"
	AskHintSingleSelect = "up/down/pgup/pgdn move · enter pick · or type your answer"
	AskHintMultiSelect  = "up/down/pgup/pgdn move · space toggle · enter confirm · or type your answer"
	AskHintFreeText     = "type your answer"

	// AskBlockedByApproval replaces the ask dialog's normal hint when a tool
	// approval is also pending: the approval's key-driven choice mode swallows
	// every keypress (arrows, space, typed text) except its own, so none of
	// the usual ask-dialog affordances actually do anything until it resolves
	// — a silent dead end without this note.
	AskBlockedByApproval = "waiting on the tool approval above — resolve that first"

	// /resume picker copy (Phase 3 Task 15). ResumeTitle is the dialog's
	// heading; HelpResume is the footer help line while the picker is open —
	// no free-text escape hatch here (unlike HelpAsk), since a session id
	// picked from a list has no meaningful typed alternative. ResumeEmpty is
	// the notice for a cwd-scoped picker with nothing to show; ResumeRestored
	// (a %d format string for the message count) confirms a successful
	// restore.
	ResumeTitle    = "resume a session"
	HelpResume     = "up/down move · pgup/pgdn page · enter resume · d delete · esc cancel"
	ResumeEmpty    = "no recorded sessions here · /resume --all to look wider"
	ResumeRestored = "restored session · %d messages"

	// ResumeDeleteConfirm replaces the picker's normal hint once its delete
	// key has been pressed once (Task 20): deleting a recorded session
	// removes the file outright with no trash/undo (chat.SessionStore has
	// neither), so a single "d" only ARMS deletion of the highlighted row —
	// this is the prompt shown while armed. A second "d" (with nothing else
	// pressed in between) performs the delete; any other key cancels the arm.
	ResumeDeleteConfirm = "press d again to delete this session · any other key cancels"

	// YoloUsage is the /yolo slash command's usage error, shown when the
	// argument after "yolo" is neither empty, "on" nor "off".
	YoloUsage = "usage: /yolo [on|off]"
	// ApproveUsage is the /approve slash command's usage error.
	ApproveUsage = "usage: /approve [prompt|strict|allowlist|classify|auto]"
	// ApproveModeNotice reports the approval mode after /approve or Shift+Tab.
	ApproveModeNotice = "approval mode: %s"
	// ClassifyBadge is the header badge in approval_mode classify.
	ClassifyBadge = "classify"
	// AutoApprovedNotice is the transcript line for a call the classifier
	// approved without asking: category, confidence, the call.
	AutoApprovedNotice = "auto-approved · %s %.2f · %s"
	// ClassifierVerdict prefixes the classifier's verdict in the approval
	// prompt.
	ClassifierVerdict = "classifier: "

	// Built-in `/` completion entries (tui/completion.go's buildCompItems).
	// Name is the verb shown, matched against the typed query, and used to
	// build the option's Insert token; Desc is the one-line summary shown
	// beside it in the popup.
	CompLoopName     = "loop"
	CompLoopDesc     = "recurring or self-paced task"
	CompCompactName  = "compact"
	CompCompactDesc  = "compact the conversation"
	CompGoalName     = "goal"
	CompGoalDesc     = "set a goal nib checks before stopping"
	CompModelName    = "model"
	CompModelDesc    = "switch model (current provider)"
	CompModelsName   = "models"
	CompModelsDesc   = "list the current provider's models"
	CompAttachName   = "attach"
	CompAttachDesc   = "stage a file for the next message"
	CompYoloName     = "yolo"
	CompYoloDesc     = "toggle (or on/off) auto-approve every tool call"
	CompApproveName  = "approve"
	CompApproveDesc  = "set the approval mode (prompt, strict, allowlist, classify, auto)"
	CompResumeName   = "resume"
	CompResumeDesc   = "resume a recorded session"
	CompLoginName    = "login"
	CompLoginDesc    = "log in to a provider"
	CompLogoutName   = "logout"
	CompLogoutDesc   = "remove a stored provider login"
	CompEndpointName = "endpoint"
	CompEndpointDesc = "switch endpoint"
	CompAboutName    = "about"
	CompAboutDesc    = "show version, config paths, and tool inventory"

	// ToolResultNoOutput is fmtBashResult's (chat/resultfmt.go) fallback for a
	// failed bash/bash_job_output call whose stdout and stderr were both
	// empty — a %d format string for the exit code.
	ToolResultNoOutput = "(exit %d, no output)"

	// ToolExitCode is the detail a failed bash call's header carries — a %d
	// format string for the exit code.
	ToolExitCode = "exit %d"
	// ToolLineCount is the one-line summary a read collapses to in the
	// transcript — a %d format string for the number of lines read.
	ToolLineCount = "%d lines"
	// DiffNewFile tags a write that created its file, in the diff summary.
	DiffNewFile = "new file"
	// DiffMore is the fold line under a capped diff — a %d format string for
	// the number of rows not shown.
	DiffMore = "… %d more lines"

	// UsageEstimatedPrefix marks the session usage badge (tui/model.go's
	// usageBadge) when its figure is chat.Session.EstimatedUsage's byte/4
	// guess rather than measured spend — the same "~" convention prunedNotice
	// and compactNotice already use for their own estimates. Kept to a single
	// ASCII character on purpose: footerBadges drops the whole usage badge
	// when the footer is tight, so a longer marker only makes it disappear
	// sooner.
	UsageEstimatedPrefix = "~"
)

// ReasoningMaxLines is how many trailing lines a collapsed reasoning box
// shows.
const ReasoningMaxLines = 5

// CLIApprovePrompt builds the line-based CLI approval prompt (the TUI uses
// the numbered single-key menu instead). alwaysScope describes what `a`
// grants for this call — e.g. "`git …`", "any bash command", or a tool name.
func CLIApprovePrompt(alwaysScope string) string {
	return "y yes · a always (" + alwaysScope + ") · all this turn · n no · or type a change"
}

// Status verbs shown while the agent works.
const (
	VerbThinking = "thinking"
	VerbWorking  = "working"
	VerbReading  = "reading"
)

// EmptyExamples are the sample prompts shown on the first-run empty state.
var EmptyExamples = []string{
	"what changed in the last commit?",
	"undo my last git commit",
	"find every TODO in this repo",
}

// Ctrl+C / Esc copy. Ctrl+C does one step per press (see tui.handleCtrlC), and
// each step says what it did, so no press leaves the user guessing.
const (
	StatusInterrupting = "Interrupting…"
	HintDraftCleared   = "draft cleared · ↑ to restore"
	HintExitArmed      = "press ctrl+c again to exit"
	HintQueueHeld      = "queue on hold · enter send it · ↑↓ pick · ^e edit · ^x delete"

	NoticeGoalPaused       = "goal paused · /goal resume to continue, /goal clear to drop it"
	NoticeQueueHeld        = "%d queued, on hold · press enter on an empty composer to send"
	NoticeStillRunningHelp = " · ctrl+o logs · /loop stop"
)
