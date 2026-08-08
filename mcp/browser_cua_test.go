package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

const testCUASessionID = "browser-session-7"

func startCUABrowserTestServer(
	t *testing.T,
	handlers map[string]fakeCUAHandler,
) (*cuaBrowserServer, *fakeCUADriver) {
	t.Helper()
	fake := startFakeCUA(t, context.Background(), "0.19.0", cuaBrowserRequiredTools, handlers)
	runtime := &cuaRuntime{client: fake.Session, sessionID: testCUASessionID}
	server := newCUABrowserServer(runtime, types.Config{
		Browser: types.BrowserConfig{Backend: "cua", ProfileName: "profile_7"},
	})
	return server, fake
}

func cuaBrowserApps() map[string]any {
	return map[string]any{
		"apps": []any{map[string]any{
			"name":        "Google Chrome",
			"bundle_id":   "com.google.Chrome",
			"launch_path": "/opt/google/chrome",
		}},
	}
}

func cuaBrowserWindows(windows ...map[string]any) map[string]any {
	items := make([]any, len(windows))
	for index, window := range windows {
		items[index] = window
	}
	return map[string]any{"windows": items}
}

func cuaBrowserWindow(pid, windowID int) map[string]any {
	return map[string]any{
		"pid": pid, "window_id": windowID, "app": "Google Chrome",
		"name": "Google Chrome", "path": "/opt/google/chrome",
	}
}

func cuaBrowserBind(target string, tabs ...map[string]any) map[string]any {
	items := make([]any, len(tabs))
	for index, tab := range tabs {
		items[index] = tab
	}
	return map[string]any{
		"mode": "bind", "target_id": target, "binding_quality": "exact",
		"mutation_allowed": true, "tabs": items,
	}
}

func cuaBrowserTab(id, title string, active any) map[string]any {
	return map[string]any{
		"tab_id": id, "title": title, "url": "https://example.test/" + id,
		"active": active,
	}
}

func cuaBrowserSnapshot(target, tab, outline string, refs ...map[string]any) map[string]any {
	items := make([]any, len(refs))
	for index, ref := range refs {
		items[index] = ref
	}
	return map[string]any{
		"mode": "snapshot", "target_id": target, "tab_id": tab,
		"snapshot": map[string]any{"format": "semantic_v2", "complete": true},
		"outline":  outline, "refs": items, "content_refs": []any{},
	}
}

func cuaBrowserRef(raw, name string, actions ...string) map[string]any {
	actionItems := make([]any, len(actions))
	for index, action := range actions {
		actionItems[index] = action
	}
	return map[string]any{
		"ref": raw, "role": "button", "name": name, "value": "",
		"states": map[string]any{}, "actions": actionItems,
		"frame": "main", "visibility": "in_viewport",
	}
}

func scriptedWindowHandler(responses ...map[string]any) fakeCUAHandler {
	var mu sync.Mutex
	index := 0
	return func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(responses) == 0 {
			return cuaOK(cuaBrowserWindows())
		}
		response := responses[index]
		if index < len(responses)-1 {
			index++
		}
		return cuaOK(response)
	}
}

func standardRunningCUAHandlers(bind map[string]any, navigate map[string]any) map[string]fakeCUAHandler {
	return map[string]fakeCUAHandler{
		"list_apps": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserApps())
		},
		"list_windows": scriptedWindowHandler(
			cuaBrowserWindows(cuaBrowserWindow(41, 11)),
			cuaBrowserWindows(cuaBrowserWindow(91, 21)),
		),
		"browser_prepare": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{"prepared_pid": 91})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(bind)
		},
		"browser_navigate": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(navigate)
		},
	}
}

func callCUANavigate(t *testing.T, server *cuaBrowserServer) BrowserOutput {
	t.Helper()
	_, output, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
	if err != nil {
		t.Fatalf("browserNavigate: %v", err)
	}
	return output
}

func fakeCallNames(calls []fakeCUACall) []string {
	names := make([]string, len(calls))
	for index, call := range calls {
		names[index] = call.Name
	}
	return names
}

