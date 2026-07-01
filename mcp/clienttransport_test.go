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

func TestTransportForServerSetsHTTPClientWhenAuthed(t *testing.T) {
	got := TransportForServer(types.MCPServer{URL: "http://x", BearerToken: "tok"})
	st, ok := got.(*sdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("got %T, want *StreamableClientTransport", got)
	}
	if st.HTTPClient == nil {
		t.Fatalf("expected non-nil HTTPClient when BearerToken is set")
	}
}

func TestTransportForServerSSESetsHTTPClientWhenAuthed(t *testing.T) {
	got := TransportForServer(types.MCPServer{URL: "http://x", Transport: "sse", Headers: map[string]string{"X-Api-Key": "k"}})
	st, ok := got.(*sdk.SSEClientTransport)
	if !ok {
		t.Fatalf("got %T, want *SSEClientTransport", got)
	}
	if st.HTTPClient == nil {
		t.Fatalf("expected non-nil HTTPClient when Headers is set")
	}
}

func TestTransportForServerNoHTTPClientWhenUnauthed(t *testing.T) {
	got := TransportForServer(types.MCPServer{URL: "http://x"})
	st, ok := got.(*sdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("got %T, want *StreamableClientTransport", got)
	}
	if st.HTTPClient != nil {
		t.Fatalf("expected nil HTTPClient for unauthenticated server, got %+v", st.HTTPClient)
	}
}
