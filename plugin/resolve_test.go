package plugin

import (
	"path/filepath"
	"testing"
)

func TestResolveFragment(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "frag.md"), "FILE BODY")

	got, err := resolveFragment(FragmentSpec{Text: "inline"}, root)
	if err != nil || got != "inline" {
		t.Fatalf("inline: got %q err %v", got, err)
	}
	got, err = resolveFragment(FragmentSpec{File: "frag.md"}, root)
	if err != nil || got != "FILE BODY" {
		t.Fatalf("file: got %q err %v", got, err)
	}
	if _, err := resolveFragment(FragmentSpec{}, root); err == nil {
		t.Fatal("expected error for empty fragment")
	}
	if _, err := resolveFragment(FragmentSpec{File: "../escape.md"}, root); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestResolveSkill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "skills", "s.md"), "SKILL BODY")

	sk, err := resolveSkill(SkillSpec{Name: "a", Description: "d", Instructions: InstructionsSpec{Inline: "body"}, Tools: []string{"bash"}}, root)
	if err != nil || sk.Name != "a" || sk.Instructions != "body" || sk.Description != "d" || len(sk.Tools) != 1 {
		t.Fatalf("inline skill wrong: %+v err %v", sk, err)
	}
	sk, err = resolveSkill(SkillSpec{Name: "b", Instructions: InstructionsSpec{File: "skills/s.md"}}, root)
	if err != nil || sk.Instructions != "SKILL BODY" {
		t.Fatalf("file skill wrong: %+v err %v", sk, err)
	}
	if _, err := resolveSkill(SkillSpec{Name: "c"}, root); err == nil {
		t.Fatal("expected error for skill with no instructions")
	}
}
