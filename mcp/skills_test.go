package mcp

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestLoadSkillResult(t *testing.T) {
	index, names := skillIndex([]types.Skill{
		{Name: "git-commit", Instructions: "COMMIT BODY"},
		{Name: "deploy", Instructions: "DEPLOY BODY"},
	})
	if len(names) != 2 {
		t.Fatalf("want 2 names, got %v", names)
	}

	out := loadSkillResult(index, loadSkillInput{Name: "git-commit"})
	if !out.Found || out.Instructions != "COMMIT BODY" {
		t.Fatalf("known skill: %+v", out)
	}

	out = loadSkillResult(index, loadSkillInput{Name: "nope"})
	if out.Found || out.Error == "" {
		t.Fatalf("unknown skill should report not found: %+v", out)
	}
}

func TestLoadSkillResultInjectsBaseDir(t *testing.T) {
	index, _ := skillIndex([]types.Skill{
		{Name: "withdir", Instructions: "do the thing", Dir: "/packs/sp/skills/withdir"},
		{Name: "nodir", Instructions: "no base dir here"},
	})

	got := loadSkillResult(index, loadSkillInput{Name: "withdir"})
	if !got.Found {
		t.Fatalf("expected found")
	}
	if !strings.HasPrefix(got.Instructions, "Base directory for this skill: /packs/sp/skills/withdir\n\n") {
		t.Fatalf("missing base-dir prefix; got:\n%s", got.Instructions)
	}
	if !strings.HasSuffix(got.Instructions, "do the thing") {
		t.Fatalf("body not preserved; got:\n%s", got.Instructions)
	}

	noDir := loadSkillResult(index, loadSkillInput{Name: "nodir"})
	if strings.Contains(noDir.Instructions, "Base directory") {
		t.Fatalf("must not inject base dir when Dir empty; got:\n%s", noDir.Instructions)
	}
	if noDir.Instructions != "no base dir here" {
		t.Fatalf("body changed; got: %q", noDir.Instructions)
	}

	miss := loadSkillResult(index, loadSkillInput{Name: "ghost"})
	if miss.Found {
		t.Fatalf("expected not found for unknown skill")
	}
}
