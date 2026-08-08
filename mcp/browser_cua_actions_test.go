package mcp

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type failingCUAClient struct{ err error }

func (client *failingCUAClient) CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return nil, client.err
}

func (*failingCUAClient) InitializeResult() *mcp.InitializeResult { return nil }

func (*failingCUAClient) ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return nil, nil
}

func (*failingCUAClient) Close() error { return nil }

func preparedCUABrowserTestServer(
	t *testing.T,
	handlers map[string]fakeCUAHandler,
) (*cuaBrowserServer, *fakeCUADriver) {
	t.Helper()
	server, fake := startCUABrowserTestServer(t, handlers)
	server.preparedPID = 91
	server.windowID = 21
	server.targetID = "target-1"
	server.tabs["tab-a"] = cuaTab{ID: "tab-a", Title: "A", URL: "https://example.test", Active: actionBoolPointer(true)}
	server.tabAliases["@t1"] = "tab-a"
	server.tabReverse["tab-a"] = "@t1"
	server.nextTabAlias = 1
	server.selectedTab = "tab-a"
	return server, fake
}

func actionBoolPointer(value bool) *bool { return &value }

func focusedCUABrowserRef(raw, name string, actions ...string) map[string]any {
	ref := cuaBrowserRef(raw, name, actions...)
	ref["states"] = map[string]any{"focused": true}
	return ref
}

func exactTestArgs(extra map[string]any) map[string]any {
	args := map[string]any{
		"session": testCUASessionID, "target_id": "target-1", "tab_id": "tab-a",
	}
	for key, value := range extra {
		args[key] = value
	}
	return args
}

func callsNamed(calls []fakeCUACall, name string) []fakeCUACall {
	var selected []fakeCUACall
	for _, call := range calls {
		if call.Name == name {
			selected = append(selected, call)
		}
	}
	return selected
}

func TestCUABrowserNavigateMapsURLAndSemanticSnapshot(t *testing.T) {
	bind := cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "A", true))
	handlers := standardRunningCUAHandlers(bind, cuaBrowserSnapshot(
		"target-1", "tab-a", "- heading Home", cuaBrowserRef("raw-link", "Continue", "click"),
	))
	server, fake := startCUABrowserTestServer(t, handlers)

	_, output, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: " https://example.test/path "})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "ok" || output.ElementCount != 1 || !strings.Contains(output.Snapshot, "@e1") {
		t.Fatalf("output = %#v", output)
	}
	calls := callsNamed(fake.Calls(), "browser_navigate")
	if len(calls) != 1 {
		t.Fatalf("browser_navigate calls = %#v", calls)
	}
	want := exactTestArgs(map[string]any{"url": "https://example.test/path", "snapshot_format": "semantic_v2"})
	if !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_navigate args = %#v, want %#v", calls[0].Args, want)
	}
	if got := server.refs["@e1"]; got.Raw != "raw-link" || !got.Actions["click"] {
		t.Fatalf("installed ref = %#v", got)
	}
}

func TestCUABrowserNavigateBlocksUnsafeURLBeforeCUA(t *testing.T) {
	server, fake := startCUABrowserTestServer(t, nil)
	_, _, err := server.browserNavigate(context.Background(), nil, BrowserInput{URL: "http://127.0.0.1/private"})
	if err == nil {
		t.Fatal("blocked URL succeeded")
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("blocked URL called Cua: %#v", calls)
	}
}

func TestCUABrowserExactArgsCannotOverridePrivateBinding(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	args, err := server.exactArgs(map[string]any{
		"target_id": "attacker-target", "tab_id": "attacker-tab", "session": "attacker-session", "safe": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := exactTestArgs(map[string]any{"safe": true})
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("exact args = %#v, want private binding %#v", args, want)
	}
}

func TestCUABrowserSnapshotMapsExactSemanticReadAndReplacesRefs(t *testing.T) {
	handlers := map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			result, output, err := cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "fresh", cuaBrowserRef("raw-new", "New", "type")))
			result.Content = []mcp.Content{&mcp.TextContent{Text: "driver-owned target-1 raw-new"}}
			return result, output, err
		},
	}
	server, fake := preparedCUABrowserTestServer(t, handlers)
	server.refs["@e9"] = cuaElement{Raw: "raw-old", Actions: map[string]bool{"click": true}}
	server.lastEditable = "raw-old"

	result, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{})
	if err != nil {
		t.Fatal(err)
	}
	if output.ElementCount != 1 || server.refs["@e1"].Raw != "raw-new" || server.refs["@e9"].Raw != "" {
		t.Fatalf("output/state = %#v refs=%#v", output, server.refs)
	}
	if got := firstText(result.Content); got != output.Snapshot || strings.Contains(got, "driver-owned") || strings.Contains(got, "raw-new") {
		t.Fatalf("public result text = %q, snapshot = %q", got, output.Snapshot)
	}
	if server.lastEditable != "" {
		t.Fatalf("ordinary snapshot retained lastEditable %q", server.lastEditable)
	}
	want := exactTestArgs(map[string]any{"snapshot_format": "semantic_v2"})
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("get_browser_state calls = %#v, want %#v", calls, want)
	}
}

