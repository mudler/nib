package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseInstallArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantURL string
		wantRef string
		wantYes bool
		wantErr bool
	}{
		{"url only", []string{"u"}, "u", "", false, false},
		{"yes after url", []string{"u", "--yes"}, "u", "", true, false},
		{"yes before url", []string{"--yes", "u"}, "u", "", true, false},
		{"ref after url", []string{"u", "--ref", "v1"}, "u", "v1", false, false},
		{"ref before url", []string{"--ref", "v1", "u"}, "u", "v1", false, false},
		{"ref eq form after url", []string{"u", "--ref=v2", "--yes"}, "u", "v2", true, false},
		{"flags both sides", []string{"--yes", "u", "--ref", "v3"}, "u", "v3", true, false},
		{"no url", []string{"--yes"}, "", "", false, true},
		{"empty", nil, "", "", false, true},
		{"extra positional", []string{"u", "extra"}, "", "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, ref, yes, err := parseInstallArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if url != c.wantURL || ref != c.wantRef || yes != c.wantYes {
				t.Fatalf("got url=%q ref=%q yes=%v; want url=%q ref=%q yes=%v",
					url, ref, yes, c.wantURL, c.wantRef, c.wantYes)
			}
		})
	}
}

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
