package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/plugin"
)

func TestPluginMarketplaceCLI(t *testing.T) {
	base := t.TempDir()
	mkt := t.TempDir()
	files := map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","owner":{"name":"o"},"plugins":[
			{"name":"greet","source":"./greet","description":"says hello","category":"fun"},
			{"name":"pkg","source":{"source":"npm","package":"x"}}]}`,
		"greet/commands/hi.md":   "Say hi to $ARGUMENTS\n",
		"greet/hooks/hooks.json": `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`,
	}
	for name, body := range files {
		p := filepath.Join(mkt, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Installing a marketplace directory registers it and lists its plugins.
	out := captureStdout(t, func() {
		if code := RunPluginCommand("", base, []string{"install", mkt}); code != 0 {
			t.Fatalf("install marketplace: %d", code)
		}
	})
	if !strings.Contains(out, "greet@team") || !strings.Contains(out, "npm source: not supported") {
		t.Fatalf("output:\n%s", out)
	}

	out = captureStdout(t, func() {
		if code := RunPluginCommand("", base, []string{"search", "hello"}); code != 0 {
			t.Fatal("search")
		}
	})
	if !strings.Contains(out, "greet@team") || strings.Contains(out, "pkg@team") {
		t.Fatalf("search output:\n%s", out)
	}

	out = captureStdout(t, func() {
		if code := RunPluginCommand("", base, []string{"install", "--yes", "greet@team"}); code != 0 {
			t.Fatal("install greet@team")
		}
	})
	if !strings.Contains(out, "Runs code: hook-command") || !strings.Contains(out, `"greet" enabled`) {
		t.Fatalf("install output:\n%s", out)
	}
	entries, _ := plugin.NewManager(base).List()
	if len(entries) != 1 || entries[0].Marketplace != "team" || !entries[0].Enabled {
		t.Fatalf("registry = %+v", entries)
	}

	if code := RunPluginCommand("", base, []string{"install", "--ref", "not-a-sha", "greet@team"}); code == 0 {
		t.Fatal("a non-SHA pin must fail")
	}
	out = captureStdout(t, func() { RunPluginCommand("", base, []string{"marketplace", "list"}) })
	if !strings.Contains(out, "team") {
		t.Fatalf("list:\n%s", out)
	}
	if code := RunPluginCommand("", base, []string{"marketplace", "update", "team"}); code != 0 {
		t.Fatal("update")
	}
	if code := RunPluginCommand("", base, []string{"marketplace", "remove", "team"}); code != 0 {
		t.Fatal("remove")
	}
	if code := RunPluginCommand("", base, []string{"marketplace"}); code == 0 {
		t.Fatal("usage")
	}
}