func TestCUABrowserSnapshotFullConsumesContinuationsUntilCap(t *testing.T) {
	var mu sync.Mutex
	continuations := []string{}
	handlers := map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			token, _ := args["continuation"].(string)
			continuations = append(continuations, token)
			switch token {
			case "":
				result := cuaBrowserSnapshot("target-1", "tab-a", "first", cuaBrowserRef("raw-1", "One", "click"))
				result["snapshot"].(map[string]any)["complete"] = false
				result["snapshot"].(map[string]any)["continuation"] = "next-1"
				return cuaOK(result)
			case "next-1":
				refs := []map[string]any{cuaBrowserRef("raw-2", "Two", "click")}
				for index := 0; index < 400; index++ {
					refs = append(refs, cuaBrowserRef("bulk-"+strings.Repeat("x", 16), strings.Repeat("long name ", 8), "click"))
				}
				result := cuaBrowserSnapshot("target-1", "tab-a", "second", refs...)
				result["snapshot"].(map[string]any)["complete"] = false
				result["snapshot"].(map[string]any)["continuation"] = "next-2"
				return cuaOK(result)
			default:
				return nil, nil, errors.New("unexpected continuation call " + token)
			}
		},
	}
	server, _ := preparedCUABrowserTestServer(t, handlers)
	_, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(continuations, []string{"", "next-1"}) {
		t.Fatalf("continuations = %#v", continuations)
	}
	if len(output.Snapshot) > maxSnapshotChars || !strings.Contains(output.Snapshot, "snapshot truncated") {
		t.Fatalf("snapshot length/text = %d %q", len(output.Snapshot), output.Snapshot)
	}
	if server.refs["@e1"].Raw != "raw-1" || server.refs["@e2"].Raw != "raw-2" {
		t.Fatalf("continuation refs were not aggregated: %#v", server.refs)
	}
}

func TestCUABrowserSnapshotFullFalseMakesOneRead(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			result := cuaBrowserSnapshot("target-1", "tab-a", "first")
			result["snapshot"].(map[string]any)["complete"] = false
			result["snapshot"].(map[string]any)["continuation"] = "unused"
			return cuaOK(result)
		},
	})
	if _, _, err := server.browserSnapshot(context.Background(), nil, BrowserInput{}); err != nil {
		t.Fatal(err)
	}
	if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != 1 {
		t.Fatalf("get_browser_state calls = %d, want 1", got)
	}
}

func TestCUABrowserSnapshotRetryBudgetSpansAllContinuations(t *testing.T) {
	readCount, bindCount := 0, 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if _, binding := args["pid"]; binding {
				bindCount++
				return cuaOK(cuaBrowserBind("target-2", cuaBrowserTab("tab-b", "B", true)))
			}
			readCount++
			switch readCount {
			case 1:
				return cuaRefused("browser_binding_stale", "first page stale", nil)
			case 2:
				result := cuaBrowserSnapshot("target-2", "tab-b", "first")
				result["snapshot"].(map[string]any)["complete"] = false
				result["snapshot"].(map[string]any)["continuation"] = "next"
				return cuaOK(result)
			default:
				return cuaRefused("browser_binding_stale", "continuation stale", nil)
			}
		},
	})

	result, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{Full: true})
	if err != nil || result == nil || output.Status != "refused" || output.Refusal.Code != "browser_binding_stale" {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if bindCount != 1 {
		t.Fatalf("bind calls = %d, want one retry budget for complete operation; calls=%#v", bindCount, fake.Calls())
	}
}

