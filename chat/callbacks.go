package chat

import "time"

// AgentStatus mirrors cogito's sub-agent lifecycle states for UI consumption,
// decoupling the UI from the cogito type.
type AgentStatus string

const (
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
)

// AgentEvent is emitted on sub-agent lifecycle changes (spawn/complete/fail).
type AgentEvent struct {
	ID     string
	Type   string // agent type name (e.g. "explore"); empty for generic
	Task   string
	Status AgentStatus
	Result string
	Err    error
	// Populated on completion/failure events (zero otherwise):
	ToolCount   int           // tools the sub-agent executed
	TotalTokens int           // cumulative tokens consumed across the run
	Elapsed     time.Duration // wall-clock from spawn to completion
	// OutputTokens is the part of TotalTokens the sub-agent generated. The
	// session fills it from measured usage. A UI that counts the sub-agent's
	// stream may fill it with that count when the backend reports no usage,
	// and then sets OutputEstimated.
	OutputTokens    int
	OutputEstimated bool
	// TokensPerSec is the sub-agent's generation rate. The session cannot
	// time generation, so it leaves this 0; a UI that meters the sub-agent's
	// stream fills it.
	TokensPerSec float64
}

// StreamEvent is a single live delta during generation, forwarded only when a
// consumer sets Callbacks.OnStream (which opts the session into cogito's
// streaming path). It lets a UI render reasoning/answer/tool-selection as the
// model produces them, instead of only at step boundaries. Kind is one of
// "reasoning", "content", "tool_call", "tool_result", "status", "done", "error".
type StreamEvent struct {
	Kind     string // delta kind
	Content  string // text delta, for reasoning/content
	ToolName string // tool name, for tool_call (first chunk only)
	ToolArgs string // streamed argument fragment, for tool_call
	// AgentID is the sub-agent that produced the delta, "" for the main
	// agent. A foreground sub-agent keeps its delta's Kind; a background
	// one arrives with Kind "sub_agent".
	AgentID string
}

// Message represents a chat message.
type Message struct {
	Role    string
	Content string
}

// ToolCallRequest contains information about a tool the agent wants to run.
type ToolCallRequest struct {
	Name      string
	Arguments string
	Reasoning string
	AgentID   string // non-empty when the requesting caller is a sub-agent
	// ExternalSources identifies untrusted data still present in the active
	// conversation. A non-empty value forces consequential calls through the
	// approval gate unless session-wide auto-approval is active.
	ExternalSources []string
	// Change is the predicted effect of a write or edit call on its file, for
	// showing the call as a diff before it runs. Nil for other tools and for
	// files that cannot be diffed.
	Change *FileChange
	// Verdict is the classifier's opinion of the call in approval_mode
	// "classify", e.g. "destructive (0.82)" or "unavailable". Empty when no
	// classifier judged it.
	Verdict string
}

// ToolResult is the outcome of a tool execution, surfaced to the UI after the
// tool runs.
type ToolResult struct {
	Name      string
	Result    string
	Arguments string // marshaled JSON of the call's arguments, for display
	AgentID   string // non-empty when the tool was run by a sub-agent
	// Change is what a successful write or edit did to its file, for showing
	// the result as a diff. Nil for other tools, failed calls, and files that
	// cannot be diffed.
	Change *FileChange
}

// ToolStart announces an approved root-agent tool call that is about to run,
// so a UI can show it while it runs. Its ToolResult follows when it finishes.
type ToolStart struct {
	Name      string
	Arguments string // marshaled JSON of the call's arguments
}

// ToolCallResponse represents the user's decision on a tool call.
type ToolCallResponse struct {
	Approved    bool
	Adjustment  string
	AlwaysAllow bool
	// AlwaysPrefix narrows an AlwaysAllow grant for the bash tool to scripts
	// whose first word matches (e.g. "git" → simple `git …` commands run
	// without prompting). Empty means the grant covers the whole tool.
	// Ignored unless AlwaysAllow is set.
	AlwaysPrefix string
	// AllowAllTurn, when set together with Approved, approves every remaining
	// tool call for the rest of the current turn (incl. sub-agents) w/o prompting.
	AllowAllTurn bool
}

// AskRequest is a question the agent wants to ask the user.
type AskRequest struct {
	Question string
	Options  []string // optional multiple-choice options
	// MultiSelect, when true, lets the user pick several options (checkbox);
	// otherwise it's a single choice (radio). Only meaningful with Options.
	MultiSelect bool
}

