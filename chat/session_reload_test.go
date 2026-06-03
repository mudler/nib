package chat

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
	if s.systemPrompt != "Agents: explore" {
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
