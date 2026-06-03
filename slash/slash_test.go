package slash

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestExpand(t *testing.T) {
	out, err := Expand(types.CommandConfig{Prompt: "Review: {{.Args}}"}, "the diff")
	if err != nil || out != "Review: the diff" {
		t.Fatalf("expand: %q err %v", out, err)
	}
}

func TestResolve(t *testing.T) {
	cmds := []types.CommandConfig{
		{Name: "review", Prompt: "Review: {{.Args}}"},
		{Name: "scan", Prompt: "Scan it", Agent: "explore"},
	}
	skills := []types.Skill{{Name: "git-commit", Instructions: "body"}}
	agents := []types.AgentTypeConfig{{Name: "explore"}}

	if a := Resolve("hello world", cmds, skills, agents); a.Kind != KindSend || a.Text != "hello world" {
		t.Fatalf("plain: %+v", a)
	}
	if a := Resolve("/review the diff", cmds, skills, agents); a.Kind != KindSend || a.Text != "Review: the diff" {
		t.Fatalf("command: %+v", a)
	}
	a := Resolve("/scan", cmds, skills, agents)
	if a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "Scan it") {
		t.Fatalf("agent-bound command: %+v", a)
	}
	if a := Resolve("/skill git-commit", cmds, skills, agents); a.Kind != KindLoadSkill || a.Skill != "git-commit" {
		t.Fatalf("skill: %+v", a)
	}
	if a := Resolve("/skill nope", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown skill should error: %+v", a)
	}
	if a := Resolve("/skill", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("skill with no name should error: %+v", a)
	}
	if a := Resolve("/agent explore find bugs", cmds, skills, agents); a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "find bugs") {
		t.Fatalf("agent: %+v", a)
	}
	if a := Resolve("/agent ghost x", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown agent should error: %+v", a)
	}
	if a := Resolve("/bogus", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown command should error: %+v", a)
	}
}
