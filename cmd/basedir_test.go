package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
)

// isolatedHome points nib's default resolution at an empty throwaway root, so
// any state a command creates there is a leak out of the injected root. It
// returns that default root (which must stay untouched).
func isolatedHome(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// plugin.BaseDir falls back to the legacy wiz dir only when the nib dir is
	// absent, so create the nib dir to pin the default to a single known path.
	root := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// assertUntouched fails if the command wrote any registry or config file into
// nib's default root.
func assertUntouched(t *testing.T, defaultRoot string) {
	t.Helper()
	for _, name := range []string{"plugins.yaml", "skills.yaml", "sources.yaml", "config.yaml"} {
		if _, err := os.Stat(filepath.Join(defaultRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("%s written into the default root %s (err = %v)", name, defaultRoot, err)
		}
	}
}

// seedPlugin installs a disabled plugin under root without going through git.
func seedPlugin(t *testing.T, root, name string) {
	t.Helper()
	pdir := filepath.Join(plugin.PluginsDir(root), name)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "nib-plugin.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins.yaml"),
		[]byte("plugins:\n  - name: "+name+"\n    enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedSkillPack installs a disabled skill pack under root.
func seedSkillPack(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(skill.SkillsDir(root), name, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: demo\ndescription: d\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(skill.Entry{Name: name, Enabled: false})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
}

// `nib plugin enable` under an override must read and write the injected
// registry. The plugin exists only there, so resolving the default root would
// fail the enable outright, and the flipped flag must persist in the injected
// plugins.yaml rather than a fresh one under ~/.config/nib.
func TestRunPluginCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()
	seedPlugin(t, root, "demo")

	if code := RunPluginCommand(root, []string{"enable", "demo"}); code != 0 {
		t.Fatalf("plugin enable exit = %d, want 0", code)
	}
	reg, err := plugin.LoadRegistry(root)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	e := reg.Find("demo")
	if e == nil || !e.Enabled {
		t.Fatalf("injected registry not updated: %+v", e)
	}
	assertUntouched(t, defaultRoot)
}

// The catalog subcommands took the root from catalogBaseDir(), which always
// resolved the default. `source add` must persist into the injected root.
func TestRunPluginCommandSourceUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()

	if code := RunPluginCommand(root, []string{"source", "add", "https://example.invalid/skills.yaml"}); code != 0 {
		t.Fatalf("plugin source add exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(root, "sources.yaml")); err != nil {
		t.Fatalf("sources.yaml not written into the injected root: %v", err)
	}
	assertUntouched(t, defaultRoot)
}

// `nib skill enable` under an override must read and write the injected registry.
func TestRunSkillCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()
	seedSkillPack(t, root, "pack")

	if code := RunSkillCommand(root, []string{"enable", "pack"}); code != 0 {
		t.Fatalf("skill enable exit = %d, want 0", code)
	}
	reg, err := skill.LoadRegistry(root)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	e := reg.Find("pack")
	if e == nil || !e.Enabled {
		t.Fatalf("injected registry not updated: %+v", e)
	}
	assertUntouched(t, defaultRoot)
}

// `nib mcp add` writes to a config file. Under an override that file is
// <root>/config.yaml; the user's real config must not be created or edited.
func TestRunMCPCommandUsesInjectedBaseDir(t *testing.T) {
	defaultRoot := isolatedHome(t)
	root := t.TempDir()

	if code := RunMCPCommand(root, []string{"add", "demo", "--", "demo-mcp"}); code != 0 {
		t.Fatalf("mcp add exit = %d, want 0", code)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml not written into the injected root: %v", err)
	}
	if !strings.Contains(string(data), "demo-mcp") {
		t.Fatalf("injected config missing the server: %q", data)
	}
	assertUntouched(t, defaultRoot)
}