func TestCUABrowserReadRebindPreservesSelectedTabWithinGeneration(t *testing.T) {
	readCount := 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if _, binding := args["pid"]; binding {
				return cuaOK(cuaBrowserBind("target-1",
					cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)))
			}
			readCount++
			if readCount == 1 {
				return cuaRefused("browser_binding_stale", "stale binding", nil)
			}
			if args["tab_id"] != "tab-a" {
				return nil, nil, errors.New("retry switched logical tab")
			}
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "same generation"))
		},
	})
	_, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{})
	if err != nil || !strings.Contains(output.Snapshot, "same generation") {
		t.Fatalf("output/error = %#v %v; calls=%#v", output, err, fake.Calls())
	}
	if server.selectedTab != "tab-a" {
		t.Fatalf("selected tab = %q, want tab-a", server.selectedTab)
	}
}

func TestCUABrowserClickMapsCapabilityAndRefreshes(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_click": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			result, output, err := cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "clicked", cuaBrowserRef("raw-after", "After", "click")))
			result.Content = []mcp.Content{&mcp.TextContent{Text: "driver-owned raw-after"}}
			return result, output, err
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-click", Actions: map[string]bool{"click": true}}

	result, output, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Snapshot, "clicked") || server.refs["@e1"].Raw != "raw-after" {
		t.Fatalf("output/state = %#v %#v", output, server.refs)
	}
	if got := firstText(result.Content); got != output.Snapshot || strings.Contains(got, "driver-owned") || strings.Contains(got, "raw-after") {
		t.Fatalf("public action result text = %q, snapshot = %q", got, output.Snapshot)
	}
	want := exactTestArgs(map[string]any{"ref": "raw-click", "input_route": "trusted"})
	if calls := callsNamed(fake.Calls(), "browser_click"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_click calls = %#v, want %#v", calls, want)
	}
}

func TestCUABrowserClickRejectsUnknownOrIncapableAliasBeforeCUA(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, nil)
	server.refs["@e1"] = cuaElement{Raw: "raw-type", Actions: map[string]bool{"type": true}}
	for _, ref := range []string{"@e404", "@e1"} {
		if _, _, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: ref}); err == nil {
			t.Fatalf("click %s succeeded", ref)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("invalid aliases called Cua: %#v", calls)
	}
}

func TestCUABrowserTypeMapsInsertAndCachesUniqueFocusedEditable(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_type": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "typed", focusedCUABrowserRef("raw-fresh", "Query", "type")))
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-input", Actions: map[string]bool{"type": true}}

	_, _, err := server.browserType(context.Background(), nil, BrowserInput{Ref: "@e1", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	want := exactTestArgs(map[string]any{"ref": "raw-input", "text": "hello", "mode": "insert_text", "replace": true})
	if calls := callsNamed(fake.Calls(), "browser_type"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_type calls = %#v, want %#v", calls, want)
	}
	if server.lastEditable != "raw-fresh" || server.refs["@e1"].Raw != "raw-fresh" {
		t.Fatalf("post-type state refs=%#v lastEditable=%q", server.refs, server.lastEditable)
	}
}

func TestCUABrowserTypeLeavesEditableEmptyWhenFreshSnapshotIsAmbiguous(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_type": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "typed",
				focusedCUABrowserRef("raw-a", "A", "type"), focusedCUABrowserRef("raw-b", "B", "type")))
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-input", Actions: map[string]bool{"type": true}}
	server.lastEditable = "old-editable"
	if _, _, err := server.browserType(context.Background(), nil, BrowserInput{Ref: "@e1", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if server.lastEditable != "" {
		t.Fatalf("ambiguous focused refs cached %q", server.lastEditable)
	}
}

func TestCUABrowserPressEnterUsesCachedEditableThenRefreshes(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_type": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "submitted"))
		},
	})
	server.lastEditable = "raw-editable"

	_, _, err := server.browserPress(context.Background(), nil, BrowserInput{Key: "Enter"})
	if err != nil {
		t.Fatal(err)
	}
	want := exactTestArgs(map[string]any{"ref": "raw-editable", "text": "\n", "mode": "keystrokes", "replace": false})
	if calls := callsNamed(fake.Calls(), "browser_type"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_type calls = %#v, want %#v", calls, want)
	}
	if calls := callsNamed(fake.Calls(), "press_key"); len(calls) != 0 {
		t.Fatalf("cached Enter used native key: %#v", calls)
	}
	if server.lastEditable != "" {
		t.Fatalf("post-press cache = %q", server.lastEditable)
	}
}

