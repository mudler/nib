package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The in-memory servers below run until ctx ends. A server whose ctx is
// cancelled at exit returns context.Canceled, which is shutdown, not a
// failure, so it is not reported. Whether a server sees that or its client
// closing first depends on goroutine timing.
func StartTransports(ctx context.Context, cfg types.Config, shellJobs *ShellJobs, limits *OutputLimitsPolicy, artifacts *ArtifactStore) ([]mcp.Transport, error) {
	if shellJobs == nil {
		shellJobs = NewShellJobsInDir(cfg.WorkingDir)
	}
	if limits == nil {
		limits = NewOutputLimitsPolicy(cfg.ToolOutputLimits)
	}
	if artifacts == nil {
		artifacts = NewArtifactStore()
	}
	// Set MCP servers
	bashMCPServerTransport, bashMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := startBashMCPServer(ctx, bashMCPServerTransport, shellJobs.mgr, limits, artifacts); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		}
	}()

	// Start filesystem MCP server
	filesystemMCPServerTransport, filesystemMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := StartFileSystemMCPServer(ctx, filesystemMCPServerTransport, cfg.WorkingDir, limits, artifacts); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Filesystem MCP server error: %v\n", err)
		}
	}()

	// Start web MCP server (web_fetch + web_search)
	webMCPServerTransport, webMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := StartWebMCPServer(ctx, webMCPServerTransport, cfg); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Web MCP server error: %v\n", err)
		}
	}()

	transports := []mcp.Transport{bashMCPServerClient, filesystemMCPServerClient, webMCPServerClient}

	// Start the computer_use MCP server only when desktop control is armed
	// (opt-in). It proxies to the cua-driver over stdio.
	if cfg.Computer.Enabled {
		computerServerTransport, computerClient := mcp.NewInMemoryTransports()
		go func() {
			if err := StartComputerMCPServer(ctx, computerServerTransport, cfg); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "computer MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, computerClient)
	}

	// Start the browser MCP server only when browser automation is armed
	// (opt-in). It drives a headed, persistent-profile Chrome via chromedp.
	if cfg.Browser.Enabled {
		browserServerTransport, browserClient := mcp.NewInMemoryTransports()
		go func() {
			if err := StartBrowserMCPServer(ctx, browserServerTransport, cfg); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "browser MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, browserClient)
	}

	return transports, nil
}
