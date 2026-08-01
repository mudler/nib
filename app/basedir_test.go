package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/plugin"
)

// Options.BaseDir must reach the management subcommands: `nib mcp add` under an
// override writes <root>/config.yaml, never the user's real one.
func TestRunMCPAddHonorsOptionsBaseDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	defaultRoot := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(defaultRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	code := run(Options{Args: []string{"mcp", "add", "demo", "--", "demo-mcp"}, BaseDir: root})
	if code != 0 {
		t.Fatalf("mcp add exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written into the injected root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(defaultRoot, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote into the user's real nib root: err = %v", err)
	}
}

// `nib plugin enable` under an override resolves the injected registry: the
// plugin exists only there, so a default-root resolution exits non-zero.
func TestRunPluginEnableHonorsOptionsBaseDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "nib"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	pdir := filepath.Join(plugin.PluginsDir(root), "demo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "nib-plugin.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins.yaml"),
		[]byte("plugins:\n  - name: demo\n    enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := run(Options{Args: []string{"plugin", "enable", "demo"}, BaseDir: root}); code != 0 {
		t.Fatalf("plugin enable exit = %d, want 0", code)
	}
	reg, err := plugin.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if e := reg.Find("demo"); e == nil || !e.Enabled {
		t.Fatalf("injected registry not updated: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(xdg, "nib", "plugins.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote a registry into the user's real nib root: err = %v", err)
	}
}

// runCtx's config load must use the override too. With no model in the injected
// root and no interactive terminal, the setup gate aborts with the "no model
// configured" message; a load from the default root would have found a model
// there and gone on to start MCP transports instead.
func TestRunLoadsConfigFromOptionsBaseDir(t *testing.T) {
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	defaultRoot := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(defaultRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, "config.yaml"),
		[]byte("model: user-model\napi_key: user-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	// An explicit non-*os.File stdin keeps the TTY probe deterministic, so the
	// gate aborts instead of launching the interactive wizard under a real tty.
	var errOut bytes.Buffer
	o := Options{Args: nil, BaseDir: root, Stdin: strings.NewReader(""), Stderr: &errOut}
	if code := run(o); code != 1 {
		t.Fatalf("exit = %d, want 1: the empty injected root has no model", code)
	}
	if !strings.Contains(errOut.String(), "no model configured") {
		t.Fatalf("stderr = %q, want the no-model abort", errOut.String())
	}
}
