package mcp

import (
	"context"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestStartTransportsIncludesSkillsWhenPresent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No skills → bash + filesystem only.
	base, err := StartTransports(ctx, types.Config{}, NewShellJobs())
	if err != nil {
		t.Fatalf("StartTransports (no skills): %v", err)
	}
	withoutSkills := len(base)

	// With skills → exactly one more transport (the skills server).
	withSkills, err := StartTransports(ctx, types.Config{
		Skills: []types.Skill{{Name: "s", Instructions: "body"}},
	}, NewShellJobs())
	if err != nil {
		t.Fatalf("StartTransports (skills): %v", err)
	}
	if len(withSkills) != withoutSkills+1 {
		t.Fatalf("expected one extra transport for skills, got %d vs %d", len(withSkills), withoutSkills)
	}
}
