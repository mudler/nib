package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// ComputerElement is one AX element surfaced from a capture.
type ComputerElement struct {
	Index int    `json:"element_index"`
	Role  string `json:"role"`
	Label string `json:"label"`
}

// ComputerUseOutput is the structured result returned to the model.
type ComputerUseOutput struct {
	Summary   string            `json:"summary"`
	Elements  []ComputerElement `json:"elements,omitempty"`
	ImageMIME string            `json:"image_mime_type,omitempty"`
}

type computerServer struct {
	driver *mcp.ClientSession
	cfg    types.ComputerConfig
	mu     sync.Mutex
	sticky StickyContext
}

func newComputerServer(driver *mcp.ClientSession, cfg types.ComputerConfig) *computerServer {
	return &computerServer{driver: driver, cfg: cfg, sticky: StickyContext{SessionID: cfg.SessionID}}
}

func (c *computerServer) call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	return c.driver.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
}

// structuredMap coerces res.StructuredContent to a map.
func structuredMap(res *mcp.CallToolResult) map[string]any {
	if res == nil {
		return nil
	}
	if m, ok := res.StructuredContent.(map[string]any); ok {
		return m
	}
	return nil
}

func (c *computerServer) capture(ctx context.Context, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	winRes, err := c.call(ctx, "list_windows", map[string]any{"on_screen_only": true, "session": c.cfg.SessionID})
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	pid, windowID := frontmostWindow(structuredMap(winRes))
	c.mu.Lock()
	c.sticky.PID, c.sticky.WindowID = pid, windowID
	c.mu.Unlock()

	// Fetch the window's AT-SPI element tree WITHOUT a screenshot
	// (include_screenshot=false). That skips get_window_state's screenshot resize
	// step — which fails on some displays with "unsupported color type for resize:
	// L8" — while still registering the element snapshot so the model can click by
	// `element` index. Best-effort; skipped for vision mode or when no window is
	// focused. The actual screenshot comes from get_desktop_state below.
	var elements []ComputerElement
	var treeRes *mcp.CallToolResult
	if in.Mode != "vision" && pid != 0 {
		if st, e := c.call(ctx, "get_window_state", map[string]any{
			"pid": pid, "window_id": windowID, "include_screenshot": false, "session": c.cfg.SessionID,
		}); e == nil && st != nil && !st.IsError {
			treeRes = st
			elements = parseElements(structuredMap(st), in.MaxElements)
		}
	}

	// ax mode wants the AT-SPI text tree only (no screenshot). Hand back the tree
	// result when we have it; otherwise fall through to a plain screenshot.
	if in.Mode == "ax" && treeRes != nil {
		return treeRes, ComputerUseOutput{Summary: "captured window (ax)", Elements: elements}, nil
	}

	// The screenshot always comes from get_desktop_state: full display at native
	// size with NO downscale/resize, so it never hits the L8 resize failure. It is
	// combined with the element tree parsed above so the model both SEES the screen
	// and can click elements by index.
	res, err := c.call(ctx, "get_desktop_state", map[string]any{"session": c.cfg.SessionID})
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	out := ComputerUseOutput{Summary: "captured screen", Elements: elements}
	for _, ct := range res.Content {
		if img, ok := ct.(*mcp.ImageContent); ok {
			out.ImageMIME = img.MIMEType
		}
	}
	return res, out, nil
}

func filterOutImages(cs []mcp.Content) []mcp.Content {
	out := cs[:0]
	for _, c := range cs {
		if _, ok := c.(*mcp.ImageContent); ok {
			continue
		}
		out = append(out, c)
	}
	return out
}

func frontmostWindow(m map[string]any) (int, int) {
	ws, _ := m["windows"].([]any)
	type win struct{ pid, id, z int }
	var wins []win
	for _, raw := range ws {
		wm, _ := raw.(map[string]any)
		wins = append(wins, win{pid: toInt(wm["pid"]), id: toInt(wm["window_id"]), z: toInt(wm["z_index"])})
	}
	if len(wins) == 0 {
		return 0, 0
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].z < wins[j].z })
	return wins[0].pid, wins[0].id
}

