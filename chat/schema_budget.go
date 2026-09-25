package chat

import (
	"encoding/json"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
)

// schemaWarnFraction is the share of the context budget the fixed prompt
// overhead (tool schemas + system prompt) may take before SchemaBudget warns.
const schemaWarnFraction = 0.60

// schemaErrorFraction is the share of the whole context window the fixed prompt
// overhead may take before SchemaBudget reports an error: past it, compaction
// cannot free enough room for a useful turn.
const schemaErrorFraction = 0.90

// SchemaBudget is a read-only observation of how much of the context the fixed
// per-request overhead takes. Floor is the estimated token count of the tool
// schemas plus the system prompt — the floor no compaction can go below.
type SchemaBudget struct {
	Floor          int
	WarnThreshold  float64
	ErrorThreshold float64
	Warn           bool
	Error          bool
}

// estimateSchemaTokens estimates the tokens the tool schemas and the system
// prompt take in every request, with the same byte/4 heuristic estimateTokens
// uses for messages.
func estimateSchemaTokens(tools []cogito.ToolDefinitionInterface, systemPrompt string) int {
	n := len(systemPrompt)
	for _, td := range tools {
		if td == nil {
			continue
		}
		if b, err := json.Marshal(td.Tool()); err == nil {
			n += len(b)
		}
	}
	return n / 4
}

// withTool registers td as a cogito tool and records it, so SchemaBudget can
// measure the tool definitions the session advertises. cogito keeps its tool
// list private, so this is the only place the session can see them.
func (s *Session) withTool(td cogito.ToolDefinitionInterface) cogito.Option {
	s.schemaToolsMu.Lock()
	s.schemaTools = append(s.schemaTools, td)
	s.schemaToolsMu.Unlock()
	return cogito.WithTools(td)
}

// resetSchemaTools clears the recorded tool definitions; toolOptions calls it
// before it registers the tools again.
func (s *Session) resetSchemaTools() {
	s.schemaToolsMu.Lock()
	s.schemaTools = nil
	s.schemaToolsMu.Unlock()
}

// SchemaBudget estimates the fixed prompt overhead and compares it with the
// context budget and window. It does not change behavior; it only logs a
// warning when the overhead is large, for a caller to show the user.
//
// The tool definitions are the ones the last toolOptions call registered
// through withTool. MCP tools and the agent-spawning tools are added inside
// cogito and are not counted.
func (s *Session) SchemaBudget() SchemaBudget {
	s.schemaToolsMu.Lock()
	tools := append([]cogito.ToolDefinitionInterface(nil), s.schemaTools...)
	s.schemaToolsMu.Unlock()

	floor := estimateSchemaTokens(tools, s.systemPrompt)
	window := s.contextWindow()
	budget := ContextBudget(s.compactionConfig(), window)

	sb := SchemaBudget{
		Floor:          floor,
		WarnThreshold:  schemaWarnFraction,
		ErrorThreshold: schemaErrorFraction,
	}
	if window <= 0 {
		// Unknown window: nothing to compare against.
		return sb
	}
	sb.Warn = floor > int(float64(budget)*schemaWarnFraction)
	sb.Error = floor > int(float64(window)*schemaErrorFraction)
	switch {
	case sb.Error:
		xlog.Warn("tool schemas and system prompt fill almost the whole context window; compaction cannot make room",
			"schema_tokens", floor, "window", window, "budget", budget)
	case sb.Warn:
		xlog.Warn("tool schemas and system prompt take a large share of the context budget",
			"schema_tokens", floor, "window", window, "budget", budget)
	}
	return sb
}
