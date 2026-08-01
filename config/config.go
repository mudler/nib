package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/skill"
	"github.com/mudler/nib/types"

	"gopkg.in/yaml.v3"
)

const defaultPrompt = `
You are a Operative System terminal assistant that helps the user into automatizing common tasks, and can also do perform coding tasks.
You will use the tools at your disposal to fullfill the user request, and, for instance run bash scripts to execute and automate things.

IMPORTANT: Always act by CALLING the available tools. Never narrate or describe a tool action (like reading files or "exploring") without actually invoking the corresponding tool — describing an action does not perform it.

For self-contained subtasks (exploring a codebase, researching, drafting a plan) you can delegate to a sub-agent by calling the spawn_agent tool with an appropriate agent_type. Use background=true to keep working while it runs.
{{- if .Config.Agents }}
Available sub-agent types:
{{- range .Config.Agents }}
- {{ .Name }}: {{ .Description }}
{{- end }}
{{- end }}

Current directory: {{.CurrentDirectory}}
Current user: {{.CurrentUser}}
`

// configPaths returns the list of config file paths to try, in order of priority
func configPaths() []string {
	var paths []string

	// current directory, .nib.yaml (legacy .wiz.yaml as fallback)
	cwd, err := os.Getwd()
	if err == nil {
		paths = append(paths, filepath.Join(cwd, ".nib.yaml"))
		paths = append(paths, filepath.Join(cwd, ".wiz.yaml"))
	}

	// First priority: XDG config directory (legacy wiz dir as fallback)
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		paths = append(paths, filepath.Join(xdgConfig, "nib", "config.yaml"))
		paths = append(paths, filepath.Join(xdgConfig, "wiz", "config.yaml"))
	}

	// Second priority: ~/.config/nib/config.yaml (legacy wiz dir as fallback)
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "nib", "config.yaml"))
		paths = append(paths, filepath.Join(home, ".config", "wiz", "config.yaml"))
		// Third priority: ~/.nib.yaml (legacy ~/.wiz.yaml as fallback)
		paths = append(paths, filepath.Join(home, ".nib.yaml"))
		paths = append(paths, filepath.Join(home, ".wiz.yaml"))
	}

	paths = append(paths, filepath.Join("/etc", "nib", "config.yaml"))
	paths = append(paths, filepath.Join("/etc", "wiz", "config.yaml"))

	return paths
}

// configPathsIn returns the config paths to try. A non-empty root collapses
// the search to that single directory, so an embedder gets exactly the file it
// asked for and never picks up a stray ~/.config/nib or /etc/nib.
func configPathsIn(root string) []string {
	if root != "" {
		return []string{filepath.Join(root, "config.yaml")}
	}
	return configPaths()
}

// WritablePath returns the config file self-configuration should write to: the
// first existing config path (so additions are visible to Load), else the
// preferred default ~/.config/nib/config.yaml.
func WritablePath() string {
	for _, p := range configPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "nib", "config.yaml")
	}
	return ".nib.yaml"
}

// WritablePathIn returns the config file to write to for the given root.
func WritablePathIn(root string) string {
	if root != "" {
		return filepath.Join(root, "config.yaml")
	}
	return WritablePath()
}

// BaseDirOf resolves the plugins/skills root that a loaded config points at.
//
// types.Config.BaseDir holds the RAW OVERRIDE, empty for standalone nib, so it
// is not a directory and must never be joined onto a path: filepath.Join("",
// "plugins") silently yields a relative path under the process working
// directory. Use this helper wherever a resolved directory is wanted. Handing
// the raw field to something that documents its parameter as an override
// (manage.NewIn, setup.Save) is the other correct use and needs no wrapping.
//
// This cannot be a method on types.Config: plugin already imports types, so
// types importing plugin would be an import cycle.
func BaseDirOf(cfg types.Config) string { return plugin.BaseDirIn(cfg.BaseDir) }

// loadFromFileIn attempts to load config from the first existing config file
// under root (or, for an empty root, the default search path).
func loadFromFileIn(root string) types.Config {
	var cfg types.Config

	for _, path := range configPathsIn(root) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}

		// Found and parsed a config file
		break
	}

	return cfg
}

