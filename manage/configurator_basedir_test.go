package manage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/skill"
	"github.com/mudler/nib/types"
)

// seedUserRoot fills root with a config file and an ENABLED skill pack, then
// points the XDG resolution at its parent so root is what standalone nib would
// find. Returns nothing: the caller asserts on what does or does not surface.
func seedUserRoot(t *testing.T, xdgHome string) {
	t.Helper()
	root := filepath.Join(xdgHome, "nib")
	skillDir := filepath.Join(skill.SkillsDir(root), "userpack", "leaky")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: leaky\ndescription: from the user's real nib\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(skill.Entry{Name: "userpack", Enabled: true})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: user-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// EffectiveConfig is what a live session reloads through after any
// self-configuration change. Under a root override it must reload from the
// injected root: reloading from the default paths would pull the user's real
// model and merge the user's enabled skill packs into an embedded session.
func TestEffectiveConfigUsesInjectedRoot(t *testing.T) {
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	seedUserRoot(t, xdg)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("model: embedded-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewIn(root).EffectiveConfig()
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if cfg.Model != "embedded-model" {
		t.Fatalf("Model = %q, want embedded-model (read the user's real config)", cfg.Model)
	}
	if cfg.BaseDir != root {
		t.Fatalf("BaseDir = %q, want %q: the reloaded config dropped the override", cfg.BaseDir, root)
	}
	for _, s := range cfg.Skills {
		if s.Name == "leaky" {
			t.Fatalf("skill %q from the user's real nib root leaked into the reloaded config", s.Name)
		}
	}
}

// The zero override must reproduce standalone nib: EffectiveConfig reloads from
// the default XDG paths, including the user's enabled skill packs.
func TestEffectiveConfigWithoutOverrideUsesDefaultPaths(t *testing.T) {
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	seedUserRoot(t, xdg)

	cfg, err := NewIn("").EffectiveConfig()
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if cfg.Model != "user-model" {
		t.Fatalf("Model = %q, want user-model", cfg.Model)
	}
	var found bool
	for _, s := range cfg.Skills {
		if s.Name == "leaky" {
			found = true
		}
	}
	if !found {
		t.Fatalf("standalone reload lost the user's enabled skill pack: %+v", cfg.Skills)
	}
}

// NewIn resolves the registry root and the writable config path from the
// override, so plugin/skill operations land in the injected root too.
func TestNewInRootsManagersAtInjectedDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	root := t.TempDir()

	c := NewIn(root)
	if c.baseDir != root {
		t.Fatalf("baseDir = %q, want %q", c.baseDir, root)
	}
	if want := filepath.Join(root, "config.yaml"); c.configPath != want {
		t.Fatalf("configPath = %q, want %q", c.configPath, want)
	}

	if err := c.AddMCPServer("demo", types.MCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Fatalf("config not written into the injected root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "nib", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote into the user's real nib root: err = %v", err)
	}
}

// NewIn is the only constructor precisely so a Configurator cannot be built
// with a write target and a reload target that disagree. That invariant is what
// this pins: a server added through the Configurator must be visible to its own
// EffectiveConfig. The deleted two-argument New could satisfy the write half
// and still reload from ~/.config/nib.
func TestConfiguratorWriteAndReloadShareOneRoot(t *testing.T) {
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	seedUserRoot(t, xdg)

	c := NewIn(t.TempDir())
	if err := c.AddMCPServer("only-here", types.MCPServer{Command: "demo-mcp"}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	cfg, err := c.EffectiveConfig()
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if _, ok := cfg.MCPServers["only-here"]; !ok {
		t.Fatalf("the write target and the reload target disagree: %+v", cfg.MCPServers)
	}
}
