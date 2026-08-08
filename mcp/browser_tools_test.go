package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

func TestBrowserOutputFlattensOutcomeAndOmitsItForChromedp(t *testing.T) {
	chromedpJSON, err := json.Marshal(BrowserOutput{Snapshot: "page", ElementCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(chromedpJSON), `{"snapshot":"page","element_count":2}`; got != want {
		t.Fatalf("Chromedp BrowserOutput JSON = %s, want %s", got, want)
	}

	refusedJSON, err := json.Marshal(BrowserOutput{
		BrowserOutcome: BrowserOutcome{
			Status: "refused",
			Refusal: &BrowserRefusal{
				Code:    "policy_denied",
				Message: "blocked",
				Detail:  map[string]any{"retryable": false},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRefused := `{"status":"refused","refusal":{"code":"policy_denied","message":"blocked","detail":{"retryable":false}}}`
	if got := string(refusedJSON); got != wantRefused {
		t.Fatalf("refused BrowserOutput JSON = %s, want %s", got, wantRefused)
	}
}

func TestBrowserToolContractPinsChromedpNamesAndInputSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- startChromedpBrowserMCPServer(ctx, serverTransport, types.BrowserConfig{})
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "browser-contract-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect to Chromedp browser MCP server: %v", err)
	}
	defer session.Close()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list browser tools: %v", err)
	}

	wantNames := []string{
		"browser_click",
		"browser_navigate",
		"browser_press",
		"browser_scroll",
		"browser_snapshot",
		"browser_type",
		"browser_vision",
	}
	wantProperties := []string{"direction", "full", "key", "question", "ref", "text", "url"}
	wantDirections := []any{"up", "down"}
	wantKeys := []any{
		"ArrowDown", "ArrowLeft", "ArrowRight", "ArrowUp", "Backspace", "Delete", "End", "Enter",
		"Escape", "Home", "PageDown", "PageUp", "Tab",
	}

	gotNames := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		gotNames = append(gotNames, tool.Name)
		properties := browserToolSchemaProperties(t, tool)
		gotProperties := make([]string, 0, len(properties))
		for name := range properties {
			gotProperties = append(gotProperties, name)
		}
		slices.Sort(gotProperties)
		if !reflect.DeepEqual(gotProperties, wantProperties) {
			t.Errorf("%s input properties = %v, want %v", tool.Name, gotProperties, wantProperties)
		}
		if got := browserToolPropertyEnum(t, tool.Name, properties, "direction"); !reflect.DeepEqual(got, wantDirections) {
			t.Errorf("%s direction enum = %v, want %v", tool.Name, got, wantDirections)
		}
		if got := browserToolPropertyEnum(t, tool.Name, properties, "key"); !reflect.DeepEqual(got, wantKeys) {
			t.Errorf("%s key enum = %v, want %v", tool.Name, got, wantKeys)
		}
	}
	slices.Sort(gotNames)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("browser tool names = %v, want exactly %v", gotNames, wantNames)
	}

	cancel()
	select {
	case err := <-serverErr:
		if err != nil && !errorsIsContextCancellation(err) {
			t.Fatalf("Chromedp browser MCP server shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Chromedp browser MCP server did not stop after cancellation")
	}
}

func browserToolSchemaProperties(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s input schema type = %T, want map[string]any", tool.Name, tool.InputSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s input schema properties type = %T, want map[string]any", tool.Name, schema["properties"])
	}
	return properties
}

func browserToolPropertyEnum(t *testing.T, toolName string, properties map[string]any, propertyName string) []any {
	t.Helper()
	property, ok := properties[propertyName].(map[string]any)
	if !ok {
		t.Fatalf("%s property %s type = %T, want map[string]any", toolName, propertyName, properties[propertyName])
	}
	enum, ok := property["enum"].([]any)
	if !ok {
		t.Fatalf("%s property %s enum type = %T, want []any", toolName, propertyName, property["enum"])
	}
	return enum
}

func errorsIsContextCancellation(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
