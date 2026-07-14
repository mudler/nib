package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	scope  string // current cua-driver capture_scope ("window" | "desktop")
}

func newComputerServer(driver *mcp.ClientSession, cfg types.ComputerConfig) *computerServer {
	// Startup sets capture_scope=window (see StartComputerMCPServer), so mirror it.
	return &computerServer{driver: driver, cfg: cfg, sticky: StickyContext{SessionID: cfg.SessionID}, scope: "window"}
}

// setScope flips the driver's capture_scope, but only when it actually changes.
// window scope drives a specific window (get_window_state, window-local coords);
// desktop scope captures the whole display (get_desktop_state) and enables
// window-less screen-absolute clicks — the only path that works on native
// Wayland, where list_windows (X11 _NET_CLIENT_LIST) can't see Wayland toplevels.
func (c *computerServer) setScope(ctx context.Context, scope string) error {
	c.mu.Lock()
	cur := c.scope
	c.mu.Unlock()
	if cur == scope {
		return nil
	}
	if _, err := c.call(ctx, "set_config", map[string]any{"capture_scope": scope}); err != nil {
		return err
	}
	c.mu.Lock()
	c.scope = scope
	c.mu.Unlock()
	return nil
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

// listFrontmost enumerates windows and returns the frontmost addressable
// pid/window_id, or (0, 0) when there is none. list_windows over the MCP stdio
// bridge intermittently comes back empty on a busy session (hermes hits the same
// flakiness and re-fetches), and some window managers under-report with
// on_screen_only, so we retry and relax the filter. A (0, 0) result is not an
// error — it means "no addressable window", and the caller captures the whole
// screen in desktop scope instead (the native-Wayland path). list_windows' schema
// takes no session param.
func (c *computerServer) listFrontmost(ctx context.Context) (int, int) {
	for _, onScreen := range []bool{true, false} {
		for attempt := 0; attempt < 2; attempt++ {
			res, err := c.call(ctx, "list_windows", map[string]any{"on_screen_only": onScreen})
			if err != nil {
				// list_windows is X11-only (_NET_CLIENT_LIST); on a Wayland session
				// (DISPLAY dropped) it can't connect and errors. That is NOT fatal —
				// it just means "no addressable window", so the caller captures the
				// whole screen in desktop scope. A truly dead driver resurfaces on
				// the get_desktop_state call.
				xlog.Debug("cua-driver list_windows failed; falling back to desktop-scope capture", "err", err)
				continue
			}
			if res != nil && res.IsError {
				continue
			}
			if pid, windowID := frontmostWindow(structuredMap(res)); pid != 0 {
				return pid, windowID
			}
		}
	}
	return 0, 0 // no addressable window — caller falls back to a desktop-scope capture
}

// captureDesktop grabs the whole display in desktop scope. This is the fallback
// when list_windows finds no addressable top-level window — the normal case on
// native Wayland, where list_windows (X11 _NET_CLIENT_LIST) can't enumerate
// Wayland toplevels and only the driver's own overlay appears. get_desktop_state
// captures via the portal/grim/native screenshot path (no window, no AT-SPI, no
// L8 resize), and the model acts by screen-absolute pixel (x,y).
func (c *computerServer) captureDesktop(ctx context.Context, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	if err := c.setScope(ctx, "desktop"); err != nil {
		return nil, ComputerUseOutput{}, fmt.Errorf("switch to desktop capture scope: %w", err)
	}
	c.mu.Lock()
	c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop = 0, 0, true
	c.mu.Unlock()
	st, err := c.call(ctx, "get_desktop_state", map[string]any{"session": c.cfg.SessionID})
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	if st != nil && st.IsError {
		return st, ComputerUseOutput{}, nil
	}
	mime := ""
	for _, ct := range st.Content {
		if img, ok := ct.(*mcp.ImageContent); ok {
			mime = img.MIMEType
		}
	}
	return st, ComputerUseOutput{Summary: "captured full screen (no addressable window — act by pixel: pass screen-absolute x,y off this screenshot)", ImageMIME: mime}, nil
}

func (c *computerServer) capture(ctx context.Context, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	pid, windowID := c.listFrontmost(ctx)
	if pid == 0 {
		return c.captureDesktop(ctx, in)
	}
	if e := c.setScope(ctx, "window"); e != nil {
		xlog.Warn("cua-driver set_config capture_scope=window failed", "err", e)
	}
	c.mu.Lock()
	c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop = pid, windowID, false
	c.mu.Unlock()

	// The standard cua-driver capture, matching the reference (hermes-agent):
	// one window-scoped get_window_state returns the window screenshot (with SOM
	// numbered overlays) AND the AT-SPI element tree. Everything is window-scoped
	// so element clicks resolve against this same frame — we never switch to the
	// desktop scope that get_desktop_state would require. Server-side resize is
	// disabled at startup (max_image_dimension=0), so this no longer hits the L8
	// resize failure that previously forced a fallback.
	args := map[string]any{"pid": pid, "window_id": windowID, "session": c.cfg.SessionID}
	if in.Mode == "ax" {
		args["include_screenshot"] = false // tree only — the cheap re-index path
	}
	if in.MaxElements > 0 {
		args["max_elements"] = in.MaxElements
	}
	st, err := c.call(ctx, "get_window_state", args)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	if st != nil && st.IsError {
		return st, ComputerUseOutput{}, nil // surface the driver's own error, honestly
	}

	imageMIME := func() string {
		for _, ct := range st.Content {
			if img, ok := ct.(*mcp.ImageContent); ok {
				return img.MIMEType
			}
		}
		return ""
	}

	// Graceful degradation: when no AT-SPI accessibility tree is available (common
	// on wlroots Wayland with no a11y bus, or any locked-down surface), the capture
	// still carries a screenshot but zero elements. Rather than fail, tell the
	// model to act by pixel (x,y) off the screenshot — the driver's "element px
	// action" — so som degrades to functional vision automatically.
	const noAXHint = " (no accessibility elements available — act by pixel: pass x,y off this screenshot instead of an element index)"

	switch in.Mode {
	case "ax":
		st.Content = filterOutImages(st.Content)
		els := parseElements(structuredMap(st), in.MaxElements)
		st.Content = withElementText(st.Content, els)
		summary := "captured window (ax)"
		if len(els) == 0 {
			summary = "captured window (ax)" + noAXHint
		}
		xlog.Debug("computer capture", "mode", "ax", "elements", len(els))
		return st, ComputerUseOutput{Summary: summary, Elements: els}, nil
	case "vision":
		// Still surface the clickable element list — the model chooses the mode,
		// and it must never be left with an image it can't act on (that just makes
		// it re-capture forever). The image is the grounding; the list is how it
		// clicks.
		els := parseElements(structuredMap(st), in.MaxElements)
		st.Content = withElementText(st.Content, els)
		xlog.Debug("computer capture", "mode", "vision", "elements", len(els))
		return st, ComputerUseOutput{Summary: "captured screen", ImageMIME: imageMIME(), Elements: els}, nil
	default: // som
		els := parseElements(structuredMap(st), in.MaxElements)
		// Surface the clickable elements as TEXT alongside the screenshot. The
		// driver returns them only as structured data, and the model's tool
		// message otherwise collapses to a bare "[image content …]" placeholder —
		// so without this the model sees a picture with no numbers to reference
		// and can never issue a click. This is the model-facing Set-of-Marks list.
		st.Content = withElementText(st.Content, els)
		summary := "captured window"
		if len(els) == 0 {
			summary = "captured screen" + noAXHint
		}
		xlog.Debug("computer capture", "mode", "som", "elements", len(els))
		return st, ComputerUseOutput{Summary: summary, ImageMIME: imageMIME(), Elements: els}, nil
	}
}

// withElementText prepends a numbered, model-readable list of the clickable
// elements to the tool result, so a model can pick an element index to act on
// (click/type/scroll with `element: N`). No-op when there are no elements.
func withElementText(cs []mcp.Content, els []ComputerElement) []mcp.Content {
	if len(els) == 0 {
		return cs
	}
	var b strings.Builder
	b.WriteString("Clickable elements (act with computer_use action=\"click\"/\"type\"/\"scroll\" and element=N):\n")
	for _, e := range els {
		label := e.Label
		if label == "" {
			label = "(no label)"
		}
		fmt.Fprintf(&b, "  %d: %s %q\n", e.Index, e.Role, label)
	}
	return append([]mcp.Content{&mcp.TextContent{Text: b.String()}}, cs...)
}

func firstText(cs []mcp.Content) string {
	for _, c := range cs {
		if t, ok := c.(*mcp.TextContent); ok && t.Text != "" {
			return t.Text
		}
	}
	return ""
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
		pid := toInt(wm["pid"])
		if pid <= 0 {
			continue // unowned windows report pid=None — e.g. cua-driver's own agent-cursor overlay
		}
		name := strings.ToLower(str(wm["app_name"]) + " " + str(wm["title"]))
		if strings.Contains(name, "agentcursoroverlay") || strings.Contains(name, "cua.") {
			continue // never target the driver's own overlay window
		}
		wins = append(wins, win{pid: pid, id: toInt(wm["window_id"]), z: toInt(wm["z_index"])})
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
		pid, windowID := c.listFrontmost(ctx) // simplistic v1: front window; app-name filtering is a follow-up
		c.mu.Lock()
		c.sticky.PID, c.sticky.WindowID = pid, windowID
		c.mu.Unlock()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "focused"}}}, ComputerUseOutput{Summary: "focused app"}, nil
	case "open_app":
		return c.openApp(ctx, in)
	}

	c.mu.Lock()
	sticky := c.sticky
	c.mu.Unlock()

	// Auto-capture first: a small model often issues a click/type before ever
	// calling capture(). Rather than dead-end on "no active window", capture the
	// screen now to establish window/desktop context. If the action didn't say
	// where to act, hand the screenshot back so the model can pick a target
	// instead of failing.
	if IsDestructiveComputerAction(in.Action) && sticky.PID == 0 && !sticky.Desktop {
		capRes, capOut, capErr := c.capture(ctx, ComputerUseInput{Action: "capture", Mode: in.Mode, MaxElements: in.MaxElements})
		if capErr != nil {
			return nil, ComputerUseOutput{}, capErr
		}
		c.mu.Lock()
		sticky = c.sticky
		c.mu.Unlock()
		if !actionHasTarget(in) {
			capOut.Summary = "No screenshot yet — captured the screen first. " + capOut.Summary +
				" Now call computer_use again to " + in.Action + " at a specific target (an element index, or x,y read off this screenshot)."
			return capRes, capOut, nil
		}
	}

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

