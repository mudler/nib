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
	name, skills, err := mgr.InstallDir(src, "mypack")
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
}

func TestInstallDir_NoSkillMd(t *testing.T) {
	mgr := NewManager(t.TempDir())
	empty := t.TempDir()
	if _, _, err := mgr.InstallDir(empty, "empty"); err == nil {
		t.Fatal("expected error when no SKILL.md present")
	}
}
