package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/cogito"
)

// agentLogLimit bounds how many activity lines we retain per sub-agent.
const agentLogLimit = 60

// agentLogStore captures a rolling, per-sub-agent activity log (tool calls and
// their results) so the parent agent can inspect what a backgrounded sub-agent
// is doing or did. It correlates results back to an agent by tool-call id, since
// the result callback doesn't carry the agent id itself.
type agentLogStore struct {
	mu        sync.Mutex
	lines     map[string][]string // agentID -> recent activity lines
	toolAgent map[string]string   // toolCallID -> agentID
}

func newAgentLogStore() *agentLogStore {
	return &agentLogStore{lines: map[string][]string{}, toolAgent: map[string]string{}}
}

func (s *agentLogStore) append(agentID, line string) {
	if agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := append(s.lines[agentID], line)
	if len(l) > agentLogLimit {
		l = l[len(l)-agentLogLimit:]
	}
	s.lines[agentID] = l
}

// recordCall logs a sub-agent's tool request and remembers its id for result
// correlation.
func (s *agentLogStore) recordCall(agentID string, tool *cogito.ToolChoice) {
	if agentID == "" || tool == nil {
		return
	}
	if tool.ID != "" {
		s.mu.Lock()
		s.toolAgent[tool.ID] = agentID
		s.mu.Unlock()
	}
	args := ""
	if len(tool.Arguments) > 0 {
		if b, err := json.Marshal(tool.Arguments); err == nil {
			args = truncMid(string(b), 200)
		}
	}
	s.append(agentID, fmt.Sprintf("→ %s(%s)", tool.Name, args))
}

// recordResult logs a tool result against the agent that issued the call.
func (s *agentLogStore) recordResult(status cogito.ToolStatus) {
	s.mu.Lock()
	agentID := s.toolAgent[status.ToolArguments.ID]
	s.mu.Unlock()
	if agentID == "" {
		return
	}
	s.append(agentID, fmt.Sprintf("← %s: %s", status.Name, truncMid(strings.TrimSpace(status.Result), 400)))
}

// dump returns the captured log for an agent (newest entries last), or "".
func (s *agentLogStore) dump(agentID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := s.lines[agentID]
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// truncMid shortens s to max runes, keeping head and tail with an ellipsis.
func truncMid(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := (max - 1) / 2
	return s[:half] + "…" + s[len(s)-half:]
}

// --- agent_logs tool ---

type agentLogsArgs struct {
	AgentID string `json:"agent_id" jsonschema:"the id of the sub-agent whose activity log to read"`
}

type agentLogsTool struct {
	read func(agentID string) string
}

func (t *agentLogsTool) Run(args map[string]any) (string, any, error) {
	id, _ := args["agent_id"].(string)
	out := t.read(id)
	if out == "" {
		return fmt.Sprintf("No activity recorded for sub-agent %q (unknown id, or it hasn't called any tools yet).", id), nil, nil
	}
	return out, nil, nil
}

// agentLogsToolDefinition builds the cogito tool definition for agent_logs.
func agentLogsToolDefinition(read func(agentID string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&agentLogsTool{read: read},
		agentLogsArgs{},
		"agent_logs",
		"Read the recent activity log (tool calls and their results) of a sub-agent by id. Use it to monitor what a backgrounded sub-agent is doing or did, complementing check_agent (status) and get_agent_result (final result).",
	)
}
