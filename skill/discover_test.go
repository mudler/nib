package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
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

// writeSkillAt writes <relDir>/SKILL.md under root, creating parents.
func writeSkillAt(t *testing.T, root, relDir, body string) {
	t.Helper()
	dir := filepath.Join(root, relDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHarvestPackFlexible(t *testing.T) {
	root := t.TempDir()
	// Flat: skill dirs directly under root.
	writeSkillAt(t, root, "git", "---\nname: git\ndescription: vcs\n---\nbody\n")
	// No name frontmatter → name falls back to the directory basename.
	writeSkillAt(t, root, "writing", "---\ndescription: prose\n---\nbody\n")
	// Nested several levels deep.
	writeSkillAt(t, root, "a/b/deep", "---\nname: deep\ndescription: d\n---\nbody\n")
	// Legacy skills/<name>/ layout still resolves.
	writeSkillAt(t, root, "skills/legacy", "---\nname: legacy\ndescription: d\n---\nbody\n")
	// Prune: a SKILL.md inside a recognized skill's subtree must be ignored.
	writeSkillAt(t, root, "git/references/example", "---\nname: nope\ndescription: d\n---\nbody\n")
	// Dotted dirs are skipped entirely.
	writeSkillAt(t, root, ".git/hooks", "---\nname: hidden\ndescription: d\n---\nbody\n")

	skills, err := HarvestPack(root)
	if err != nil {
		t.Fatalf("HarvestPack: %v", err)
	}
	got := map[string]string{} // name -> Dir
	for _, s := range skills {
		got[s.Name] = s.Dir
	}
	want := []string{"git", "writing", "deep", "legacy"}
	if len(got) != len(want) {
		t.Fatalf("got %d skills %v, want %v", len(got), got, want)
	}
	for _, n := range want {
		if _, ok := got[n]; !ok {
			t.Fatalf("missing skill %q (got %v)", n, got)
		}
	}
	if _, ok := got["nope"]; ok {
		t.Fatalf("prune failed: skill inside a skill subtree was harvested")
	}
	if _, ok := got["hidden"]; ok {
		t.Fatalf("dotted dir was not skipped")
	}
	if got["writing"] != filepath.Join(root, "writing") {
		t.Fatalf("Dir for fallback-named skill = %q", got["writing"])
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

func TestApplyPrecedenceAndEnabledOnly(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(base)

	// Pack A (enabled): contributes "shared" and "onlyA".
	srcA := t.TempDir()
	writeSkill(t, srcA, "shared", "---\nname: shared\ndescription: from A\n---\nA body\n", nil)
	writeSkill(t, srcA, "onlyA", "---\nname: onlyA\ndescription: only in A\n---\nA only\n", nil)
	nameA, _, err := mgr.Install(srcA, "", false)
	if err != nil {
		t.Fatalf("install A: %v", err)
	}
	if err := mgr.SetEnabled(nameA, true); err != nil {
		t.Fatal(err)
	}

	// Pack B (left disabled): contributes "onlyB" — must NOT appear.
	srcB := t.TempDir()
	writeSkill(t, srcB, "onlyB", "---\nname: onlyB\ndescription: only in B\n---\nB only\n", nil)
	if _, _, err := mgr.Install(srcB, "", false); err != nil {
		t.Fatalf("install B: %v", err)
	}

	// A user skill named "shared" must win over the pack's.
	cfg := &types.Config{Skills: []types.Skill{{Name: "shared", Description: "user wins", Instructions: "user body"}}}

	if err := Apply(cfg, base); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	byName := map[string]types.Skill{}
	for _, s := range cfg.Skills {
		byName[s.Name] = s
	}
	if byName["shared"].Description != "user wins" {
		t.Fatalf("user skill should win, got %+v", byName["shared"])
	}
	if _, ok := byName["onlyA"]; !ok {
		t.Fatalf("enabled pack A skill missing")
	}
	if _, ok := byName["onlyB"]; ok {
		t.Fatalf("disabled pack B skill must not be applied")
	}
	if byName["onlyA"].Dir == "" {
		t.Fatalf("applied pack skill should carry Dir")
	}
}
