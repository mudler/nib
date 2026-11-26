package claudeplugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// Code is a stable machine code for the reason; Reason is the text.
	Code SkipCode
	// Component is the kind: "skill", "command", "agent", "hook", "mcp",
	// "lsp", "manifest", "monitor", "theme", "workflow", "channel".
	Component string
	// Name is the component name or the path, when known.
	Name   string
	Reason string
}

// SkipCode is a stable machine code for a [Skipped] reason, for hosts that
// translate or group the reasons.
type SkipCode string

// Skip codes.
const (
	SkipInvalidPath          SkipCode = "invalid-path"          // not "./", "..", escapes the plugin
	SkipNotFound             SkipCode = "not-found"             // a listed path does not exist
	SkipNonstandardPath      SkipCode = "nonstandard-path"      // accepted, but outside the format
	SkipReadError            SkipCode = "read-error"            // a file could not be read
	SkipInvalidJSON          SkipCode = "invalid-json"          // a JSON file or object does not parse
	SkipInvalidField         SkipCode = "invalid-field"         // a manifest field has the wrong shape
	SkipInvalidFrontmatter   SkipCode = "invalid-frontmatter"   // YAML frontmatter does not parse
	SkipIgnoredField         SkipCode = "ignored-field"         // a field the format ignores for plugins
	SkipIgnoredFile          SkipCode = "ignored-file"          // a file the format does not load
	SkipUnsupportedComponent SkipCode = "unsupported-component" // monitors, themes, workflows, channels, hook modules, MCP bundles
	SkipUnknownHookEvent     SkipCode = "unknown-hook-event"    // kept as data
	SkipUnsupportedHookType  SkipCode = "unsupported-hook-type" // a handler type outside the format
	SkipInvalidHook          SkipCode = "invalid-hook"          // a malformed matcher group or handler
	SkipInvalidMCPServer     SkipCode = "invalid-mcp-server"
	SkipInvalidLSPServer     SkipCode = "invalid-lsp-server"
	SkipInvalidUserConfig    SkipCode = "invalid-user-config"
	SkipInvalidEntry         SkipCode = "invalid-entry"  // marketplace entry does not parse
	SkipInvalidName          SkipCode = "invalid-name"   // marketplace entry name
	SkipInvalidSource        SkipCode = "invalid-source" // marketplace entry source
	SkipDuplicate            SkipCode = "duplicate"      // marketplace entry name used twice
)

// Code reasons returned by [Plugin.CodeReasons].
const (
	CodeHookCommand = "hook-command"
	CodeMCPStdio    = "mcp-stdio"
	CodeBin         = "bin"
	CodeLSP         = "lsp"
	CodeMonitor     = "monitor"
	// CodeShellInjection marks a skill or command with a !`cmd` or ```!
	// injection: invoking it runs a shell command.
	CodeShellInjection = "shell-injection"
)

// RunsCode reports whether the plugin brings code that runs on a machine:
// command hooks, stdio MCP servers, bin/ executables, LSP servers, monitors,
// or shell injections (!`cmd`) in skills and commands. A plugin that only has
// skills, commands, agents, remote (http, sse, ws) MCP servers and
// non-command hooks is text-only.
func (p *Plugin) RunsCode() bool { return len(p.CodeReasons()) > 0 }

// CodeReasons returns the sorted, de-duplicated list of reasons that make the
// plugin run code (see the Code* constants). Empty means text-only.
func (p *Plugin) CodeReasons() []string {
	set := map[string]bool{}
	for _, g := range p.Hooks {
		for _, h := range g.Handlers {
			if h.Type == HookTypeCommand {
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
	if p.HasShellInjections() {
		set[CodeShellInjection] = true
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

func (p *Plugin) skip(code SkipCode, component, name, reason string) {
	p.Skipped = append(p.Skipped, Skipped{Code: code, Component: component, Name: name, Reason: reason})
}

// CodeFiles returns the files under the plugin root that run as code, for a
// host to show in an install or update review: the bin/ executables, the
// hook, MCP and LSP configuration files, and every existing file or
// directory under the root that a hook command, an MCP server or an LSP
// server names, through ${CLAUDE_PLUGIN_ROOT}/... or a ./ path. A named
// directory adds the files under it. Paths are absolute, sorted and unique.
func (p *Plugin) CodeFiles() []string {
	set := map[string]bool{}
	add := func(path string) {
		if path == "" || !inside(p.Root, path) {
			return
		}
		fi, err := os.Stat(path)
		if err != nil {
			return
		}
		if !fi.IsDir() {
			set[path] = true
			return
		}
		_ = filepath.WalkDir(path, func(f string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				set[f] = true
			}
			if err == nil && d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		})
	}
	scan := func(vals ...string) {
		for _, v := range vals {
			for _, ref := range codeRefs(v) {
				if strings.HasPrefix(ref, "./") {
					add(filepath.Join(p.Root, filepath.FromSlash(ref)))
				} else {
					add(filepath.Join(p.Root, filepath.FromSlash(strings.TrimPrefix(ref, "/"))))
				}
			}
		}
	}
	for _, b := range p.Bin {
		add(b)
	}
	for _, g := range p.Hooks {
		if g.Source != "plugin.json" && g.Source != "marketplace entry" {
			add(filepath.Join(p.Root, filepath.FromSlash(g.Source)))
		}
		for _, h := range g.Handlers {
			if h.Type == HookTypeCommand {
				scan(append([]string{h.Command}, h.Args...)...)
			}
		}
	}
	for _, s := range p.MCPServers {
		if !s.IsRemote() {
			scan(append([]string{s.Command, s.Cwd}, s.Args...)...)
		}
	}
	for _, s := range p.LSPServers {
		scan(append([]string{s.Command}, s.Args...)...)
	}
	for _, f := range []string{".mcp.json", ".lsp.json"} {
		if isFile(filepath.Join(p.Root, f)) && (f == ".mcp.json" && len(p.MCPServers) > 0 || f == ".lsp.json" && len(p.LSPServers) > 0) {
			add(filepath.Join(p.Root, f))
		}
	}
	if isFile(filepath.Join(p.Root, ManifestPath)) && p.RunsCode() {
		add(filepath.Join(p.Root, filepath.FromSlash(ManifestPath)))
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

var codeRef = regexp.MustCompile(`\$\{CLAUDE_PLUGIN_ROOT\}(/[^\s"'` + "`" + `;|&)]*)|(?:^|[\s"'=])(\./[^\s"'` + "`" + `;|&)]+)`)

// codeRefs returns the plugin-relative paths a command string names: the
// part after ${CLAUDE_PLUGIN_ROOT} (starting with "/") or a "./" path.
func codeRefs(s string) []string {
	var out []string
	for _, m := range codeRef.FindAllStringSubmatch(s, -1) {
		switch {
		case m[1] != "":
			out = append(out, m[1])
		case m[2] != "":
			out = append(out, m[2])
		}
	}
	return out
}
