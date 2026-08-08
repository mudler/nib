package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

func advancedFloat(value float64) *float64 { return &value }

func TestCUABrowserPointerAndDialogSchemasExposeExactEnums(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- startCUABrowserMCPServer(ctx, serverTransport, types.Config{}, &cuaRuntime{sessionID: testCUASessionID})
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "advanced-browser-contract-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect to Cua browser MCP server: %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list Cua browser tools: %v", err)
	}

	wantEnums := map[string]map[string][]any{
		"browser_pointer": {
			"action":      {"hover", "right_click", "double_click", "scroll", "drag"},
			"input_route": {"trusted", "dom_event"},
		},
		"browser_dialog": {
			"action":        {"inspect", "accept", "dismiss"},
			"delivery_mode": {"background", "foreground"},
		},
	}
	found := make(map[string]bool)
	for _, tool := range listed.Tools {
		properties, wanted := wantEnums[tool.Name]
		if !wanted {
			continue
		}
		found[tool.Name] = true
		schemaProperties := browserToolSchemaProperties(t, tool)
		for property, want := range properties {
			if got := browserToolPropertyEnum(t, tool.Name, schemaProperties, property); !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s enum = %#v, want %#v", tool.Name, property, got, want)
			}
		}
	}
	if !found["browser_pointer"] || !found["browser_dialog"] {
		t.Fatalf("Cua-only tools found = %#v, want pointer and dialog", found)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil && !errorsIsContextCancellation(err) {
			t.Fatalf("Cua browser MCP server shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cua browser MCP server did not stop after cancellation")
	}
}

func TestCUABrowserPointerMapsEverySupportedLocationShape(t *testing.T) {
	tests := []struct {
		name string
		in   BrowserPointerInput
		refs map[string]cuaElement
		want map[string]any
	}{
		{
			name: "ref hover defaults trusted",
			in:   BrowserPointerInput{Action: "hover", Ref: "@e1"},
			refs: map[string]cuaElement{"@e1": {Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}},
			want: map[string]any{"action": "hover", "input_route": "trusted", "ref": "raw-origin"},
		},
		{
			name: "coordinate right click preserves zero",
			in:   BrowserPointerInput{Action: "right_click", InputRoute: "trusted", X: advancedFloat(0), Y: advancedFloat(0)},
			want: map[string]any{"action": "right_click", "input_route": "trusted", "x": float64(0), "y": float64(0)},
		},
		{
			name: "ref double click uses dom event",
			in:   BrowserPointerInput{Action: "double_click", InputRoute: "dom_event", Ref: "@e1"},
			refs: map[string]cuaElement{"@e1": {Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}},
			want: map[string]any{"action": "double_click", "input_route": "dom_event", "ref": "raw-origin"},
		},
		{
			name: "scroll ref accepts scroll capability and one delta",
			in:   BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaY: advancedFloat(240)},
			refs: map[string]cuaElement{"@e1": {Raw: "raw-scroll", Actions: map[string]bool{"scroll": true}}},
			want: map[string]any{"action": "scroll", "input_route": "trusted", "ref": "raw-scroll", "delta_y": float64(240)},
		},
		{
			name: "scroll ref accepts pointer capability and explicit zero delta",
			in:   BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaX: advancedFloat(0), DeltaY: advancedFloat(-90)},
			refs: map[string]cuaElement{"@e1": {Raw: "raw-scroll", Actions: map[string]bool{"pointer": true}}},
			want: map[string]any{"action": "scroll", "input_route": "trusted", "ref": "raw-scroll", "delta_x": float64(0), "delta_y": float64(-90)},
		},
		{
			name: "drag ref to ref",
			in:   BrowserPointerInput{Action: "drag", Ref: "@e1", DestinationRef: "@e2"},
			refs: map[string]cuaElement{
				"@e1": {Raw: "raw-origin", Actions: map[string]bool{"pointer": true}},
				"@e2": {Raw: "raw-destination", Actions: map[string]bool{"pointer": true}},
			},
			want: map[string]any{"action": "drag", "input_route": "trusted", "ref": "raw-origin", "destination_ref": "raw-destination"},
		},
		{
			name: "drag ref to coordinates",
			in:   BrowserPointerInput{Action: "drag", Ref: "@e1", ToX: advancedFloat(0), ToY: advancedFloat(20)},
			refs: map[string]cuaElement{"@e1": {Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}},
			want: map[string]any{"action": "drag", "input_route": "trusted", "ref": "raw-origin", "to_x": float64(0), "to_y": float64(20)},
		},
		{
			name: "drag coordinates to coordinates",
			in: BrowserPointerInput{
				Action: "drag", X: advancedFloat(1), Y: advancedFloat(2), ToX: advancedFloat(3), ToY: advancedFloat(4),
			},
			want: map[string]any{
				"action": "drag", "input_route": "trusted", "x": float64(1), "y": float64(2),
				"to_x": float64(3), "to_y": float64(4),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					route := test.in.InputRoute
					if route == "" {
						route = "trusted"
					}
					return cuaOK(map[string]any{
						"target_id": "target-1", "tab_id": "tab-a", "action": test.in.Action, "route": route,
					})
				},
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "pointer complete", cuaBrowserRef("raw-fresh", "Fresh", "click")))
				},
			})
			for alias, ref := range test.refs {
				server.refs[alias] = ref
			}

			_, output, err := server.browserPointer(context.Background(), nil, test.in)
			if err != nil {
				t.Fatal(err)
			}
			if output.Status != "ok" || !strings.Contains(output.Snapshot, "pointer complete") {
				t.Fatalf("output = %#v", output)
			}
			calls := callsNamed(fake.Calls(), "browser_pointer")
			if len(calls) != 1 {
				t.Fatalf("browser_pointer calls = %#v", calls)
			}
			if want := exactTestArgs(test.want); !reflect.DeepEqual(calls[0].Args, want) {
				t.Fatalf("browser_pointer args = %#v, want %#v", calls[0].Args, want)
			}
			if got := server.refs["@e1"].Raw; got != "raw-fresh" {
				t.Fatalf("post-pointer refs = %#v", server.refs)
			}
		})
	}
}

