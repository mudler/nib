package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Format identifies a plugin's on-disk layout.
type Format int

const (
	FormatUnknown Format = iota
	FormatNative         // wiz-plugin.yaml
	FormatClaude         // .claude-plugin/plugin.json
)

// NativeManifestFile is the native manifest filename at a plugin's root.
const NativeManifestFile = "wiz-plugin.yaml"

// DetectFormat inspects a plugin directory and reports its layout.
func DetectFormat(dir string) Format {
	if _, err := os.Stat(filepath.Join(dir, NativeManifestFile)); err == nil {
		return FormatNative
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		return FormatClaude
	}
	return FormatUnknown
}

// LoadManifest detects a plugin's format, loads it via the matching adapter,
// stamps its install dir as root, and validates it against wizVersion.
func LoadManifest(dir string, wizVersion string) (Manifest, error) {
	var (
		m   Manifest
		err error
	)
	switch DetectFormat(dir) {
	case FormatNative:
		var data []byte
		data, err = os.ReadFile(filepath.Join(dir, NativeManifestFile))
		if err != nil {
			return Manifest{}, err
		}
		m, err = ParseManifest(data)
	case FormatClaude:
		m, err = loadClaudeManifest(dir)
	default:
		return Manifest{}, fmt.Errorf("no plugin manifest found in %s", dir)
	}
	if err != nil {
		return Manifest{}, err
	}
	m.root = dir
	if err := m.Validate(wizVersion); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