func TestCUABrowserPressGuardsNativeTargetAndMapsKey(t *testing.T) {
	read := 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			read++
			if _, binding := args["pid"]; binding {
				return cuaOK(cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "A", true)))
			}
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "pressed"))
		},
		"press_key": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
	})

	_, _, err := server.browserPress(context.Background(), nil, BrowserInput{Key: "ArrowDown"})
	if err != nil {
		t.Fatal(err)
	}
	if read != 2 {
		t.Fatalf("state reads = %d, want guard + snapshot", read)
	}
	want := map[string]any{"session": testCUASessionID, "pid": float64(91), "window_id": float64(21), "key": "down"}
	calls := callsNamed(fake.Calls(), "press_key")
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("press_key calls = %#v, want %#v", calls, want)
	}
}

func TestCUABrowserPressRefusesAmbiguousOrInactiveNativeTarget(t *testing.T) {
	tests := []struct {
		name string
		tabs []map[string]any
	}{
		{name: "inactive", tabs: []map[string]any{cuaBrowserTab("tab-a", "A", false)}},
		{name: "unknown", tabs: []map[string]any{cuaBrowserTab("tab-a", "A", nil)}},
		{name: "different active", tabs: []map[string]any{cuaBrowserTab("tab-a", "A", false), cuaBrowserTab("tab-b", "B", true)}},
		{name: "ambiguous active", tabs: []map[string]any{cuaBrowserTab("tab-a", "A", true), cuaBrowserTab("tab-b", "B", true)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(cuaBrowserBind("target-1", test.tabs...))
				},
			})
			result, output, err := server.browserPress(context.Background(), nil, BrowserInput{Key: "Tab"})
			if err != nil {
				t.Fatalf("guard error = %v, want structured refusal", err)
			}
			if result == nil || output.Status != "refused" || output.Refusal == nil {
				t.Fatalf("output = %#v", output)
			}
			if calls := callsNamed(fake.Calls(), "press_key"); len(calls) != 0 {
				t.Fatalf("guarded key was delivered: %#v", calls)
			}
		})
	}
}

func TestCUABrowserEnterWithoutEditableRefUsesSameGuard(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "A", false)))
		},
	})
	result, output, err := server.browserPress(context.Background(), nil, BrowserInput{Key: "Enter"})
	if err != nil || result == nil || output.Status != "refused" {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if len(callsNamed(fake.Calls(), "browser_type")) != 0 || len(callsNamed(fake.Calls(), "press_key")) != 0 {
		t.Fatalf("unguarded Enter delivery: %#v", fake.Calls())
	}
}

func TestCUABrowserScrollMapsViewportCenterAndDelta(t *testing.T) {
	stateReads := 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			stateReads++
			result := cuaBrowserSnapshot("target-1", "tab-a", "scrolled")
			if stateReads == 1 {
				result["screenshot"] = map[string]any{
					"mime_type": "image/png", "width": 1200, "height": 800,
					"viewport_css_width": 1000.0, "viewport_css_height": 600.0,
				}
			}
			return cuaOK(result)
		},
		"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
	})

	_, _, err := server.browserScroll(context.Background(), nil, BrowserInput{Direction: "up"})
	if err != nil {
		t.Fatal(err)
	}
	want := exactTestArgs(map[string]any{
		"action": "scroll", "input_route": "trusted", "x": 500.0, "y": 300.0,
		"delta_x": 0.0, "delta_y": -540.0,
	})
	calls := callsNamed(fake.Calls(), "browser_pointer")
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_pointer calls = %#v, want %#v", calls, want)
	}
	preflight := callsNamed(fake.Calls(), "get_browser_state")[0].Args
	if preflight["include_screenshot"] != true || preflight["snapshot_format"] != "semantic_v2" {
		t.Fatalf("scroll preflight args = %#v", preflight)
	}
}