func parseElements(m map[string]any, max int) []ComputerElement {
	if max <= 0 {
		max = 100
	}
	raw, _ := m["elements"].([]any)
	var out []ComputerElement
	for _, r := range raw {
		em, _ := r.(map[string]any)
		out = append(out, ComputerElement{Index: toInt(em["element_index"]), Role: str(em["role"]), Label: str(em["label"])})
		if len(out) >= max {
			break
		}
	}
	return out
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// computerUse is the single MCP tool handler.
func (c *computerServer) computerUse(ctx context.Context, _ *mcp.CallToolRequest, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	if reason := BlockedComputerReason(in); reason != "" {
		return nil, ComputerUseOutput{}, fmt.Errorf("%s", reason)
	}
	switch in.Action {
	case "capture":
		return c.capture(ctx, in)
	case "wait":
		// pure local wait, clamped 0..30; no driver call.
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "waited"}}}, ComputerUseOutput{Summary: "waited"}, nil
	case "focus_app":
		winRes, err := c.call(ctx, "list_windows", map[string]any{"on_screen_only": true, "session": c.cfg.SessionID})
		if err != nil {
			return nil, ComputerUseOutput{}, err
		}
		pid, windowID := frontmostWindow(structuredMap(winRes)) // simplistic v1: front window; app-name filtering is a follow-up
		c.mu.Lock()
		c.sticky.PID, c.sticky.WindowID = pid, windowID
		c.mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "focused"}}}, ComputerUseOutput{Summary: "focused app"}, nil
	}

	c.mu.Lock()
	sticky := c.sticky
	c.mu.Unlock()
	tool, args, err := buildCuaCall(in, sticky)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	res, err := c.call(ctx, tool, args)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	out := ComputerUseOutput{Summary: fmt.Sprintf("%s ok", in.Action)}
	if in.CaptureAfter {
		return c.capture(ctx, ComputerUseInput{Action: "capture", Mode: "som"})
	}
	return res, out, nil
}

// StartComputerMCPServer spawns cua-driver as a stdio MCP child and serves the
// in-process computer_use tool that proxies to it. Blocks until ctx is done.
func StartComputerMCPServer(ctx context.Context, transport mcp.Transport, cfg types.Config) error {
	cmdPath := cfg.Computer.Command
	if cmdPath == "" {
		cmdPath = os.Getenv("NIB_CUA_DRIVER_CMD")
	}
	if cmdPath == "" {
		cmdPath = "cua-driver"
	}
	args := cfg.Computer.Args
	if len(args) == 0 {
		args = []string{"mcp"}
	}
	child := exec.Command(cmdPath, args...)
	child.Env = scrubbedDriverEnv(cfg.Computer.Env)
	driverClient := mcp.NewClient(&mcp.Implementation{Name: "nib-computer", Version: "v1.0.0"}, nil)
	driverSess, err := driverClient.Connect(ctx, &mcp.CommandTransport{Command: child}, nil)
	if err != nil {
		return fmt.Errorf("connect cua-driver (%s): %w", cmdPath, err)
	}
	defer driverSess.Close()

	cs := newComputerServer(driverSess, cfg.Computer)
	server := mcp.NewServer(&mcp.Implementation{Name: "computer", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "computer_use",
		Description: "See and control the screen: take a SCREENSHOT of the desktop, then click, type, " +
			"scroll, and drag. Use this whenever the user wants to look at, see, screenshot, read, or " +
			"interact with the screen, a window, or any running app — do NOT use shell commands (e.g. " +
			"`screenshot`, `scrot`, `screencapture`) for this. To see the screen, call action='capture' " +
			"(mode='som' numbers the clickable elements so you can click by `element` index). Runs in the " +
			"background without moving the user's real cursor. Requires an armed session.",
	}, cs.computerUse)
	xlog.Info("computer_use MCP server ready", "driver", cmdPath)
	return server.Run(ctx, transport)
}

// scrubbedDriverEnv disables cua-driver telemetry and drops provider API keys so
// the third-party binary never inherits them.
func scrubbedDriverEnv(extra map[string]string) []string {
	drop := map[string]bool{"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true, "LOCALAI_API_KEY": true}
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if drop[key] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CUA_DRIVER_RS_TELEMETRY_ENABLED=0")
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
