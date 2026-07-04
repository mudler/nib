package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/internal/vcs"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
)

// zipBytes builds an in-memory zip from name→content entries.
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstall_KindRouting(t *testing.T) {
	skillZip := zipBytes(t, map[string]string{
		"SKILL.md": "---\nname: greeter\ndescription: hi\n---\nbody",
	})
	pluginZip := zipBytes(t, map[string]string{
		"nib-plugin.yaml": "name: gitplug\nversion: 1.0.0\ndescription: tools\n",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(skillZip) })
	mux.HandleFunc("/plugin.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(pluginZip) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	base := t.TempDir()

	// skill kind → skill.Manager, DISABLED, with the real zip URL as provenance.
	skillURL := srv.URL + "/skill.zip"
	name, err := c.Install(context.Background(),
		Meta{Kind: KindSkill, Name: "greeter", ZipURL: skillURL}, base, "v0.0.0")
	if err != nil || name != "greeter" {
		t.Fatalf("skill install: name=%q err=%v", name, err)
	}
	if _, err := os.Stat(filepath.Join(skill.SkillsDir(base), "greeter", "SKILL.md")); err != nil {
		t.Fatalf("skill not placed: %v", err)
	}
	sreg, _ := skill.LoadRegistry(base)
	e := sreg.Find("greeter")
	if e == nil || e.Enabled {
		t.Fatalf("skill must land disabled: %+v", e)
	}
	if e.SourceURL != skillURL {
		t.Fatalf("skill SourceURL = %q, want the real zip locator %q", e.SourceURL, skillURL)
	}

	// plugin kind → plugin.Manager, DISABLED, with the real zip URL as provenance.
	pluginURL := srv.URL + "/plugin.zip"
	pname, err := c.Install(context.Background(),
		Meta{Kind: KindPlugin, Name: "gitplug", ZipURL: pluginURL}, base, "v0.0.0")
	if err != nil || pname != "gitplug" {
		t.Fatalf("plugin install: name=%q err=%v", pname, err)
	}
	preg, _ := plugin.LoadRegistry(base)
	pe := preg.Find("gitplug")
	if pe == nil || pe.Enabled {
		t.Fatalf("plugin must land disabled: %+v", pe)
	}
	if pe.SourceURL != pluginURL {
		t.Fatalf("plugin SourceURL = %q, want the real zip locator %q", pe.SourceURL, pluginURL)
	}
}

func TestInstall_GitSubpath(t *testing.T) {
	// Stub the injectable git Clone: it "checks out" a repo whose skill pack
	// lives under sub/pack. No real network is touched.
	orig := vcs.Clone
	defer func() { vcs.Clone = orig }()
	vcs.Clone = func(url, ref, dest string) error {
		packDir := filepath.Join(dest, "sub", "pack")
		if err := os.MkdirAll(packDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(packDir, "SKILL.md"),
			[]byte("---\nname: cloned\ndescription: from git\n---\nbody"), 0o644)
	}

	base := t.TempDir()
	c := NewClient()
	name, err := c.Install(context.Background(),
		Meta{Kind: KindSkill, Name: "cloned", Repo: "owner/repo", Path: "sub/pack", Ref: "main"},
		base, "v0.0.0")
	if err != nil || name != "cloned" {
		t.Fatalf("git-subpath install: name=%q err=%v", name, err)
	}
	if _, err := os.Stat(filepath.Join(skill.SkillsDir(base), "cloned", "SKILL.md")); err != nil {
		t.Fatalf("cloned skill not placed: %v", err)
	}
	sreg, _ := skill.LoadRegistry(base)
	e := sreg.Find("cloned")
	if e == nil || e.Enabled {
		t.Fatalf("cloned skill must land disabled: %+v", e)
	}
	// SourceURL must be the meaningful owner/repo/subpath locator, never a temp dir.
	if want := "owner/repo/sub/pack"; e.SourceURL != want {
		t.Fatalf("SourceURL = %q, want %q (real locator, not a temp path)", e.SourceURL, want)
	}
}

func TestInstall_NoLocator(t *testing.T) {
	c := NewClient()
	if _, err := c.Install(context.Background(), Meta{Kind: KindSkill, Name: "x"}, t.TempDir(), ""); err == nil {
		t.Fatal("expected error when Meta has no install locator")
	}
}
