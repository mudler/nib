package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// mcpListTimeout bounds how long mcpSchemaCosts waits for one server to list
// its tools.
const mcpListTimeout = 3 * time.Second

// schemaWarnFraction is the share of the context budget the fixed prompt
// overhead (tool schemas + system prompt) may take before SchemaBudget warns.
const schemaWarnFraction = 0.60

// schemaErrorFraction is the share of the whole context window the fixed prompt
// overhead may take before SchemaBudget reports an error: past it, compaction
// cannot free enough room for a useful turn.
const schemaErrorFraction = 0.90

// SchemaBudget is a read-only observation of how much of the context the fixed
// per-request overhead takes. Floor is the token count of the tool schemas plus
// the system prompt — the floor no compaction can go below. Measured says Floor
// comes from a request the session actually sent (see trackedLLM.prepare);
// before the first one it is an estimate. Servers is the MCP servers' share of
// it, largest first.
type SchemaBudget struct {
	Floor          int
	Measured       bool
	Servers        []ServerSchemaCost
	WarnThreshold  float64
	ErrorThreshold float64
	Warn           bool
	Error          bool
}

// ServerSchemaCost is what one MCP server's tools add to every request: the
// number of tools it advertises and the byte/4 size of their schemas.
type ServerSchemaCost struct {
	Server string
	Tools  int
	Tokens int
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

// invalidateSchemaCosts drops the cached mcpSchemaCosts result; called when
// the set of MCP servers changes.
func (s *Session) invalidateSchemaCosts() {
	s.schemaCostsMu.Lock()
	s.schemaCosts, s.schemaCostsValid = nil, false
	s.schemaCostsMu.Unlock()
}

// namedClients returns the session's MCP clients with a name for each: the
// configured name of a config server, "skills" for the skills server, and the
// server's own reported name for a built-in one.
func (s *Session) namedClients() ([]string, []*mcp.ClientSession) {
	var names []string
	var clients []*mcp.ClientSession
	for i, c := range s.clients {
		name := fmt.Sprintf("mcp-%d", i+1)
		if r := c.InitializeResult(); r != nil && r.ServerInfo != nil && r.ServerInfo.Name != "" {
			name = r.ServerInfo.Name
		}
		names, clients = append(names, name), append(clients, c)
	}
	if s.skillsClient != nil {
		names, clients = append(names, "skills"), append(clients, s.skillsClient)
	}
	for _, name := range slices.Sorted(maps.Keys(s.cfgClients)) {
		names, clients = append(names, name), append(clients, s.cfgClients[name])
	}
	return names, clients
}

// mcpSchemaCosts lists the tools of every connected MCP client (the ones
// toolOptions hands cogito.WithMCPs) and returns what each server adds to a
// request, largest first. Tools the turn's filter drops are not counted, as
// cogito does not send them. Each listing is bounded by mcpListTimeout; a
// server that fails to list is left out. The result is cached until the set of
// servers changes (ReconcileMCPServers, SetSkills).
//
// Like allClients, it reads the client set without a lock: that set changes
// only at turn start, on the turn goroutine.
func (s *Session) mcpSchemaCosts(ctx context.Context) []ServerSchemaCost {
	s.schemaCostsMu.Lock()
	if s.schemaCostsValid {
		out := slices.Clone(s.schemaCosts)
		s.schemaCostsMu.Unlock()
		return out
	}
	s.schemaCostsMu.Unlock()

	names, clients := s.namedClients()
	filter := s.mcpToolFilter()
	var out []ServerSchemaCost
	for i, c := range clients {
		lctx, cancel := context.WithTimeout(ctx, mcpListTimeout)
		res, err := c.ListTools(lctx, nil)
		cancel()
		if err != nil {
			xlog.Debug("schema budget: cannot list MCP tools", "server", names[i], "error", err)
			continue
		}
		cost := ServerSchemaCost{Server: names[i]}
		bytes := 0
		for _, t := range res.Tools {
			if !filter(c, t.Name) {
				continue
			}
			ot := openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{
				Name: t.Name, Description: t.Description, Parameters: t.InputSchema}}
			if b, err := json.Marshal(ot); err == nil {
				bytes += len(b)
			}
			cost.Tools++
		}
		if cost.Tools == 0 {
			continue
		}
		cost.Tokens = bytes / 4
		out = append(out, cost)
	}
	slices.SortStableFunc(out, func(a, b ServerSchemaCost) int { return cmp.Compare(b.Tokens, a.Tokens) })

	s.schemaCostsMu.Lock()
	s.schemaCosts, s.schemaCostsValid = out, true
	s.schemaCostsMu.Unlock()
	return slices.Clone(out)
}

// SchemaBudget reports the fixed prompt overhead and compares it with the
// context budget and window. It does not change behavior; it only logs a
// warning when the overhead is large, for a caller to show the user.
//
// Once the session sent a request with tools, Floor is what the request
// wrapper measured on the latest one (all the tools cogito sent, MCP and agent
// tools included, plus the system messages), raised by the backend's own count
// when that was larger. Before that it is an estimate: the tool definitions the
// last toolOptions call registered through withTool, the system prompt, and
// the MCP servers' tools.
func (s *Session) SchemaBudget() SchemaBudget {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	servers := s.mcpSchemaCosts(ctx)

	floor, measured := s.live.schemaFloor()
	if !measured {
		s.schemaToolsMu.Lock()
		tools := append([]cogito.ToolDefinitionInterface(nil), s.schemaTools...)
		s.schemaToolsMu.Unlock()
		floor = estimateSchemaTokens(tools, s.systemPrompt)
		for _, c := range servers {
			floor += c.Tokens
		}
	}
	window := s.contextWindow()
	budget := ContextBudget(s.compactionConfig(), window)

	sb := SchemaBudget{
		Floor:          floor,
		Measured:       measured,
		Servers:        servers,
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

// notifySchemaBudget tells the user, through OnStatus, when the fixed prompt
// overhead first passes the warn threshold, and again only when it passes the
// error threshold after that.
func (s *Session) notifySchemaBudget() {
	if s.callbacks.OnStatus == nil {
		return
	}
	sb := s.SchemaBudget()
	level := 0
	switch {
	case sb.Error:
		level = 2
	case sb.Warn:
		level = 1
	}
	s.schemaCostsMu.Lock()
	if level <= s.schemaNoticeLevel {
		s.schemaCostsMu.Unlock()
		return
	}
	s.schemaNoticeLevel = level
	s.schemaCostsMu.Unlock()
	s.callbacks.OnStatus(schemaBudgetNotice(sb, s.contextWindow()))
}

// schemaBudgetNotice is the one-line notice for sb, naming at most the three
// largest MCP servers.
func schemaBudgetNotice(sb SchemaBudget, window int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tool schemas use %s of the %s context window", fmtTokensK(sb.Floor), fmtTokensK(window))
	if len(sb.Servers) > 0 {
		parts := make([]string, 0, 4)
		for _, c := range sb.Servers[:min(3, len(sb.Servers))] {
			parts = append(parts, fmt.Sprintf("%s %s (%d tools)", c.Server, fmtTokensK(c.Tokens), c.Tools))
		}
		if len(sb.Servers) > 3 {
			parts = append(parts, "…")
		}
		b.WriteString(" — ")
		b.WriteString(strings.Join(parts, ", "))
	}
	b.WriteString(": disable MCP servers or filter their tools")
	return b.String()
}

// fmtTokensK renders a token count as "34k", or as is below a thousand.
func fmtTokensK(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%dk", (n+500)/1000)
}