// openApp launches an app by name (or bundle id) via the driver's launch_app —
// idempotent: it launches the app if it isn't running and no-ops (still
// returning the pid) if it already is — then brings it to the front and pins
// its pid as the sticky target so the next click/type lands on it. This is the
// reliable "open X" primitive; without it small models fumble "open Chrome" by
// clicking desktop elements that don't launch anything.
func (c *computerServer) openApp(ctx context.Context, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	app := strings.TrimSpace(in.App)
	if app == "" {
		return nil, ComputerUseOutput{}, fmt.Errorf(`open_app needs an app in "app", e.g. app="Google Chrome"`)
	}
	args := map[string]any{}
	if c.sticky.SessionID != "" {
		args["session"] = c.sticky.SessionID
	}
	// launch_app takes bundle_id OR name; route by shape (com.google.Chrome vs
	// "Google Chrome"). A wrong guess just surfaces the driver's error honestly.
	if looksLikeBundleID(app) {
		args["bundle_id"] = app
	} else {
		args["name"] = app
	}
	res, err := c.call(ctx, "launch_app", args)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	if res.IsError {
		return res, ComputerUseOutput{}, nil // surface the driver's own error, honestly
	}
	m := structuredMap(res)
	pid := toInt(m["pid"])
	windowID := 0
	if ws, ok := m["windows"].([]any); ok && len(ws) > 0 {
		if w0, ok := ws[0].(map[string]any); ok {
			windowID = toInt(w0["window_id"])
		}
	}
	summary := fmt.Sprintf("launched %q", app)
	if pid != 0 {
		// Bring it forward so the user sees it and foreground input can land;
		// launch_app opens in the background. Best-effort — never fail the open on
		// a bring_to_front hiccup (e.g. a platform without it).
		bf := map[string]any{"pid": pid}
		if c.sticky.SessionID != "" {
			bf["session"] = c.sticky.SessionID
		}
		_, _ = c.call(ctx, "bring_to_front", bf)
		c.mu.Lock()
		c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop = pid, windowID, false
		c.mu.Unlock()
		summary += fmt.Sprintf(" (pid %d), now frontmost. Call computer_use action=capture to see it, then click/type.", pid)
	} else {
		summary += ". Call computer_use action=capture to see it."
	}
	return res, ComputerUseOutput{Summary: summary}, nil
}