// LoadOptions configures LoadWith. The zero value is what standalone nib uses.
type LoadOptions struct {
	// BaseDir overrides the config/plugins/skills root. Empty means the
	// default XDG resolution.
	BaseDir string
	// Defaults seed fields the config file leaves empty. They sit beneath the
	// file so a user edit always wins.
	Defaults types.Config
	// SkipBareEnv suppresses the bare MODEL / API_KEY / BASE_URL environment
	// variables. Embedders that expose their own prefixed variables set this so
	// a MODEL meant for some other tool cannot retarget the agent.
	SkipBareEnv bool
}

// Load loads the configuration from YAML file and environment variables.
// Environment variables take precedence over YAML config.
func Load() types.Config { return LoadWith(LoadOptions{}) }

// LoadWith is Load with injectable roots and behavior switches.
func LoadWith(o LoadOptions) types.Config {
	// Load from YAML file first
	cfg := loadFromFileIn(o.BaseDir)

	// Seed the gaps the file left. This runs before the env block and before
	// withDefaults, which is what makes the precedence read, lowest to highest:
	// nib's built-in defaults, the embedder's seeds, the config file, the
	// environment.
	if cfg.Model == "" {
		cfg.Model = o.Defaults.Model
	}
	if cfg.APIKey == "" {
		cfg.APIKey = o.Defaults.APIKey
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = o.Defaults.BaseURL
	}
	if cfg.ApprovalMode == "" {
		cfg.ApprovalMode = o.Defaults.ApprovalMode
	}
	if cfg.TraceDir == "" {
		cfg.TraceDir = o.Defaults.TraceDir
	}

	// Override with environment variables if set. An embedder that publishes its
	// own prefixed variables suppresses these, so a bare MODEL exported for some
	// unrelated tool cannot silently retarget the agent.
	if !o.SkipBareEnv {
		if model := os.Getenv("MODEL"); model != "" {
			cfg.Model = model
		}
		if apiKey := os.Getenv("API_KEY"); apiKey != "" {
			cfg.APIKey = apiKey
		}
		if baseURL := os.Getenv("BASE_URL"); baseURL != "" {
			cfg.BaseURL = baseURL
		}
	}

	cfg = withDefaults(cfg)

	// Carry the override (not the resolved root) so consumers keep resolving it
	// through plugin.BaseDirIn / config.WritablePathIn: with no override those
	// two reproduce standalone nib exactly, including WritablePath's
	// first-existing-file search, which <base>/config.yaml would not.
	cfg.BaseDir = o.BaseDir
	root := BaseDirOf(cfg)

	// Merge enabled skill-pack skills before plugins, so precedence is
	// built-in defaults < plugins < skill-packs < user. plugin.Apply skips any
	// skill name already present (user + packs), so packs win over plugins.
	if err := skill.Apply(&cfg, root); err != nil {
		fmt.Fprintf(os.Stderr, "nib: skill load: %v\n", err)
	}

	// Merge enabled plugin contributions (mcp servers + agents) before the
	// agent default-merge, so precedence is built-in defaults < plugins < user.
	if err := plugin.Apply(&cfg, root, internal.Version); err != nil {
		fmt.Fprintf(os.Stderr, "nib: plugin load: %v\n", err)
	}

	// Merge user-provided agent types with the built-in defaults.
	cfg.Agents = MergeAgentTypes(cfg.Agents)

	return cfg
}

// withDefaults fills in zero-valued config fields with their defaults. It is
// pure (no file/env access) so it can be unit-tested directly.
func withDefaults(cfg types.Config) types.Config {
	if cfg.Prompt == "" {
		cfg.Prompt = defaultPrompt
	}
	if cfg.AgentOptions.Iterations == 0 {
		cfg.AgentOptions.Iterations = 10
	}
	if cfg.AgentOptions.MaxAttempts == 0 {
		cfg.AgentOptions.MaxAttempts = 3
	}
	if cfg.AgentOptions.MaxRetries == 0 {
		cfg.AgentOptions.MaxRetries = 3
	}
	// ForceReasoning defaults to false (zero value), which is intentional;
	// users must explicitly enable it in config.

	// Compaction defaults (auto-compaction is ON unless Disabled).
	if cfg.Compaction.MaxContextTokens == 0 {
		cfg.Compaction.MaxContextTokens = 128000
	}
	if cfg.Compaction.Threshold == 0 {
		cfg.Compaction.Threshold = 0.8
	}
	if cfg.Compaction.KeepRecent == 0 {
		cfg.Compaction.KeepRecent = 8
	}
	return cfg
}
