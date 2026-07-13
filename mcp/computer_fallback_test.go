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
		return &mcp.CallToolResult{}, map[string]any{"windows": []any{map[string]any{"pid": 42, "window_id": 7, "z_index": 0}}}, nil
	})
	// get_window_state fails with the L8 resize error WHEN it grabs a screenshot,
	// but the screenshot-less (include_screenshot=false) call succeeds with the tree.
	mcp.AddTool(srv, &mcp.Tool{Name: "get_window_state"}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		if v, ok := in["include_screenshot"].(bool); ok && !v {
			return &mcp.CallToolResult{}, map[string]any{"elements": []any{map[string]any{"element_index": 1, "role": "AXButton", "label": "OK"}}}, nil
		}
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
	res, out, err := cs.computerUse(ctx, nil, ComputerUseInput{Action: "capture", Mode: "som"})
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
	if len(out.Elements) != 1 || out.Elements[0].Label != "OK" {
		t.Fatalf("hybrid fallback must keep clickable elements via include_screenshot=false, got %+v", out.Elements)
	}
}