func TestCUABrowserPointerRejectsInvalidShapesBeforeCUA(t *testing.T) {
	tests := []struct {
		name string
		in   BrowserPointerInput
	}{
		{name: "missing action", in: BrowserPointerInput{Ref: "@e1"}},
		{name: "unknown action", in: BrowserPointerInput{Action: "click", Ref: "@e1"}},
		{name: "unknown route", in: BrowserPointerInput{Action: "hover", InputRoute: "native", Ref: "@e1"}},
		{name: "missing origin", in: BrowserPointerInput{Action: "hover"}},
		{name: "ref and coordinates", in: BrowserPointerInput{Action: "hover", Ref: "@e1", X: advancedFloat(1), Y: advancedFloat(2)}},
		{name: "origin x only", in: BrowserPointerInput{Action: "hover", X: advancedFloat(1)}},
		{name: "origin y only", in: BrowserPointerInput{Action: "hover", Y: advancedFloat(2)}},
		{name: "origin nan", in: BrowserPointerInput{Action: "hover", X: advancedFloat(math.NaN()), Y: advancedFloat(2)}},
		{name: "origin infinity", in: BrowserPointerInput{Action: "hover", X: advancedFloat(1), Y: advancedFloat(math.Inf(1))}},
		{name: "dom event coordinates", in: BrowserPointerInput{Action: "hover", InputRoute: "dom_event", X: advancedFloat(1), Y: advancedFloat(2)}},
		{name: "drag missing destination", in: BrowserPointerInput{Action: "drag", Ref: "@e1"}},
		{name: "drag destination ref and coordinates", in: BrowserPointerInput{Action: "drag", Ref: "@e1", DestinationRef: "@e2", ToX: advancedFloat(3), ToY: advancedFloat(4)}},
		{name: "drag destination x only", in: BrowserPointerInput{Action: "drag", Ref: "@e1", ToX: advancedFloat(3)}},
		{name: "drag destination y only", in: BrowserPointerInput{Action: "drag", Ref: "@e1", ToY: advancedFloat(4)}},
		{name: "drag destination nan", in: BrowserPointerInput{Action: "drag", Ref: "@e1", ToX: advancedFloat(math.NaN()), ToY: advancedFloat(4)}},
		{name: "coordinate origin to ref cannot prove frame", in: BrowserPointerInput{Action: "drag", X: advancedFloat(1), Y: advancedFloat(2), DestinationRef: "@e2"}},
		{name: "non drag destination ref", in: BrowserPointerInput{Action: "hover", Ref: "@e1", DestinationRef: "@e2"}},
		{name: "non drag destination coordinates", in: BrowserPointerInput{Action: "right_click", Ref: "@e1", ToX: advancedFloat(3), ToY: advancedFloat(4)}},
		{name: "scroll missing delta", in: BrowserPointerInput{Action: "scroll", Ref: "@e1"}},
		{name: "scroll both deltas zero", in: BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaX: advancedFloat(0), DeltaY: advancedFloat(0)}},
		{name: "scroll delta nan", in: BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaX: advancedFloat(math.NaN())}},
		{name: "scroll delta infinity", in: BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaY: advancedFloat(math.Inf(-1))}},
		{name: "non scroll delta", in: BrowserPointerInput{Action: "hover", Ref: "@e1", DeltaX: advancedFloat(0)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			server.refs["@e1"] = cuaElement{Raw: "raw-origin", Actions: map[string]bool{"pointer": true, "scroll": true}}
			server.refs["@e2"] = cuaElement{Raw: "raw-destination", Actions: map[string]bool{"pointer": true}}
			if _, _, err := server.browserPointer(context.Background(), nil, test.in); err == nil {
				t.Fatal("invalid pointer input succeeded")
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("invalid pointer input called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserPointerEnforcesCurrentRefCapabilities(t *testing.T) {
	tests := []struct {
		name string
		in   BrowserPointerInput
		refs map[string]cuaElement
	}{
		{name: "unknown origin", in: BrowserPointerInput{Action: "hover", Ref: "@e404"}},
		{name: "hover needs pointer", in: BrowserPointerInput{Action: "hover", Ref: "@e1"}, refs: map[string]cuaElement{"@e1": {Raw: "raw", Actions: map[string]bool{"click": true}}}},
		{name: "right click needs pointer", in: BrowserPointerInput{Action: "right_click", Ref: "@e1"}, refs: map[string]cuaElement{"@e1": {Raw: "raw", Actions: map[string]bool{"scroll": true}}}},
		{name: "double click needs pointer", in: BrowserPointerInput{Action: "double_click", Ref: "@e1"}, refs: map[string]cuaElement{"@e1": {Raw: "raw", Actions: map[string]bool{"click": true}}}},
		{name: "scroll needs scroll or pointer", in: BrowserPointerInput{Action: "scroll", Ref: "@e1", DeltaY: advancedFloat(1)}, refs: map[string]cuaElement{"@e1": {Raw: "raw", Actions: map[string]bool{"click": true}}}},
		{
			name: "drag destination needs pointer",
			in:   BrowserPointerInput{Action: "drag", Ref: "@e1", DestinationRef: "@e2"},
			refs: map[string]cuaElement{
				"@e1": {Raw: "raw-origin", Actions: map[string]bool{"pointer": true}},
				"@e2": {Raw: "raw-destination", Actions: map[string]bool{"click": true}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			for alias, ref := range test.refs {
				server.refs[alias] = ref
			}
			if _, _, err := server.browserPointer(context.Background(), nil, test.in); err == nil {
				t.Fatal("incapable pointer ref succeeded")
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("incapable ref called Cua: %#v", calls)
			}
		})
	}
}

func TestCUABrowserPointerNeverRetriesAndInvalidatesBeforeVerification(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "action": "hover", "route": "trusted",
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("browser_binding_stale", "stale after pointer", nil)
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-secret"

	_, output, err := server.browserPointer(context.Background(), nil, BrowserPointerInput{Action: "hover", Ref: "@e1"})
	if err != nil || output.Status != "refused" || output.Refusal == nil {
		t.Fatalf("output/error = %#v %v, want structured post-action refusal", output, err)
	}
	if got := len(callsNamed(fake.Calls(), "browser_pointer")); got != 1 {
		t.Fatalf("browser_pointer calls = %d, want one", got)
	}
	if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != 1 {
		t.Fatalf("verification calls = %d, want one without rebind retry", got)
	}
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" {
		t.Fatalf("stale capabilities survived pointer mutation: refs=%#v editable=%q dialog=%q", server.refs, server.lastEditable, server.dialogID)
	}
}

func TestCUABrowserPointerTransportFailureClearsCapabilities(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	server.runtime.client = &failingCUAClient{err: context.DeadlineExceeded}
	server.refs["@e1"] = cuaElement{Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-old"
	server.dialogKind = "alert"

	_, _, err := server.browserPointer(context.Background(), nil, BrowserPointerInput{Action: "hover", Ref: "@e1"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want pointer deadline", err)
	}
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("uncertain pointer retained capabilities: refs=%#v editable=%q dialog=%q/%q",
			server.refs, server.lastEditable, server.dialogID, server.dialogKind)
	}
}

func TestCUABrowserPointerValidatesMutationResultBeforeSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
	}{
		{name: "missing target", response: map[string]any{"tab_id": "tab-a", "action": "hover", "route": "trusted"}},
		{name: "missing tab", response: map[string]any{"target_id": "target-1", "action": "hover", "route": "trusted"}},
		{name: "missing action", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "route": "trusted"}},
		{name: "missing route", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "action": "hover"}},
		{name: "wrong target", response: map[string]any{"target_id": "target-other", "tab_id": "tab-a", "action": "hover", "route": "trusted"}},
		{name: "wrong tab", response: map[string]any{"target_id": "target-1", "tab_id": "tab-other", "action": "hover", "route": "trusted"}},
		{name: "wrong action", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "action": "drag", "route": "trusted"}},
		{name: "wrong route", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "action": "hover", "route": "dom_event"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(test.response)
				},
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "must not snapshot", cuaBrowserRef("raw-new", "New", "click")))
				},
			})
			server.refs["@e1"] = cuaElement{Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}
			server.lastEditable = "raw-editable"
			server.dialogID = "dialog-old"
			server.dialogKind = "alert"

			_, _, err := server.browserPointer(context.Background(), nil, BrowserPointerInput{Action: "hover", Ref: "@e1"})
			if err == nil {
				t.Fatal("malformed pointer success was accepted")
			}
			if got := len(callsNamed(fake.Calls(), "browser_pointer")); got != 1 {
				t.Fatalf("pointer calls = %d, want one", got)
			}
			if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 0 {
				t.Fatalf("malformed pointer success was snapshot-verified: %#v", calls)
			}
			if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
				t.Fatalf("pointer validation failure retained capabilities: refs=%#v editable=%q dialog=%q/%q",
					server.refs, server.lastEditable, server.dialogID, server.dialogKind)
			}
		})
	}
}

