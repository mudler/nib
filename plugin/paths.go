package plugin

import (
	"os"
	"path/filepath"
)

// BaseDir resolves nib's config base directory (XDG-aware), where the plugin
// registry and installed plugins live. The legacy wiz base directory is used as
// a fallback only when the nib dir is absent but the wiz dir exists.
func BaseDir() string {
	// resolve the nib base (XDG-aware), with the legacy wiz base as fallback.
	nib, wiz := basePair()
	if !dirExists(nib) && dirExists(wiz) {
		return wiz
	}
	return nib
}

// basePair returns the candidate nib and wiz base directories using the same
// XDG-aware resolution logic for both.
func basePair() (nib, wiz string) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "nib"), filepath.Join(x, "wiz")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "nib"), filepath.Join(home, ".config", "wiz")
	}
	return ".nib", ".wiz"
}

// dirExists reports whether p exists and is a directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// PluginsDir is where plugin git checkouts live.
func PluginsDir(baseDir string) string { return filepath.Join(baseDir, "plugins") }

func pluginDir(baseDir, name string) string { return filepath.Join(PluginsDir(baseDir), name) }

func registryPath(baseDir string) string { return filepath.Join(baseDir, "plugins.yaml") }
