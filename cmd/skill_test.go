package cmd

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/skill"
)

// writeTestZip builds a zip at path with the given name→body entries.
// Same body as extsource's writeZip test helper.
func writeTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
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
}

func TestSkillLocalImport_Zip(t *testing.T) {
	base := t.TempDir()
	// build a zip containing a SKILL.md (reuse extsource test helper pattern)
	zp := filepath.Join(t.TempDir(), "greeter.zip")
	writeTestZip(t, zp, map[string]string{
		"SKILL.md": "---\nname: greet\ndescription: hi\n---\nbody",
	})
	mgr := skill.NewManager(base)
	name, skills, handled, err := skillLocalImport(mgr, zp)
	if err != nil || !handled {
		t.Fatalf("zip import: handled=%v err=%v", handled, err)
	}
	if name != "greeter" || len(skills) != 1 {
		t.Fatalf("got name=%q skills=%d", name, len(skills))
	}
	// The registry must record the real .zip path as provenance, not the
	// throwaway extraction temp dir.
	reg, _ := skill.LoadRegistry(base)
	if e := reg.Find("greeter"); e == nil || e.SourceURL != zp {
		t.Fatalf("SourceURL = %+v, want %q", e, zp)
	}
}

func TestSkillLocalImport_GitPassthrough(t *testing.T) {
	mgr := skill.NewManager(t.TempDir())
	_, _, handled, _ := skillLocalImport(mgr, "https://github.com/owner/repo")
	if handled {
		t.Fatal("git URL must not be handled by local import")
	}
}

func TestParseInstallArgsForSkill(t *testing.T) {
	// cmd/skill.go reuses parseInstallArgs (defined in cmd/plugin.go) for plugins.
	src, ref, yes, err := parseInstallArgs([]string{"--ref", "v2", "--yes", "/local/path"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if src != "/local/path" || ref != "v2" || !yes {
		t.Fatalf("parsed wrong: src=%q ref=%q yes=%v", src, ref, yes)
	}
	if _, _, _, err := parseInstallArgs([]string{}); err == nil {
		t.Fatalf("expected error on missing source")
	}
}

func TestParseSkillInstallArgsLink(t *testing.T) {
	src, ref, yes, link, err := parseSkillInstallArgs([]string{"--link", "/local/path"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if src != "/local/path" || ref != "" || yes || !link {
		t.Fatalf("parsed wrong: src=%q ref=%q yes=%v link=%v", src, ref, yes, link)
	}
	// --link combined with --ref is rejected.
	if _, _, _, _, err := parseSkillInstallArgs([]string{"--link", "--ref", "v1", "/p"}); err == nil {
		t.Fatalf("expected error combining --link with --ref")
	}
	// Plain install (no --link) still parses.
	src, _, _, link, err = parseSkillInstallArgs([]string{"/local/path"})
	if err != nil || src != "/local/path" || link {
		t.Fatalf("plain parse wrong: src=%q link=%v err=%v", src, link, err)
	}
}

func TestRunSkillCommandUnknownSubcommand(t *testing.T) {
	if code := RunSkillCommand("", []string{"frobnicate"}); code != 1 {
		t.Fatalf("expected exit 1 for unknown subcommand, got %d", code)
	}
	if code := RunSkillCommand("", nil); code != 1 {
		t.Fatalf("expected exit 1 for no args, got %d", code)
	}
}

func TestSkillListAnnotatesLinked(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	dir := filepath.Join(src, "skills", "s")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: s\ndescription: d\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := skill.NewManager(base)
	name, _, err := mgr.Install(src, "", true)
	if err != nil {
		t.Fatalf("Install link: %v", err)
	}
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	skillList(mgr)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "(linked → ") {
		t.Fatalf("list output missing linked annotation:\n%s", out)
	}
}
