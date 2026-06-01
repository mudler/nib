package plugin

import (
	"testing"

	"github.com/mudler/wiz/types"
)

func TestMergeManifestsPrecedence(t *testing.T) {
	cfg := &types.Config{
		MCPServers: map[string]types.MCPServer{
			"shared": {Command: "user-cmd"}, // user-defined; must survive
		},
		Agents: []types.AgentTypeConfig{
			{Name: "explore", SystemPrompt: "USER"}, // user override; must stay last
		},
	}
	manifests := []Manifest{
		{
			Name:       "p1",
			MCPServers: map[string]types.MCPServer{"shared": {Command: "p1-cmd"}, "only1": {Command: "c1"}},
			Agents:     []types.AgentTypeConfig{{Name: "explore", SystemPrompt: "P1"}, {Name: "p1agent"}},
		},
		{
			Name:       "p2",
			MCPServers: map[string]types.MCPServer{"only2": {Command: "c2"}},
			Agents:     []types.AgentTypeConfig{{Name: "p2agent"}},
		},
	}

	mergeManifests(cfg, manifests)

	// User MCP key wins over plugin.
	if cfg.MCPServers["shared"].Command != "user-cmd" {
		t.Fatalf("user mcp overwritten: %q", cfg.MCPServers["shared"].Command)
	}
	// Plugin-only MCP keys added.
	if cfg.MCPServers["only1"].Command != "c1" || cfg.MCPServers["only2"].Command != "c2" {
		t.Fatalf("plugin mcp not merged: %+v", cfg.MCPServers)
	}
	// Plugin agents prepended; user 'explore' override is LAST so it wins in MergeAgentTypes.
	last := cfg.Agents[len(cfg.Agents)-1]
	if last.Name != "explore" || last.SystemPrompt != "USER" {
		t.Fatalf("user agent not last: %+v", cfg.Agents)
	}
	// Plugin agents present.
	var names []string
	for _, a := range cfg.Agents {
		names = append(names, a.Name)
	}
	if len(cfg.Agents) != 4 { // p1.explore, p1agent, p2agent, user.explore
		t.Fatalf("expected 4 agents, got %d: %v", len(cfg.Agents), names)
	}
}

func TestMergeFragmentsAndSkills(t *testing.T) {
	root := t.TempDir() // plugins share a root here for simplicity
	cfg := &types.Config{
		Skills: []types.Skill{{Name: "shared", Instructions: "USER BODY"}},
	}
	manifests := []Manifest{
		{
			Name:            "p1",
			root:            root,
			PromptFragments: []FragmentSpec{{Text: "frag-from-p1"}},
			Skills: []SkillSpec{
				{Name: "shared", Instructions: InstructionsSpec{Inline: "P1 BODY"}}, // loses to user
				{Name: "p1skill", Instructions: InstructionsSpec{Inline: "p1 body"}},
			},
		},
		{
			Name:            "p2",
			root:            root,
			PromptFragments: []FragmentSpec{{Text: "frag-from-p2"}},
			Skills:          []SkillSpec{{Name: "p1skill", Instructions: InstructionsSpec{Inline: "p2 overrides"}}}, // plugin-vs-plugin: last wins
		},
	}

	mergeManifests(cfg, manifests)

	if len(cfg.PromptFragments) != 2 || cfg.PromptFragments[0] != "frag-from-p1" || cfg.PromptFragments[1] != "frag-from-p2" {
		t.Fatalf("fragments wrong: %+v", cfg.PromptFragments)
	}
	var shared *types.Skill
	var p1skill *types.Skill
	for i := range cfg.Skills {
		switch cfg.Skills[i].Name {
		case "shared":
			shared = &cfg.Skills[i]
		case "p1skill":
			p1skill = &cfg.Skills[i]
		}
	}
	if shared == nil || shared.Instructions != "USER BODY" {
		t.Fatalf("user skill overwritten: %+v", shared)
	}
	if p1skill == nil || p1skill.Instructions != "p2 overrides" {
		t.Fatalf("plugin-vs-plugin last-wins failed: %+v", p1skill)
	}
	if len(cfg.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(cfg.Skills), cfg.Skills)
	}
}

func TestApplyEnabledOnly(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\nagents:\n  - name: fromplugin\n")
	mgr := NewManager(base)
	if _, err := mgr.Install("u", "", "0.9.0"); err != nil {
		t.Fatal(err)
	}

	// Disabled: contributes nothing.
	cfg := &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(cfg.Agents) != 0 {
		t.Fatalf("disabled plugin contributed: %+v", cfg.Agents)
	}

	// Enabled: contributes its agent.
	if err := mgr.SetEnabled("demo", true); err != nil {
		t.Fatal(err)
	}
	cfg = &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "fromplugin" {
		t.Fatalf("enabled plugin agent missing: %+v", cfg.Agents)
	}
}
