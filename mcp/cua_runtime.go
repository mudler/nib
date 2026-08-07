package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

type cuaCaller interface {
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

type cuaClient interface {
	cuaCaller
	InitializeResult() *mcp.InitializeResult
	ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	Close() error
}

type cuaRuntime struct {
	client     cuaClient
	sessionID  string
	connection *cuaOwnedTransport
	connCancel context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// cuaOwnedTransport retains the raw MCP connection created by a transport.
// ClientSession.Close waits for in-flight JSON-RPC calls, so shutdown paths
// must be able to break the underlying stream first when a peer ignores request
// cancellation. closeRequested handles cancellation racing with Connect.
type cuaOwnedTransport struct {
	transport mcp.Transport

	mu             sync.Mutex
	connection     mcp.Connection
	closeRequested bool
}

func (t *cuaOwnedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.transport.Connect(ctx)
	t.mu.Lock()
	t.connection = connection
	closeRequested := t.closeRequested
	t.mu.Unlock()
	if closeRequested && connection != nil {
		_ = connection.Close()
	}
	return connection, err
}

func (t *cuaOwnedTransport) Close() error {
	t.mu.Lock()
	t.closeRequested = true
	connection := t.connection
	t.mu.Unlock()
	if connection == nil {
		return nil
	}
	return connection.Close()
}

var cuaBrowserRequiredTools = []string{
	"start_session", "end_session", "health_report", "set_config",
	"list_apps", "list_windows", "launch_app", "kill_app", "press_key",
	"get_browser_state", "browser_prepare", "browser_navigate", "browser_click",
	"browser_type", "browser_pointer", "browser_dialog",
	"browser_set_input_files", "browser_download",
}

var minimumCUABrowserVersion = semver.MustParse("0.19.0")

func newCUARuntime(ctx context.Context, cfg types.Config, requireBrowser bool) (*cuaRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start cua-driver: %w", err)
	}

	resolved := resolveCUAConfig(cfg)
	child := exec.Command(resolved.Command, resolved.Args...)
	child.Env = scrubbedDriverEnv(resolved.Env)
	child.Stderr = os.Stderr

	connCtx, connCancel := context.WithCancel(context.Background())
	ownedTransport := &cuaOwnedTransport{transport: &mcp.CommandTransport{Command: child}}
	stopStartupCancel := context.AfterFunc(ctx, func() {
		connCancel()
		_ = ownedTransport.Close()
	})
	driverClient := mcp.NewClient(&mcp.Implementation{Name: "nib-cua", Version: "v1.0.0"}, nil)
	driverSession, err := driverClient.Connect(connCtx, ownedTransport, nil)
	startupStillActive := stopStartupCancel()
	if err != nil {
		connCancel()
		return nil, errors.Join(
			fmt.Errorf("connect cua-driver (%s): %w", resolved.Command, err),
			ownedTransport.Close(),
		)
	}
	if !startupStillActive && ctx.Err() != nil {
		connCancel()
		forceCloseErr := ownedTransport.Close()
		closeErr := driverSession.Close()
		return nil, errors.Join(
			fmt.Errorf("connect cua-driver (%s): %w", resolved.Command, ctx.Err()),
			forceCloseErr,
			closeErr,
		)
	}

	runtime, err := newCUARuntimeFromOwnedClient(ctx, driverSession, cfg, requireBrowser, ownedTransport, connCancel)
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func newCUARuntimeFromClient(ctx context.Context, client cuaClient, cfg types.Config, requireBrowser bool) (*cuaRuntime, error) {
	return newCUARuntimeFromOwnedClient(ctx, client, cfg, requireBrowser, nil, nil)
}

func newCUARuntimeFromOwnedClient(
	ctx context.Context,
	client cuaClient,
	cfg types.Config,
	requireBrowser bool,
	connection *cuaOwnedTransport,
	connCancel context.CancelFunc,
) (*cuaRuntime, error) {
	r := &cuaRuntime{client: client, connection: connection, connCancel: connCancel}
	resolved := resolveCUAConfig(cfg)
	sessionID := resolved.SessionID
	if sessionID == "" {
		var err error
		sessionID, err = mintCUASessionID()
		if err != nil {
			return nil, r.closeAfterStartupError(err, false)
		}
	}
	r.sessionID = sessionID
	if sessionID == "default" {
		return nil, r.closeAfterStartupError(errors.New("cua browser sessions require a non-default declared session ID"), false)
	}

	if requireBrowser {
		if err := validateCUABrowserClient(ctx, client); err != nil {
			return nil, r.closeAfterStartupError(err, false)
		}
	}

	startResult, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "start_session",
		Arguments: map[string]any{
			"session":       sessionID,
			"capture_scope": "window",
		},
	})
	if err != nil {
		return nil, r.closeAfterStartupError(fmt.Errorf("start cua-driver session %q: %w", sessionID, err), false)
	}
	if startResult == nil || startResult.IsError || cuaResultRefused(startResult) {
		return nil, r.closeAfterStartupError(cuaToolResultError("start_session", startResult), false)
	}

	if cfg.Computer.Enabled {
		// Retina screenshots can overflow a model context, so cap the longest
		// side where the driver's RGB resize path is safe. Linux remains uncapped
		// because some Wayland captures are grayscale frames the driver cannot resize.
		maxImageDim := 1568
		if runtime.GOOS == "linux" {
			maxImageDim = 0
		}
		if _, err := r.CallTool(ctx, &mcp.CallToolParams{Name: "set_config", Arguments: map[string]any{
			"capture_scope": "window", "max_image_dimension": maxImageDim,
		}}); err != nil {
			xlog.Warn("cua-driver set_config (screenshot dimension cap) failed; captures may overflow the model context", "err", err)
		}
	}

	if health, err := r.CallTool(ctx, &mcp.CallToolParams{Name: "health_report"}); err != nil {
		xlog.Warn("cua-driver health_report failed", "err", err)
	} else if health != nil {
		xlog.Info("cua-driver capabilities", "health", structuredMap(health), "text", firstText(health.Content))
	}
	return r, nil
}

