package mcp

import (
	"context"
	"testing"

	"github.com/mudler/nib/types"
)

func TestStartTransportsReturnsOnlyBuiltins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Skills and config MCP servers are now wired by the Session, not here:
	// StartTransports returns only the built-in bash + filesystem servers,
	// regardless of cfg contents.
	base, err := StartTransports(ctx, types.Config{}, NewShellJobs())
	if err != nil {
		t.Fatalf("StartTransports (empty cfg): %v", err)
	}
	withExtras, err := StartTransports(ctx, types.Config{
		Skills:     []types.Skill{{Name: "s", Instructions: "body"}},
		MCPServers: map[string]types.MCPServer{"x": {Command: "true"}},
	}, NewShellJobs())
	if err != nil {
		t.Fatalf("StartTransports (with skills+mcp): %v", err)
	}
	if len(base) != len(withExtras) {
		t.Fatalf("StartTransports should ignore skills/mcp_servers now: %d vs %d", len(base), len(withExtras))
	}
}
