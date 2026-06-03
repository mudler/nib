package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates skills/<name>/SKILL.md (plus optional extra files) under root.
func writeSkill(t *testing.T, root, name, body string, extra map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range extra {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHarvestPack(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming",
		"---\nname: brainstorming\ndescription: design first\n---\nask questions\n",
		map[string]string{"scripts/run.sh": "echo hi"})
	// A subdir without SKILL.md must be ignored.
	if err := os.MkdirAll(filepath.Join(root, "skills", "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, err := HarvestPack(root)
	if err != nil {
		t.Fatalf("HarvestPack: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if s.Name != "brainstorming" || s.Description != "design first" {
		t.Fatalf("bad metadata: %+v", s)
	}
	if s.Instructions != "ask questions\n" {
		t.Fatalf("body = %q", s.Instructions)
	}
	if s.Dir != filepath.Join(root, "skills", "brainstorming") {
		t.Fatalf("Dir = %q", s.Dir)
	}
}

func TestHarvestPackNoSkillsDir(t *testing.T) {
	skills, err := HarvestPack(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil error for missing skills/, got %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(skills))
	}
}
