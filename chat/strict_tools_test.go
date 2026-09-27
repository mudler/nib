package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

type sentTool struct {
	Function struct {
		Name       string         `json:"name"`
		Strict     bool           `json:"strict"`
		Parameters map[string]any `json:"parameters"`
	} `json:"function"`
}

// toolsOfFirstRequest runs one turn against a fake server with nib's real
// tool servers and returns the tools the first request advertised.
func toolsOfFirstRequest(t *testing.T, strict bool) map[string]sentTool {
	t.Helper()
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var first []sentTool
	inner := messageCapturingOpenAI(func([]capturedMessage) {})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []sentTool `json:"tools"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		if first == nil && len(req.Tools) > 0 {
			first = req.Tools
		}
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		inner(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		WorkingDir:   t.TempDir(),
		StrictTools:  strict,
	}
	transports, err := wizmcp.StartTransports(ctx, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("StartTransports: %v", err)
	}
	session, err := chat.NewSession(ctx, cfg, chat.Callbacks{}, transports...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()
	if _, err := session.SendMessage("hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	out := map[string]sentTool{}
	for _, tl := range first {
		out[tl.Function.Name] = tl
	}
	if _, ok := out["bash"]; !ok {
		t.Fatalf("the request did not advertise bash; tools: %v", out)
	}
	return out
}

// TestStrictToolsSendsBashAsAStrictTool: with strict_tools on, bash goes out
// strict, closed to unknown arguments, with its optional timeout nullable,
// so a backend that honors strict mode cannot generate {"command": ...}.
func TestStrictToolsSendsBashAsAStrictTool(t *testing.T) {
	bash := toolsOfFirstRequest(t, true)["bash"]
	if !bash.Function.Strict {
		t.Fatal("bash was not sent strict")
	}
	p := bash.Function.Parameters
	if p["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %v, want false", p["additionalProperties"])
	}
	timeout, _ := p["properties"].(map[string]any)["timeout"].(map[string]any)
	if types, _ := timeout["type"].([]any); len(types) != 2 || types[1] != "null" {
		t.Fatalf("timeout type = %v, want [integer null]", timeout["type"])
	}
}

// TestStrictToolsOffByDefault: without the setting, nothing is strict, but
// bash still carries the additionalProperties its server declares.
func TestStrictToolsOffByDefault(t *testing.T) {
	tools := toolsOfFirstRequest(t, false)
	for name, tl := range tools {
		if tl.Function.Strict {
			t.Errorf("%s was sent strict without strict_tools", name)
		}
	}
	if tools["bash"].Function.Parameters["additionalProperties"] != false {
		t.Fatalf("bash lost additionalProperties: %v", tools["bash"].Function.Parameters)
	}
}
