package plugin

import (
	"path/filepath"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter([]byte("---\nname: foo\ndescription: bar\n---\nhello body\nmore\n"))
	if string(fm) != "name: foo\ndescription: bar\n" {
		t.Fatalf("frontmatter wrong: %q", fm)
	}
	if body != "hello body\nmore\n" {
		t.Fatalf("body wrong: %q", body)
	}
	fm, body = splitFrontmatter([]byte("just body\n"))
	if len(fm) != 0 || body != "just body\n" {
		t.Fatalf("no-frontmatter wrong: fm=%q body=%q", fm, body)
	}
}

func TestLoadClaudeSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "git-commit", "SKILL.md"),
		"---\nname: git-commit\ndescription: make a commit\nallowed-tools: Bash, Read\n---\nDo the commit.\n")
	skills := loadClaudeSkills(dir)
	if len(skills) != 1 || skills[0].Name != "git-commit" || skills[0].Description != "make a commit" {
		t.Fatalf("skills wrong: %+v", skills)
	}
	if skills[0].Instructions.Inline != "Do the commit.\n" {
		t.Fatalf("instructions wrong: %q", skills[0].Instructions.Inline)
	}
	if len(skills[0].Tools) != 2 {
		t.Fatalf("tools not aliased: %+v", skills[0].Tools)
	}
}

func TestLoadClaudeCommands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "commands", "review.md"),
		"---\ndescription: review the diff\n---\nReview this: $ARGUMENTS\n")
	cmds := loadClaudeCommands(dir)
	if len(cmds) != 1 || cmds[0].Name != "review" || cmds[0].Description != "review the diff" {
		t.Fatalf("commands wrong: %+v", cmds)
	}
	if cmds[0].Prompt != "Review this: {{.Args}}\n" {
		t.Fatalf("$ARGUMENTS not translated: %q", cmds[0].Prompt)
	}
}

func TestLoadClaudeAgents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "agents", "researcher.md"),
		"---\nname: researcher\ndescription: digs\ntools: Read, Grep\nmodel: sonnet\n---\nYou research.\n")
	agents := loadClaudeAgents(dir)
	if len(agents) != 1 || agents[0].Name != "researcher" || agents[0].SystemPrompt != "You research.\n" {
		t.Fatalf("agents wrong: %+v", agents)
	}
	if len(agents[0].Tools) != 2 || agents[0].Model != "sonnet" {
		t.Fatalf("agent tools/model wrong: %+v", agents[0])
	}
}
