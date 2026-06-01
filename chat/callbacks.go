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

// ToolCallResponse represents the user's decision on a tool call.
type ToolCallResponse struct {
	Approved    bool
	Adjustment  string
	AlwaysAllow bool
}

// Plan represents a plan with description and subtasks.
type Plan struct {
	Description string
	Subtasks    []string
}

// PlanResponse represents the user's decision on a plan.
type PlanResponse struct {
	Approved bool
}

// AskRequest is a question the agent wants to ask the user.
type AskRequest struct {
	Question string
	Options  []string // optional multiple-choice options
}

// Callbacks defines the interface for UI interactions.
type Callbacks struct {
	OnStatus    func(status string)
	OnReasoning func(reasoning string)
	OnToolCall  func(req ToolCallRequest) ToolCallResponse
	OnPlan      func(plan Plan) PlanResponse
	OnResponse  func(response string)
	OnError     func(err error)
	// OnAgentEvent is called on sub-agent lifecycle changes. Optional.
	OnAgentEvent func(ev AgentEvent)
	// OnAskUser is called when the agent asks the user a question (ask_user tool).
	// It blocks until the user answers and returns the answer.
	OnAskUser func(req AskRequest) string
}
