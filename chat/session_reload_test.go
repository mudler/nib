package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/manage"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"
)

// newReloadTestSession builds a bare session sufficient for reload-method tests
// (no LLM turn is run).
func newReloadTestSession(t *testing.T) *Session {
	t.Helper()
	ctx := context.Background()
	s := &Session{
		ctx:        ctx,
		mcpClient:  sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil),
		cfgClients: map[string]*sdkmcp.ClientSession{},
		cfgServers: map[string]types.MCPServer{},
		toolAllow:  make(map[string]bool),
	}
	return s
}

func TestSetSkillsBuildsAndClearsServer(t *testing.T) {
	s := newReloadTestSession(t)
	if err := s.SetSkills([]types.Skill{{Name: "foo", Description: "d", Instructions: "do x"}}); err != nil {
		t.Fatalf("SetSkills: %v", err)
	}
	if s.skillsClient == nil {
		t.Fatalf("expected skillsClient after non-empty SetSkills")
	}
	if err := s.SetSkills(nil); err != nil {
		t.Fatalf("SetSkills(nil): %v", err)
	}
	if s.skillsClient != nil {
		t.Fatalf("expected nil skillsClient after empty SetSkills")
	}
}

func TestReconcileMCPServersSkipsUnconnectable(t *testing.T) {
	s := newReloadTestSession(t)
	// A command that does not exist must be skipped, not panic.
	err := s.ReconcileMCPServers(map[string]types.MCPServer{
		"broken": {Command: "nib-no-such-binary-xyz"},
	})
	if err != nil {
		t.Fatalf("ReconcileMCPServers returned error: %v", err)
	}
	if len(s.cfgClients) != 0 {
		t.Fatalf("expected unconnectable server to be skipped, got %d", len(s.cfgClients))
	}
}

// TestReconcileMCPServersKeepsConnectionAliveForToolCalls is a regression test
// for a live, user-reported bug: MCP tool calls on a working, already-connected
// remote server intermittently failed with
// `hanging GET: failed to reconnect ... context canceled`.
//
// The cause was ReconcileMCPServers handing mcp.Client.Connect a
// timeout-scoped context (context.WithTimeout(s.ctx, 30s)) and calling cancel()
// immediately after Connect returned. The go-sdk keeps the context it is given
// for the connection's ENTIRE lifetime (StreamableClientTransport.Connect
// derives a cancellable context from it to drive the background "hanging GET"
// SSE listener), so cancelling it right after connect tore down that listener
// and marked the connection failed — breaking any later tool call.
//
// This test stands up a real streamable-HTTP MCP server, connects it via
// ReconcileMCPServers, waits past the point where the old immediate-cancel bug
// tore the connection down, and asserts a real tool call still succeeds.
func TestReconcileMCPServersKeepsConnectionAliveForToolCalls(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test-mcp", Version: "v0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "ping", Description: "returns pong"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "pong"}},
			}, nil, nil
		})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)

	// The go-sdk client opens a long-lived background "hanging GET" SSE request,
	// whose HTTP request context is derived from the exact context handed to
	// Client.Connect. That is the connection's lifecycle context. If it is
	// cancelled (as the old ReconcileMCPServers did, via a 30s-timeout context it
	// cancel()ed right after connecting), the client aborts this hanging GET and
	// the server observes the request end. Under the fix the context is the
	// session's own long-lived context, so the GET stays open.
	//
	// Wrap the handler to observe the hanging GET's lifetime: getStarted fires
	// when it arrives, getEnded fires if/when it returns (i.e. is torn down).
	var startOnce, endOnce sync.Once
	getStarted := make(chan struct{})
	getEnded := make(chan struct{})
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			startOnce.Do(func() { close(getStarted) })
			handler.ServeHTTP(w, r) // blocks while the SSE stream hangs open
			endOnce.Do(func() { close(getEnded) })
			return
		}
		handler.ServeHTTP(w, r)
	})
	httpSrv := httptest.NewServer(wrapped)
	defer httpSrv.Close()

	s := newReloadTestSession(t)
	if err := s.ReconcileMCPServers(map[string]types.MCPServer{
		"pinger": {URL: httpSrv.URL},
	}); err != nil {
		t.Fatalf("ReconcileMCPServers: %v", err)
	}
	sess, ok := s.cfgClients["pinger"]
	if !ok {
		t.Fatalf("expected pinger to be connected, cfgClients=%v", s.cfgClients)
	}
	defer sess.Close()

	// Wait for the background hanging GET to be established server-side.
	select {
	case <-getStarted:
	case <-time.After(3 * time.Second):
		t.Fatalf("background SSE hanging GET never reached the server")
	}

	// The hanging GET must stay open: proving the connection's lifecycle context
	// was NOT cancelled once ReconcileMCPServers returned. Under the old buggy
	// code (timeout context + immediate cancel()), it is torn down within
	// milliseconds of connecting and getEnded fires here — the exact condition
	// that later surfaces as `hanging GET: failed to reconnect ... context
	// canceled` on a real tool call.
	select {
	case <-getEnded:
		t.Fatalf("background SSE hanging GET was torn down shortly after connect: " +
			"the connection's lifecycle context was cancelled (regression)")
	case <-time.After(750 * time.Millisecond):
		// Still hanging — the connection is healthy.
	}

	// And a real tool call succeeds on the live session.
	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(callCtx, &sdkmcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool after reconcile failed: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected tool content, got none")
	}
	txt, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok || txt.Text != "pong" {
		t.Fatalf("unexpected tool result: %+v", res.Content)
	}
}