// looksLikeBundleID reports whether s is a reverse-DNS bundle identifier
// (com.google.Chrome) rather than a display name ("Google Chrome"): dotted with
// at least two separators and no spaces or slashes.
func looksLikeBundleID(s string) bool {
	return !strings.ContainsAny(s, " /") && strings.Count(s, ".") >= 2
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

	// Cap the screenshot's longest side. A full-resolution (retina) capture is
	// huge as vision tokens — a native screen can be ~25k tokens and blow a small
	// model's context window ("request exceeds the available context size"). 1568
	// keeps it to ~2k tokens while staying legible for SOM. EXCEPT on Linux: the
	// driver's resize path fails with "unsupported color type for resize: L8" on
	// the grayscale frames some Wayland captures produce, so we keep 0 (no resize,
	// native-size PNG) there; macOS/Windows screenshots are RGB, so the resize is
	// safe. capture_scope stays window so get_window_state is the capture path.
	maxImageDim := 1568
	if runtime.GOOS == "linux" {
		maxImageDim = 0
	}
	if _, e := driverSess.CallTool(ctx, &mcp.CallToolParams{Name: "set_config", Arguments: map[string]any{
		"capture_scope": "window", "max_image_dimension": maxImageDim,
	}}); e != nil {
		xlog.Warn("cua-driver set_config (screenshot dimension cap) failed; captures may overflow the model context", "err", e)
	}

	// Probe the driver's capabilities once and log them. This tells us — and the
	// user's exported logs — exactly what the display server supports on this
	// machine (ax_capability, screen_capture_capability, wayland_backend, input),
	// which is the difference between "full control", "read-only" (GNOME/KDE
	// Wayland input is gated off — cua #1982), and "unsupported". Best-effort.
	if hr, e := driverSess.CallTool(ctx, &mcp.CallToolParams{Name: "health_report"}); e != nil {
		xlog.Warn("cua-driver health_report failed", "err", e)
	} else if hr != nil {
		xlog.Info("cua-driver capabilities", "health", structuredMap(hr), "text", firstText(hr.Content))
	}

	cs := newComputerServer(driverSess, cfg.Computer)
	server := mcp.NewServer(&mcp.Implementation{Name: "computer", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "computer_use",
		Description: "See and control the screen: take a SCREENSHOT of the desktop, then click, type, " +
			"scroll, and drag. Use this whenever the user wants to look at, see, screenshot, read, or " +
			"interact with the screen, a window, or any running app — do NOT use shell commands (e.g. " +
			"`screenshot`, `scrot`, `screencapture`) for this. To OPEN or launch an app, call " +
			"action='open_app' with app='<App Name>' (e.g. app='Google Chrome') — do NOT try to click it " +
			"open. To see the screen, call action='capture' (mode='som' numbers the clickable elements so " +
			"you can click by `element` index). Runs in the background without moving the user's real " +
			"cursor. Requires an armed session.",
	}, cs.computerUse)
	xlog.Info("computer_use MCP server ready", "driver", cmdPath)
	return server.Run(ctx, transport)
}

