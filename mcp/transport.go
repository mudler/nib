package mcp

import (
	"context"
	"fmt"
	"os"

	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type cuaRuntimeFactory func(context.Context, types.Config, bool) (*cuaRuntime, error)

func StartTransports(ctx context.Context, cfg types.Config, shellJobs *ShellJobs) ([]mcp.Transport, error) {
	return startTransports(ctx, cfg, shellJobs, newCUARuntime)
}

func startTransports(
	ctx context.Context,
	cfg types.Config,
	shellJobs *ShellJobs,
	makeCUA cuaRuntimeFactory,
) ([]mcp.Transport, error) {
	if err := validateBrowserConfig(cfg.Browser); err != nil {
		return nil, err
	}
	backend, _ := browserBackend(cfg.Browser)
	requireBrowser := cfg.Browser.Enabled && backend == "cua"
	requireCUA := cfg.Computer.Enabled || requireBrowser

	var runtime *cuaRuntime
	if requireCUA {
		var err error
		runtime, err = makeCUA(ctx, cfg, requireBrowser)
		if err != nil {
			return nil, err
		}
		sharedRuntime := runtime
		go func() {
			<-ctx.Done()
			if err := sharedRuntime.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Cua runtime cleanup error: %v\n", err)
			}
		}()
	}

	if shellJobs == nil {
		shellJobs = NewShellJobsInDir(cfg.WorkingDir)
	}
	// Set MCP servers
	bashMCPServerTransport, bashMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := startBashMCPServer(ctx, bashMCPServerTransport, shellJobs.mgr); err != nil {
			fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		}
	}()

	// Start filesystem MCP server
	filesystemMCPServerTransport, filesystemMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := StartFileSystemMCPServer(ctx, filesystemMCPServerTransport, cfg.WorkingDir); err != nil {
			fmt.Fprintf(os.Stderr, "Filesystem MCP server error: %v\n", err)
		}
	}()

	// Start web MCP server (web_fetch + web_search)
	webMCPServerTransport, webMCPServerClient := mcp.NewInMemoryTransports()

	go func() {
		if err := StartWebMCPServer(ctx, webMCPServerTransport, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Web MCP server error: %v\n", err)
		}
	}()

	transports := []mcp.Transport{bashMCPServerClient, filesystemMCPServerClient, webMCPServerClient}

	// Start the computer_use MCP server only when desktop control is armed
	// (opt-in). It proxies to the cua-driver over stdio.
	if cfg.Computer.Enabled {
		computerServerTransport, computerClient := mcp.NewInMemoryTransports()
		go func() {
			if err := startComputerMCPServer(ctx, computerServerTransport, cfg, runtime); err != nil {
				fmt.Fprintf(os.Stderr, "computer MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, computerClient)
	}

	// Start the selected browser MCP server only when browser automation is
	// armed (opt-in). Chromedp remains the default backend.
	if cfg.Browser.Enabled {
		browserServerTransport, browserClient := mcp.NewInMemoryTransports()
		go func() {
			if err := startBrowserMCPServer(ctx, browserServerTransport, cfg, runtime); err != nil {
				fmt.Fprintf(os.Stderr, "browser MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, browserClient)
	}

	return transports, nil
}
