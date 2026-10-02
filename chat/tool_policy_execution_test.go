package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

// Exercise nib -> Cogito -> spawned child -> inherited MCP tool. The tool is
// advertised and root execution succeeds; a handler-side check cannot recover
// the child's identity from the identical arguments.
func TestToolPolicySpawnedHelperExecution(t *testing.T) {
	for _, grant := range []string{"turn", "always", "nil policy"} {
		t.Run(grant, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var rootRequests, childRequests, executions, approvals, childPolicies atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Messages []struct {
						Role    string `json:"role"`
						Content any    `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				child := false
				for _, m := range req.Messages {
					if m.Role == "user" {
						child = m.Content == "policy child"
						break
					}
				}
				name, args := "", "{}"
				if child {
					if childRequests.Add(1) == 1 {
						name = "delegate_background"
					}
				} else {
					switch rootRequests.Add(1) {
					case 1:
						name = "delegate_background"
					case 2:
						name = "spawn_agent"
						args = `{"task":"policy child","background":true}`
					}
				}
				msg := map[string]any{"role": "assistant", "content": "done"}
				if name != "" {
					msg["content"] = ""
					msg["tool_calls"] = []any{map[string]any{"id": "policy-call", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"id": "policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
			}))
			defer backend.Close()
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "policy", Version: "v0"}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "delegate_background", Description: "probe handler"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
				executions.Add(1)
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "executed"}}}, nil, nil
			})
			mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
			defer mcpServer.Close()
			completed := make(chan struct{}, 1)
			cb := Callbacks{
				OnToolCall: func(r ToolCallRequest) ToolCallResponse {
					approvals.Add(1)
					if r.AgentID != "" {
						t.Error("helper reached ordinary prompt despite grant")
					}
					return ToolCallResponse{Approved: true, AllowAllTurn: grant != "always", AlwaysAllow: grant == "always"}
				},
				OnAgentEvent: func(e AgentEvent) {
					if e.Status == AgentStatusCompleted || e.Status == AgentStatusFailed {
						completed <- struct{}{}
					}
				},
				ToolPolicy: func(r ToolCallRequest) error {
					if r.Name == "delegate_background" && r.AgentID != "" {
						childPolicies.Add(1)
						return errors.New("helper delegation is unsupported")
					}
					return nil
				},
			}
			if grant == "nil policy" {
				cb.ToolPolicy = nil
			}
			s, err := NewSession(ctx, types.Config{Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent"}, AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}, cb)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReconcileMCPServers(map[string]types.MCPServer{"policy": {URL: mcpServer.URL}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SendMessage("root"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-completed:
			case <-ctx.Done():
				t.Fatal("child did not finish")
			}
			want := int32(1)
			if grant == "nil policy" {
				want = 2
			}
			if executions.Load() != want {
				t.Fatalf("handler executions=%d, want %d", executions.Load(), want)
			}
			if grant != "nil policy" && childPolicies.Load() != 1 {
				t.Fatalf("stamped child policy calls=%d", childPolicies.Load())
			}
			wantApprovals := int32(1)
			if grant == "always" {
				wantApprovals = 2
			}
			if approvals.Load() != wantApprovals {
				t.Fatalf("ordinary approvals=%d want %d", approvals.Load(), wantApprovals)
			}
		})
	}
}
