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

// Save writes the LLM connection fields (model, api_key, base_url) into
// <configDir>/config.yaml, preserving any keys that already exist in the file.
// It returns the path written. The file uses mode 0600 because it holds a key.
func Save(cfg types.Config) (string, error) {
	dir, err := configDir()
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
