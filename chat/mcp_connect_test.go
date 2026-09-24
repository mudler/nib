package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConnectMCPHandshakeTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	_, err := connectMCP(ctx, client, &mcp.StreamableClientTransport{Endpoint: server.URL}, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("handshake must time out before parent: err=%v parent=%v", err, ctx.Err())
	}
}

func TestConnectMCPKeepsSuccessfulConnectionAlive(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := connectMCP(ctx, client, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	time.Sleep(250 * time.Millisecond)
	callCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err := session.Ping(callCtx, nil); err != nil {
		t.Fatalf("connection expired after successful handshake: %v", err)
	}
}
