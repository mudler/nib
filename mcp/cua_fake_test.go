package mcp

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeCUACall struct {
	Name string
	Args map[string]any
}

type fakeCUAHandler func(map[string]any) (*mcp.CallToolResult, map[string]any, error)

type fakeCUADriver struct {
	Session *mcp.ClientSession
	mu      sync.Mutex
	calls   []fakeCUACall
}

func startFakeCUA(
	t *testing.T,
	ctx context.Context,
	version string,
	toolNames []string,
	handlers map[string]fakeCUAHandler,
) *fakeCUADriver {
	t.Helper()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-cua", Version: version}, nil)
	fake := &fakeCUADriver{}
	for _, toolName := range toolNames {
		mcp.AddTool(server, &mcp.Tool{Name: toolName}, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			fake.mu.Lock()
			fake.calls = append(fake.calls, fakeCUACall{Name: toolName, Args: cloneCUAArgs(args)})
			fake.mu.Unlock()

			if handler := handlers[toolName]; handler != nil {
				return handler(args)
			}
			return cuaOK(nil)
		})
	}

	go func() { _ = server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect fake Cua driver: %v", err)
	}
	fake.Session = session
	t.Cleanup(func() { _ = session.Close() })
	return fake
}

func (f *fakeCUADriver) Calls() []fakeCUACall {
	f.mu.Lock()
	defer f.mu.Unlock()

	calls := make([]fakeCUACall, len(f.calls))
	for i, call := range f.calls {
		calls[i] = fakeCUACall{Name: call.Name, Args: cloneCUAArgs(call.Args)}
	}
	return calls
}

func cuaOK(structured map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	structured = cloneCUAArgs(structured)
	if structured == nil {
		structured = make(map[string]any)
	}
	if _, ok := structured["status"]; !ok {
		structured["status"] = "ok"
	}
	return &mcp.CallToolResult{}, structured, nil
}

func cuaRefused(code, message string, detail map[string]any) (*mcp.CallToolResult, map[string]any, error) {
	return &mcp.CallToolResult{}, map[string]any{
		"status": "refused",
		"refusal": map[string]any{
			"code":    code,
			"message": message,
			"detail":  cloneCUAArgs(detail),
		},
	}, nil
}

func cloneCUAArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	cloned := make(map[string]any, len(args))
	for key, value := range args {
		cloned[key] = cloneCUAValue(value)
	}
	return cloned
}

func cloneCUAValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneCUAArgs(value)
	case []any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = cloneCUAValue(item)
		}
		return cloned
	default:
		return value
	}
}

func TestFakeCUARecordsCallsAndReturnsStructuredContent(t *testing.T) {
	ctx := context.Background()
	fake := startFakeCUA(t, ctx, "0.19.0", []string{"health_report"}, map[string]fakeCUAHandler{
		"health_report": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			return cuaOK(map[string]any{"status": "healthy", "version": "0.19.0"})
		},
	})

	args := map[string]any{"session": "run-7"}
	result, err := fake.Session.CallTool(ctx, &mcp.CallToolParams{Name: "health_report", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	wantStructured := map[string]any{"status": "healthy", "version": "0.19.0"}
	if !reflect.DeepEqual(result.StructuredContent, wantStructured) {
		t.Fatalf("structured content = %#v, want %#v", result.StructuredContent, wantStructured)
	}
	if calls := fake.Calls(); !reflect.DeepEqual(calls, []fakeCUACall{{Name: "health_report", Args: args}}) {
		t.Fatalf("calls = %#v, want one health_report call with %#v", calls, args)
	}
}
