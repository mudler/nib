package types

import (
	"strings"
	"testing"
)

func TestGetPromptAppendsSkillsAndFragments(t *testing.T) {
	c := &Config{
		Prompt: "BASE PROMPT",
		Skills: []Skill{
			{Name: "git-commit", Description: "make a conventional commit"},
			{Name: "deploy", Description: "ship to prod"},
		},
		PromptFragments: []string{"Prefer small PRs.", "Use tabs."},
	}
	got := c.GetPrompt()

	if !strings.Contains(got, "BASE PROMPT") {
		t.Fatalf("base prompt missing:\n%s", got)
	}
	if !strings.Contains(got, "load_skill") {
		t.Fatalf("skill index should mention load_skill:\n%s", got)
	}
	if !strings.Contains(got, "git-commit: make a conventional commit") ||
		!strings.Contains(got, "deploy: ship to prod") {
		t.Fatalf("skill index entries missing:\n%s", got)
	}
	if !strings.Contains(got, "Prefer small PRs.") || !strings.Contains(got, "Use tabs.") {
		t.Fatalf("fragments missing:\n%s", got)
	}
}

func TestGetPromptNoSkillsNoIndex(t *testing.T) {
	c := &Config{Prompt: "BASE"}
	got := c.GetPrompt()
	if strings.Contains(got, "load_skill") {
		t.Fatalf("should not mention load_skill when no skills:\n%s", got)
	}
	if strings.TrimSpace(got) != "BASE" {
		t.Fatalf("expected just the base prompt, got:\n%q", got)
	}
}