func TestCUABrowserScrollRejectsInvalidMetricsBeforeMutation(t *testing.T) {
	for _, height := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		t.Run("metric", func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					result := cuaBrowserSnapshot("target-1", "tab-a", "")
					result["screenshot"] = map[string]any{"mime_type": "image/png", "width": 1, "height": 1, "viewport_css_width": 10.0, "viewport_css_height": height}
					return cuaOK(result)
				},
			})
			server.refs["@e1"] = cuaElement{Raw: "old-ref", Actions: map[string]bool{"click": true}}
			server.lastEditable = "old-editable"
			if _, _, err := server.browserScroll(context.Background(), nil, BrowserInput{Direction: "down"}); err == nil {
				t.Fatal("invalid metrics succeeded")
			}
			if len(callsNamed(fake.Calls(), "browser_pointer")) != 0 {
				t.Fatalf("invalid metrics mutated: %#v", fake.Calls())
			}
			if !math.IsNaN(height) && !math.IsInf(height, 0) && (len(server.refs) != 0 || server.lastEditable != "") {
				t.Fatalf("invalid unseen screenshot retained aliases: %#v %q", server.refs, server.lastEditable)
			}
		})
	}
}

func TestCUABrowserScrollRefusalDoesNotInstallPreflightRefs(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			result := cuaBrowserSnapshot("target-1", "tab-a", "unseen", cuaBrowserRef("unseen-preflight", "Hidden", "click"))
			result["screenshot"] = map[string]any{
				"mime_type": "image/png", "width": 100, "height": 100,
				"viewport_css_width": 100.0, "viewport_css_height": 100.0,
			}
			return cuaOK(result)
		},
		"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("browser_consent_required", "consent required", nil)
		},
	})
	result, output, err := server.browserScroll(context.Background(), nil, BrowserInput{Direction: "down"})
	if err != nil || result == nil || output.Status != "refused" {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if len(server.refs) != 0 || server.lastEditable != "" {
		t.Fatalf("scroll retained unseen preflight capabilities: %#v %q", server.refs, server.lastEditable)
	}
}

func TestCUABrowserVisionMapsScreenshotAndFlattensContent(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			structured := cuaBrowserSnapshot("target-1", "tab-a", "private raw outline", cuaBrowserRef("secret-ref", "Secret", "click"))
			structured["screenshot"] = map[string]any{
				"source": "cdp_tab", "scope": "viewport", "mime_type": "image/png",
				"width": 2, "height": 1, "coordinate_space": "viewport_css_px",
				"viewport_css_width": 2.0, "viewport_css_height": 1.0,
			}
			result, output, err := cuaOK(structured)
			result.Content = []mcp.Content{
				&mcp.TextContent{Text: "driver-owned secret text"},
				&mcp.ImageContent{MIMEType: "image/png", Data: []byte("png-data")},
			}
			return result, output, err
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "old", Actions: map[string]bool{"click": true}}
	server.lastEditable = "old-editable"

	result, output, err := server.browserVision(context.Background(), nil, BrowserInput{Question: "what changed?"})
	if err != nil {
		t.Fatal(err)
	}
	if output != (BrowserOutput{}) {
		t.Fatalf("vision structured output = %#v", output)
	}
	if len(result.Content) != 2 {
		t.Fatalf("vision content = %#v", result.Content)
	}
	label, labelOK := result.Content[0].(*mcp.TextContent)
	image, imageOK := result.Content[1].(*mcp.ImageContent)
	if !labelOK || label.Text != "screenshot for: what changed?" || !imageOK || image.MIMEType != "image/png" || string(image.Data) != "png-data" {
		t.Fatalf("vision content = %#v", result.Content)
	}
	if len(server.refs) != 0 || server.lastEditable != "" {
		t.Fatalf("vision retained hidden capabilities: %#v %q", server.refs, server.lastEditable)
	}
	want := exactTestArgs(map[string]any{"include_screenshot": true, "snapshot_format": "semantic_v2"})
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("vision read calls = %#v, want %#v", calls, want)
	}
}

func TestCUABrowserVisionRejectsMissingOrAmbiguousPNG(t *testing.T) {
	tests := []struct {
		name    string
		content []mcp.Content
	}{
		{name: "missing"},
		{name: "empty", content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png"}}},
		{name: "wrong mime", content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/jpeg", Data: []byte("x")}}},
		{name: "ambiguous", content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("x")}, &mcp.ImageContent{MIMEType: "image/png", Data: []byte("y")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					structured := cuaBrowserSnapshot("target-1", "tab-a", "", cuaBrowserRef("unseen-invalid", "Hidden", "click"))
					structured["screenshot"] = map[string]any{"mime_type": "image/png", "width": 1, "height": 1, "viewport_css_width": 1.0, "viewport_css_height": 1.0}
					result, output, err := cuaOK(structured)
					result.Content = test.content
					return result, output, err
				},
			})
			server.refs["@e1"] = cuaElement{Raw: "old-ref", Actions: map[string]bool{"click": true}}
			server.lastEditable = "old-editable"
			if _, _, err := server.browserVision(context.Background(), nil, BrowserInput{}); err == nil {
				t.Fatal("invalid screenshot succeeded")
			}
			if len(server.refs) != 0 || server.lastEditable != "" {
				t.Fatalf("invalid unseen screenshot retained aliases: %#v %q", server.refs, server.lastEditable)
			}
		})
	}
}

