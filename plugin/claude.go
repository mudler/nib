package plugin

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/nib/types"
)

// SourceFile is written into a plugin installed from a marketplace. It keeps
// the marketplace entry, which serves as the manifest of a plugin without
// plugin.json, and the commit the plugin was installed at.
const SourceFile = ".nib-source.json"

// sourceRecord is the content of SourceFile.
type sourceRecord struct {
	Marketplace string          `json:"marketplace"`
	Entry       json.RawMessage `json:"entry"`
	SHA         string          `json:"sha,omitempty"`
}

func readSourceRecord(dir string) (*sourceRecord, *claudeplugin.Entry, error) {
	data, err := os.ReadFile(filepath.Join(dir, SourceFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var rec sourceRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", SourceFile, err)
	}
	var e claudeplugin.Entry
	if err := json.Unmarshal(rec.Entry, &e); err != nil {
		return nil, nil, fmt.Errorf("%s: entry: %w", SourceFile, err)
	}
	e.Raw = rec.Entry
	return &rec, &e, nil
}

func writeSourceRecord(dir string, rec sourceRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SourceFile), data, 0o644)
}

// claudeEvents are the hook events nib fires.
var claudeEvents = map[string]bool{
	"SessionStart":     true,
	"UserPromptSubmit": true,
	"PreToolUse":       true,
	"PostToolUse":      true,
	"Stop":             true,
}

// loadClaudeManifest parses a plugin in the Claude Code format with
// claudeplugin and maps it into a nib Manifest. Components are namespaced
// "<plugin>:<name>". Parts nib cannot use are listed in Manifest.Notes.
func loadClaudeManifest(dir string) (Manifest, error) {
	rec, entry, err := readSourceRecord(dir)
	if err != nil {
		return Manifest{}, err
	}
	opts := claudeplugin.LoadOptions{}
	if rec != nil {
		opts.Entry = entry
		opts.Marketplace = rec.Marketplace
	}
	cp, err := claudeplugin.Load(dir, opts)
	if err != nil {
		return Manifest{}, fmt.Errorf("claude plugin: %w", err)
	}
	return claudeToManifest(cp, dir), nil
}

// claudeDataDir is the persistent data directory of a plugin, the value of
// ${CLAUDE_PLUGIN_DATA}: <base>/plugin-data/<id>, next to plugins/.
func claudeDataDir(pluginRoot, id string) string {
	base := filepath.Dir(filepath.Dir(pluginRoot))
	return filepath.Join(base, "plugin-data", claudeplugin.DataDirName(id))
}

// claudeOptionValues resolves userConfig: the option default, overridden by a
// CLAUDE_PLUGIN_OPTION_<KEY> environment variable. nib has no settings store
// for plugin options yet.
func claudeOptionValues(cp *claudeplugin.Plugin) (map[string]string, error) {
	vals := map[string]any{}
	for k := range cp.UserConfig {
		if v, ok := os.LookupEnv("CLAUDE_PLUGIN_OPTION_" + strings.ToUpper(k)); ok {
			vals[k] = v
		}
	}
	return cp.ResolveUserConfig(vals)
}

