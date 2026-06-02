package plugin

import (
	"os"
	"path/filepath"
)

// BaseDir resolves wiz's config base directory (XDG-aware), where the plugin
// registry and installed plugins live.
func BaseDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "wiz")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "wiz")
	}
	return ".wiz"
}

// PluginsDir is where plugin git checkouts live.
func PluginsDir(baseDir string) string { return filepath.Join(baseDir, "plugins") }

func pluginDir(baseDir, name string) string { return filepath.Join(PluginsDir(baseDir), name) }

func registryPath(baseDir string) string { return filepath.Join(baseDir, "plugins.yaml") }