func TestCUABrowserOtherToolsRequireNavigateFirst(t *testing.T) {
	server, fake := startCUABrowserTestServer(t, nil)
	tests := []struct {
		name string
		call func() error
	}{
		{"snapshot", func() error {
			_, _, err := server.browserSnapshot(context.Background(), nil, BrowserInput{})
			return err
		}},
		{"click", func() error {
			_, _, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
			return err
		}},
		{"type", func() error {
			_, _, err := server.browserType(context.Background(), nil, BrowserInput{Ref: "@e1", Text: "x"})
			return err
		}},
		{"press", func() error {
			_, _, err := server.browserPress(context.Background(), nil, BrowserInput{Key: "Enter"})
			return err
		}},
		{"scroll", func() error {
			_, _, err := server.browserScroll(context.Background(), nil, BrowserInput{Direction: "down"})
			return err
		}},
		{"vision", func() error { _, _, err := server.browserVision(context.Background(), nil, BrowserInput{}); return err }},
		{"tabs", func() error {
			_, _, err := server.browserTabs(context.Background(), nil, BrowserTabsInput{})
			return err
		}},
		{"select tab", func() error {
			_, _, err := server.browserSelectTab(context.Background(), nil, BrowserSelectTabInput{TabID: "@t1"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil || !strings.Contains(err.Error(), "call browser_navigate first") {
				t.Fatalf("error = %v, want navigate-first guidance", err)
			}
		})
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("pre-navigation tools called Cua: %#v", calls)
	}
}

func TestCUABrowserNavigateReusesRunningSource(t *testing.T) {
	bind := cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "A", true))
	navigate := cuaBrowserSnapshot("target-1", "tab-a", "- heading Home")
	server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(bind, navigate))

	output := callCUANavigate(t, server)
	if output.Status != "ok" || !strings.Contains(output.Snapshot, "Home") {
		t.Fatalf("navigate output = %#v", output)
	}
	wantNames := []string{"list_apps", "list_windows", "browser_prepare", "list_windows", "get_browser_state", "browser_navigate"}
	if got := fakeCallNames(fake.Calls()); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Cua calls = %v, want %v", got, wantNames)
	}
	for _, call := range fake.Calls() {
		if call.Args["session"] != testCUASessionID {
			t.Fatalf("%s session = %#v, want %q", call.Name, call.Args["session"], testCUASessionID)
		}
	}
}

func TestCUABrowserNavigateRequiresExactConfiguredExecutableIdentity(t *testing.T) {
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-explicit", cuaBrowserTab("tab-explicit", "Explicit", true)),
		cuaBrowserSnapshot("target-explicit", "tab-explicit", "explicit"),
	)
	handlers["list_apps"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"apps": []any{
			map[string]any{"name": "Google Chrome", "launch_path": "/opt/wrong/chrome"},
			map[string]any{"name": "Google Chrome", "launch_path": "/opt/exact/chrome"},
		}})
	}
	handlers["list_windows"] = scriptedWindowHandler(
		cuaBrowserWindows(map[string]any{
			"pid": 41, "window_id": 11, "app": "Google Chrome", "path": "/opt/wrong/chrome",
		}),
		cuaBrowserWindows(map[string]any{
			"pid": 51, "window_id": 12, "app": "Google Chrome", "path": "/opt/exact/chrome",
		}),
		cuaBrowserWindows(cuaBrowserWindow(91, 21)),
	)
	handlers["launch_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"pid": 51})
	}
	server, fake := startCUABrowserTestServer(t, handlers)
	server.cfg.Browser.ChromePath = "/opt/exact/chrome"

	callCUANavigate(t, server)
	calls := fake.Calls()
	if countFakeCUACalls(calls, "launch_app") != 1 {
		t.Fatalf("calls = %#v, want launch instead of same-name wrong executable reuse", calls)
	}
	for _, call := range calls {
		if call.Name == "browser_prepare" && call.Args["pid"] != float64(51) {
			t.Fatalf("browser_prepare pid = %#v, want explicitly configured executable pid 51", call.Args["pid"])
		}
	}
}

func TestCUABrowserExecutableIdentityUsesPlatformCaseRules(t *testing.T) {
	upper := normalizedExecutableIdentity(`/opt/Chrome`)
	lower := normalizedExecutableIdentity(`/opt/chrome`)
	if runtime.GOOS == "windows" {
		if upper != lower {
			t.Fatalf("Windows executable identities differ by case: %q != %q", upper, lower)
		}
		return
	}
	if upper == lower {
		t.Fatalf("POSIX executable identities collapsed distinct paths: %q == %q", upper, lower)
	}
}

