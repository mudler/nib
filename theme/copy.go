package theme

// Microcopy — calm, lowercase, no wizard metaphor, no emoji.
const (
	BrandName = "nib"

	HelpDefault      = "enter send · ctrl+y use command · esc exit"
	HelpApproval     = "1 once · 2 always · 3 this turn · n no · e edit · esc deny"
	HelpApprovalEdit = "enter submit · esc cancel"
	ApproveEditHint  = "describe the change · enter submit · esc cancel"

	// The numbered approval menu. Line 2 is dynamic — the TUI composes
	// ApproveAlwaysPrefix + chat.GrantScope(...) + ApproveAlwaysSuffix.
	ApproveOnce         = "[1] run it once"
	ApproveAlwaysPrefix = "[2] always allow "
	ApproveAlwaysSuffix = "  (this session)"
	ApproveTurn         = "[3] yes to everything this turn"
	ApproveDenyEdit     = "[n] no · [e] edit"

	EmptyTagline = "a calm assistant for your terminal."
	EmptyTryLead = "try:"
	EmptySlash   = "type /  for skills, agents & commands"
	SlashHint    = "/ for skills"
	Starting     = "starting…"

	CLIWelcome = "a calm assistant for your terminal."
	CLIExit    = "ctrl+c or 'exit' to leave · 'help' for commands"

	// Shown when --yolo / NIB_YOLO auto-approves every tool call. The header
	// carries the compact badge; the CLI prints the fuller notice at startup.
	YoloBadge  = "yolo"
	YoloNotice = "yolo — auto-approving every tool call (no prompts)"
)

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

// Status renders a verb with an animated trailing run of dots:
// phase 0 → "thinking", 1 → "thinking.", 2 → "thinking..", 3 → "thinking…".
func Status(verb string, phase int) string {
	dots := []string{"", ".", "..", "…"}
	return verb + dots[phase%len(dots)]
}
