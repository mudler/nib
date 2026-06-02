package mcp

import (
	"testing"

	"github.com/mudler/wiz/types"
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