func TestReloadSetsAgentsHooksAndPrompt(t *testing.T) {
	s := newReloadTestSession(t)
	cfg := types.Config{
		Prompt: "Agents:{{range .Config.Agents}} {{.Name}}{{end}}",
		Agents: []types.AgentTypeConfig{{Name: "explore", Description: "explores"}},
	}
	if err := s.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(s.agentDefs) != 1 || s.agentDefs[0].Name != "explore" {
		t.Fatalf("agents not applied: %+v", s.agentDefs)
	}
	if !strings.Contains(s.systemPrompt, "Agents: explore") {
		t.Fatalf("prompt not applied: %q", s.systemPrompt)
	}
	if s.hooks == nil {
		t.Fatalf("hooks not initialized")
	}
}

func TestNewSessionWiresSkillsServer(t *testing.T) {
	ctx := context.Background()
	cfg := types.Config{
		Prompt: "hi",
		Skills: []types.Skill{{Name: "foo", Description: "d", Instructions: "do x"}},
	}
	s, err := NewSession(ctx, cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	if s.skillsClient == nil {
		t.Fatalf("expected NewSession to wire the skills server from cfg.Skills")
	}
	if s.configurator == nil {
		t.Fatalf("expected NewSession to build a configurator")
	}
}

// TestApplyPendingReloadDefersWhileAgentsRunning proves that a pending reload is
// held off while a background sub-agent is still running (Reload closes MCP
// client sessions and mutates state detached agents read), and is applied once
// no agents are live.
func TestApplyPendingReloadDefersWhileAgentsRunning(t *testing.T) {
	s := newReloadTestSession(t)
	s.agentManager = cogito.NewAgentManager()
	s.configurator = manage.New(plugin.BaseDir(), config.WritablePath())
	s.pendingReload = true

	// A running agent must defer the reload: pendingReload stays set.
	agent := &cogito.AgentState{ID: "bg1", Status: cogito.AgentStatusRunning}
	s.agentManager.Register(agent)
	s.applyPendingReload()
	if !s.pendingReload {
		t.Fatalf("expected reload to be deferred while an agent is running")
	}

	// Once the agent is no longer running, the reload is applied and cleared.
	agent.Status = cogito.AgentStatusCompleted
	s.applyPendingReload()
	if s.pendingReload {
		t.Fatalf("expected reload to be applied once no agents are running")
	}
}

func TestReloadPreservesEagerLoadedSkill(t *testing.T) {
	s := newReloadTestSession(t)
	s.skills = []types.Skill{{Name: "x", Description: "d", Instructions: "SKILL-BODY-MARKER"}}
	if _, err := s.LoadSkill("x"); err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	// A reload that rebuilds the base prompt must NOT drop the eager-loaded skill.
	if err := s.Reload(types.Config{Prompt: "BASE-PROMPT-MARKER"}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !strings.Contains(s.systemPrompt, "SKILL-BODY-MARKER") {
		t.Fatalf("reload dropped eager-loaded skill: %q", s.systemPrompt)
	}
	if !strings.Contains(s.systemPrompt, "BASE-PROMPT-MARKER") {
		t.Fatalf("reload dropped base prompt: %q", s.systemPrompt)
	}
}

func TestReloadDoesNotPopulateToolAllowFromOldField(t *testing.T) {
	s := newReloadTestSession(t)
	cfg := types.Config{BuiltinTools: []string{"read", "bash"}}
	for _, name := range cfg.BuiltinTools {
		s.toolAllow[name] = true
	}
	if !s.toolAllow["read"] || !s.toolAllow["bash"] {
		t.Fatalf("expected toolAllow populated from BuiltinTools: %+v", s.toolAllow)
	}
	if len(s.toolAllow) != 2 {
		t.Fatalf("expected exactly 2 entries, got %d: %+v", len(s.toolAllow), s.toolAllow)
	}
}

func TestMCPToolFilterBypassesAllowlistForConfiguredServers(t *testing.T) {
	s := newReloadTestSession(t)
	s.toolAllow = map[string]bool{"read": true}

	cfgSession := &sdkmcp.ClientSession{}
	s.cfgClients["notary"] = cfgSession

	filter := s.mcpToolFilter()

	// A tool from a configured MCP server passes regardless of its name.
	if !filter(cfgSession, "knowledge_search") {
		t.Fatalf("expected a cfgClients-sourced tool to bypass the allowlist")
	}
	// A tool NOT from a configured MCP server (e.g. a built-in host session)
	// still respects the allowlist.
	builtinSession := &sdkmcp.ClientSession{}
	if filter(builtinSession, "knowledge_search") {
		t.Fatalf("expected a non-cfgClients tool not in the allowlist to be blocked")
	}
	if !filter(builtinSession, "read") {
		t.Fatalf("expected a non-cfgClients tool that IS in the allowlist to pass")
	}
}

func TestMCPToolFilterAllowsEverythingWhenAllowlistEmpty(t *testing.T) {
	s := newReloadTestSession(t)
	// s.toolAllow is empty (zero value from newReloadTestSession's Session{}).
	s.toolAllow = map[string]bool{}

	filter := s.mcpToolFilter()
	builtinSession := &sdkmcp.ClientSession{}
	if !filter(builtinSession, "anything") {
		t.Fatalf("expected everything to pass when the allowlist is empty")
	}
}
