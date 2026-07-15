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
	"time"

	"github.com/google/jsonschema-go/jsonschema"
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

	// lastElements is the element list (index→role/label) from the most recent
	// capture, so type/click can validate a target's role without a fresh walk.
	lastElements []ComputerElement
	// focusedInput is true when the last action put keyboard focus on a text
	// input (a click on a text field). It gates a bare type (no element) so the
	// model can't type into "whatever's focused" (usually the address bar).
	focusedInput bool
	// dirty marks that the last action likely changed the window, so the next
	// capture should wait for the AX tree to settle before returning.
	dirty bool
}

// inputRoles are the AX roles that accept typed text.
var inputRoles = map[string]bool{
	"AXTextField": true, "AXTextArea": true, "AXSearchField": true,
	"AXComboBox": true, "AXSecureTextField": true,
}

func isInputRole(role string) bool { return inputRoles[role] }

// roleOf returns the role of the element with the given index in els, or "".
func roleOf(els []ComputerElement, idx int) string {
	for _, e := range els {
		if e.Index == idx {
			return e.Role
		}
	}
	return ""
}

// inputFieldsHint lists the text-input elements in els for a self-correcting
// error message ("click one of these, or pass its element").
func inputFieldsHint(els []ComputerElement) string {
	var parts []string
	for _, e := range els {
		if isInputRole(e.Role) {
			label := e.Label
			if label == "" {
				label = "(no label)"
			}
			parts = append(parts, fmt.Sprintf("[%d] %s %q", e.Index, e.Role, label))
		}
	}
	if len(parts) == 0 {
		return "none in the last capture — capture again after the page/app loads"
	}
	return strings.Join(parts, ", ")
}

const (
	settleMaxProbes = 4                     // ~1.2s ceiling
	settleInterval  = 350 * time.Millisecond
)

// settleTree polls the window's AX tree cheaply (tree-only, no screenshot) until
// it stops growing or a timeout, so a subsequent capture sees the fully-built
// tree. App-agnostic: keyed on element-count growth, not app type.
func (c *computerServer) settleTree(ctx context.Context, pid, windowID, maxEls int) {
	prev := -1
	for probe := 0; probe < settleMaxProbes; probe++ {
		ps, err := c.call(ctx, "get_window_state", map[string]any{
			"pid": pid, "window_id": windowID, "session": c.cfg.SessionID,
			"max_elements": maxEls, "include_screenshot": false,
		})
		if err != nil || (ps != nil && ps.IsError) {
			return // let the real capture surface any error
		}
		n := len(parseElements(structuredMap(ps), maxEls))
		if n > 0 && n == prev {
			return // two identical walks → settled
		}
		prev = n
		if probe < settleMaxProbes-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(settleInterval):
			}
		}
	}
}

// cacheElements records the last capture's elements for later role lookups
// (type/click target validation).
func (c *computerServer) cacheElements(els []ComputerElement) {
	c.mu.Lock()
	c.lastElements = els
	c.mu.Unlock()
}

