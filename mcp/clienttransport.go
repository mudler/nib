package mcp

import (
	"os"
	"os/exec"

	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TransportForServer returns the client transport for a configured MCP server.
// A server with URL set uses a remote HTTP/SSE transport (Streamable HTTP by
// default, SSE when Transport == "sse"); otherwise it launches Command over
// stdio, inheriting the process environment plus the server's Env.
func TransportForServer(srv types.MCPServer) mcp.Transport {
	if srv.URL != "" {
		client := authenticatedHTTPClient(srv)
		if srv.Transport == "sse" {
			return &mcp.SSEClientTransport{Endpoint: srv.URL, HTTPClient: client}
		}
		return &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: client}
	}
	command := exec.Command(srv.Command, srv.Args...)
	command.Env = os.Environ()
	for k, v := range srv.Env {
		command.Env = append(command.Env, k+"="+v)
	}
	return &mcp.CommandTransport{Command: command}
}
