package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mudler/cogito"
)

// Bounds of wait_agents. A call blocks one tool call, so it always has a
// limit; the model waits again when it needs more.
const (
	waitAgentsDefaultTimeout = 30 * time.Second
	waitAgentsMinTimeout     = 10 * time.Second
	waitAgentsMaxTimeout     = 10 * time.Minute
)

// waitAgentsPoll is how often the sub-agent registry is read while waiting.
var waitAgentsPoll = 250 * time.Millisecond

const (
	waitModeAll            = "all"
	waitModeAnyFailedOrAll = "any_failed_or_all"
)

type waitAgentsArgs struct {
	IDs            []string `json:"ids" jsonschema:"sub-agent ids to wait for. Empty = every sub-agent that is running now."`
	Mode           string   `json:"mode" jsonschema:"'all' (default) = return when every agent is done; 'any_failed_or_all' = return when every agent is done or as soon as one fails"`
	TimeoutSeconds int      `json:"timeout_seconds" jsonschema:"longest time to block (default 30, min 10, max 600). On timeout the current statuses are returned; call again to keep waiting."`
}

// agentSource is where wait_agents reads sub-agent state. *cogito.AgentManager
// is the one the check_agent and get_agent_result tools read.
type agentSource interface {
	Get(id string) (*cogito.AgentState, bool)
	List() []*cogito.AgentState
}

type waitAgentsTool struct {
	src agentSource
	ctx context.Context
}

func (t *waitAgentsTool) Run(args map[string]any) (string, any, error) {
	var ids []string
	switch v := args["ids"].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
	case []string:
		ids = v
	}
	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = waitModeAll
	}
	if mode != waitModeAll && mode != waitModeAnyFailedOrAll {
		return fmt.Sprintf("Unknown mode %q: use %s or %s.", mode, waitModeAll, waitModeAnyFailedOrAll), nil, nil
	}
	n := 0
	switch v := args["timeout_seconds"].(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	}
	timeout := clampSeconds(n, waitAgentsDefaultTimeout, waitAgentsMinTimeout, waitAgentsMaxTimeout)
	return waitAgents(t.ctx, t.src, ids, mode, timeout), nil, nil
}

// waitAgents blocks until the agents in ids are done (mode all), or until one
// of them fails (any_failed_or_all), or until timeout. Whatever ends the wait,
// it returns the statuses as they are then.
func waitAgents(ctx context.Context, src agentSource, ids []string, mode string, timeout time.Duration) string {
	if len(ids) == 0 {
		for _, a := range src.List() {
			if a.Status == cogito.AgentStatusRunning {
				ids = append(ids, a.ID)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return "No sub-agent is running."
		}
	}
	var unknown []string
	for _, id := range ids {
		if _, ok := src.Get(id); !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return "Unknown sub-agent id(s): " + strings.Join(unknown, ", ") + ". Nothing was waited for."
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(waitAgentsPoll)
	defer tick.Stop()
	for {
		running, failed := 0, 0
		for _, id := range ids {
			a, _ := src.Get(id)
			switch a.Status {
			case cogito.AgentStatusRunning:
				running++
			case cogito.AgentStatusFailed:
				failed++
			}
		}
		switch {
		case running == 0:
			return agentReport(src, ids, "All agents are done.")
		case mode == waitModeAnyFailedOrAll && failed > 0:
			return agentReport(src, ids, fmt.Sprintf("%d agent(s) failed; %d still running. Stopped waiting early.", failed, running))
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return agentReport(src, ids, fmt.Sprintf("Timed out after %s: %d agent(s) still running. Call wait_agents again to keep waiting.", timeout, running))
		case <-ctx.Done():
			return agentReport(src, ids, "Stopped waiting: the turn was cancelled.")
		}
	}
}

func agentReport(src agentSource, ids []string, head string) string {
	var b strings.Builder
	b.WriteString(head + "\n")
	for _, id := range ids {
		a, _ := src.Get(id)
		fmt.Fprintf(&b, "- %s: %s", id, a.Status)
		if a.Task != "" {
			fmt.Fprintf(&b, " (task: %s)", oneLineN(a.Task, 80))
		}
		if a.Status == cogito.AgentStatusFailed && a.Error != nil {
			fmt.Fprintf(&b, " error: %s", oneLineN(a.Error.Error(), 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("Use get_agent_result for a finished agent's result.")
	return b.String()
}

func oneLineN(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

func waitAgentsToolDefinition(ctx context.Context, src agentSource) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&waitAgentsTool{src: src, ctx: ctx},
		waitAgentsArgs{},
		"wait_agents",
		"Block until several sub-agents are done, for at most timeout_seconds (default 30, max 600). Use it when you need the results of more than one agent before you can go on. mode=any_failed_or_all returns early when one agent fails, so you can react to it. On timeout it returns the current statuses, not an error: call it again to keep waiting. It reports statuses only; read a result with get_agent_result. If other work can go on, do that instead of waiting.",
	)
}
