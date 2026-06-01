// Command echo-mcp is a minimal standalone MCP server exposing a single `echo`
// tool. It demonstrates how a wiz plugin ships an MCP server: a normal program
// that speaks MCP over stdio. wiz spawns it (per the plugin's mcp_servers entry)
// and connects as a client.
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

type echoOutput struct {
	Echoed string `json:"echoed" jsonschema:"the echoed text"`
}

func echo(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
	return nil, echoOutput{Echoed: in.Text}, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echo back the provided text (demo MCP tool).",
	}, echo)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("echo-mcp: %v", err)
	}
}
