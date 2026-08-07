package mcp

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

func runtimeBrowserToolNames() []string {
	return []string{
		"start_session", "end_session", "health_report", "set_config",
		"list_apps", "list_windows", "launch_app", "kill_app", "press_key",
		"get_browser_state", "browser_prepare", "browser_navigate", "browser_click",
		"browser_type", "browser_pointer", "browser_dialog",
		"browser_set_input_files", "browser_download",
	}
}

func TestCUARuntimeStartsAndEndsOneDeclaredSession(t *testing.T) {
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
		{Name: "set_config", Args: map[string]any{"capture_scope": "window", "max_image_dimension": float64(0)}},
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
