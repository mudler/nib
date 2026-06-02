package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/mudler/wiz/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// commandTransport creates a new transport for a command
func commandTransport(cmd string, args []string, env ...string) mcp.Transport {
	command := exec.Command(cmd, args...)
	command.Env = os.Environ()
	command.Env = append(command.Env, env...)

	transport := &mcp.CommandTransport{Command: command}
	return transport
}

func StartTransports(ctx context.Context, cfg types.Config, shellJobs *ShellJobs) ([]mcp.Transport, error) {
	if shellJobs == nil {
		shellJobs = NewShellJobs()
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
		if err := StartFileSystemMCPServer(ctx, filesystemMCPServerTransport); err != nil {
			fmt.Fprintf(os.Stderr, "Filesystem MCP server error: %v\n", err)
		}
	}()

	transports := []mcp.Transport{bashMCPServerClient, filesystemMCPServerClient}

	// Skills server (load_skill tool) — only when the config carries skills.
	if len(cfg.Skills) > 0 {
		skillsServerTransport, skillsServerClient := mcp.NewInMemoryTransports()
		go func() {
			if err := StartSkillsMCPServer(ctx, skillsServerTransport, cfg.Skills); err != nil {
				fmt.Fprintf(os.Stderr, "Skills MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, skillsServerClient)
	}

	for _, c := range cfg.MCPServers {
		envs := []string{}
		for k, v := range c.Env {
			envs = append(envs, fmt.Sprintf("%s=%s", k, v))
		}
		transports = append(transports, commandTransport(c.Command, c.Args, envs...))
	}

	return transports, nil
}
