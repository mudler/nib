package cmd

import (
	"testing"

	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/types"
)

func TestResolveCLIInputRoutesSlash(t *testing.T) {
	cfg := types.Config{
		Skills: []types.Skill{{Name: "reviewer", Description: "x"}},
	}
	act := resolveCLIInput("/skill reviewer", cfg)
	if act.Kind != slash.KindLoadSkill || act.Skill != "reviewer" {
		t.Fatalf("got %+v, want KindLoadSkill reviewer", act)
	}

	plain := resolveCLIInput("hello there", cfg)
	if plain.Kind != slash.KindSend || plain.Text != "hello there" {
		t.Fatalf("got %+v, want KindSend passthrough", plain)
	}
}
