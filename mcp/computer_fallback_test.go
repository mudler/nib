package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

// When get_window_state fails (e.g. the cua-driver "unsupported color type for
// resize: L8" error), capture must fall back to get_desktop_state so the model
// still receives a screenshot.
func startL8FakeDriver(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	srvT, cliT := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-l8", Version: "v0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_windows"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"windows": []any{map[string]any{"pid": 42, "window_id": 7, "z_index": 0}}}}, nil, nil
	})
	// get_window_state returns a tool error (the L8 resize failure).
	mcp.AddTool(srv, &mcp.Tool{Name: "get_window_state"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Capture error: unsupported color type for resize: L8"}}}, nil, nil
	})
	// get_desktop_state returns a full-screen screenshot (the robust fallback).
	mcp.AddTool(srv, &mcp.Tool{Name: "get_desktop_state"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("\x89PNGdesktop")}}}, nil, nil
	})
	go func() { _ = srv.Run(ctx, srvT) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return sess
}

func TestCaptureFallsBackToDesktopOnWindowError(t *testing.T) {
	ctx := context.Background()
	cs := newComputerServer(startL8FakeDriver(t, ctx), types.ComputerConfig{SessionID: "x"})
	res, _, err := cs.computerUse(ctx, nil, ComputerUseInput{Action: "capture", Mode: "som"})
	if err != nil {
		t.Fatalf("capture should not surface the window error; expected desktop fallback: %v", err)
	}
	if res.IsError {
		t.Fatal("capture result must not be an error after fallback")
	}
	var gotImage bool
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			gotImage = true
		}
	}
	if !gotImage {
		t.Fatal("fallback must return a desktop screenshot image")
	}
}
