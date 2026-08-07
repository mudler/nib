package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

func runtimeBrowserToolNames() []string {
	return slices.Clone(cuaBrowserRequiredTools)
}

func TestCUARuntimeStartsAndEndsOneDeclaredSession(t *testing.T) {
	expectedMaxImageDimension := float64(1568)
	if runtime.GOOS == "linux" {
		expectedMaxImageDimension = 0
	}
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.19.0", runtimeBrowserToolNames(), nil)
	runtime, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{
		CUA:      types.CUAConfig{SessionID: "run-7"},
		Computer: types.ComputerConfig{Enabled: true},
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	wantBeforeClose := []fakeCUACall{
		{Name: "start_session", Args: map[string]any{"session": "run-7", "capture_scope": "window"}},
		{Name: "set_config", Args: map[string]any{"capture_scope": "window", "max_image_dimension": expectedMaxImageDimension}},
		{Name: "health_report", Args: map[string]any{}},
	}
	if calls := fake.Calls(); !reflect.DeepEqual(calls, wantBeforeClose) {
		t.Fatalf("startup calls = %#v, want %#v", calls, wantBeforeClose)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close = %v, want the first result", err)
	}

	wantCalls := append(wantBeforeClose,
		fakeCUACall{Name: "end_session", Args: map[string]any{"session": "run-7"}},
	)
	if calls := fake.Calls(); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("lifecycle calls = %#v, want %#v", calls, wantCalls)
	}
}

const (
	cuaHelperModeEnv   = "NIB_CUA_RUNTIME_HELPER_MODE"
	cuaHelperMarkerEnv = "NIB_CUA_RUNTIME_HELPER_MARKER"
	cuaHelperPIDEnv    = "NIB_CUA_RUNTIME_HELPER_PID"
)

func TestCUARuntimeHelperProcess(t *testing.T) {
	mode := os.Getenv(cuaHelperModeEnv)
	if mode == "" {
		return
	}
	if err := os.WriteFile(os.Getenv(cuaHelperPIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(2)
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(3)
		}
		if len(request.ID) == 0 {
			continue
		}

		var result any
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(4)
			}
			protocolVersion := params.ProtocolVersion
			if mode == "unsupported-protocol" {
				protocolVersion = "unsupported-test-version"
			}
			result = map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "cua-helper", "version": "0.19.0"},
			}
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(5)
			}
			if (mode == "hung-start" && params.Name == "start_session") ||
				(mode == "hung-end" && params.Name == "end_session") {
				continue
			}
			result = map[string]any{
				"content":           []any{},
				"structuredContent": map[string]any{"status": "ok"},
			}
		default:
			result = map[string]any{}
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  result,
		}); err != nil {
			os.Exit(6)
		}
	}

	if err := os.WriteFile(os.Getenv(cuaHelperMarkerEnv), []byte("reaped"), 0o600); err != nil {
		os.Exit(7)
	}
	os.Exit(0)
}

func helperCUARuntimeConfig(t *testing.T, mode string) (types.Config, string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	markerPath := dir + "/reaped"
	pidPath := dir + "/pid"
	return types.Config{CUA: types.CUAConfig{
		Command: executable,
		Args:    []string{"-test.run=^TestCUARuntimeHelperProcess$"},
		Env: map[string]string{
			cuaHelperModeEnv:   mode,
			cuaHelperMarkerEnv: markerPath,
			cuaHelperPIDEnv:    pidPath,
		},
	}}, markerPath, pidPath
}

func killCUARuntimeHelper(pidPath string) {
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
}

func cleanupCUARuntimeHelper(markerPath, pidPath string) {
	if _, err := os.Stat(markerPath); err == nil {
		return
	}
	killCUARuntimeHelper(pidPath)
}