func TestCUABrowserPOSIXExecutableIdentityPreservesPathBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable identity rules do not apply on Windows")
	}
	paths := []string{
		`/opt/Chrome\bin`,
		`/opt/Chrome/bin`,
		` /opt/Chrome/bin`,
		`/opt/Chrome/bin `,
	}
	for _, path := range paths {
		if got := normalizedExecutableIdentity(path); got != path {
			t.Errorf("normalizedExecutableIdentity(%q) = %q, want verbatim POSIX identity", path, got)
		}
	}
	if normalizedExecutableIdentity(paths[0]) == normalizedExecutableIdentity(paths[1]) {
		t.Error("POSIX backslash and slash paths collapsed to one executable identity")
	}
	if normalizedExecutableIdentity(paths[1]) == normalizedExecutableIdentity(paths[2]) ||
		normalizedExecutableIdentity(paths[1]) == normalizedExecutableIdentity(paths[3]) {
		t.Error("POSIX boundary-whitespace paths collapsed to one executable identity")
	}
}

func TestCUABrowserNavigateLaunchesAndKillsOnlyOwnedTemporarySource(t *testing.T) {
	bind := cuaBrowserBind("target-owned", cuaBrowserTab("tab-owned", "Owned", true))
	handlers := standardRunningCUAHandlers(bind, cuaBrowserSnapshot("target-owned", "tab-owned", "ready"))
	handlers["list_windows"] = scriptedWindowHandler(
		cuaBrowserWindows(),
		cuaBrowserWindows(cuaBrowserWindow(51, 12)),
		cuaBrowserWindows(cuaBrowserWindow(91, 21)),
	)
	handlers["launch_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"pid": 51})
	}
	server, fake := startCUABrowserTestServer(t, handlers)

	callCUANavigate(t, server)
	wantNames := []string{
		"list_apps", "list_windows", "launch_app", "list_windows", "browser_prepare",
		"list_windows", "get_browser_state", "kill_app", "browser_navigate",
	}
	calls := fake.Calls()
	if got := fakeCallNames(calls); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("Cua calls = %v, want %v", got, wantNames)
	}
	if got := calls[2].Args; !reflect.DeepEqual(got, map[string]any{"launch_path": "/opt/google/chrome", "session": testCUASessionID}) {
		t.Fatalf("launch_app args = %#v", got)
	}
	if got := calls[7].Args; !reflect.DeepEqual(got, map[string]any{"pid": float64(51), "session": testCUASessionID}) &&
		!reflect.DeepEqual(got, map[string]any{"pid": 51, "session": testCUASessionID}) {
		t.Fatalf("kill_app args = %#v, want owned pid only", got)
	}
}

func TestCUABrowserPreparedOwnedSourceIsNeverKilledAfterLaterBindFailure(t *testing.T) {
	handlers := standardRunningCUAHandlers(nil, nil)
	handlers["list_windows"] = scriptedWindowHandler(
		cuaBrowserWindows(),
		cuaBrowserWindows(cuaBrowserWindow(51, 12)),
		cuaBrowserWindows(cuaBrowserWindow(51, 12)),
	)
	handlers["launch_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"pid": 51})
	}
	handlers["browser_prepare"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"prepared_pid": 51})
	}
	handlers["get_browser_state"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{
			"mode": "bind", "target_id": "target-owned-prepared",
			"binding_quality": "ambiguous", "mutation_allowed": false,
			"tabs": []any{cuaBrowserTab("tab", "Tab", true)},
		})
	}
	server, fake := startCUABrowserTestServer(t, handlers)

	_, _, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
	if err == nil || !strings.Contains(err.Error(), "binding quality") {
		t.Fatalf("error = %v, want later exact-binding failure", err)
	}
	if countFakeCUACalls(fake.Calls(), "kill_app") != 0 {
		t.Fatalf("prepared process was killed after ownership transfer: %#v", fake.Calls())
	}
}

