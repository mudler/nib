package chat

import (
	"context"
	"encoding/json"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
	"time"
)

func TestHostWaitForPassesTheSourceAndLeavesTimesToTheHost(t *testing.T) {
	var got WaitRequest
	tool := &hostWaitForTool{arm: func(r WaitRequest) string { got = r; return "armed" }}
	out, _, err := tool.Run(map[string]any{
		"tool": "list_downloads", "arguments": map[string]any{"state": "active"},
		"until": "json_equals", "field": "items.0.status", "equals": "done",
		"then": "tell me", "reason": "the download",
	})
	if err != nil || out != "armed" {
		t.Fatalf("out %q err %v", out, err)
	}
	if got.Tool != "list_downloads" || got.Arguments["state"] != "active" || got.Field != "items.0.status" || got.Equals != "done" || got.Reason != "the download" {
		t.Errorf("request lost a field: %+v", got)
	}
	if got.Timeout != 0 || got.IntervalSeconds != 0 {
		t.Errorf("the times were defaulted by nib: %v %d", got.Timeout, got.IntervalSeconds)
	}
}

func TestHostWaitForRefusesBadSources(t *testing.T) {
	tool := &hostWaitForTool{arm: func(WaitRequest) string { t.Fatal("armed a bad wait"); return "" }}
	for name, args := range map[string]map[string]any{
		"none":    {"then": "x"},
		"both":    {"tool": "a", "url": "http://x", "then": "x"},
		"script":  {"script": "ls", "then": "x"},
		"no then": {"url": "http://x"},
	} {
		if out, _, _ := tool.Run(args); out == "" {
			t.Errorf("%s: no refusal", name)
		}
	}
}

func TestHostWaitSchemasAreObjectsWithTheHostFields(t *testing.T) {
	def := hostWaitForToolDefinition(func(WaitRequest) string { return "" }).Tool()
	b, _ := json.Marshal(def.Function.Parameters)
	for _, want := range []string{`"tool"`, `"json_equals"`, `"field"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), `"script"`) || strings.Contains(string(b), `exit_zero`) {
		t.Errorf("the host schema offers a script: %s", b)
	}
	if hostWaitAgentsToolDefinition(context.Background(), nil).Tool().Function.Name != "wait_agents" {
		t.Error("wrong name")
	}
}

func TestHostWaitAgentsPassesTheRequestAndTheTurnContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, 1)
	var gotCtx context.Context
	var got WaitAgentsRequest
	tool := &hostWaitAgentsTool{ctx: ctx, run: func(c context.Context, r WaitAgentsRequest) string { gotCtx, got = c, r; return "done" }}
	out, _, _ := tool.Run(map[string]any{"ids": []any{"a", "b"}, "mode": "any_failed_or_all", "timeout_seconds": float64(20)})
	if out != "done" || gotCtx.Value(key{}) != 1 || len(got.IDs) != 2 || got.Mode != "any_failed_or_all" || got.Timeout != 20*time.Second {
		t.Errorf("out %q ctx %v req %+v", out, gotCtx, got)
	}
}

func TestWaitToolsReadJSONNumbers(t *testing.T) {
	// The tool loop decodes numbers as json.Number.
	var got WaitRequest
	tool := &hostWaitForTool{arm: func(r WaitRequest) string { got = r; return "ok" }}
	tool.Run(map[string]any{"url": "http://x", "then": "t", "timeout_seconds": json.Number("90"), "interval_seconds": json.Number("30"), "max_errors": json.Number("2")})
	if got.Timeout != 90*time.Second || got.IntervalSeconds != 30 || got.MaxErrors != 2 {
		t.Errorf("numbers lost: %+v", got)
	}
	var gotA WaitAgentsRequest
	wa := &hostWaitAgentsTool{ctx: context.Background(), run: func(_ context.Context, r WaitAgentsRequest) string { gotA = r; return "" }}
	wa.Run(map[string]any{"timeout_seconds": json.Number("20")})
	if gotA.Timeout != 20*time.Second {
		t.Errorf("timeout lost: %v", gotA.Timeout)
	}
}

func TestHostTransportToolsPassTheAllowlistAndReplaceBuiltins(t *testing.T) {
	s := newReloadTestSession(t)
	host := connectTestMCP(t, "host", 1, 10)
	other := connectTestMCP(t, "other", 1, 10)
	s.hostClients = map[*sdkmcp.ClientSession]bool{host: true}
	s.hostNames = map[string]bool{"cron": true}
	s.toolAllow = map[string]bool{"memory": true}

	// The filter is rebuilt per call, as its comment requires.
	f := s.mcpToolFilter()
	if !f(host, "host_0") {
		t.Error("a host tool not in the allowlist must pass")
	}
	if f(other, "other_0") {
		t.Error("a tool of another transport is still gated by the allowlist")
	}
	if !f(other, "memory") {
		t.Error("the allowlist still admits what it names")
	}
	if s.toolEnabled("cron") {
		t.Error("a built-in is registered although the host serves a tool of that name")
	}
	s.toolAllow = nil
	if s.toolEnabled("cron") {
		t.Error("an empty allowlist must not bring the replaced built-in back")
	}
	if !s.toolEnabled("tree") {
		t.Error("an empty allowlist still enables the other built-ins")
	}
}