// scrubbedDriverEnv disables cua-driver telemetry and drops provider API keys so
// the third-party binary never inherits them.
func scrubbedDriverEnv(extra map[string]string) []string {
	drop := map[string]bool{"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true, "LOCALAI_API_KEY": true}
	// On a Wayland session, force the driver down its native Wayland path. With
	// DISPLAY set, cua-driver prefers X11/XWayland for BOTH input and capture —
	// but XWayland only exposes the driver's own overlay window, and its
	// root-window screenshot fails ("X11 error ... GetImage") on many
	// compositors. Dropping DISPLAY/XAUTHORITY makes it capture via
	// zwlr_screencopy / xdg-desktop-portal and inject via zwlr_virtual_pointer,
	// which is what actually works on wlroots. A pure-X11 session (no
	// WAYLAND_DISPLAY) keeps DISPLAY and full X11 support. Override via
	// cfg.Computer.Env (appended last) if you must pin a transport.
	wayland := os.Getenv("WAYLAND_DISPLAY") != ""
	if wayland {
		drop["DISPLAY"] = true
		drop["XAUTHORITY"] = true
		xlog.Info("cua-driver: Wayland session detected — using native Wayland capture/input (DISPLAY dropped so it won't fall back to the broken XWayland X11 path)")
	}
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
	// Opt into cua-driver's native Wayland backend. Off by default, it runs
	// X11-only, so on a Wayland session list_windows (which reads the X11
	// _NET_CLIENT_LIST) sees nothing and every capture fails with "no on-screen
	// window". Enabled, wlroots compositors (sway/labwc/hyprland) work; on
	// GNOME/KDE Wayland the driver surfaces its own actionable error (this build
	// lacks libei/portal input — cua issue #1982) instead of a silent empty list.
	// Harmless on X11. Callers can still override via cfg.Computer.Env below.
	env = append(env, "CUA_DRIVER_RS_ENABLE_WAYLAND=1")
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
