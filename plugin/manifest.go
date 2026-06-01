// Package plugin loads, installs, and merges wiz plugins. A plugin is a git
// repo whose contributions (MCP servers, sub-agent types, and — in later
// phases — prompt fragments, skills, commands, hooks) are normalized into a
// format-agnostic Manifest and merged into the effective wiz config.
package plugin

import (
	"github.com/mudler/wiz/types"

	"gopkg.in/yaml.v3"
)

// Manifest is the normalized, format-agnostic representation of a plugin.
// Both the native (wiz-plugin.yaml) and Claude (.claude-plugin/) adapters
// produce a Manifest. P0 carries only the config-driven contributions.
type Manifest struct {
	Name        string                     `yaml:"name"`
	Version     string                     `yaml:"version"`
	Description string                     `yaml:"description"`
	WizVersion  string                     `yaml:"wiz_version"`
	MCPServers  map[string]types.MCPServer `yaml:"mcp_servers"`
	Agents      []types.AgentTypeConfig    `yaml:"agents"`

	// root is the plugin's install directory. Set by the loader, never parsed.
	// (Unexported: yaml ignores it; no struct tag, to keep `go vet` quiet.)
	root string
}

// ParseManifest parses a native wiz-plugin.yaml document.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
