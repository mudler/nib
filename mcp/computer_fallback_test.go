package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

// The capture path is window-scoped only (matching hermes-agent): server-side
// screenshot resize is disabled at startup (max_image_dimension=0) so the
// "unsupported color type for resize: L8" failure no longer occurs, and there is
// NO get_desktop_state fallback — get_desktop_state requires the desktop capture
// scope, and switching scope mid-session would break window-relative element
// clicks. If get_window_state still returns an error, capture surfaces it
// honestly instead of silently changing scope.
func startWindowErrorFakeDriver(t *testing.T, ctx context.Context, desktopCalled *bool) *mcp.ClientSession {
	t.Helper()
	srvT, cliT := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-winerr", Version: "v0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_windows"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{}, map[string]any{"windows": []any{map[string]any{"pid": 42, "window_id": 7, "z_index": 0}}}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "get_window_state"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Capture error: unsupported color type for resize: L8"}}}, nil, nil
	})
	// get_desktop_state must never be reached; record it if it is.
	mcp.AddTool(srv, &mcp.Tool{Name: "get_desktop_state"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		*desktopCalled = true
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

// When the driver returns a screenshot but no AT-SPI elements (e.g. wlroots
// Wayland with no accessibility bus), som must not fail — it degrades to pixel
// action, guiding the model to click by (x,y) off the screenshot.
func startNoAXFakeDriver(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	srvT, cliT := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-noax", Version: "v0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "list_windows"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{}, map[string]any{"windows": []any{map[string]any{"pid": 42, "window_id": 7, "z_index": 0}}}, nil
	})
	// Screenshot present, but the element tree is empty (AT-SPI unavailable).
	mcp.AddTool(srv, &mcp.Tool{Name: "get_window_state"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("\x89PNGwindow")}}}, map[string]any{"elements": []any{}}, nil
	})
	go func() { _ = srv.Run(ctx, srvT) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return sess
}

func TestCaptureDegradesToPixelWhenNoAX(t *testing.T) {
	ctx := context.Background()
	cs := newComputerServer(startNoAXFakeDriver(t, ctx), types.ComputerConfig{SessionID: "x"})
	res, out, err := cs.computerUse(ctx, nil, ComputerUseInput{Action: "capture", Mode: "som"})
	if err != nil {
		t.Fatalf("som capture with no AX tree must not fail: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatal("capture must still return the screenshot when the AX tree is empty")
	}
	var gotImage bool
	for _, c := range res.Content {
		if _, ok := c.(*mcp.ImageContent); ok {
			gotImage = true
		}
	}
	if !gotImage {
		t.Fatal("degraded capture must still carry the screenshot")
	}
	if len(out.Elements) != 0 {
		t.Fatalf("expected no elements, got %d", len(out.Elements))
	}
	if !strings.Contains(out.Summary, "by pixel") {
		t.Fatalf("summary must guide the model to act by pixel; got %q", out.Summary)
	}
}

func TestCaptureSurfacesWindowErrorWithoutDesktopFallback(t *testing.T) {
	ctx := context.Background()
	var desktopCalled bool
	cs := newComputerServer(startWindowErrorFakeDriver(t, ctx, &desktopCalled), types.ComputerConfig{SessionID: "x"})
	res, _, err := cs.computerUse(ctx, nil, ComputerUseInput{Action: "capture", Mode: "som"})
	if err != nil {
		t.Fatalf("a driver tool-error must be surfaced as an error result, not a Go error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("capture must surface the driver's get_window_state error result")
	}
	if desktopCalled {
		t.Fatal("capture must stay window-scoped and never fall back to get_desktop_state")
	}
}