func TestCUABrowserPointerRejectsUnknownResultStatusBeforeSnapshot(t *testing.T) {
	const secret = "partial raw-origin raw-editable dialog-old backend-secret"
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_pointer": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"status": secret, "target_id": "target-1", "tab_id": "tab-a",
				"action": "hover", "route": "trusted",
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "must not snapshot"))
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-origin", Actions: map[string]bool{"pointer": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-old"
	server.dialogKind = "alert"

	result, output, err := server.browserPointer(context.Background(), nil, BrowserPointerInput{Action: "hover", Ref: "@e1"})
	if err == nil || result != nil || output.Status != "" {
		t.Fatalf("unknown pointer status result/output/error = %#v %#v %v", result, output, err)
	}
	if got := len(callsNamed(fake.Calls(), "browser_pointer")); got != 1 {
		t.Fatalf("pointer calls = %d, want one", got)
	}
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 0 {
		t.Fatalf("unknown pointer status was snapshot-verified: %#v", calls)
	}
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("unknown pointer status retained capabilities: refs=%#v editable=%q dialog=%q/%q",
			server.refs, server.lastEditable, server.dialogID, server.dialogKind)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "raw-origin") || strings.Contains(err.Error(), "backend-secret") {
		t.Fatalf("unknown pointer status leaked backend text: %v", err)
	}
}