func waitForCUARuntimeMarker(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestCUARuntimeCloseReapsChildWhenEndSessionHangs(t *testing.T) {
	cfg, markerPath, pidPath := helperCUARuntimeConfig(t, "hung-end")
	t.Cleanup(func() { cleanupCUARuntimeHelper(markerPath, pidPath) })
	runtime, err := newCUARuntime(context.Background(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close() error = %v, want end_session deadline", err)
		}
	case <-time.After(7 * time.Second):
		killCUARuntimeHelper(pidPath)
		<-closed
		t.Fatal("Close blocked after end_session ignored cancellation")
	}
	if !waitForCUARuntimeMarker(markerPath, time.Second) {
		t.Fatal("Close returned without reaping the Cua child")
	}
}

func TestCUARuntimeStartupCancellationReapsChild(t *testing.T) {
	cfg, markerPath, pidPath := helperCUARuntimeConfig(t, "hung-start")
	t.Cleanup(func() { cleanupCUARuntimeHelper(markerPath, pidPath) })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	type result struct {
		runtime *cuaRuntime
		err     error
	}
	started := make(chan result, 1)
	go func() {
		runtime, err := newCUARuntime(ctx, cfg, false)
		started <- result{runtime: runtime, err: err}
	}()
	select {
	case got := <-started:
		if got.runtime != nil || !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("newCUARuntime() = (%v, %v), want nil, deadline exceeded", got.runtime, got.err)
		}
	case <-time.After(2 * time.Second):
		killCUARuntimeHelper(pidPath)
		<-started
		t.Fatal("newCUARuntime blocked while closing a canceled start_session")
	}
	if !waitForCUARuntimeMarker(markerPath, time.Second) {
		t.Fatal("startup cancellation returned without reaping the Cua child")
	}
}

func TestCUARuntimeProtocolErrorReapsChild(t *testing.T) {
	cfg, markerPath, pidPath := helperCUARuntimeConfig(t, "unsupported-protocol")
	t.Cleanup(func() { cleanupCUARuntimeHelper(markerPath, pidPath) })
	if runtime, err := newCUARuntime(context.Background(), cfg, false); runtime != nil || err == nil {
		t.Fatalf("newCUARuntime() = (%v, %v), want protocol negotiation error", runtime, err)
	}
	if !waitForCUARuntimeMarker(markerPath, time.Second) {
		t.Fatal("protocol negotiation error leaked the Cua child")
	}
}

func TestCUARuntimeMintsStableNonDefaultSession(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.1.0", []string{"start_session", "end_session", "health_report"}, nil)
	runtime, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	first, second := runtime.SessionID(), runtime.SessionID()
	if first != second {
		t.Fatalf("SessionID changed from %q to %q", first, second)
	}
	if !regexp.MustCompile(`^nib-[0-9a-f]{24}$`).MatchString(first) {
		t.Fatalf("minted SessionID = %q, want nib- plus 24 lowercase hex characters", first)
	}
	if first == "default" {
		t.Fatal("minted SessionID must not be default")
	}
	if calls := fake.Calls(); len(calls) == 0 || calls[0].Args["session"] != first {
		t.Fatalf("start_session calls = %#v, want minted session %q", calls, first)
	}
}

func TestCUARuntimeUsesConfiguredSessionID(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.1.0", []string{"start_session", "end_session", "health_report"}, nil)
	runtime, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{
		CUA: types.CUAConfig{SessionID: "configured-session"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if got := runtime.SessionID(); got != "configured-session" {
		t.Fatalf("SessionID() = %q, want configured-session", got)
	}
}

func TestCUARuntimeRejectsDefaultSessionID(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.19.0", runtimeBrowserToolNames(), nil)
	_, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{
		CUA: types.CUAConfig{SessionID: "default"},
	}, true)
	if err == nil || !strings.Contains(err.Error(), "non-default") {
		t.Fatalf("newCUARuntimeFromClient(default) error = %v, want actionable non-default-session error", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("default session must be rejected before driver calls, got %#v", calls)
	}
}

func TestCUARuntimeRejectsOldBrowserDriver(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.18.9", runtimeBrowserToolNames(), nil)
	_, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, true)
	if err == nil || !strings.Contains(err.Error(), "0.19.0") || !strings.Contains(err.Error(), "0.18.9") {
		t.Fatalf("old browser driver error = %v, want installed and required versions", err)
	}
}

func TestCUARuntimeRejectsMalformedBrowserDriverVersion(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "not-a-version", runtimeBrowserToolNames(), nil)
	_, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, true)
	if err == nil || !strings.Contains(err.Error(), "not-a-version") || !strings.Contains(err.Error(), "version") {
		t.Fatalf("malformed browser driver version error = %v, want actionable version error", err)
	}
}

func TestCUARuntimeRejectsRefusedSessionStart(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.1.0", []string{"start_session"}, map[string]fakeCUAHandler{
		"start_session": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("session_denied", "session could not be declared", nil)
		},
	})

	_, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, false)
	if err == nil || !strings.Contains(err.Error(), "session_denied") {
		t.Fatalf("refused start_session error = %v, want refusal code", err)
	}
}

