package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallDir_LandsDisabled(t *testing.T) {
	base := t.TempDir()
	// prepare a source dir with a SKILL.md
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "SKILL.md"),
		[]byte("---\nname: greet\ndescription: say hi\n---\nSay hello."), 0o644)

	mgr := NewManager(base)
	const sourceURL = "/downloads/mypack.zip"
	name, skills, err := mgr.InstallDir(src, "mypack", sourceURL)
	if err != nil {
		t.Fatalf("InstallDir: %v", err)
	}
	if name != "mypack" || len(skills) != 1 {
		t.Fatalf("got name=%q skills=%d", name, len(skills))
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(base), "mypack", "SKILL.md")); err != nil {
		t.Fatalf("pack not placed: %v", err)
	}
	reg, _ := LoadRegistry(base)
	e := reg.Find("mypack")
	if e == nil || e.Enabled {
		t.Fatalf("expected registered & disabled, got %+v", e)
	}
	// The real source (zip path / URL) must be recorded as provenance, not the
	// throwaway extraction temp dir.
	if e.SourceURL != sourceURL {
		t.Fatalf("SourceURL = %q, want %q", e.SourceURL, sourceURL)
	}
}

func TestInstallDir_NoSkillMd(t *testing.T) {
	mgr := NewManager(t.TempDir())
	empty := t.TempDir()
	if _, _, err := mgr.InstallDir(empty, "empty", "/downloads/empty.zip"); err == nil {
		t.Fatal("expected error when no SKILL.md present")
	}
}
