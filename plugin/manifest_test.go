package plugin

import "testing"

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