func TestCUARuntimeReportsEveryMissingBrowserTool(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.19.0", []string{"start_session"}, nil)
	_, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, true)
	if err == nil {
		t.Fatal("newCUARuntimeFromClient must reject an incomplete browser tool roster")
	}

	message := err.Error()
	missing := runtimeBrowserToolNames()[1:]
	slices.Sort(missing)
	previous := -1
	for _, name := range missing {
		index := strings.Index(message, name)
		if index < 0 {
			t.Errorf("missing-tools error %q does not report %q", message, name)
		}
		if index < previous {
			t.Errorf("missing-tools error does not sort names: %q", message)
			break
		}
		previous = index
	}
}

func TestCUARuntimeAllowsOldComputerOnlyDriver(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.1.0", []string{"start_session", "end_session", "health_report"}, nil)
	runtime, err := newCUARuntimeFromClient(ctx, fake.Session, types.Config{}, false)
	if err != nil {
		t.Fatalf("computer-only runtime rejected an old driver: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

type pagedCUAClient struct {
	mu          sync.Mutex
	listCursors []string
	calls       []fakeCUACall
	closed      bool
}

func (c *pagedCUAClient) InitializeResult() *mcp.InitializeResult {
	return &mcp.InitializeResult{ServerInfo: &mcp.Implementation{Name: "paged-cua", Version: "0.19.0"}}
}

func (c *pagedCUAClient) ListTools(_ context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listCursors = append(c.listCursors, params.Cursor)
	names := runtimeBrowserToolNames()
	if params.Cursor == "" {
		return toolPage(names[:7], "page-2"), nil
	}
	return toolPage(names[7:], ""), nil
}

func toolPage(names []string, next string) *mcp.ListToolsResult {
	tools := make([]*mcp.Tool, len(names))
	for i, name := range names {
		tools[i] = &mcp.Tool{Name: name}
	}
	return &mcp.ListToolsResult{Tools: tools, NextCursor: next}
}

func (c *pagedCUAClient) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	args, _ := params.Arguments.(map[string]any)
	c.mu.Lock()
	c.calls = append(c.calls, fakeCUACall{Name: params.Name, Args: cloneCUAArgs(args)})
	c.mu.Unlock()
	return &mcp.CallToolResult{}, nil
}

func (c *pagedCUAClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func TestCUARuntimeListsAllToolPages(t *testing.T) {
	client := &pagedCUAClient{}
	runtime, err := newCUARuntimeFromClient(context.Background(), client, types.Config{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.listCursors, []string{"", "page-2"}) {
		t.Fatalf("ListTools cursors = %#v, want both pages", client.listCursors)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScrubbedDriverEnvDropsProviderKeysAndKeepsOverrides(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("OPENAI_API_KEY", "inherited-openai")
	t.Setenv("ANTHROPIC_API_KEY", "inherited-anthropic")
	t.Setenv("LOCALAI_API_KEY", "inherited-localai")
	t.Setenv("CUA_RUNTIME_TEST_KEEP", "inherited")

	env := scrubbedDriverEnv(map[string]string{
		"OPENAI_API_KEY":                  "explicit-openai",
		"CUA_DRIVER_RS_TELEMETRY_ENABLED": "1",
		"CUA_RUNTIME_TEST_OVERRIDE":       "kept",
	})
	values := make(map[string]string)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}

	if _, ok := values["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("inherited ANTHROPIC_API_KEY was passed to cua-driver")
	}
	if _, ok := values["LOCALAI_API_KEY"]; ok {
		t.Fatal("inherited LOCALAI_API_KEY was passed to cua-driver")
	}
	if values["OPENAI_API_KEY"] != "explicit-openai" {
		t.Fatalf("explicit provider override = %q, want explicit-openai", values["OPENAI_API_KEY"])
	}
	if values["CUA_DRIVER_RS_TELEMETRY_ENABLED"] != "1" {
		t.Fatalf("telemetry override = %q, want 1", values["CUA_DRIVER_RS_TELEMETRY_ENABLED"])
	}
	if values["CUA_RUNTIME_TEST_KEEP"] != "inherited" || values["CUA_RUNTIME_TEST_OVERRIDE"] != "kept" {
		t.Fatalf("ordinary environment values were not preserved: %#v", values)
	}
}
