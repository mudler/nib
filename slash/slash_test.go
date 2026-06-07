package slash

import (
	"strings"
	"testing"
	"time"

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

func TestResolveCompact(t *testing.T) {
	got := Resolve("/compact", nil, nil, nil)
	if got.Kind != KindCompact {
		t.Fatalf("/compact resolved to kind %v, want KindCompact", got.Kind)
	}
}

func TestResolveLoop(t *testing.T) {
	var none []types.CommandConfig
	var noSkills []types.Skill
	var noAgents []types.AgentTypeConfig

	// Fixed interval: "/loop 5m /foo".
	a := Resolve("/loop 5m /foo", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 5*time.Minute || a.Payload != "/foo" {
		t.Fatalf("fixed: %+v", a)
	}

	// Self-paced: "/loop /foo" (no parseable interval → interval 0).
	a = Resolve("/loop /foo", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 0 || a.Payload != "/foo" {
		t.Fatalf("self-paced: %+v", a)
	}

	// 1s is at the floor now → NOT clamped.
	a = Resolve("/loop 1s ping", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 1*time.Second {
		t.Fatalf("1s floor: %+v", a)
	}

	// Sub-second interval is clamped up to the 1s floor.
	a = Resolve("/loop 500ms ping", none, noSkills, noAgents)
	if a.Kind != KindLoopStart || a.Interval != 1*time.Second {
		t.Fatalf("clamp: %+v", a)
	}

	// Control verbs.
	if a := Resolve("/loop stop", none, noSkills, noAgents); a.Kind != KindLoopStop || a.LoopID != "" {
		t.Fatalf("stop-all: %+v", a)
	}
	if a := Resolve("/loop stop loop-2", none, noSkills, noAgents); a.Kind != KindLoopStop || a.LoopID != "loop-2" {
		t.Fatalf("stop-id: %+v", a)
	}
	if a := Resolve("/loop list", none, noSkills, noAgents); a.Kind != KindLoopList {
		t.Fatalf("list: %+v", a)
	}

	// Empty payload → error.
	if a := Resolve("/loop", none, noSkills, noAgents); a.Kind != KindError {
		t.Fatalf("empty: %+v", a)
	}
	if a := Resolve("/loop 5m", none, noSkills, noAgents); a.Kind != KindError {
		t.Fatalf("interval-only: %+v", a)
	}
}
