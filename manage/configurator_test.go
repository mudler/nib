package manage

import (
	"path/filepath"
	"testing"

	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"
)

func newTestConfigurator(t *testing.T) (*Configurator, string) {
	t.Helper()
	base := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	return New(base, cfgPath), base
}

func TestListPluginsEmpty(t *testing.T) {
	c, _ := newTestConfigurator(t)
	plugins, err := c.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(plugins))
	}
}

func TestListPluginsReflectsRegistry(t *testing.T) {
	c, base := newTestConfigurator(t)
	reg, err := plugin.LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	reg.Upsert(plugin.Entry{Name: "demo", SourceURL: "https://example/demo", Ref: "main", Enabled: true})
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	plugins, err := c.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(plugins) != 1 || plugins[0].Name != "demo" || !plugins[0].Enabled {
		t.Fatalf("unexpected: %+v", plugins)
	}
}

func TestEnabledMCPServersDropsDisabled(t *testing.T) {
	in := map[string]types.MCPServer{
		"on":  {Command: "a"},
		"off": {Command: "b", Disabled: true},
	}
	out := enabledMCPServers(in)
	if _, ok := out["off"]; ok {
		t.Fatalf("disabled server should be dropped: %+v", out)
	}
	if _, ok := out["on"]; !ok {
		t.Fatalf("enabled server should be kept: %+v", out)
	}
	// Input map is not mutated (in still has both).
	if len(in) != 2 {
		t.Fatalf("input map must not be mutated: %+v", in)
	}
	// nil in, nil out.
	if enabledMCPServers(nil) != nil {
		t.Fatalf("nil input should return nil")
	}
}
