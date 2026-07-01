package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mudler/nib/config"
	"github.com/mudler/nib/manage"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var mcpManageSubcommands = map[string]bool{"add": true, "list": true, "remove": true, "test": true}

// IsMCPManageSubcommand reports whether `nib mcp <s>` is a management command
// (as opposed to the server-serving forms: bare, --http, --stdio, --addr).
func IsMCPManageSubcommand(s string) bool { return mcpManageSubcommands[s] }

func mcpUsage() {
	fmt.Fprintln(os.Stderr, "usage: nib mcp <add|list|remove|test> ...")
}

// RunMCPCommand dispatches `nib mcp <sub> ...` and returns an exit code.
func RunMCPCommand(args []string) int {
	if len(args) == 0 {
		mcpUsage()
		return 1
	}
	cfgr := manage.New(plugin.BaseDir(), config.WritablePath())
	switch args[0] {
	case "add":
		return mcpAdd(cfgr, args[1:])
	case "list":
		return mcpList(cfgr)
	case "remove":
		return mcpRemove(cfgr, args[1:])
	case "test":
		return mcpTest(cfgr, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mcp command: %s\n", args[0])
		mcpUsage()
		return 1
	}
}

// kvFlag is a flag.Value that accumulates repeated KEY=VALUE flags into a
// shared map. Register the same underlying map twice (once per flag name)
// to get two independently-repeatable KEY=VALUE flags, e.g. --env and
// --header.
type kvFlag map[string]string

func (k kvFlag) String() string { return "" }
func (k kvFlag) Set(s string) error {
	key, val, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("must be KEY=VALUE, got %q", s)
	}
	k[key] = val
	return nil
}

// parseAddArgs parses `<name> [--env K=V]... [--url U] [--transport http|sse] [-- <command> args...]`.
// Everything after a standalone "--" is the command and its arguments.
func parseAddArgs(args []string) (string, types.MCPServer, error) {
	if len(args) == 0 {
		return "", types.MCPServer{}, fmt.Errorf("missing <name>")
	}

	var srv types.MCPServer
	env := map[string]string{}
	headers := map[string]string{}

	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&srv.URL, "url", "", "base URL for a remote MCP server")
	fs.StringVar(&srv.Transport, "transport", "", "remote transport: http (default) or sse")
	fs.StringVar(&srv.BearerToken, "token", "", "bearer token for a remote MCP server")
	fs.Var(kvFlag(env), "env", "environment variable, KEY=VALUE (repeatable)")
	fs.Var(kvFlag(headers), "header", "custom HTTP header, KEY=VALUE (repeatable)")

	// First pass: consume any flags before the name (none expected in
	// practice — name is documented as the first token — but this mirrors
	// parseInstallArgs's tolerance for either order).
	if err := fs.Parse(args); err != nil {
		return "", srv, err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", srv, fmt.Errorf("missing <name>")
	}
	name := rest[0]

	// Second pass: parse the flags (and the -- boundary) that follow name.
	if err := fs.Parse(rest[1:]); err != nil {
		return "", srv, err
	}
	cmdParts := fs.Args()

	if len(cmdParts) > 0 {
		srv.Command = cmdParts[0]
		srv.Args = cmdParts[1:]
	}
	if len(env) > 0 {
		srv.Env = env
	}
	if len(headers) > 0 {
		srv.Headers = headers
	}
	if srv.Transport != "" && srv.Transport != "http" && srv.Transport != "sse" {
		return "", srv, fmt.Errorf("--transport must be http or sse, got %q", srv.Transport)
	}
	if (srv.Command == "") == (srv.URL == "") {
		return "", srv, fmt.Errorf("provide exactly one of: a command after '--', or --url")
	}
	return name, srv, nil
}

func mcpAdd(cfgr *manage.Configurator, args []string) int {
	name, srv, err := parseAddArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintln(os.Stderr, "usage: nib mcp add <name> [--env K=V]... -- <command> [args...]")
		fmt.Fprintln(os.Stderr, "       nib mcp add <name> --url <url> [--transport http|sse] [--token T] [--header K=V]...")
		return 1
	}
	if err := cfgr.AddMCPServer(name, srv); err != nil {
		fmt.Fprintf(os.Stderr, "add failed: %v\n", err)
		return 1
	}
	fmt.Printf("Added MCP server %q. It will be available on the next nib session (verify now with: nib mcp test %s).\n", name, name)
	return 0
}

func mcpList(cfgr *manage.Configurator) int {
	servers, err := cfgr.ListMCPServers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		return 1
	}
	if len(servers) == 0 {
		fmt.Println("No MCP servers configured.")
		return 0
	}
	for _, s := range servers {
		if s.URL != "" {
			tr := s.Transport
			if tr == "" {
				tr = "http"
			}
			suffix := ""
			if s.Authenticated {
				suffix = " (authenticated)"
			}
			fmt.Printf("%-20s %s %s%s\n", s.Name, tr, s.URL, suffix)
		} else {
			fmt.Printf("%-20s %s\n", s.Name, strings.TrimSpace(s.Command+" "+strings.Join(s.Args, " ")))
		}
	}
	return 0
}

func mcpRemove(cfgr *manage.Configurator, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: nib mcp remove <name>")
		return 1
	}
	if err := cfgr.RemoveMCPServer(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "remove failed: %v\n", err)
		return 1
	}
	fmt.Printf("Removed MCP server %q.\n", args[0])
	return 0
}

func mcpTest(cfgr *manage.Configurator, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: nib mcp test <name>")
		return 1
	}
	name := args[0]
	srv, err := cfgr.GetMCPServer(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "nib", Version: "v1.0.0"}, nil)
	sess, err := client.Connect(ctx, wizmcp.TransportForServer(srv), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		return 1
	}
	defer sess.Close()
	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tools failed: %v\n", err)
		return 1
	}
	fmt.Printf("%s: %d tool(s)\n", name, len(res.Tools))
	for _, tool := range res.Tools {
		fmt.Printf("  - %s\n", tool.Name)
	}
	return 0
}