func TestCUABrowserReadRebindsAndRetriesOnceOnStaleBinding(t *testing.T) {
	readCount := 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if _, bind := args["pid"]; bind {
				return cuaOK(cuaBrowserBind("target-2", cuaBrowserTab("tab-b", "B", true)))
			}
			readCount++
			if readCount == 1 {
				return cuaRefused("browser_binding_stale", "target target-1 is stale", nil)
			}
			return cuaOK(cuaBrowserSnapshot("target-2", "tab-b", "rebound"))
		},
	})

	_, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{})
	if err != nil || !strings.Contains(output.Snapshot, "rebound") {
		t.Fatalf("output/error = %#v %v", output, err)
	}
	if got := fakeCallNames(fake.Calls()); !reflect.DeepEqual(got, []string{"get_browser_state", "get_browser_state", "get_browser_state"}) {
		t.Fatalf("calls = %v", got)
	}
	if server.targetID != "target-2" || server.selectedTab != "tab-b" {
		t.Fatalf("rebound state target=%q tab=%q", server.targetID, server.selectedTab)
	}
}

func TestCUABrowserReadNeverRetriesTwice(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"get_browser_state": func(args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			if _, bind := args["pid"]; bind {
				return cuaOK(cuaBrowserBind("target-2", cuaBrowserTab("tab-b", "B", true)))
			}
			return cuaRefused("browser_reconnect_exhausted", "still stale", nil)
		},
	})
	result, output, err := server.browserSnapshot(context.Background(), nil, BrowserInput{})
	if err != nil || result == nil || output.Status != "refused" || output.Refusal.Code != "browser_reconnect_exhausted" {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != 3 {
		t.Fatalf("state calls = %d, want read + bind + one retry", got)
	}
}

func TestCUABrowserMutationNeverRetries(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_click": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("browser_binding_stale", "stale", nil)
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-click", Actions: map[string]bool{"click": true}}
	result, output, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
	if err != nil || result == nil || output.Status != "refused" {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if len(callsNamed(fake.Calls(), "browser_click")) != 1 || len(callsNamed(fake.Calls(), "get_browser_state")) != 0 {
		t.Fatalf("mutation retried or rebound: %#v", fake.Calls())
	}
}

func TestCUABrowserMutationReportsPostActionSnapshotFailure(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_click": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(nil) },
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return nil, nil, errors.New("verification unavailable")
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-click", Actions: map[string]bool{"click": true}}
	_, _, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
	if err == nil || !strings.Contains(err.Error(), "post-action snapshot") {
		t.Fatalf("error = %v", err)
	}
	if len(callsNamed(fake.Calls(), "browser_click")) != 1 || len(callsNamed(fake.Calls(), "get_browser_state")) != 1 {
		t.Fatalf("calls = %#v", fake.Calls())
	}
}

func TestCUABrowserPreservesStructuredRefusal(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_click": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("browser_consent_required", "consent needed for raw-click", map[string]any{"retryable": false})
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-click", Actions: map[string]bool{"click": true}}
	result, output, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
	if err != nil || result == nil || output.Refusal == nil {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	if output.Refusal.Code != "browser_consent_required" || !strings.Contains(output.Refusal.Message, "@e1") || !reflect.DeepEqual(output.Refusal.Detail, map[string]any{"retryable": false}) {
		t.Fatalf("refusal = %#v", output.Refusal)
	}
}

func TestCUABrowserSeparatesTransportErrorFromRefusal(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	server.runtime.client = &failingCUAClient{err: errors.New("wire down")}
	server.refs["@e1"] = cuaElement{Raw: "raw-click", Actions: map[string]bool{"click": true}}
	result, output, err := server.browserClick(context.Background(), nil, BrowserInput{Ref: "@e1"})
	if err == nil || result != nil || output.Refusal != nil || !strings.Contains(err.Error(), "wire down") {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
}