func TestCUABrowserNeverKillsPreexistingSource(t *testing.T) {
	bind := cuaBrowserBind("target-existing",
		cuaBrowserTab("tab-a", "A", false),
		cuaBrowserTab("tab-b", "B", false),
	)
	server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(bind, nil))

	_, output, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "refused" {
		t.Fatalf("output = %#v, want local ambiguity refusal", output)
	}
	if slices.Contains(fakeCallNames(fake.Calls()), "kill_app") {
		t.Fatalf("pre-existing source was killed: %#v", fake.Calls())
	}
}

func TestCUABrowserPrepareUsesIsolatedNamedProfile(t *testing.T) {
	bind := cuaBrowserBind("target-profile", cuaBrowserTab("tab-profile", "Profile", true))
	server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(
		bind, cuaBrowserSnapshot("target-profile", "tab-profile", "profile ready"),
	))
	callCUANavigate(t, server)

	var prepare fakeCUACall
	for _, call := range fake.Calls() {
		if call.Name == "browser_prepare" {
			prepare = call
		}
	}
	want := map[string]any{
		"pid": float64(41), "session": testCUASessionID, "allow_launch": true,
		"profile": map[string]any{"mode": "isolated_named", "name": "profile_7"},
	}
	if !reflect.DeepEqual(prepare.Args, want) {
		t.Fatalf("browser_prepare args = %#v, want %#v", prepare.Args, want)
	}
	raw, err := json.Marshal(prepare.Args)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(string(raw))
	if strings.Contains(encoded, "remote-debug") || strings.Contains(encoded, "approval") {
		t.Fatalf("browser_prepare received forbidden private flags: %#v", prepare.Args)
	}
}

func TestCUABrowserPrepareTimesOutAfterConfiguredBound(t *testing.T) {
	handlers := standardRunningCUAHandlers(nil, nil)
	handlers["list_windows"] = scriptedWindowHandler(cuaBrowserWindows())
	handlers["launch_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"pid": 51})
	}
	server, fake := startCUABrowserTestServer(t, handlers)
	server.prepareTimeout = 15 * time.Millisecond
	server.pollInterval = time.Millisecond

	started := time.Now()
	_, _, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want preparation deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("injected preparation bound took %s", elapsed)
	}
	if countFakeCUACalls(fake.Calls(), "kill_app") != 1 {
		t.Fatalf("timed-out owned source cleanup calls = %#v", fake.Calls())
	}
}

func TestCUABrowserPreparationRefusalSurvivesCleanupFailure(t *testing.T) {
	handlers := standardRunningCUAHandlers(nil, nil)
	handlers["list_windows"] = scriptedWindowHandler(
		cuaBrowserWindows(),
		cuaBrowserWindows(cuaBrowserWindow(51, 12)),
	)
	handlers["launch_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaOK(map[string]any{"pid": 51})
	}
	handlers["browser_prepare"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return cuaRefused("profile_denied", "profile preparation denied", nil)
	}
	handlers["kill_app"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return nil, nil, errors.New("scripted cleanup failure")
	}
	server, fake := startCUABrowserTestServer(t, handlers)

	_, output, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
	if err != nil {
		t.Fatalf("structured preparation refusal became a Go error: %v", err)
	}
	if output.Status != "refused" || output.Refusal == nil || output.Refusal.Code != "profile_denied" {
		t.Fatalf("output = %#v, want preserved structured refusal", output)
	}
	if !strings.Contains(output.Refusal.Message, "cleanup") {
		t.Fatalf("refusal message = %q, want safe cleanup warning", output.Refusal.Message)
	}
	if countFakeCUACalls(fake.Calls(), "kill_app") != 1 {
		t.Fatalf("cleanup calls = %#v, want exactly one", fake.Calls())
	}
}

func TestCUABrowserSelectsOnlyTabOrUniqueActiveTab(t *testing.T) {
	tests := []struct {
		name string
		tabs []map[string]any
		want string
	}{
		{"sole tab without active proof", []map[string]any{cuaBrowserTab("tab-only", "Only", nil)}, "tab-only"},
		{"unique active tab", []map[string]any{
			cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true), cuaBrowserTab("tab-c", "C", nil),
		}, "tab-b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bind := cuaBrowserBind("target-select", test.tabs...)
			server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(
				bind, cuaBrowserSnapshot("target-select", test.want, "selected"),
			))
			callCUANavigate(t, server)
			calls := fake.Calls()
			navigate := calls[len(calls)-1]
			if navigate.Args["tab_id"] != test.want {
				t.Fatalf("navigate tab_id = %#v, want %q", navigate.Args["tab_id"], test.want)
			}
		})
	}
}