func validateCUABrowserClient(ctx context.Context, client cuaClient) error {
	initialized := client.InitializeResult()
	if initialized == nil || initialized.ServerInfo == nil {
		return errors.New("validate cua-driver browser support: initialize response did not include server version")
	}
	versionText := initialized.ServerInfo.Version
	version, err := semver.NewVersion(versionText)
	if err != nil {
		return fmt.Errorf("validate cua-driver browser support: invalid driver version %q: %w", versionText, err)
	}
	if version.LessThan(minimumCUABrowserVersion) {
		return fmt.Errorf("cua browser backend requires cua-driver >= %s; connected version is %s", minimumCUABrowserVersion, versionText)
	}

	available := make(map[string]struct{})
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		if _, seen := seenCursors[cursor]; seen {
			return fmt.Errorf("list cua-driver browser tools: repeated pagination cursor %q", cursor)
		}
		seenCursors[cursor] = struct{}{}
		page, err := client.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("list cua-driver browser tools: %w", err)
		}
		if page == nil {
			return errors.New("list cua-driver browser tools: empty response")
		}
		for _, tool := range page.Tools {
			if tool != nil {
				available[tool.Name] = struct{}{}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	missing := make([]string, 0)
	for _, name := range cuaBrowserRequiredTools {
		if _, ok := available[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("cua-driver is missing required browser tools: %s; install cua-driver >= %s with browser support", strings.Join(missing, ", "), minimumCUABrowserVersion)
	}
	return nil
}

func mintCUASessionID() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("mint cua session ID: %w", err)
	}
	return "nib-" + hex.EncodeToString(random), nil
}

func cuaToolResultError(name string, result *mcp.CallToolResult) error {
	if result == nil {
		return fmt.Errorf("cua-driver %s returned an empty response", name)
	}
	if cuaResultRefused(result) {
		refusal, _ := structuredMap(result)["refusal"].(map[string]any)
		code, _ := refusal["code"].(string)
		message, _ := refusal["message"].(string)
		if code != "" && message != "" {
			return fmt.Errorf("cua-driver %s refused (%s): %s", name, code, message)
		}
		if code != "" {
			return fmt.Errorf("cua-driver %s refused (%s)", name, code)
		}
		return fmt.Errorf("cua-driver %s refused", name)
	}
	detail := firstText(result.Content)
	if detail == "" {
		detail = "tool result was marked as an error"
	}
	return fmt.Errorf("cua-driver %s failed: %s", name, detail)
}

func cuaResultRefused(result *mcp.CallToolResult) bool {
	status, _ := structuredMap(result)["status"].(string)
	return status == "refused"
}

func (r *cuaRuntime) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return r.client.CallTool(ctx, params)
}

func (r *cuaRuntime) SessionID() string {
	return r.sessionID
}

func (r *cuaRuntime) closeAfterStartupError(cause error, sessionStarted bool) error {
	var endErr error
	if sessionStarted {
		endErr = r.endSession()
	}
	forceCloseErr := r.forceCloseConnection()
	if r.connCancel != nil {
		r.connCancel()
	}
	clientCloseErr := r.client.Close()
	if clientCloseErr != nil {
		clientCloseErr = fmt.Errorf("close cua-driver after startup failure: %w", clientCloseErr)
	}
	return errors.Join(cause, endErr, forceCloseErr, clientCloseErr)
}

func (r *cuaRuntime) endSession() error {
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endResult, err := r.client.CallTool(closeCtx, &mcp.CallToolParams{
		Name:      "end_session",
		Arguments: map[string]any{"session": r.sessionID},
	})
	if err != nil {
		return fmt.Errorf("end cua-driver session %q: %w", r.sessionID, err)
	}
	if endResult == nil || endResult.IsError || cuaResultRefused(endResult) {
		return cuaToolResultError("end_session", endResult)
	}
	return nil
}

func (r *cuaRuntime) forceCloseConnection() error {
	if r.connection == nil {
		return nil
	}
	if err := r.connection.Close(); err != nil {
		return fmt.Errorf("force-close cua-driver connection: %w", err)
	}
	return nil
}

func (r *cuaRuntime) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.endSession()
		if errors.Is(r.closeErr, context.Canceled) || errors.Is(r.closeErr, context.DeadlineExceeded) {
			r.closeErr = errors.Join(r.closeErr, r.forceCloseConnection())
		}

		if r.connCancel != nil {
			r.connCancel()
		}
		if err := r.client.Close(); err != nil {
			r.closeErr = errors.Join(r.closeErr, fmt.Errorf("close cua-driver: %w", err))
		}
	})
	return r.closeErr
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
	// WAYLAND_DISPLAY) keeps DISPLAY and full X11 support. Override via cua.env
	// (appended last) if you must pin a transport.
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
	// Harmless on X11. Callers can still override via cua.env below.
	env = append(env, "CUA_DRIVER_RS_ENABLE_WAYLAND=1")
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}
