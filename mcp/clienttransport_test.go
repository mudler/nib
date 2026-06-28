package mcp

import (
	"testing"

	"github.com/mudler/nib/types"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTransportForServer(t *testing.T) {
	if got := TransportForServer(types.MCPServer{Command: "echo", Args: []string{"hi"}}); func() bool { _, ok := got.(*sdk.CommandTransport); return !ok }() {
		t.Fatalf("stdio: got %T, want *CommandTransport", got)
	}
	if got := TransportForServer(types.MCPServer{URL: "http://x"}); func() bool { _, ok := got.(*sdk.StreamableClientTransport); return !ok }() {
		t.Fatalf("http: got %T, want *StreamableClientTransport", got)
	}
	if got := TransportForServer(types.MCPServer{URL: "http://x", Transport: "sse"}); func() bool { _, ok := got.(*sdk.SSEClientTransport); return !ok }() {
		t.Fatalf("sse: got %T, want *SSEClientTransport", got)
	}
}
