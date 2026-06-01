package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPluginCommandLifecycle(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	// Seed an installed-but-disabled plugin directly via the registry + files,
	// avoiding git in this CLI-level test.
	wizBase := filepath.Join(base, "wiz")
	pdir := filepath.Join(wizBase, "plugins", "demo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "wiz-plugin.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wizBase, "plugins.yaml"),
		[]byte("plugins:\n  - name: demo\n    enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := RunPluginCommand([]string{"list"}); code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	if code := RunPluginCommand([]string{"enable", "demo"}); code != 0 {
		t.Fatalf("enable exit = %d", code)
	}
	if code := RunPluginCommand([]string{"enable", "missing"}); code == 0 {
		t.Fatal("enable missing should fail")
	}
	if code := RunPluginCommand([]string{"disable", "demo"}); code != 0 {
		t.Fatalf("disable exit = %d", code)
	}
	if code := RunPluginCommand([]string{"remove", "demo"}); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	if code := RunPluginCommand([]string{"bogus"}); code == 0 {
		t.Fatal("unknown subcommand should fail")
	}
	if code := RunPluginCommand(nil); code == 0 {
		t.Fatal("no subcommand should fail")
	}
}
