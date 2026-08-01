package setup

import (
	"os"
	"path/filepath"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

// configDir returns the directory where the user config lives:
// $XDG_CONFIG_HOME/nib if set, otherwise ~/.config/nib.
func configDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nib"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nib"), nil
}

// configDirIn returns the config directory for a root override, falling back to
// the default resolution when the override is empty. It guards against joining
// an empty override onto a path, which would silently write into the process
// working directory. The default is setup's own XDG resolution rather than
// plugin.BaseDirIn's: the latter also falls back to the legacy wiz directory,
// and onboarding has never written there.
func configDirIn(root string) (string, error) {
	if root != "" {
		return root, nil
	}
	return configDir()
}

// Save writes the LLM connection fields (model, api_key, base_url) into
// <configDir>/config.yaml, preserving any keys that already exist in the file.
// It returns the path written. The file uses mode 0600 because it holds a key.
//
// cfg.BaseDir, when set, redirects the write into that root: onboarding inside
// an embedded nib must not put the embedder's model and api_key into the user's
// real ~/.config/nib.
func Save(cfg types.Config) (string, error) {
	dir, err := configDirIn(cfg.BaseDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")

	// Overlay onto any existing config so unrelated keys survive.
	existing := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}
	existing["model"] = cfg.Model
	existing["api_key"] = cfg.APIKey
	existing["base_url"] = cfg.BaseURL

	out, err := yaml.Marshal(existing)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
