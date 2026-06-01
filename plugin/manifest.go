// Package plugin loads, installs, and merges wiz plugins. A plugin is a git
// repo whose contributions (MCP servers, sub-agent types, and — in later
// phases — prompt fragments, skills, commands, hooks) are normalized into a
// format-agnostic Manifest and merged into the effective wiz config.
package plugin

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
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

// Validate checks required fields and the wiz_version constraint. wizVersion is
// the running build version (internal.Version); empty or non-semver values
// (dev builds) skip the constraint check.
func (m Manifest) Validate(wizVersion string) error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("plugin manifest: name is required")
	}
	if m.Name != filepath.Base(m.Name) || m.Name == "." || m.Name == ".." || strings.ContainsRune(m.Name, '/') || strings.ContainsRune(m.Name, '\\') {
		return fmt.Errorf("plugin manifest: invalid name %q (must be a single path segment)", m.Name)
	}
	for k, s := range m.MCPServers {
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("plugin manifest: mcp server %q missing command", k)
		}
	}
	for i, a := range m.Agents {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("plugin manifest: agent #%d missing name", i)
		}
	}
	return checkWizVersion(m.WizVersion, wizVersion)
}

func checkWizVersion(constraint, current string) error {
	if strings.TrimSpace(constraint) == "" {
		return nil
	}
	cur, err := semver.NewVersion(strings.TrimPrefix(current, "v"))
	if err != nil {
		// Dev/unknown build: cannot evaluate, treat as satisfied.
		return nil
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return fmt.Errorf("plugin manifest: invalid wiz_version %q: %w", constraint, err)
	}
	if !c.Check(cur) {
		return fmt.Errorf("plugin requires wiz %s, running %s", constraint, current)
	}
	return nil
}
