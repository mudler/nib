package plugin

import (
	"testing"

	"github.com/mudler/wiz/types"
)

func TestParseManifestNative(t *testing.T) {
	data := []byte(`
name: demo
version: 0.1.0
description: a demo plugin
wiz_version: ">=0.0.0"
mcp_servers:
  github:
    command: gh-mcp
    args: ["serve"]
    env: { TOKEN: abc }
agents:
  - name: researcher
    description: digs through docs
    system_prompt: be thorough
    tools: [bash]
`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "demo" || m.Version != "0.1.0" {
		t.Fatalf("meta wrong: %+v", m)
	}
	if got := m.MCPServers["github"].Command; got != "gh-mcp" {
		t.Fatalf("mcp command = %q", got)
	}
	if len(m.Agents) != 1 || m.Agents[0].Name != "researcher" {
		t.Fatalf("agents wrong: %+v", m.Agents)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		m       Manifest
		wiz     string
		wantErr bool
	}{
		{"ok", Manifest{Name: "a"}, "0.9.0", false},
		{"missing name", Manifest{}, "0.9.0", true},
		{"mcp missing command", Manifest{Name: "a", MCPServers: map[string]types.MCPServer{"x": {}}}, "0.9.0", true},
		{"agent missing name", Manifest{Name: "a", Agents: []types.AgentTypeConfig{{}}}, "0.9.0", true},
		{"wiz constraint met", Manifest{Name: "a", WizVersion: ">=0.5.0"}, "0.9.0", false},
		{"wiz constraint unmet", Manifest{Name: "a", WizVersion: ">=1.0.0"}, "0.9.0", true},
		{"dev build skips constraint", Manifest{Name: "a", WizVersion: ">=1.0.0"}, "", false},
		{"prefixed v version", Manifest{Name: "a", WizVersion: ">=0.5.0"}, "v0.9.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.m.Validate(c.wiz)
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestValidateRejectsUnsafeName(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "..", ".", "foo/../bar", `a\b`} {
		if err := (Manifest{Name: bad}).Validate("0.9.0"); err == nil {
			t.Errorf("expected name %q to be rejected", bad)
		}
	}
	for _, ok := range []string{"demo", "my-plugin", "my_plugin.v2", "Plugin123"} {
		if err := (Manifest{Name: ok}).Validate("0.9.0"); err != nil {
			t.Errorf("expected name %q to be accepted, got %v", ok, err)
		}
	}
}
