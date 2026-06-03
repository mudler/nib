package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/nib/internal"
	"github.com/mudler/nib/plugin"
)

// confirmFn reads a yes/no answer. Var for test injection.
var confirmFn = func(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// RunPluginCommand dispatches `nib plugin <sub> ...` and returns an exit code.
func RunPluginCommand(args []string) int {
	if len(args) == 0 {
		pluginUsage()
		return 1
	}
	mgr := plugin.NewManager(plugin.BaseDir())
	switch args[0] {
	case "install":
		return pluginInstall(mgr, args[1:])
	case "list":
		return pluginList(mgr)
	case "update":
		return pluginByName(args, "update", mgr.Update)
	case "remove":
		return pluginByName(args, "remove", mgr.Remove)
	case "enable":
		return pluginSetEnabled(mgr, args[1:], true)
	case "disable":
		return pluginSetEnabled(mgr, args[1:], false)
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin command: %s\n", args[0])
		pluginUsage()
		return 1
	}
}

func pluginUsage() {
	fmt.Fprintln(os.Stderr, "usage: nib plugin <install|list|update|enable|disable|remove> ...")
}

// parseInstallArgs parses `[--ref REF] [--yes] <git-url>` with flags allowed
// before or after the URL (Go's flag package otherwise stops at the first
// positional, silently dropping trailing flags).
func parseInstallArgs(args []string) (url, ref string, yes bool, err error) {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	refp := fs.String("ref", "", "git ref (tag or branch) to install")
	yesp := fs.Bool("yes", false, "skip the confirmation prompt")
	// First pass: consumes any leading flags, stops at the URL.
	if e := fs.Parse(args); e != nil {
		return "", "", false, e
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return "", "", false, fmt.Errorf("missing <git-url>")
	}
	url = rest[0]
	// Second pass: parse any flags that appeared AFTER the URL.
	if e := fs.Parse(rest[1:]); e != nil {
		return "", "", false, e
	}
	if fs.NArg() > 0 {
		return "", "", false, fmt.Errorf("unexpected extra arguments: %v", fs.Args())
	}
	return url, *refp, *yesp, nil
}

func pluginInstall(mgr *plugin.Manager, args []string) int {
	url, ref, yes, err := parseInstallArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage: nib plugin install [--ref REF] [--yes] <git-url>")
		return 1
	}

	m, err := mgr.Install(url, ref, internal.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}

	fmt.Printf("Installed %q v%s — %s\n", m.Name, m.Version, m.Description)
	fmt.Printf("Contributes: %d MCP server(s), %d sub-agent(s)\n", len(m.MCPServers), len(m.Agents))

	if yes || confirmFn("Enable this plugin?") {
		if err := mgr.SetEnabled(m.Name, true); err != nil {
			fmt.Fprintf(os.Stderr, "enable failed: %v\n", err)
			return 1
		}
		fmt.Printf("Plugin %q enabled.\n", m.Name)
		return 0
	}
	fmt.Printf("Plugin %q installed but left disabled. Enable later: nib plugin enable %s\n", m.Name, m.Name)
	return 0
}

func pluginList(mgr *plugin.Manager) int {
	entries, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("No plugins installed.")
		return 0
	}
	for _, e := range entries {
		status := "disabled"
		if e.Enabled {
			status = "enabled"
		}
		fmt.Printf("%-20s %-9s %s\n", e.Name, status, e.SourceURL)
	}
	return 0
}

func pluginByName(args []string, verb string, fn func(string) error) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: nib plugin %s <name>\n", verb)
		return 1
	}
	if err := fn(args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", verb, err)
		return 1
	}
	fmt.Printf("Plugin %q %sd.\n", args[1], verb) // "updated" / "removed"
	return 0
}

func pluginSetEnabled(mgr *plugin.Manager, args []string, enabled bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: nib plugin enable|disable <name>")
		return 1
	}
	if err := mgr.SetEnabled(args[0], enabled); err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		return 1
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf("Plugin %q %s.\n", args[0], state)
	return 0
}