func TestCUABrowserRefusesAmbiguousInitialTabs(t *testing.T) {
	tests := []struct {
		name string
		tabs []map[string]any
	}{
		{"no active tab", []map[string]any{cuaBrowserTab("a", "A", false), cuaBrowserTab("b", "B", nil)}},
		{"multiple active tabs", []map[string]any{cuaBrowserTab("a", "A", true), cuaBrowserTab("b", "B", true)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(cuaBrowserBind("target", test.tabs...), nil))
			_, output, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
			if err != nil {
				t.Fatal(err)
			}
			if output.Status != "refused" || output.Refusal == nil || output.Refusal.Code != "browser_tab_ambiguous" {
				t.Fatalf("output = %#v, want ambiguity refusal", output)
			}
			if slices.Contains(fakeCallNames(fake.Calls()), "browser_navigate") {
				t.Fatalf("ambiguous binding navigated: %#v", fake.Calls())
			}
		})
	}
}

func TestCUABrowserTargetChangeInvalidatesCapabilities(t *testing.T) {
	bindCalls := 0
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-old", cuaBrowserTab("old-tab", "Old", true)),
		cuaBrowserSnapshot("target-old", "old-tab", "old-ref", cuaBrowserRef("raw-old", "Old", "click")),
	)
	handlers["get_browser_state"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		bindCalls++
		if bindCalls == 1 {
			return cuaOK(cuaBrowserBind("target-old", cuaBrowserTab("old-tab", "Old", true)))
		}
		return cuaOK(cuaBrowserBind("target-new", cuaBrowserTab("new-tab", "New", true)))
	}
	server, _ := startCUABrowserTestServer(t, handlers)
	callCUANavigate(t, server)
	server.mu.Lock()
	server.lastEditable = "raw-editable"
	server.dialogID = "raw-dialog"
	server.mu.Unlock()

	_, output, err := server.browserTabs(context.Background(), nil, BrowserTabsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Tabs) != 1 || output.Tabs[0].ID != "@t1" || !output.Tabs[0].Selected {
		t.Fatalf("tabs after generation change = %#v", output.Tabs)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.targetID != "target-new" {
		t.Fatalf("generation state was not invalidated: refs=%#v editable=%q dialog=%q target=%q",
			server.refs, server.lastEditable, server.dialogID, server.targetID)
	}
}

func TestCUABrowserTabsPreserveAliasesWithoutActivating(t *testing.T) {
	bindCalls := 0
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-tabs", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)),
		cuaBrowserSnapshot("target-tabs", "tab-b", "ready"),
	)
	handlers["get_browser_state"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		bindCalls++
		if bindCalls == 1 {
			return cuaOK(cuaBrowserBind("target-tabs", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)))
		}
		return cuaOK(cuaBrowserBind("target-tabs",
			cuaBrowserTab("tab-b", "B2", true), cuaBrowserTab("tab-a", "A2", false), cuaBrowserTab("tab-c", "C", false),
		))
	}
	server, fake := startCUABrowserTestServer(t, handlers)
	callCUANavigate(t, server)
	before := len(fake.Calls())

	_, output, err := server.browserTabs(context.Background(), nil, BrowserTabsInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := []BrowserTabOutput{
		{ID: "@t2", Title: "B2", URL: "https://example.test/tab-b", Active: boolPointer(true), Selected: true},
		{ID: "@t1", Title: "A2", URL: "https://example.test/tab-a", Active: boolPointer(false)},
		{ID: "@t3", Title: "C", URL: "https://example.test/tab-c", Active: boolPointer(false)},
	}
	if !reflect.DeepEqual(output.Tabs, want) {
		t.Fatalf("tabs = %#v, want %#v", output.Tabs, want)
	}
	after := fake.Calls()[before:]
	if got := fakeCallNames(after); !reflect.DeepEqual(got, []string{"get_browser_state"}) {
		t.Fatalf("browser_tabs native calls = %v, want exact rebind only", got)
	}
}

