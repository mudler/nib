package plugin

import (
	"fmt"
	"os"
	"strings"

	"github.com/mudler/wiz/types"
)

// EnabledManifests loads the manifest of every enabled plugin in the registry,
// in registry order. A plugin that fails to load is skipped with a warning.
func (mgr *Manager) EnabledManifests(wizVersion string) []Manifest {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wiz: plugin registry: %v\n", err)
		return nil
	}
	var out []Manifest
	for _, e := range reg.Plugins {
		if !e.Enabled {
			continue
		}
		m, err := LoadManifest(pluginDir(mgr.baseDir, e.Name), wizVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wiz: skipping plugin %q: %v\n", e.Name, err)
			continue
		}
		out = append(out, m)
	}
	return out
}

// Apply merges all enabled plugins' contributions into cfg. Precedence is
// plugins < user; user config (already in cfg) always wins.
func Apply(cfg *types.Config, baseDir, wizVersion string) error {
	mergeManifests(cfg, NewManager(baseDir).EnabledManifests(wizVersion))
	return nil
}

func mergeManifests(cfg *types.Config, manifests []Manifest) {
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]types.MCPServer{}
	}
	userKeys := map[string]bool{}
	for k := range cfg.MCPServers {
		userKeys[k] = true
	}

	mcpFrom := map[string]string{}   // mcp key   -> plugin that set it
	agentFrom := map[string]string{} // agent name -> plugin that set it
	var pluginAgents []types.AgentTypeConfig

	for _, m := range manifests {
		for k, v := range m.MCPServers {
			if userKeys[k] {
				continue // user wins
			}
			if prev, ok := mcpFrom[k]; ok {
				fmt.Fprintf(os.Stderr, "wiz: mcp server %q from plugin %q overrides plugin %q\n", k, m.Name, prev)
			}
			cfg.MCPServers[k] = v
			mcpFrom[k] = m.Name
		}
		for _, a := range m.Agents {
			if prev, ok := agentFrom[a.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: agent %q from plugin %q overrides plugin %q\n", a.Name, m.Name, prev)
			}
			agentFrom[a.Name] = m.Name
			pluginAgents = append(pluginAgents, a)
		}
	}

	// Prepend plugin agents so user agents stay last → user wins when
	// config.MergeAgentTypes overlays the list (defaults < plugins < user).
	cfg.Agents = append(pluginAgents, cfg.Agents...)

	mergePromptFragments(cfg, manifests)
	mergeSkills(cfg, manifests)
	mergeCommands(cfg, manifests)
}

// mergePromptFragments appends each enabled plugin's resolved prompt fragments
// to cfg (accumulate; fragments never override).
func mergePromptFragments(cfg *types.Config, manifests []Manifest) {
	for _, m := range manifests {
		for _, fs := range m.PromptFragments {
			text, err := resolveFragment(fs, m.root)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wiz: plugin %q prompt fragment: %v\n", m.Name, err)
				continue
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			cfg.PromptFragments = append(cfg.PromptFragments, text)
		}
	}
}

// mergeSkills merges plugin skills into cfg with precedence plugins < user: a
// user skill of the same name wins; a plugin-vs-plugin name clash is last-wins
// with a warning. Resolution failures are skipped with a warning.
func mergeSkills(cfg *types.Config, manifests []Manifest) {
	userSkills := map[string]bool{}
	for _, s := range cfg.Skills {
		userSkills[s.Name] = true
	}
	order := []string{}
	byName := map[string]types.Skill{}
	from := map[string]string{}

	for _, m := range manifests {
		for _, ss := range m.Skills {
			if userSkills[ss.Name] {
				continue // user wins
			}
			skill, err := resolveSkill(ss, m.root)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wiz: plugin %q skill %q: %v\n", m.Name, ss.Name, err)
				continue
			}
			if _, ok := byName[ss.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: skill %q from plugin %q overrides plugin %q\n", ss.Name, m.Name, from[ss.Name])
			} else {
				order = append(order, ss.Name)
			}
			byName[ss.Name] = skill
			from[ss.Name] = m.Name
		}
	}
	for _, name := range order {
		cfg.Skills = append(cfg.Skills, byName[name])
	}
}

// mergeCommands merges plugin commands into cfg with precedence plugins < user:
// a user command of the same name wins; plugin-vs-plugin clash is last-wins
// with a warning.
func mergeCommands(cfg *types.Config, manifests []Manifest) {
	userCmds := map[string]bool{}
	for _, c := range cfg.Commands {
		userCmds[c.Name] = true
	}
	order := []string{}
	byName := map[string]types.CommandConfig{}
	from := map[string]string{}

	for _, m := range manifests {
		for _, c := range m.Commands {
			if userCmds[c.Name] {
				continue
			}
			if _, ok := byName[c.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: command %q from plugin %q overrides plugin %q\n", c.Name, m.Name, from[c.Name])
			} else {
				order = append(order, c.Name)
			}
			byName[c.Name] = c
			from[c.Name] = m.Name
		}
	}
	for _, name := range order {
		cfg.Commands = append(cfg.Commands, byName[name])
	}
}
