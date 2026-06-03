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
type Configurator struct {
	baseDir    string
	configPath string
	plugins    *plugin.Manager
	skills     *skill.Manager
}

// New returns a Configurator rooted at baseDir (use plugin.BaseDir() in prod)
// writing MCP-server config to configPath (use config.WritablePath() in prod).
func New(baseDir, configPath string) *Configurator {
	return &Configurator{
		baseDir:    baseDir,
		configPath: configPath,
		plugins:    plugin.NewManager(baseDir),
		skills:     skill.NewManager(baseDir),
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
			continue
		}
		for _, s := range skills {
			out = append(out, SkillInfo{Name: s.Name, Description: s.Description, Pack: p.Name})
		}
	}
	return out, nil
}

// EffectiveConfig recomputes the merged config (same as startup) so callers can
// re-wire a live session after a change.
func (c *Configurator) EffectiveConfig() (types.Config, error) {
	return config.Load(), nil
}
