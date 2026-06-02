package plugin

import (
	"testing"

	"github.com/mudler/nib/types"
)

func TestExpandServerRoot(t *testing.T) {
	root := "/home/u/.config/wiz/plugins/demo"
	in := types.MCPServer{
		Command: "${WIZ_PLUGIN_ROOT}/bin/echo-mcp",
		Args:    []string{"--data", "${CLAUDE_PLUGIN_ROOT}/data", "static"},
		Env:     map[string]string{"ROOT": "${WIZ_PLUGIN_ROOT}", "X": "y"},
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
	if in.Command != "${WIZ_PLUGIN_ROOT}/bin/echo-mcp" || in.Args[1] != "${CLAUDE_PLUGIN_ROOT}/data" {
		t.Fatalf("input was mutated: %+v", in)
	}
}
