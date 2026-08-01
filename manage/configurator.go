// Package manage provides session-independent operations for the assistant to
// configure itself: install/enable/remove plugins, author skills, and add/remove
// MCP servers. All operations persist to disk (plugin/skill registries and the
// user config file) and are fully unit-testable without a running session.
package manage

import (
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
	"github.com/mudler/nib/types"
)

// Configurator performs self-configuration operations against a base directory
// (the plugin/skill registries) and a single user config file (mcp_servers).
//
// Every field is derived from the one root override handed to NewIn, which is
// the only constructor. That is deliberate: a Configurator built from
// pre-resolved paths could not tell EffectiveConfig which root to reload from,
// so the reload would silently fall back to nib's default paths while the
// writes went somewhere else. Keeping the override as the sole input makes that
// mismatch unrepresentable rather than merely discouraged.
type Configurator struct {
	// baseDirOverride is the RAW root override, empty for standalone nib. It is
	// not a directory: resolve it, never join onto it.
	baseDirOverride string
	// baseDir and configPath are resolved from baseDirOverride at construction.
	baseDir    string
	configPath string
	plugins    *plugin.Manager
	skills     *skill.Manager
}

// NewIn returns a Configurator for a base-directory override, resolving the
// registry root and the writable config path from it. An empty override
// reproduces standalone nib exactly: plugin.BaseDirIn and config.WritablePathIn
// both fall back to the default resolution. Tests pass a temp dir.
func NewIn(baseDirOverride string) *Configurator {
	baseDir := plugin.BaseDirIn(baseDirOverride)
	return &Configurator{
		baseDirOverride: baseDirOverride,
		baseDir:         baseDir,
		configPath:      config.WritablePathIn(baseDirOverride),
		plugins:         plugin.NewManager(baseDir),
		skills:          skill.NewManager(baseDir),
	}
}

// PluginInfo is a registry plugin record in tool-facing form.
type PluginInfo struct {
	Name      string
	SourceURL string
	Ref       string
	Enabled   bool
}

// SkillInfo is one available skill in tool-facing form.
type SkillInfo struct {
	Name        string
	Description string
	Pack        string
}

// ListPlugins returns all installed plugins from the registry.
func (c *Configurator) ListPlugins() ([]PluginInfo, error) {
	entries, err := c.plugins.List()
	if err != nil {
		return nil, err
	}
	out := make([]PluginInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, PluginInfo{Name: e.Name, SourceURL: e.SourceURL, Ref: e.Ref, Enabled: e.Enabled})
	}
	return out, nil
}

// InstallPlugin installs a plugin (DISABLED) and returns its record.
func (c *Configurator) InstallPlugin(url, ref string) (PluginInfo, error) {
	m, err := c.plugins.Install(url, ref, internal.Version)
	if err != nil {
		return PluginInfo{}, err
	}
	return PluginInfo{Name: m.Name, SourceURL: url, Ref: ref, Enabled: false}, nil
}

// SetPluginEnabled flips a plugin's enabled flag.
func (c *Configurator) SetPluginEnabled(name string, enabled bool) error {
	return c.plugins.SetEnabled(name, enabled)
}

// RemovePlugin deletes a plugin's files and registry record.
func (c *Configurator) RemovePlugin(name string) error {
	return c.plugins.Remove(name)
}

// ListSkills returns the skills contributed by enabled skill packs.
func (c *Configurator) ListSkills() ([]SkillInfo, error) {
	packs, err := c.skills.List()
	if err != nil {
		return nil, err
	}
	var out []SkillInfo
	for _, p := range packs {
		if !p.Enabled {
			continue
		}
		skills, err := c.skills.Skills(p.Name)
		if err != nil {
			continue // skip packs that fail to harvest; one bad pack shouldn't break listing
		}
		for _, s := range skills {
			out = append(out, SkillInfo{Name: s.Name, Description: s.Description, Pack: p.Name})
		}
	}
	return out, nil
}

// EffectiveConfig recomputes the merged config (same as startup) so callers can
// re-wire a live session after a change. Disabled MCP servers are dropped here
// so they never start a transport; ListMCPServers still reports them for the UI.
func (c *Configurator) EffectiveConfig() (types.Config, error) {
	cfg := config.LoadWith(config.LoadOptions{BaseDir: c.baseDirOverride})
	cfg.MCPServers = enabledMCPServers(cfg.MCPServers)
	return cfg, nil
}

// enabledMCPServers returns a copy of servers without entries whose Disabled
// flag is set. nil/empty input is returned unchanged; the input is never mutated.
func enabledMCPServers(servers map[string]types.MCPServer) map[string]types.MCPServer {
	if len(servers) == 0 {
		return servers
	}
	out := make(map[string]types.MCPServer, len(servers))
	for name, s := range servers {
		if s.Disabled {
			continue
		}
		out[name] = s
	}
	return out
}
