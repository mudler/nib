package mcp

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/nib/types"
)

const defaultCUAProfileName = "nib"

var cuaProfileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func resolveCUAConfig(cfg types.Config) types.CUAConfig {
	resolved := types.CUAConfig{
		Command:   cfg.CUA.Command,
		Args:      slices.Clone(cfg.CUA.Args),
		Env:       maps.Clone(cfg.CUA.Env),
		SessionID: cfg.CUA.SessionID,
	}

	if resolved.Command == "" {
		resolved.Command = cfg.Computer.Command
	}
	if resolved.Command == "" {
		resolved.Command = os.Getenv("NIB_CUA_DRIVER_CMD")
	}
	if resolved.Command == "" {
		resolved.Command = "cua-driver"
	}

	if len(resolved.Args) == 0 {
		resolved.Args = slices.Clone(cfg.Computer.Args)
	}
	if len(resolved.Args) == 0 {
		resolved.Args = []string{"mcp"}
	}

	if len(resolved.Env) == 0 {
		resolved.Env = maps.Clone(cfg.Computer.Env)
	}
	if resolved.SessionID == "" {
		resolved.SessionID = cfg.Computer.SessionID
	}

	return resolved
}

func browserBackend(cfg types.BrowserConfig) (string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "", "chromedp":
		return "chromedp", nil
	case "cua":
		return "cua", nil
	default:
		return "", fmt.Errorf("unsupported browser backend %q: want chromedp or cua", cfg.Backend)
	}
}

func cuaProfileName(cfg types.BrowserConfig) (string, error) {
	name := cfg.ProfileName
	if name == "" {
		name = defaultCUAProfileName
	}
	if !cuaProfileNamePattern.MatchString(name) {
		return "", fmt.Errorf(
			"invalid browser profile_name %q: want 1-64 ASCII letters, digits, hyphens, or underscores",
			name,
		)
	}
	return name, nil
}

func validateBrowserConfig(cfg types.BrowserConfig) error {
	backend, err := browserBackend(cfg)
	if err != nil {
		return err
	}
	if backend != "cua" {
		return nil
	}
	if cfg.ProfileDir != "" {
		return fmt.Errorf("browser.profile_dir is not supported by the Cua backend; use browser.profile_name")
	}
	_, err = cuaProfileName(cfg)
	return err
}
