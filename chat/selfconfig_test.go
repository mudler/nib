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

// TestSelfConfigToolSchemasBuild guards against arg-struct fields that cogito's
// JSON-schema generator cannot handle (notably map fields, which panic). This is
// the schema-generation path the agent loop runs when registering tools — the
// step that plain Run-based tests skip.
func TestSelfConfigToolSchemasBuild(t *testing.T) {
	for _, d := range selfConfigToolDefs(newToolConfigurator(t), func() {}) {
		tool := d.def.Tool() // panics if the arg schema is unsupported
		if tool.Function == nil || tool.Function.Name != d.name {
			t.Fatalf("tool %q built a bad definition: %+v", d.name, tool)
		}
	}
}

func TestAddMCPServerParsesEnv(t *testing.T) {
	c := newToolConfigurator(t)
	defs := selfConfigToolDefs(c, func() {})
	out := runTool(t, defs, "add_mcp_server", map[string]any{
		"name": "svc", "command": "svc-bin",
		"env": []any{"TOKEN=abc", "MODE=fast"},
	})
	if !strings.Contains(out, "svc") {
		t.Fatalf("add_mcp_server: %q", out)
	}
}
