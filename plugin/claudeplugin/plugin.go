package claudeplugin

import (
	"sort"
	"strings"
)

// Plugin is a parsed plugin. All paths are absolute. Bodies are raw: no
// variable or argument is substituted at load time.
type Plugin struct {
	// Name is the manifest name. Components are namespaced under it as
	// "<Name>:<component>".
	Name string
	// ID is "<entry name>@<marketplace>" for a marketplace plugin, or Name
	// when the plugin was loaded without a marketplace.
	ID          string
	DisplayName string
	Version     string
	Description string
	Author      *Person
	Homepage    string
	Repository  string
	License     string
	Keywords    []string

	// Root is the plugin directory, the value of ${CLAUDE_PLUGIN_ROOT}.
	Root string

	Skills     []Skill
	Commands   []Command
	Agents     []Agent
	MCPServers map[string]MCPServer
	Hooks      []HookGroup
	LSPServers map[string]LSPServer
	// Bin lists the executables in the plugin's bin/ directory, as absolute
	// paths. A host that supports them puts BinDir on PATH.
	Bin    []string
	BinDir string
	// OutputStyles lists output style files. They are recorded, not parsed.
	OutputStyles []string
	UserConfig   map[string]UserConfigOption

	// Skipped lists every component, field or file that was not loaded, or
	// that was loaded but cannot be honoured, with the reason.
	Skipped []Skipped
}

// Person is an author or an owner.
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Skipped describes one part of a plugin that the parser did not load or that
// a host must not expect to work.
type Skipped struct {
	// Component is the kind: "skill", "command", "agent", "hook", "mcp",
	// "lsp", "manifest", "monitor", "theme", "workflow", "channel".
	Component string
	// Name is the component name or the path, when known.
	Name   string
	Reason string
}

// Code reasons returned by [Plugin.CodeReasons].
const (
	CodeHookCommand = "hook-command"
	CodeMCPStdio    = "mcp-stdio"
	CodeBin         = "bin"
	CodeLSP         = "lsp"
	CodeMonitor     = "monitor"
)

// RunsCode reports whether the plugin brings code that runs on a machine:
// command hooks, stdio MCP servers, bin/ executables or LSP servers. A plugin
// that only has skills, commands, agents, remote MCP servers and non-command
// hooks is text-only.
//
// Shell injections (!`cmd`) in skills and commands are not counted here, since
// they run only when a host chooses to run them through its own shell tool.
// See [Plugin.HasShellInjections].
func (p *Plugin) RunsCode() bool { return len(p.CodeReasons()) > 0 }

// CodeReasons returns the sorted, de-duplicated list of reasons that make the
// plugin run code (see the Code* constants). Empty means text-only.
func (p *Plugin) CodeReasons() []string {
	set := map[string]bool{}
	for _, g := range p.Hooks {
		for _, h := range g.Handlers {
			if h.Type == HookCommand {
				set[CodeHookCommand] = true
			}
		}
	}
	for _, s := range p.MCPServers {
		if !s.IsRemote() {
			set[CodeMCPStdio] = true
		}
	}
	if len(p.Bin) > 0 {
		set[CodeBin] = true
	}
	if len(p.LSPServers) > 0 {
		set[CodeLSP] = true
	}
	for _, s := range p.Skipped {
		if s.Component == "monitor" {
			set[CodeMonitor] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasShellInjections reports whether any skill or command body holds a
// !`cmd` or ```! shell injection.
func (p *Plugin) HasShellInjections() bool {
	for _, s := range p.Skills {
		for _, in := range s.Injections {
			if in.Kind != InjectFile {
				return true
			}
		}
	}
	for _, c := range p.Commands {
		for _, in := range c.Injections {
			if in.Kind != InjectFile {
				return true
			}
		}
	}
	return false
}

// Qualify returns "<plugin>:<name>", the namespaced form of a component name.
func Qualify(plugin, name string) string { return plugin + ":" + name }

// DataDirName returns the directory name for a plugin's persistent data dir
// (the last segment of ${CLAUDE_PLUGIN_DATA}): id with every character other
// than a letter, digit, '_' or '-' replaced by '-'. "fmt@tools" gives
// "fmt-tools". The host picks the parent directory.
func DataDirName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (p *Plugin) skip(component, name, reason string) {
	p.Skipped = append(p.Skipped, Skipped{Component: component, Name: name, Reason: reason})
}
