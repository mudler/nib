package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/nib/types"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestWebServerRegistersTools starts the web server over an in-memory transport
// and asserts both tools are advertised (exercises tool registration, not just
// the handlers).
func TestWebServerRegistersTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverT, clientT := mcp.NewInMemoryTransports()
	go func() {
		_ = StartWebMCPServer(ctx, serverT, types.Config{Model: "m", APIKey: "k", BaseURL: "http://localhost:1/v1"})
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range res.Tools {
		found[tool.Name] = true
	}
	for _, name := range []string{"web_fetch", "web_search"} {
		if !found[name] {
			t.Errorf("tool %q not registered", name)
		}
	}
}