// isAppSwitchCombo reports whether a key combo is an app switcher (cmd+tab /
// alt+tab, ± shift). The driver posts keystrokes to a single pid, which does NOT
// drive the system app switcher — so these are no-ops that waste turns.
func isAppSwitchCombo(keys string) bool {
	var mods, key string
	first := true
	for _, p := range normalizeKeys(keys) {
		lp := strings.ToLower(p)
		if lp == "shift" {
			continue
		}
		if modifierKeys[lp] {
			mods += lp
		} else if first {
			key = lp
			first = false
		}
	}
	if key != "tab" {
		return false
	}
	return strings.Contains(mods, "cmd") || strings.Contains(mods, "alt") ||
		strings.Contains(mods, "option") || strings.Contains(mods, "win") || strings.Contains(mods, "super")
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
	c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop, c.sticky.Explicit = 0, 0, true, false
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
	pid, windowID, explicit := c.resolveCaptureTarget(ctx, in)
	if pid == 0 {
		return c.captureDesktop(ctx, in)
	}
	if e := c.setScope(ctx, "window"); e != nil {
		xlog.Warn("cua-driver set_config capture_scope=window failed", "err", e)
	}
	c.mu.Lock()
	c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop, c.sticky.Explicit = pid, windowID, false, explicit
	c.mu.Unlock()

	// The standard cua-driver capture, matching the reference (hermes-agent):
	// one window-scoped get_window_state returns the window screenshot (with SOM
	// numbered overlays) AND the AT-SPI element tree. Everything is window-scoped
	// so element clicks resolve against this same frame — we never switch to the
	// desktop scope that get_desktop_state would require. Server-side resize is
	// disabled at startup (max_image_dimension=0), so this no longer hits the L8
	// resize failure that previously forced a fallback.
	// Always bound the element count. An app whose window content isn't
	// AX-exposed (Chrome, Electron) can return its entire menu bar with every
	// menu expanded — hundreds of AXMenuItem nodes — which, unbounded, blows the
	// model's context in a couple of captures.
	maxEls := in.MaxElements
	if maxEls <= 0 {
		maxEls = defaultMaxElements
	}
	args := map[string]any{"pid": pid, "window_id": windowID, "session": c.cfg.SessionID, "max_elements": maxEls}
	if in.Mode == "ax" {
		args["include_screenshot"] = false // tree only — the cheap re-index path
	}
	// Settle the AX tree before the real capture. Many UIs build their
	// accessibility tree LAZILY after a focus/navigation/action — a browser's web
	// content, an Electron renderer, a slow native view — so the first capture can
	// miss the real content and the model acts on a stale tree. App-agnostic: poll
	// the tree cheaply (no screenshot) and stop as soon as it stops growing. Only
	// runs when `dirty` (the last action likely changed the window), so plain
	// re-index captures stay instant.
	c.mu.Lock()
	dirty := c.dirty
	c.dirty = false
	c.mu.Unlock()
	if dirty {
		c.settleTree(ctx, pid, windowID, maxEls)
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
	// menuOnlyHint fires when a window exposes only its menu bar and no real
	// content — the signature of a browser (Chrome) or Electron app that gates
	// accessibility. Element clicks are useless there; steer to pixels / URLs.
	const menuOnlyHint = " (this window exposes only its menu bar — its content is not accessible, typical of a browser like Chrome, whose web page can't be driven here. Act by pixel: pass x,y off this screenshot; for web tasks use the web tools instead.)"

	switch in.Mode {
	case "ax":
		els := parseElements(structuredMap(st), maxEls)
		c.cacheElements(els)
		// Drop the driver's verbose Markdown tree; nib's numbered list is the
		// model-facing surface. Keeping both duplicated hundreds of menu nodes and
		// overflowed context.
		st.Content = withElementText(nil, els)
		summary := "captured window (ax)"
		if len(els) == 0 {
			summary += noAXHint
		} else if allMenuElements(els) {
			summary += menuOnlyHint
		}
		xlog.Debug("computer capture", "mode", "ax", "elements", len(els))
		return st, ComputerUseOutput{Summary: summary, Elements: els}, nil
	case "vision":
		// Still surface the clickable element list — the model chooses the mode,
		// and it must never be left with an image it can't act on (that just makes
		// it re-capture forever). The image is the grounding; the list is how it
		// clicks. Keep only the image; drop the driver's Markdown tree (context).
		els := parseElements(structuredMap(st), maxEls)
		c.cacheElements(els)
		st.Content = withElementText(imagesOnly(st.Content), els)
		summary := "captured screen"
		if allMenuElements(els) {
			summary += menuOnlyHint
		}
		xlog.Debug("computer capture", "mode", "vision", "elements", len(els))
		return st, ComputerUseOutput{Summary: summary, ImageMIME: imageMIME(), Elements: els}, nil
	default: // som
		els := parseElements(structuredMap(st), maxEls)
		c.cacheElements(els)
		// Surface the clickable elements as TEXT alongside the screenshot. The
		// driver returns them only as structured data, and the model's tool
		// message otherwise collapses to a bare "[image content …]" placeholder —
		// so without this the model sees a picture with no numbers to reference
		// and can never issue a click. This is the model-facing Set-of-Marks list.
		// Keep only the image; drop the driver's Markdown tree (huge on menu-only
		// windows) so it doesn't overflow the model's context.
		st.Content = withElementText(imagesOnly(st.Content), els)
		summary := "captured window"
		if len(els) == 0 {
			summary = "captured screen" + noAXHint
		} else if allMenuElements(els) {
			summary += menuOnlyHint
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

// defaultMaxElements bounds how many AX elements a capture surfaces. Menu-only
// windows (Chrome/Electron) can otherwise return hundreds of nodes and overflow
// the model's context in a couple of captures.
const defaultMaxElements = 80

// imagesOnly keeps only the image content, dropping the driver's verbose text
// (its Markdown AX-tree rendering) — nib's own numbered element list replaces it.
func imagesOnly(cs []mcp.Content) []mcp.Content {
	var out []mcp.Content
	for _, c := range cs {
		if _, ok := c.(*mcp.ImageContent); ok {
			out = append(out, c)
		}
	}
	return out
}

// allMenuElements reports whether every element is a menu-bar node (AXMenuBar /
// AXMenu / AXMenuItem) — i.e. the window exposed no real content, the tell-tale
// of a browser/Electron app that gates accessibility.
func allMenuElements(els []ComputerElement) bool {
	if len(els) == 0 {
		return false
	}
	for _, e := range els {
		if !strings.HasPrefix(strings.ToLower(e.Role), "axmenu") {
			return false
		}
	}
	return true
}

func frontmostWindow(m map[string]any) (int, int) { return windowMatching(m, "") }

// windowMatching returns the frontmost (lowest z_index) addressable window whose
// app_name/title contains match (case-insensitive). match=="" accepts any window
// — that is the plain "frontmost" case. The driver's own agent-cursor overlay and
// pid<=0 (unowned) windows are always skipped.
func windowMatching(m map[string]any, match string) (int, int) {
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
		if match != "" && !strings.Contains(name, match) {
			continue // caller asked for a specific app and this window isn't it
		}
		wins = append(wins, win{pid: pid, id: toInt(wm["window_id"]), z: toInt(wm["z_index"])})
	}
	if len(wins) == 0 {
		return 0, 0
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].z < wins[j].z })
	return wins[0].pid, wins[0].id
}

// windowForApp finds the frontmost on-screen window belonging to app (matched by
// name, case-insensitive substring), or (0,0) if none is open. Same retry/relax
// dance as listFrontmost since list_windows is flaky over the stdio bridge.
func (c *computerServer) windowForApp(ctx context.Context, app string) (int, int) {
	match := strings.ToLower(strings.TrimSpace(app))
	for _, onScreen := range []bool{true, false} {
		for attempt := 0; attempt < 2; attempt++ {
			res, err := c.call(ctx, "list_windows", map[string]any{"on_screen_only": onScreen})
			if err != nil || (res != nil && res.IsError) {
				continue
			}
			if pid, windowID := windowMatching(structuredMap(res), match); pid != 0 {
				return pid, windowID
			}
		}
	}
	return 0, 0
}

// resolveCaptureTarget decides which window capture() targets, in order:
//  1. in.App names an app -> that app's window (explicit). app="screen"/"desktop"
//     -> (0,0) so the caller does a whole-screen desktop capture.
//  2. a still-pinned explicit target (open_app / focus_app / capture(app=)) ->
//     reuse it, so a plain capture right after open_app sees the app just opened
//     rather than whatever stole the front (e.g. a mounted DMG's Finder window).
//  3. otherwise the frontmost window.
func (c *computerServer) resolveCaptureTarget(ctx context.Context, in ComputerUseInput) (pid, windowID int, explicit bool) {
	if app := strings.TrimSpace(in.App); app != "" {
		if isDesktopApp(app) {
			return 0, 0, false // -> captureDesktop
		}
		if p, w := c.windowForApp(ctx, app); p != 0 {
			return p, w, true
		}
		// Named app not found among on-screen windows: fall through to frontmost so
		// the model still gets a screenshot (and can open_app / retry) rather than a
		// dead-end.
	}
	c.mu.Lock()
	spid, swid, sexp := c.sticky.PID, c.sticky.WindowID, c.sticky.Explicit
	c.mu.Unlock()
	if sexp && spid != 0 {
		return spid, swid, true
	}
	p, w := c.listFrontmost(ctx)
	return p, w, false
}

func isDesktopApp(app string) bool {
	a := strings.ToLower(strings.TrimSpace(app))
	return a == "screen" || a == "desktop"
}

// actionAliases maps the raw cua-driver tool names a model reaches for to the
// real computer_use action, so a near-miss doesn't dead-end.
var actionAliases = map[string]string{
	"type_text":  "type",
	"press_key":  "key",
	"launch_app": "open_app",
	"kill_app":   "close_app",
	"screenshot": "capture",
	"launch":     "open_app",
	"open":       "open_app",
}

func normalizeAction(a string) string {
	if alias, ok := actionAliases[strings.ToLower(strings.TrimSpace(a))]; ok {
		return alias
	}
	return a
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
	in.Action = normalizeAction(in.Action)
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
		// Select the named app's on-screen window (empty app => frontmost), bring it
		// to the FRONT (so clicks/keystrokes actually land and the user can see the
		// agent work — a background app receives neither), and pin it as the sticky
		// target so the next capture/click stays on it.
		pid, windowID := c.windowForApp(ctx, in.App)
		if pid == 0 {
			if strings.TrimSpace(in.App) == "" {
				return nil, ComputerUseOutput{}, fmt.Errorf("focus_app needs an app name, e.g. app=\"Google Chrome\"")
			}
			return nil, ComputerUseOutput{}, fmt.Errorf("no on-screen window found for app %q — open it first with action=open_app", in.App)
		}
		c.bringToFront(ctx, pid, windowID)
		c.mu.Lock()
		c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop, c.sticky.Explicit = pid, windowID, false, true
		c.focusedInput, c.dirty = false, true // window changed; next capture settles
		c.mu.Unlock()
		summary := fmt.Sprintf("focused %q (pid %d) and brought it to the front — call action=capture to see it", strings.TrimSpace(in.App), pid)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, ComputerUseOutput{Summary: summary}, nil
	case "open_app":
		return c.openApp(ctx, in)
	case "close_app":
		return c.closeApp(ctx, in)
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

	// Intercept the app-switch keystrokes the model reaches for: the driver posts
	// keys to a single pid, which does NOT drive the system app switcher, so
	// cmd+tab/alt+tab are silent no-ops that burn turns. Fail with the real path.
	if in.Action == "key" && isAppSwitchCombo(in.Keys) {
		return nil, ComputerUseOutput{}, fmt.Errorf(
			"cmd+tab/alt+tab don't switch apps here (a keystroke goes to one app, not the system switcher). " +
				"Use action=focus_app with app=\"<name>\" to switch to an app.")
	}
	// Field-aware type: don't let the model type into "whatever's focused" (usually
	// the browser address bar). Prefer type with an explicit element target; a bare
	// type is only allowed right after clicking a text field.
	if in.Action == "type" {
		if err := c.checkTypeTarget(in); err != nil {
			return nil, ComputerUseOutput{}, err
		}
	}

	tool, args, err := buildCuaCall(in, sticky)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	// Front the target window before a state-changing action so it actually holds
	// focus — a background app receives neither clicks nor keystrokes (the driver
	// reports them "unverified" and nothing lands). bring_to_front is persistent,
	// so the app stays up and visible for the whole task rather than flashing back.
	if IsDestructiveComputerAction(in.Action) && sticky.PID != 0 {
		c.bringToFront(ctx, sticky.PID, sticky.WindowID)
	}
	res, err := c.call(ctx, tool, args)
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	c.trackAfterAction(in)
	out := ComputerUseOutput{Summary: fmt.Sprintf("%s ok", in.Action)}
	if in.CaptureAfter {
		return c.capture(ctx, ComputerUseInput{Action: "capture", Mode: "som"})
	}
	return res, out, nil
}

// checkTypeTarget enforces that a type action has somewhere valid to land.
// With an explicit element it must be a text-input role; a bare type is only
// allowed immediately after a click on a text field (else the text would go to
// whatever happens to be focused — typically the address bar).
func (c *computerServer) checkTypeTarget(in ComputerUseInput) error {
	c.mu.Lock()
	els, focused := c.lastElements, c.focusedInput
	c.mu.Unlock()
	if in.Element != 0 {
		role := roleOf(els, in.Element)
		if role == "" {
			return fmt.Errorf("element %d isn't in the last capture — call action=capture first", in.Element)
		}
		if !isInputRole(role) {
			return fmt.Errorf("element %d is a %s, not a text field — type into a text field. Fields available: %s",
				in.Element, role, inputFieldsHint(els))
		}
		return nil
	}
	if focused {
		return nil // the model just clicked a text field
	}
	return fmt.Errorf("nothing is focused to type into (text would go to the address bar or nowhere). "+
		"Click a text field first, or pass type element=N. Text fields in the last capture: %s", inputFieldsHint(els))
}

// trackAfterAction updates the focus/dirty state after a successful action:
// dirty (the window likely changed → next capture settles) for any mutating
// action; focusedInput true only when a click landed on a text field.
func (c *computerServer) trackAfterAction(in ComputerUseInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if IsDestructiveComputerAction(in.Action) {
		c.dirty = true
	}
	switch in.Action {
	case "click", "double_click", "right_click", "middle_click":
		// A click on a text field focuses it; a click on anything else moves focus
		// off any field. (A bare-coordinate click has no known role → treat as off.)
		c.focusedInput = in.Element != 0 && isInputRole(roleOf(c.lastElements, in.Element))
	case "type":
		// typing keeps focus on the field — leave focusedInput as-is so consecutive
		// types work.
	}
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
	// If the app is already running (has an on-screen window), just focus it —
	// don't try to launch. launch_app can't "launch" always-running apps like
	// Finder ("No installed macOS app found for name 'Finder'"), and relaunching
	// an open app is pointless. "open" means "show it", whether launch or focus.
	if pid, windowID := c.windowForApp(ctx, app); pid != 0 {
		c.bringToFront(ctx, pid, windowID)
		c.mu.Lock()
		c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop, c.sticky.Explicit = pid, windowID, false, true
		c.focusedInput, c.dirty = false, true
		c.mu.Unlock()
		summary := fmt.Sprintf("%q is already open (pid %d) — brought it to the front. Call action=capture to see it.", app, pid)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, ComputerUseOutput{Summary: summary}, nil
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
		// Bring it forward and keep it there so the user sees it and input lands;
		// launch_app opens in the background.
		c.bringToFront(ctx, pid, windowID)
		c.mu.Lock()
		// Explicit: a plain capture next must target THIS app, not re-detect the
		// frontmost window (which may be a mounted DMG / Finder window).
		c.sticky.PID, c.sticky.WindowID, c.sticky.Desktop, c.sticky.Explicit = pid, windowID, false, true
		c.focusedInput, c.dirty = false, true // just launched → next capture settles
		c.mu.Unlock()
		summary += fmt.Sprintf(" (pid %d), now frontmost. Call computer_use action=capture to see it, then click/type.", pid)
	} else {
		summary += ". Call computer_use action=capture to see it."
	}
	// Replace the driver's own launch text (which tells the model to call
	// get_window_state — a tool the model does NOT have; it only has computer_use)
	// with our capture-next guidance.
	res.Content = []mcp.Content{&mcp.TextContent{Text: summary}}
	return res, ComputerUseOutput{Summary: summary}, nil
}

// looksLikeBundleID reports whether s is a reverse-DNS bundle identifier
// (com.google.Chrome) rather than a display name ("Google Chrome"): dotted with
// at least two separators and no spaces or slashes.
func looksLikeBundleID(s string) bool {
	return !strings.ContainsAny(s, " /") && strings.Count(s, ".") >= 2
}

// closeApp quits an app by resolving its on-screen window to a pid and calling
// the driver's kill_app. The natural complement to open_app.
func (c *computerServer) closeApp(ctx context.Context, in ComputerUseInput) (*mcp.CallToolResult, ComputerUseOutput, error) {
	app := strings.TrimSpace(in.App)
	if app == "" {
		return nil, ComputerUseOutput{}, fmt.Errorf(`close_app needs an app in "app", e.g. app="Google Chrome"`)
	}
	pid, _ := c.windowForApp(ctx, app)
	if pid == 0 {
		return nil, ComputerUseOutput{}, fmt.Errorf("no running app found for %q to close", app)
	}
	res, err := c.call(ctx, "kill_app", c.withSession(map[string]any{"pid": pid}))
	if err != nil {
		return nil, ComputerUseOutput{}, err
	}
	if res.IsError {
		return res, ComputerUseOutput{}, nil
	}
	// If we just closed the app we were targeting, drop the sticky pin.
	c.mu.Lock()
	if c.sticky.PID == pid {
		c.sticky.PID, c.sticky.WindowID, c.sticky.Explicit = 0, 0, false
	}
	c.mu.Unlock()
	summary := fmt.Sprintf("closed %q (pid %d)", app, pid)
	res.Content = []mcp.Content{&mcp.TextContent{Text: summary}}
	return res, ComputerUseOutput{Summary: summary}, nil
}

// withSession adds the session id to a driver-call arg map when set.
func (c *computerServer) withSession(m map[string]any) map[string]any {
	if c.sticky.SessionID != "" {
		m["session"] = c.sticky.SessionID
	}
	return m
}

// bringToFront persistently activates the target window so it holds macOS
// foreground (and stays there) — the cheap path vs per-action foreground flashes.
// Best-effort: a hiccup here must never fail the caller's action.
func (c *computerServer) bringToFront(ctx context.Context, pid, windowID int) {
	if pid == 0 {
		return
	}
	m := map[string]any{"pid": pid}
	if windowID != 0 {
		m["window_id"] = windowID
	}
	_, _ = c.call(ctx, "bring_to_front", c.withSession(m))
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
	tool := &mcp.Tool{
		Name: "computer_use",
		Description: "See and control a native desktop app: take a SCREENSHOT, then click, type, scroll, " +
			"and drag. Use this whenever the user wants to look at, see, screenshot, read, or interact with " +
			"the screen, a window, or a running app — do NOT use shell commands (e.g. `screenshot`, `scrot`, " +
			"`screencapture`) for this. To work on a SPECIFIC app (especially one the user names), FIRST call " +
			"action='focus_app' app='<Name>' (or action='open_app' to launch it) — do NOT rely on whatever is " +
			"frontmost, and NEVER use cmd+tab/alt+tab to switch apps (they don't work here; use focus_app). " +
			"Then action='capture' (mode='som' numbers the clickable elements) and click by `element` index; " +
			"pixel x,y is the fallback. To TYPE into a field, target it: action='type' element=<N of the text " +
			"field> text='…' (or click the field first) — typing without a field goes to the wrong place. For a " +
			"keyboard SHORTCUT (cmd+a, ctrl+c) use action='key' keys='cmd+a' — never type the shortcut as text. " +
			"open_app also focuses an already-running app (use it for Finder etc). The target app is brought to " +
			"the front so actions land. This drives native app UI; it is NOT for browsing the web. " +
			"Requires an armed session.",
	}
	// Give the enum-valued fields REAL JSON Schema enums (not just a prose list in
	// the description). The go-sdk's `jsonschema:"…"` struct tag only sets a
	// description, so we infer the schema from the struct — which preserves those
	// descriptions — then stamp the enums on. cogito's MCP bridge carries the enum
	// through to the engine, which constrains the model to valid values (hard when
	// grammar-constrained decoding is on; a strong signal even when it isn't),
	// instead of the model inventing an action like "click_element".
	if schema, err := computerInputSchema(); err != nil {
		xlog.Warn("computer_use: could not build enum schema, falling back to inferred", "err", err)
	} else {
		tool.InputSchema = schema
	}
	mcp.AddTool(server, tool, cs.computerUse)
	xlog.Info("computer_use MCP server ready", "driver", cmdPath)
	return server.Run(ctx, transport)
}

// computerInputSchema builds the computer_use input schema from ComputerUseInput
// (keeping the per-field descriptions from the jsonschema tags) and stamps real
// enums onto the constrained fields.
func computerInputSchema() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[ComputerUseInput](nil)
	if err != nil {
		return nil, err
	}
	setEnum := func(prop string, vals ...string) {
		p := s.Properties[prop]
		if p == nil {
			return
		}
		p.Enum = make([]any, len(vals))
		for i, v := range vals {
			p.Enum[i] = v
		}
	}
	setEnum("action", "capture", "click", "double_click", "right_click", "middle_click",
		"drag", "scroll", "type", "key", "set_value", "wait", "list_apps", "open_app",
		"close_app", "focus_app")
	setEnum("mode", "som", "vision", "ax")
	setEnum("button", "left", "right", "middle")
	setEnum("direction", "up", "down", "left", "right")
	return s, nil
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
