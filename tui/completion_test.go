package tui

import (
	"testing"

	"github.com/mudler/nib/types"
)

func sampleRegistries() ([]types.CommandConfig, []types.Skill, []types.AgentTypeConfig) {
	return []types.CommandConfig{{Name: "review", Description: "review diff"}},
		[]types.Skill{{Name: "reviewer", Description: "guidelines"}},
		[]types.AgentTypeConfig{{Name: "explore", Description: "read-only"}}
}

func TestBuildAndFilter(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	items := buildCompItems(cmds, skills, agents)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	got := filterComp(items, "rev")
	if len(got) != 2 {
		t.Fatalf("want 2 matches for 'rev', got %d: %+v", len(got), got)
	}
	for _, it := range items {
		switch it.Cat {
		case compCmd:
			if it.Insert != "/review " {
				t.Fatalf("cmd insert wrong: %q", it.Insert)
			}
		case compSkill:
			if it.Insert != "/skill reviewer " {
				t.Fatalf("skill insert wrong: %q", it.Insert)
			}
		case compAgent:
			if it.Insert != "/agent explore " {
				t.Fatalf("agent insert wrong: %q", it.Insert)
			}
		}
	}
}

func TestCompStateSyncAndAccept(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)

	c.sync("/rev")
	if !c.active || len(c.matches) != 2 {
		t.Fatalf("expected active with 2 matches, got active=%v matches=%d", c.active, len(c.matches))
	}
	if g := c.ghost("/rev"); g != "iew " {
		t.Fatalf("ghost wrong: got %q want %q", g, "iew ")
	}
	c.sync("/review the diff")
	if c.active {
		t.Fatal("popup should be inactive once a space is typed")
	}
	c.sync("hello")
	if c.active {
		t.Fatal("popup should be inactive for non-slash input")
	}
	c.sync("/rev")
	got, ok := c.accept()
	if !ok || got != "/review " {
		t.Fatalf("accept wrong: %q ok=%v", got, ok)
	}
}

func TestCompStateNavigation(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)
	c.sync("/rev")
	c.down()
	if c.sel != 1 {
		t.Fatalf("down: sel=%d", c.sel)
	}
	c.down()
	if c.sel != 1 {
		t.Fatalf("down clamp: sel=%d", c.sel)
	}
	c.up()
	c.up()
	if c.sel != 0 {
		t.Fatalf("up clamp: sel=%d", c.sel)
	}
}