func TestCUABrowserTabsClearCapabilitiesWhenClosedSelectionIsReplaced(t *testing.T) {
	bindCalls := 0
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-tabs", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)),
		cuaBrowserSnapshot("target-tabs", "tab-b", "old-ref", cuaBrowserRef("raw-old", "Old", "click")),
	)
	handlers["get_browser_state"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		bindCalls++
		if bindCalls == 1 {
			return cuaOK(cuaBrowserBind("target-tabs",
				cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true),
			))
		}
		return cuaOK(cuaBrowserBind("target-tabs", cuaBrowserTab("tab-a", "A", true)))
	}
	server, _ := startCUABrowserTestServer(t, handlers)
	callCUANavigate(t, server)
	server.mu.Lock()
	server.lastEditable = "raw-editable"
	server.dialogID = "raw-dialog"
	server.mu.Unlock()

	_, output, err := server.browserTabs(context.Background(), nil, BrowserTabsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Tabs) != 1 || output.Tabs[0].ID != "@t1" || !output.Tabs[0].Selected {
		t.Fatalf("replacement selection = %#v, want surviving @t1 selected", output.Tabs)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" {
		t.Fatalf("replacement selection retained stale capabilities: refs=%#v editable=%q dialog=%q",
			server.refs, server.lastEditable, server.dialogID)
	}
}

func TestCUABrowserSelectTabIsLogicalOnly(t *testing.T) {
	bindCalls := 0
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-select", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)),
		cuaBrowserSnapshot("target-select", "tab-b", "before"),
	)
	handlers["get_browser_state"] = func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		bindCalls++
		if bindCalls == 1 {
			return cuaOK(cuaBrowserBind("target-select", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)))
		}
		if args["tab_id"] != "tab-a" || args["target_id"] != "target-select" {
			return nil, nil, errors.New("logical selection did not scope the snapshot exactly")
		}
		return cuaOK(cuaBrowserSnapshot("target-select", "tab-a", "after logical selection"))
	}
	server, fake := startCUABrowserTestServer(t, handlers)
	callCUANavigate(t, server)
	before := len(fake.Calls())

	_, output, err := server.browserSelectTab(context.Background(), nil, BrowserSelectTabInput{TabID: "@t1"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "ok" || !strings.Contains(output.Snapshot, "logical selection") {
		t.Fatalf("select output = %#v", output)
	}
	server.mu.Lock()
	selected := server.selectedTab
	server.mu.Unlock()
	if selected != "tab-a" {
		t.Fatalf("selected raw tab = %q, want tab-a", selected)
	}
	after := fake.Calls()[before:]
	if got := fakeCallNames(after); !reflect.DeepEqual(got, []string{"get_browser_state"}) {
		t.Fatalf("browser_select_tab calls = %v, want semantic snapshot only", got)
	}
}

func TestCUABrowserSelectTabRejectsMismatchedReturnedTabWithoutCommitting(t *testing.T) {
	bindCalls := 0
	handlers := standardRunningCUAHandlers(
		cuaBrowserBind("target-select", cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)),
		cuaBrowserSnapshot("target-select", "tab-b", "old-ref", cuaBrowserRef("raw-old", "Old", "click")),
	)
	handlers["get_browser_state"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		bindCalls++
		if bindCalls == 1 {
			return cuaOK(cuaBrowserBind("target-select",
				cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true),
			))
		}
		return cuaOK(cuaBrowserSnapshot(
			"target-select", "tab-b", "wrong-tab-ref", cuaBrowserRef("raw-wrong", "Wrong", "click"),
		))
	}
	server, _ := startCUABrowserTestServer(t, handlers)
	callCUANavigate(t, server)

	_, output, err := server.browserSelectTab(context.Background(), nil, BrowserSelectTabInput{TabID: "@t1"})
	if err == nil && output.Status != "refused" {
		t.Fatalf("mismatched returned tab was accepted: output=%#v error=%v", output, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.selectedTab != "tab-b" {
		t.Fatalf("selected tab = %q, want original tab-b after rejected response", server.selectedTab)
	}
	if got := server.refs["@e1"].Raw; got != "raw-old" {
		t.Fatalf("element capabilities changed to %q, want original raw-old", got)
	}
}

func TestCUABrowserConcurrentNavigatePreparesOnce(t *testing.T) {
	bind := cuaBrowserBind("target-concurrent", cuaBrowserTab("tab", "Tab", true))
	server, fake := startCUABrowserTestServer(t, standardRunningCUAHandlers(
		bind, cuaBrowserSnapshot("target-concurrent", "tab", "ready"),
	))
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "https://example.test"})
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := countFakeCUACalls(fake.Calls(), "browser_prepare"); got != 1 {
		t.Fatalf("browser_prepare calls = %d, want exactly one", got)
	}
	if got := countFakeCUACalls(fake.Calls(), "browser_navigate"); got != 2 {
		t.Fatalf("browser_navigate calls = %d, want one per request", got)
	}
}