func claudeToManifest(cp *claudeplugin.Plugin, root string) Manifest {
	m := Manifest{
		Name:        cp.Name,
		Version:     cp.Version,
		Description: cp.Description,
		claude:      cp,
	}
	note := func(format string, a ...any) { m.Notes = append(m.Notes, fmt.Sprintf(format, a...)) }
	for _, s := range cp.Skipped {
		note("%s %s: %s", s.Component, s.Name, s.Reason)
	}

	options, err := claudeOptionValues(cp)
	if err != nil {
		note("userConfig: %v (set CLAUDE_PLUGIN_OPTION_<KEY>)", err)
		options = map[string]string{}
	}
	cwd, _ := os.Getwd()
	dataDir := claudeDataDir(root, cp.ID)
	vars := claudeplugin.Vars{
		PluginRoot: root,
		PluginData: dataDir,
		ProjectDir: cwd,
		UserConfig: options,
		Sensitive:  cp.SensitiveKeys(),
		Env:        os.LookupEnv,
	}
	hookEnv := append([]string{"CLAUDE_PLUGIN_DATA=" + dataDir, "CLAUDE_PROJECT_DIR=" + cwd}, claudeplugin.OptionEnv(options)...)

	mapTools := func(owner string, rules []string) []string {
		mapped, unmapped := hooks.ClaudeTools.Map(rules)
		if len(unmapped) > 0 {
			note("%s: tools without a nib equivalent dropped: %s", owner, strings.Join(unmapped, ", "))
		}
		return mapped
	}
	injections := func(owner string, ins []claudeplugin.Injection) {
		if len(ins) > 0 {
			note("%s: %d !`cmd` or @file reference(s) are passed as text, not run", owner, len(ins))
		}
	}

	for _, s := range cp.Skills {
		sv := vars
		sv.SkillDir = s.Dir
		body := claudeplugin.ExpandContent(s.Body, sv)
		injections(s.QualifiedName, s.Injections)
		if s.DisableModelInvocation {
			// Only the user may run it: a command, not a skill the model
			// can load.
			m.Commands = append(m.Commands, types.CommandConfig{
				Name:        s.QualifiedName,
				Description: withHint(s.Description, s.ArgumentHint),
				Prompt:      body,
				Format:      types.HookFormatClaude,
				Arguments:   s.Arguments,
			})
			continue
		}
		desc := s.Description
		if s.WhenToUse != "" {
			desc = strings.TrimSpace(desc + " " + s.WhenToUse)
		}
		m.Skills = append(m.Skills, SkillSpec{
			Name:         s.QualifiedName,
			Description:  desc,
			Instructions: InstructionsSpec{Inline: body},
			Tools:        mapTools(s.QualifiedName, s.AllowedTools),
			Dir:          s.Dir,
		})
	}

	for _, c := range cp.Commands {
		injections(c.QualifiedName, c.Injections)
		if strings.TrimSpace(c.Body) == "" {
			note("command %s: empty body", c.QualifiedName)
			continue
		}
		m.Commands = append(m.Commands, types.CommandConfig{
			Name:        c.QualifiedName,
			Description: withHint(c.Description, c.ArgumentHint),
			Prompt:      claudeplugin.ExpandContent(c.Body, vars),
			Format:      types.HookFormatClaude,
			Arguments:   c.Arguments,
		})
	}

	for _, a := range cp.Agents {
		var tools []string
		if len(a.Tools) > 0 {
			tools = mapTools(a.QualifiedName, a.Tools)
		}
		if tier := claudeplugin.ModelTier(a.Model); tier != "inherit" {
			note("agent %s: model %q is not mapped; the agent uses the session model", a.QualifiedName, a.Model)
		}
		m.Agents = append(m.Agents, types.AgentTypeConfig{
			Name:         a.QualifiedName,
			Description:  a.Description,
			SystemPrompt: claudeplugin.ExpandContent(a.Prompt, vars),
			Tools:        tools,
			Iterations:   a.MaxTurns,
		})
	}

	if len(cp.MCPServers) > 0 {
		m.MCPServers = map[string]types.MCPServer{}
	}
	for _, name := range slices.Sorted(maps.Keys(cp.MCPServers)) {
		s, missing := cp.MCPServers[name].Expand(vars)
		if len(missing) > 0 {
			note("mcp %s: unset variables %s", name, strings.Join(missing, ", "))
		}
		switch s.Type {
		case "stdio":
			env := map[string]string{"CLAUDE_PLUGIN_ROOT": root, "CLAUDE_PLUGIN_DATA": dataDir, "CLAUDE_PROJECT_DIR": cwd}
			maps.Copy(env, s.Env)
			m.MCPServers[name] = types.MCPServer{Command: s.Command, Args: s.Args, Env: env}
		case "http", "sse":
			if s.HeadersHelper != "" {
				note("mcp %s: headersHelper is not supported", name)
			}
			m.MCPServers[name] = types.MCPServer{URL: s.URL, Transport: s.Type, Headers: s.Headers}
		default:
			note("mcp %s: %s transport is not supported by nib", name, s.Type)
		}
	}
	if len(m.MCPServers) == 0 {
		m.MCPServers = nil
	}

	for _, g := range cp.Hooks {
		if !claudeEvents[g.Event] {
			note("hook %s: event not fired by nib", g.Event)
			continue
		}
		matcher := g.Matcher
		if claudeplugin.IsToolEvent(g.Event) {
			matcher = hooks.TranslateMatcher(matcher)
		}
		for _, h := range g.Handlers {
			if h.Type != claudeplugin.HookTypeCommand {
				note("hook %s: %s handlers are not supported", g.Event, h.Type)
				continue
			}
			if h.Async || h.AsyncRewake {
				note("hook %s: async hook runs synchronously", g.Event)
			}
			if h.If != "" {
				note("hook %s: \"if\" rule %q is ignored; the hook runs for every match", g.Event, h.If)
			}
			cmd := claudeplugin.ExpandConfig(h.Command, claudeplugin.Vars{PluginRoot: root, PluginData: dataDir, ProjectDir: cwd}, nil)
			if len(h.Args) > 0 {
				// Exec form: one word per argument, no shell parsing.
				cmd = shellQuote(cmd)
				for _, arg := range h.Args {
					cmd += " " + shellQuote(claudeplugin.ExpandConfig(arg, vars, nil))
				}
			}
			m.Hooks = append(m.Hooks, types.HookConfig{
				Event:   g.Event,
				Matcher: matcher,
				Command: cmd,
				Format:  types.HookFormatClaude,
				Timeout: h.Timeout,
				Env:     hookEnv,
			})
		}
	}

	if len(cp.LSPServers) > 0 {
		note("lsp: %d language server(s) are not supported by nib", len(cp.LSPServers))
	}
	if len(cp.Bin) > 0 {
		note("bin: %d executable(s) are not added to PATH by nib", len(cp.Bin))
	}
	if len(cp.OutputStyles) > 0 {
		note("output styles are not supported by nib")
	}
	return m
}

func withHint(desc, hint string) string {
	if hint == "" {
		return desc
	}
	return strings.TrimSpace(desc + " " + hint)
}

// shellQuote quotes s as one sh word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
