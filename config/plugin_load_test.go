package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesEnabledPlugin(t *testing.T) {
	base := t.TempDir() // becomes <base>/wiz via XDG
	t.Setenv("XDG_CONFIG_HOME", base)

	wizBase := filepath.Join(base, "wiz")
	pluginRoot := filepath.Join(wizBase, "plugins", "demo")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: demo\n" +
		"mcp_servers:\n  pluginmcp:\n    command: pmcp\n" +
		"agents:\n  - name: fromplugin\n    description: d\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, "wiz-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := "plugins:\n  - name: demo\n    source_url: u\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(wizBase, "plugins.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if _, ok := cfg.MCPServers["pluginmcp"]; !ok {
		t.Fatalf("plugin mcp server not merged: %+v", cfg.MCPServers)
	}
	var found bool
	for _, a := range cfg.Agents {
		if a.Name == "fromplugin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin agent not merged into cfg.Agents: %+v", cfg.Agents)
	}
	// Built-in defaults still present (MergeAgentTypes ran after plugin merge).
	if findType(cfg.Agents, "explore") == nil {
		t.Fatal("built-in agent types lost")
	}
}
