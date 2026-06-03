package theme

// Microcopy — calm, lowercase, no wizard metaphor, no emoji.
const (
	BrandName = "nib"

	HelpDefault      = "enter send · ctrl+c interrupt · esc exit"
	HelpApproval     = "y yes · a always · n no · e edit · A all · esc deny"
	HelpApprovalEdit = "enter submit · esc cancel"
	ApprovePrompt    = "[y] yes  [a] always  [n] no  [e] edit  [A] all"
	ApproveEditHint  = "describe the change · enter submit · esc cancel"

	EmptyTagline = "a calm assistant for your terminal."
	EmptyTryLead = "try:"
	EmptySlash   = "type /  for skills, agents & commands"
	SlashHint    = "/ for skills"
	Starting     = "starting…"

	CLIWelcome = "a calm assistant for your terminal."
	CLIExit    = "ctrl+c or 'exit' to leave · 'help' for commands"
)

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