func TestCUABrowserDialogInspectReturnsOnlyCurrentCapability(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "present": true,
				"dialog_id": "dialog-7", "kind": "prompt",
			})
		},
	})
	server.dialogID = "dialog-old"

	result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "ok" || !output.Present || output.DialogID != "dialog-7" || output.Kind != "prompt" ||
		output.Action != "" || output.Snapshot != "" || output.ElementCount != 0 {
		t.Fatalf("inspect output = %#v", output)
	}
	if got := firstText(result.Content); strings.Contains(got, "target-1") || strings.Contains(got, "tab-a") {
		t.Fatalf("inspect text leaked binding: %q", got)
	}
	want := exactTestArgs(map[string]any{"action": "inspect", "delivery_mode": "background"})
	if calls := callsNamed(fake.Calls(), "browser_dialog"); len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, want) {
		t.Fatalf("browser_dialog inspect calls = %#v, want %#v", calls, want)
	}
	if server.dialogID != "dialog-7" || server.dialogKind != "prompt" {
		t.Fatalf("cached dialog = %q/%q", server.dialogID, server.dialogKind)
	}
}

func TestCUABrowserDialogInspectClearsAbsentOrMalformedCapability(t *testing.T) {
	tests := []struct {
		name      string
		response  map[string]any
		wantError bool
	}{
		{name: "absent", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "present": false}},
		{name: "present missing id", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "present": true, "kind": "alert"}, wantError: true},
		{name: "present missing kind", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "present": true, "dialog_id": "dialog-8"}, wantError: true},
		{name: "unsupported kind", response: map[string]any{"target_id": "target-1", "tab_id": "tab-a", "present": true, "dialog_id": "dialog-8", "kind": "permission"}, wantError: true},
		{name: "wrong target", response: map[string]any{"target_id": "target-new", "tab_id": "tab-a", "present": true, "dialog_id": "dialog-8", "kind": "alert"}, wantError: true},
		{name: "wrong tab", response: map[string]any{"target_id": "target-1", "tab_id": "tab-b", "present": true, "dialog_id": "dialog-8", "kind": "alert"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) { return cuaOK(test.response) },
			})
			server.dialogID = "dialog-old"
			server.dialogKind = "confirm"
			_, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "inspect"})
			if test.wantError {
				if err == nil {
					t.Fatalf("malformed inspect succeeded with %#v", output)
				}
			} else if err != nil || output.Present || output.DialogID != "" || output.Kind != "" {
				t.Fatalf("absent inspect output/error = %#v %v", output, err)
			}
			if server.dialogID != "" || server.dialogKind != "" {
				t.Fatalf("inspect retained stale dialog = %q/%q", server.dialogID, server.dialogKind)
			}
		})
	}
}

