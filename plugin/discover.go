package plugin

import (
	"fmt"
	"os"

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
}
