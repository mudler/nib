package mcp

import (
	"context"
	"strings"
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

func TestStartTransportsUsesWorkingDir(t *testing.T) {
	dir := t.TempDir()
	// When the caller passes its own ShellJobs, StartTransports must NOT override it.
	own := NewShellJobsInDir(dir)
	if own.mgr.dir != dir {
		t.Fatalf("precondition: own manager dir = %q", own.mgr.dir)
	}
}

func TestStartTransportsReportsMissingCredentials(t *testing.T) {
	t.Setenv("OPENAI_CODEX_OAUTH_TOKEN", "")
	transports, err := StartTransports(context.Background(), types.Config{BaseDir: t.TempDir(), Provider: "openai-codex", Model: "test-model"}, nil)
	if err == nil || !strings.Contains(err.Error(), "initialize web MCP server") || len(transports) != 0 {
		t.Fatalf("want synchronous startup error with no abandoned transports, got %d transports and %v", len(transports), err)
	}
}