func TestCUABrowserDialogInspectRebindsAndRetriesOnce(t *testing.T) {
	inspects := 0
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			inspects++
			if inspects == 1 {
				return cuaRefused("browser_binding_stale", "stale target", nil)
			}
			return cuaOK(map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "present": true,
				"dialog_id": "dialog-new", "kind": "alert",
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "A", true)))
		},
	})
	_, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "inspect"})
	if err != nil || output.DialogID != "dialog-new" {
		t.Fatalf("output/error = %#v %v", output, err)
	}
	if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 2 {
		t.Fatalf("browser_dialog calls = %d, want initial plus one retry", got)
	}
	if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != 1 {
		t.Fatalf("rebind calls = %d, want one", got)
	}
}

func TestCUABrowserDialogInspectUnknownStatusClearsOnlyDialogCapability(t *testing.T) {
	const secret = "partial target-1 tab-a dialog-old backend-secret"
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{"status": secret})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserBind("target-1", cuaBrowserTab("tab-a", "must not rebind", true)))
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-old", Actions: map[string]bool{"click": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-old"
	server.dialogKind = "confirm"

	result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "inspect"})
	if err == nil || result != nil || output.Status != "" {
		t.Fatalf("unknown inspect status result/output/error = %#v %#v %v", result, output, err)
	}
	if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 1 {
		t.Fatalf("inspect calls = %d, want one", got)
	}
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 0 {
		t.Fatalf("unknown inspect status triggered rebind/snapshot: %#v", calls)
	}
	if server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("unknown inspect status retained dialog = %q/%q", server.dialogID, server.dialogKind)
	}
	if server.refs["@e1"].Raw != "raw-old" || server.lastEditable != "raw-editable" {
		t.Fatalf("read-only inspect cleared unrelated capabilities: refs=%#v editable=%q", server.refs, server.lastEditable)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "target-1") || strings.Contains(err.Error(), "dialog-old") {
		t.Fatalf("unknown inspect status leaked backend text: %v", err)
	}
}

