package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/obra/superpowers":     "superpowers",
		"https://github.com/obra/superpowers.git":  "superpowers",
		"https://github.com/obra/superpowers/":     "superpowers",
		"git@github.com:obra/superpowers.git":      "superpowers",
		"/home/me/my-skills":                       "my-skills",
		"/home/me/my-skills/":                      "my-skills",
	}
	for in, want := range cases {
		if got := deriveName(in); got != want {
			t.Errorf("deriveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManagerInstallFromLocalDir(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir()
	writeSkill(t, src, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nask questions\n", nil)

	mgr := NewManager(base)
	name, skills, err := mgr.Install(src, "")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "brainstorming" {
		t.Fatalf("expected 1 harvested skill, got %+v", skills)
	}
	// Files landed at skills/<name>.
	if _, err := os.Stat(filepath.Join(packDir(base, name), "skills", "brainstorming", "SKILL.md")); err != nil {
		t.Fatalf("pack files not at expected dir: %v", err)
	}
	// Returned Dir points at the final on-disk location, not the temp dir.
	wantDir := filepath.Join(packDir(base, name), "skills", "brainstorming")
	if skills[0].Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", skills[0].Dir, wantDir)
	}
	// Registry records it, disabled.
	reg, _ := LoadRegistry(base)
	e := reg.Find(name)
	if e == nil || e.Enabled || e.SourceURL != src {
		t.Fatalf("registry entry wrong: %+v", e)
	}

	// SetEnabled flips the flag.
	if err := mgr.SetEnabled(name, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	reg, _ = LoadRegistry(base)
	if !reg.Find(name).Enabled {
		t.Fatalf("expected enabled after SetEnabled")
	}

	// Re-installing the same name is rejected.
	if _, _, err := mgr.Install(src, ""); err == nil {
		t.Fatalf("expected collision error on re-install")
	}

	// Remove deletes files and registry record.
	if err := mgr.Remove(name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(packDir(base, name)); !os.IsNotExist(err) {
		t.Fatalf("pack dir should be gone")
	}
}

func TestManagerInstallRejectsNoSkills(t *testing.T) {
	base := t.TempDir()
	src := t.TempDir() // empty: no skills/ dir
	mgr := NewManager(base)
	if _, _, err := mgr.Install(src, ""); err == nil {
		t.Fatalf("expected error when no skills found")
	}
}