// CronRequest is a recurring/one-shot job the agent registers (cron tool).
type CronRequest struct {
	Expr          string
	Prompt        string
	Recurring     bool
	Durable       bool
	MonitorScript string
	MonitorURL    string
}

// Callbacks defines the interface for UI interactions.
type Callbacks struct {
	OnStatus    func(status string)
	OnReasoning func(reasoning string)
	// OnStream, when set, receives live token-level deltas during generation
	// (reasoning/answer/tool-selection) so a UI can render progress as it
	// happens. Setting it opts the session into cogito's streaming path; the
	// existing step-boundary callbacks (OnReasoning/OnStatus/OnToolResult) still
	// fire afterwards. Optional — leave nil for the non-streaming path.
	//
	// Caveat worth knowing before you opt in: it currently zeroes token
	// accounting. cogito accumulates streaming usage from StreamEvent.Usage on
	// the done event, and its bundled clients never populate that field, so
	// Session.Usage reports 0 tokens for every streamed turn. nib's own CLI and
	// TUI do not set this, so the shipped binary is unaffected; an embedder that
	// sets it trades the token counter for live deltas until cogito's clients
	// request usage from the API.
	//
	// This is the only under-count an embedder opts into, not the only one the
	// counter has: SessionUsage's doc carries the known list, and all of them
	// under-report rather than invent spend.
	OnStream   func(ev StreamEvent)
	OnToolCall func(req ToolCallRequest) ToolCallResponse
	// OnAutoApproved, when set, is told about each call the classifier
	// approved without asking, so a UI can show what it let through.
	OnAutoApproved func(req ToolCallRequest, v Verdict)
	// OnStepContent is called with the assistant text that accompanied a tool
	// selection ("I'll search for X now…") at the step boundary, before the
	// selected tools run — so a UI can commit the commentary in chronological
	// order relative to OnToolResult. Never fires with empty content and never
	// for the turn's final reply (that arrives via OnResponse). Optional.
	OnStepContent func(content string)
	OnResponse    func(response string)
	OnError       func(err error)
	// OnToolStart is called when an approved root-agent tool call is about
	// to run. OnToolResult reports the same call when it ends. Optional.
	OnToolStart func(ts ToolStart)
	// OnToolResult is called after a tool finishes, with its output. Optional.
	OnToolResult func(res ToolResult)
	// OnAgentEvent is called on sub-agent lifecycle changes. Optional.
	OnAgentEvent func(ev AgentEvent)
	// OnAskUser is called when the agent asks the user a question (ask_user tool).
	// It blocks until the user answers and returns the answer.
	OnAskUser func(req AskRequest) string
	// OnScheduleWakeup is called when the agent schedules an in-session wake-up
	// (schedule_wakeup tool). It returns immediately with a confirmation; the
	// host re-engages the agent with the note once the delay elapses.
	OnScheduleWakeup func(req WakeupRequest) string
	// OnCronCreate registers a cron job and returns a confirmation (incl. its id).
	OnCronCreate func(req CronRequest) string
	// OnCronList returns a human-readable listing of active cron jobs.
	OnCronList func() string
	// OnCronDelete cancels a cron job by id and returns a confirmation.
	OnCronDelete func(id string) string
	// OnCronPause, OnCronResume and OnCronTrigger pause, resume, or run now
	// the cron job with the given id, and return a confirmation.
	OnCronPause   func(id string) string
	OnCronResume  func(id string) string
	OnCronTrigger func(id string) string
	// OnCompactDone is called after the conversation is compacted, with the
	// approximate token counts before and after. Optional.
	OnCompactDone func(before, after int)
	// OnPruneDone is called when tool-output pruning stubs results it had not
	// stubbed before, with how many results were replaced on this pass and the
	// approximate tokens that freed. It fires on the transition, not on every
	// LLM call: a call that re-applies existing stubs is silent. Optional.
	OnPruneDone func(results, freed int)
	// OnParked is called when the live run parks: the assistant has produced a
	// reply but cogito keeps the loop alive because background work (sub-agents
	// or shell jobs) is still pending or because the user may inject a follow-up.
	// reply is the assistant's text at the park point (may be empty). The host
	// can finalize the assistant turn in the transcript and unlock the composer
	// so the user can keep chatting (their input is injected into this same run).
	// May fire multiple times across one run. Optional.
	OnParked func(reply string)
	// OnResumed is called when an injected message wakes a parked run. The host
	// can re-lock the composer and show the working indicator again. May fire
	// multiple times across one run. Optional.
	OnResumed func()
}