func TestCUABrowserDialogResolutionRequiresCurrentCapabilityAndValidPrompt(t *testing.T) {
	tests := []struct {
		name string
		kind string
		in   BrowserDialogInput
	}{
		{name: "inspect rejects dialog id", kind: "alert", in: BrowserDialogInput{Action: "inspect", DialogID: "dialog-1"}},
		{name: "inspect rejects prompt text", kind: "prompt", in: BrowserDialogInput{Action: "inspect", PromptText: pointerToString("")}},
		{name: "accept needs id", kind: "alert", in: BrowserDialogInput{Action: "accept"}},
		{name: "dismiss needs id", kind: "confirm", in: BrowserDialogInput{Action: "dismiss"}},
		{name: "stale id", kind: "alert", in: BrowserDialogInput{Action: "accept", DialogID: "dialog-stale"}},
		{name: "dismiss rejects prompt", kind: "prompt", in: BrowserDialogInput{Action: "dismiss", DialogID: "dialog-1", PromptText: pointerToString("secret")}},
		{name: "accept non-prompt rejects prompt", kind: "confirm", in: BrowserDialogInput{Action: "accept", DialogID: "dialog-1", PromptText: pointerToString("secret")}},
		{name: "unknown action", kind: "alert", in: BrowserDialogInput{Action: "close", DialogID: "dialog-1"}},
		{name: "unknown delivery", kind: "alert", in: BrowserDialogInput{Action: "accept", DialogID: "dialog-1", DeliveryMode: "native"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, nil)
			server.dialogID = "dialog-1"
			server.dialogKind = test.kind
			if _, _, err := server.browserDialog(context.Background(), nil, test.in); err == nil {
				t.Fatal("invalid dialog resolution succeeded")
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Fatalf("invalid dialog input called Cua: %#v", calls)
			}
		})
	}
}

func pointerToString(value string) *string { return &value }

func TestCUABrowserDialogResolvesAndRefreshesSnapshot(t *testing.T) {
	tests := []struct {
		name string
		kind string
		in   BrowserDialogInput
		want map[string]any
	}{
		{
			name: "accept alert defaults background", kind: "alert",
			in:   BrowserDialogInput{Action: "accept", DialogID: "dialog-1"},
			want: map[string]any{"action": "accept", "dialog_id": "dialog-1", "delivery_mode": "background"},
		},
		{
			name: "accept prompt preserves explicit empty text", kind: "prompt",
			in:   BrowserDialogInput{Action: "accept", DialogID: "dialog-1", PromptText: pointerToString("")},
			want: map[string]any{"action": "accept", "dialog_id": "dialog-1", "delivery_mode": "background", "prompt_text": ""},
		},
		{
			name: "dismiss confirm preserves foreground", kind: "confirm",
			in:   BrowserDialogInput{Action: "dismiss", DialogID: "dialog-1", DeliveryMode: "foreground"},
			want: map[string]any{"action": "dismiss", "dialog_id": "dialog-1", "delivery_mode": "foreground"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(map[string]any{
						"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
						"kind": test.kind, "action": test.in.Action,
					})
				},
				"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "dialog resolved", cuaBrowserRef("raw-fresh", "Fresh", "click")))
				},
			})
			server.dialogID = "dialog-1"
			server.dialogKind = test.kind

			_, output, err := server.browserDialog(context.Background(), nil, test.in)
			if err != nil {
				t.Fatal(err)
			}
			if output.Status != "ok" || output.Action != test.in.Action || output.DialogID != "dialog-1" ||
				output.Kind != test.kind || !strings.Contains(output.Snapshot, "dialog resolved") || output.ElementCount != 1 {
				t.Fatalf("resolution output = %#v", output)
			}
			if server.dialogID != "" || server.dialogKind != "" {
				t.Fatalf("resolved dialog remained cached: %q/%q", server.dialogID, server.dialogKind)
			}
			calls := callsNamed(fake.Calls(), "browser_dialog")
			if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, exactTestArgs(test.want)) {
				t.Fatalf("browser_dialog calls = %#v, want %#v", calls, exactTestArgs(test.want))
			}
		})
	}
}

func TestCUABrowserDialogResolutionNeverRetriesAndClearsImmediately(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
				"kind": "alert", "action": "accept",
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused("browser_binding_stale", "snapshot stale", nil)
		},
	})
	server.dialogID = "dialog-1"
	server.dialogKind = "alert"

	_, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "accept", DialogID: "dialog-1"})
	if err != nil || output.Status != "refused" || output.Refusal == nil {
		t.Fatalf("output/error = %#v %v, want structured post-action refusal", output, err)
	}
	if server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("successful resolution retained dialog: %q/%q", server.dialogID, server.dialogKind)
	}
	if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 1 {
		t.Fatalf("browser_dialog calls = %d, want one", got)
	}
	if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != 1 {
		t.Fatalf("snapshot calls = %d, want one without rebind retry", got)
	}
}

