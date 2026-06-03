package chat

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/manage"
)

func newToolConfigurator(t *testing.T) *manage.Configurator {
	t.Helper()
	return manage.New(t.TempDir(), filepath.Join(t.TempDir(), "config.yaml"))
}

func runTool(t *testing.T, defs []toolDef, name string, args map[string]any) string {
	t.Helper()
	for _, d := range defs {
		if d.name == name {
			out, _, err := d.tool.Run(args)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return out
		}
	}
	t.Fatalf("tool %q not found", name)
	return ""
}

func TestSelfConfigToolsListAndAdd(t *testing.T) {
	c := newToolConfigurator(t)
	defs := selfConfigToolDefs(c, func() {})

	if out := runTool(t, defs, "list_plugins", map[string]any{}); !strings.Contains(out, "No plugins") {
		t.Fatalf("list_plugins: %q", out)
	}
	if out := runTool(t, defs, "add_mcp_server", map[string]any{
		"name": "weather", "command": "weather-mcp",
	}); !strings.Contains(out, "weather") {
		t.Fatalf("add_mcp_server: %q", out)
	}
	if out := runTool(t, defs, "list_mcp_servers", map[string]any{}); !strings.Contains(out, "weather") {
		t.Fatalf("list_mcp_servers: %q", out)
	}
	if out := runTool(t, defs, "generate_skill", map[string]any{
		"name": "greet", "description": "greets", "instructions": "say hi",
	}); !strings.Contains(out, "greet") {
		t.Fatalf("generate_skill: %q", out)
	}
}

func TestSelfConfigToolDefinitionsCount(t *testing.T) {
	defs := selfConfigToolDefs(newToolConfigurator(t), func() {})
	if len(defs) != 10 {
		t.Fatalf("expected 10 tools, got %d", len(defs))
	}
}
