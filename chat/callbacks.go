package chat

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
}

// ToolResult is the outcome of a tool execution, surfaced to the UI after the
// tool runs.
type ToolResult struct {
	Name      string
	Result    string
	Arguments string // marshaled JSON of the call's arguments, for display
	AgentID   string // non-empty when the tool was run by a sub-agent
}

// ToolCallResponse represents the user's decision on a tool call.
type ToolCallResponse struct {
	Approved    bool
	Adjustment  string
	AlwaysAllow bool
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

// Callbacks defines the interface for UI interactions.
type Callbacks struct {
	OnStatus    func(status string)
	OnReasoning func(reasoning string)
	OnToolCall  func(req ToolCallRequest) ToolCallResponse
	OnResponse  func(response string)
	OnError     func(err error)
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
}