func TestCUABrowserDialogSuccessfulResolutionClearsAllCapabilitiesBeforeValidation(t *testing.T) {
	tests := []struct {
		name             string
		resolution       map[string]any
		snapshot         fakeCUAHandler
		wantSnapshotCall bool
	}{
		{
			name: "response validation failure",
			resolution: map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
				"kind": "prompt", "action": "dismiss",
			},
		},
		{
			name: "snapshot transport failure",
			resolution: map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
				"kind": "prompt", "action": "accept",
			},
			snapshot: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return nil, nil, errors.New("snapshot transport failed")
			},
			wantSnapshotCall: true,
		},
		{
			name: "snapshot protocol failure",
			resolution: map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
				"kind": "prompt", "action": "accept",
			},
			snapshot: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return &mcp.CallToolResult{IsError: true}, nil, nil
			},
			wantSnapshotCall: true,
		},
		{
			name: "snapshot non stale refusal",
			resolution: map[string]any{
				"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
				"kind": "prompt", "action": "accept",
			},
			snapshot: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return cuaRefused("browser_consent_required", "verification refused", nil)
			},
			wantSnapshotCall: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
					return cuaOK(test.resolution)
				},
				"get_browser_state": test.snapshot,
			})
			server.refs["@e1"] = cuaElement{Raw: "raw-old", Actions: map[string]bool{"click": true}}
			server.lastEditable = "raw-editable"
			server.dialogID = "dialog-1"
			server.dialogKind = "prompt"

			_, _, _ = server.browserDialog(context.Background(), nil, BrowserDialogInput{
				Action: "accept", DialogID: "dialog-1", PromptText: pointerToString("response"),
			})
			if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
				t.Fatalf("successful resolution retained capabilities: refs=%#v editable=%q dialog=%q/%q",
					server.refs, server.lastEditable, server.dialogID, server.dialogKind)
			}
			if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 1 {
				t.Fatalf("dialog calls = %d, want one", got)
			}
			wantSnapshots := 0
			if test.wantSnapshotCall {
				wantSnapshots = 1
			}
			if got := len(callsNamed(fake.Calls(), "get_browser_state")); got != wantSnapshots {
				t.Fatalf("snapshot calls = %d, want %d", got, wantSnapshots)
			}
		})
	}
}

func TestCUABrowserDialogResolutionTransportFailureClearsCapabilities(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	server.runtime.client = &failingCUAClient{err: context.DeadlineExceeded}
	server.refs["@e1"] = cuaElement{Raw: "raw-old", Actions: map[string]bool{"click": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-1"
	server.dialogKind = "prompt"

	_, _, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{
		Action: "accept", DialogID: "dialog-1", PromptText: pointerToString("response"),
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want dialog deadline", err)
	}
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("uncertain dialog resolution retained capabilities: refs=%#v editable=%q dialog=%q/%q",
			server.refs, server.lastEditable, server.dialogID, server.dialogKind)
	}
}

func TestCUABrowserDialogRejectsUnknownResultStatusBeforeSnapshot(t *testing.T) {
	const secret = "partial raw-old raw-editable dialog-1 backend-secret"
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{
				"status": secret, "target_id": "target-1", "tab_id": "tab-a",
				"dialog_id": "dialog-1", "kind": "prompt", "action": "accept",
			})
		},
		"get_browser_state": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(cuaBrowserSnapshot("target-1", "tab-a", "must not snapshot"))
		},
	})
	server.refs["@e1"] = cuaElement{Raw: "raw-old", Actions: map[string]bool{"click": true}}
	server.lastEditable = "raw-editable"
	server.dialogID = "dialog-1"
	server.dialogKind = "prompt"

	result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{
		Action: "accept", DialogID: "dialog-1", PromptText: pointerToString("response"),
	})
	if err == nil || result != nil || output.Status != "" {
		t.Fatalf("unknown dialog status result/output/error = %#v %#v %v", result, output, err)
	}
	if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 1 {
		t.Fatalf("dialog calls = %d, want one", got)
	}
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 0 {
		t.Fatalf("unknown dialog status was snapshot-verified: %#v", calls)
	}
	if len(server.refs) != 0 || server.lastEditable != "" || server.dialogID != "" || server.dialogKind != "" {
		t.Fatalf("unknown dialog status retained capabilities: refs=%#v editable=%q dialog=%q/%q",
			server.refs, server.lastEditable, server.dialogID, server.dialogKind)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "raw-old") || strings.Contains(err.Error(), "backend-secret") {
		t.Fatalf("unknown dialog status leaked backend text: %v", err)
	}
}

