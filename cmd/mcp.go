package cmd

import (
	"context"
	"flag"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/nib/agentmcp"
)

func parseMCPFlags(args []string) (agentmcp.Options, error) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	httpMode := fs.Bool("http", false, "serve MCP over streamable HTTP instead of stdio")
	_ = fs.Bool("stdio", false, "serve MCP over stdio (default)")
	addr := fs.String("addr", ":8090", "HTTP listen address (used with --http)")
	if err := fs.Parse(args); err != nil {
		return agentmcp.Options{}, err
	}
	return agentmcp.Options{HTTP: *httpMode, Addr: *addr}, nil
}

// RunMCP serves nib's agent as an MCP server. args are the tokens after
// `nib mcp`; transports are the agent's tool servers.
func RunMCP(ctx context.Context, cfg types.Config, args []string, transports ...mcp.Transport) error {
	opts, err := parseMCPFlags(args)
	if err != nil {
		return err
	}
	return agentmcp.Run(ctx, cfg, opts, transports...)
}
