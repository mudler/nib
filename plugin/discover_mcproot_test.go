package plugin

import (
	"testing"

	"github.com/mudler/nib/types"
)

func TestExpandServerRoot(t *testing.T) {
	root := "/home/u/.config/wiz/plugins/demo"
	in := types.MCPServer{
		Command: "${NIB_PLUGIN_ROOT}/bin/echo-mcp",
		Args:    []string{"--data", "${CLAUDE_PLUGIN_ROOT}/data", "static"},
		Env:     map[string]string{"ROOT": "${NIB_PLUGIN_ROOT}", "X": "y"},
	}
	out := expandServerRoot(in, root)

	if out.Command != root+"/bin/echo-mcp" {
		t.Fatalf("command not expanded: %q", out.Command)
	}
	if out.Args[0] != "--data" || out.Args[1] != root+"/data" || out.Args[2] != "static" {
		t.Fatalf("args not expanded correctly: %v", out.Args)
	}
	if out.Env["ROOT"] != root || out.Env["X"] != "y" {
		t.Fatalf("env not expanded correctly: %v", out.Env)
	}

	// The original manifest value must be left untouched (we return a copy).
	if in.Command != "${NIB_PLUGIN_ROOT}/bin/echo-mcp" || in.Args[1] != "${CLAUDE_PLUGIN_ROOT}/data" {
		t.Fatalf("input was mutated: %+v", in)
	}
}

func TestExpandServerRootRemote(t *testing.T) {
	t.Setenv("NIB_TEST_TOKEN", "tok")
	in := types.MCPServer{
		URL:       "http://127.0.0.1:${NIB_TEST_PORT_UNSET:-27182}/mcp",
		Transport: "sse",
		Headers:   map[string]string{"Authorization": "Bearer ${NIB_TEST_TOKEN}"},
		Disabled:  true,
	}
	out := expandServerRoot(in, "/root")
	if out.URL != "http://127.0.0.1:27182/mcp" || out.Transport != "sse" || out.Headers["Authorization"] != "Bearer tok" || !out.Disabled {
		t.Fatalf("remote server lost fields: %+v", out)
	}
	m := Manifest{Name: "r", MCPServers: map[string]types.MCPServer{"r": in}}
	if err := m.Validate(""); err != nil {
		t.Fatalf("a url-only server is valid: %v", err)
	}
	m.MCPServers["bad"] = types.MCPServer{}
	if err := m.Validate(""); err == nil {
		t.Fatal("a server needs a command or a url")
	}
}