func TestCUABrowserDialogPromptSecretIsRedactedFromRefusalsAndErrors(t *testing.T) {
	const secret = "private prompt response 7xQ"
	tests := []struct {
		name          string
		handler       fakeCUAHandler
		snapshot      fakeCUAHandler
		directCallErr bool
	}{
		{
			name: "structured refusal",
			handler: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return cuaRefused("browser_action_unavailable", "prompt echo: "+secret, map[string]any{
					"safe":        secret,
					secret:        "secret used as a map key",
					"prompt_echo": secret,
				})
			},
		},
		{
			name:          "transport error",
			directCallErr: true,
		},
		{
			name: "protocol error content",
			handler: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: secret}}}, nil, nil
			},
		},
		{
			name: "post resolution snapshot refusal",
			handler: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return cuaOK(map[string]any{
					"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1",
					"kind": "prompt", "action": "accept",
				})
			},
			snapshot: func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
				return cuaRefused("browser_action_unavailable", "snapshot echoed "+secret, map[string]any{
					"safe": secret,
					secret: "secret used as a map key",
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
				"browser_dialog":    test.handler,
				"get_browser_state": test.snapshot,
			})
			if test.directCallErr {
				server.runtime.client = &failingCUAClient{err: errors.New("transport echoed " + secret)}
			}
			server.dialogID = "dialog-1"
			server.dialogKind = "prompt"
			result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{
				Action: "accept", DialogID: "dialog-1", PromptText: pointerToString(secret),
			})
			public := output.Refusal
			encoded := ""
			if result != nil {
				encoded += firstText(result.Content)
			}
			if public != nil {
				encoded += fmt.Sprintf("%#v", public)
			}
			if err != nil {
				encoded += err.Error()
			}
			if strings.Contains(encoded, secret) {
				t.Fatalf("public refusal/error leaked prompt text: %s", encoded)
			}
		})
	}
}

func TestCUABrowserDialogResolutionRefusalIsSanitizedAndNotRetried(t *testing.T) {
	server, fake := preparedCUABrowserTestServer(t, map[string]fakeCUAHandler{
		"browser_dialog": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaRefused(
				"browser_binding_stale",
				"raw target target-1 tab tab-a dialog dialog-1 is stale",
				map[string]any{"target_id": "target-1", "tab_id": "tab-a", "dialog_id": "dialog-1"},
			)
		},
	})
	server.dialogID = "dialog-1"
	server.dialogKind = "confirm"
	result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "dismiss", DialogID: "dialog-1"})
	if err != nil || result == nil || output.Refusal == nil {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
	encoded := firstText(result.Content) + output.Refusal.Message
	if strings.Contains(encoded, "target-1") || strings.Contains(encoded, "tab-a") || strings.Contains(encoded, "dialog-1") {
		t.Fatalf("public refusal leaked raw capability: %#v / %q", output.Refusal, encoded)
	}
	if got := len(callsNamed(fake.Calls(), "browser_dialog")); got != 1 {
		t.Fatalf("browser_dialog calls = %d, want one", got)
	}
	if calls := callsNamed(fake.Calls(), "get_browser_state"); len(calls) != 0 {
		t.Fatalf("mutating dialog refusal retried through rebind: %#v", calls)
	}
}

func TestCUABrowserDialogTransportErrorStaysDistinct(t *testing.T) {
	server, _ := preparedCUABrowserTestServer(t, nil)
	server.runtime.client = &failingCUAClient{err: errors.New("dialog wire down")}
	server.dialogID = "dialog-1"
	server.dialogKind = "alert"
	result, output, err := server.browserDialog(context.Background(), nil, BrowserDialogInput{Action: "accept", DialogID: "dialog-1"})
	if err == nil || result != nil || output.Refusal != nil || !strings.Contains(err.Error(), "dialog wire down") {
		t.Fatalf("result/output/error = %#v %#v %v", result, output, err)
	}
}
