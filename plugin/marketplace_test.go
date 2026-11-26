package plugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCommitAll(t *testing.T, repo, msg string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", msg},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestMarketplaceInstallUpdateRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	// A plugin in its own repository, referenced by url.
	ext := t.TempDir()
	writeFiles(t, ext, map[string]string{
		".claude-plugin/plugin.json": `{"name":"ext"}`,
		"skills/e/SKILL.md":          "---\ndescription: ext v1\n---\nv1\n",
	})
	extV1 := gitCommitAll(t, ext, "v1")

	// The marketplace, with one relative plugin and the external one.
	mkt := t.TempDir()
	writeFiles(t, mkt, map[string]string{
		".claude-plugin/marketplace.json": `{"name":"team","owner":{"name":"o"},"plugins":[
			{"name":"local","source":"./plugins/local","description":"a local plugin"},
			{"name":"ext","source":{"source":"url","url":"file://` + ext + `"}},
			{"name":"pkg","source":{"source":"npm","package":"x"}}]}`,
		"plugins/local/commands/hi.md": "Say hi to $ARGUMENTS\n",
	})
	mktV1 := gitCommitAll(t, mkt, "init")

	base := t.TempDir()
	mgr := NewManager(base)

	// A marketplace is not a plugin.
	if _, err := mgr.Install(mkt, "", "0.9.0"); !errors.Is(err, ErrIsMarketplace) {
		t.Fatalf("Install(marketplace) = %v, want ErrIsMarketplace", err)
	}

	m, err := mgr.AddMarketplace(ctx, mkt)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "team" || m.SHA != mktV1 || len(m.Plugins) != 3 {
		t.Fatalf("marketplace = %+v", m)
	}
	recs, _ := mgr.Marketplaces()
	if len(recs) != 1 || recs[0].Source != mkt || recs[0].SHA != mktV1 {
		t.Fatalf("records = %+v", recs)
	}

	// Relative plugin without plugin.json: the entry is the manifest.
	lm, err := mgr.InstallFromMarketplace(ctx, "local@team", "", "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if lm.Name != "local" || lm.Description != "a local plugin" || len(lm.Commands) != 1 || lm.Commands[0].Name != "local:hi" {
		t.Fatalf("local = %+v", lm)
	}
	entries, _ := mgr.List()
	if len(entries) != 1 || entries[0].Marketplace != "team" || entries[0].SHA != mktV1 || entries[0].SourceURL != "local@team" || entries[0].Enabled {
		t.Fatalf("registry = %+v", entries)
	}
	if again, err := LoadManifest(pluginDir(base, "local"), "0.9.0"); err != nil || again.Name != "local" {
		t.Fatalf("reload from disk: %+v %v", again, err)
	}

	// External plugin, then a new upstream commit and an update.
	if _, err := mgr.InstallFromMarketplace(ctx, "ext@team", "", "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetEnabled("ext", true); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, ext, map[string]string{"skills/e/SKILL.md": "---\ndescription: ext v2\n---\nv2\n"})
	extV2 := gitCommitAll(t, ext, "v2")
	if err := mgr.Update("ext"); err != nil {
		t.Fatal(err)
	}
	reg, _ := LoadRegistry(base)
	if e := reg.Find("ext"); e == nil || e.SHA != extV2 || !e.Enabled {
		t.Fatalf("after update: %+v", e)
	}

	// An explicit pin installs the older commit.
	pinned, err := mgr.InstallFromMarketplace(ctx, "ext@team", extV1, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Skills[0].Description != "ext v1" {
		t.Fatalf("pinned = %+v", pinned.Skills)
	}

	if _, err := mgr.InstallFromMarketplace(ctx, "pkg@team", "", "0.9.0"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("npm source: %v", err)
	}
	if _, err := mgr.InstallFromMarketplace(ctx, "nope@team", "", "0.9.0"); err == nil {
		t.Fatal("unknown plugin")
	}
	if _, err := mgr.InstallFromMarketplace(ctx, "local@other", "", "0.9.0"); err == nil {
		t.Fatal("unknown marketplace")
	}

	if err := mgr.RemoveMarketplace("team"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(MarketplacesDir(base), "team")); !os.IsNotExist(err) {
		t.Fatalf("cache not removed: %v", err)
	}
	if entries, _ := mgr.List(); len(entries) != 2 {
		t.Fatalf("plugins stay installed: %+v", entries)
	}
}