func TestStartTransportsCombinedCUASurfacesUseOneSessionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bind := cuaBrowserBind("shared-target", cuaBrowserTab("shared-tab", "Shared", true))
	handlers := standardRunningCUAHandlers(bind, cuaBrowserSnapshot("shared-target", "shared-tab", "shared"))
	ended := make(chan struct{}, 1)
	handlers["end_session"] = func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		ended <- struct{}{}
		return cuaOK(nil)
	}
	fake := startFakeCUA(t, context.Background(), "0.19.0", cuaBrowserRequiredTools, handlers)
	factoryCalls := 0
	requireBrowserCalls := []bool{}
	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(), CUA: types.CUAConfig{SessionID: "shared-surface-session"},
		Computer: types.ComputerConfig{Enabled: true},
		Browser:  types.BrowserConfig{Enabled: true, Backend: "cua", ProfileName: "shared_profile"},
	}, nil, newTransportRuntimeFactory(t, fake, &factoryCalls, &requireBrowserCalls))
	if err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 1 {
		t.Fatalf("runtime factory calls = %d, want 1", factoryCalls)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "combined-test", Version: "v0"}, nil)
	computerSession, err := client.Connect(ctx, transports[3], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer computerSession.Close()
	browserSession, err := client.Connect(ctx, transports[4], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browserSession.Close()
	if _, err := computerSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "computer_use", Arguments: map[string]any{"action": "list_apps"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := browserSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "browser_navigate", Arguments: map[string]any{"url": "https://example.test"},
	}); err != nil {
		t.Fatal(err)
	}

	seenComputer := false
	seenBrowser := false
	for _, call := range fake.Calls() {
		if call.Name == "list_apps" {
			seenComputer = true
		}
		if call.Name == "browser_navigate" {
			seenBrowser = true
		}
		if call.Name == "list_apps" || call.Name == "list_windows" || call.Name == "browser_prepare" ||
			call.Name == "get_browser_state" || call.Name == "browser_navigate" {
			if call.Args["session"] != "shared-surface-session" {
				t.Fatalf("%s session = %#v, want shared-surface-session", call.Name, call.Args["session"])
			}
		}
	}
	if !seenComputer || !seenBrowser {
		t.Fatalf("did not exercise both Cua surfaces: %#v", fake.Calls())
	}
	_ = browserSession.Close()
	_ = computerSession.Close()
	cancel()
	waitForTransportRuntimeClose(t, ended)
}

func TestCUABrowserInitializationFailureNeverFallsBackToChromedp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wantErr := errors.New("scripted Cua initialization failure")
	fake := startFakeCUA(t, context.Background(), "0.19.0", cuaBrowserRequiredTools, map[string]fakeCUAHandler{
		"list_apps": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return nil, nil, wantErr },
	})
	runtime := &cuaRuntime{client: fake.Session, sessionID: testCUASessionID}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- startBrowserMCPServer(ctx, serverTransport, types.Config{
			Browser: types.BrowserConfig{Backend: "cua", ProfileName: "profile_7"},
		}, runtime)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "no-fallback-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, callErr := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "browser_navigate", Arguments: map[string]any{"url": "https://example.test"},
	})
	if callErr == nil && (result == nil || !result.IsError) {
		t.Fatalf("navigate result = %#v, error = %v; want Cua initialization failure", result, callErr)
	}
	if calls := fake.Calls(); !reflect.DeepEqual(fakeCallNames(calls), []string{"list_apps"}) {
		t.Fatalf("initialization calls = %#v, want only failed Cua list_apps", calls)
	}
}
