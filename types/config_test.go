package types

import (
	"os"
	"path/filepath"
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

func TestDetectContextFiles(t *testing.T) {
	dir := t.TempDir()
	// Create files in a deliberately different order than contextFileNames to
	// confirm the result preserves the canonical order.
	for _, name := range []string{"GEMINI.md", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory named like a context file must be ignored.
	if err := os.Mkdir(filepath.Join(dir, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := detectContextFiles(dir)
	want := []string{"AGENTS.md", "GEMINI.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	if detectContextFiles("") != nil {
		t.Fatalf("empty dir should yield no files")
	}
	if detectContextFiles(t.TempDir()) != nil {
		t.Fatalf("dir without context files should yield none")
	}
}

func TestGetPromptMentionsContextFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	got := (&Config{Prompt: "BASE"}).GetPrompt()
	if !strings.Contains(got, "AGENTS.md") {
		t.Fatalf("prompt should mention AGENTS.md:\n%s", got)
	}
	if !strings.Contains(got, "before acting") {
		t.Fatalf("prompt should instruct to read before acting:\n%s", got)
	}
}

func TestGetPromptMentionsMCPAdd(t *testing.T) {
	c := Config{Prompt: "base prompt"}
	got := c.GetPrompt()
	if !strings.Contains(got, "nib mcp add") {
		t.Fatalf("system prompt should mention `nib mcp add`:\n%s", got)
	}
}

func TestGetPromptNoSkillsNoIndex(t *testing.T) {
	// Run from a context-file-free directory so GetPrompt returns just the base.
	t.Chdir(t.TempDir())

	c := &Config{Prompt: "BASE"}
	got := c.GetPrompt()
	if strings.Contains(got, "load_skill") {
		t.Fatalf("should not mention load_skill when no skills:\n%s", got)
	}
	// The base prompt is always first; only the static MCP fragment follows it
	// when there are no skills, fragments, or context files.
	if !strings.HasPrefix(strings.TrimSpace(got), "BASE") {
		t.Fatalf("expected the base prompt first, got:\n%q", got)
	}
}
