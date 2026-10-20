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

// Like TestToolPolicySpawnedHelperExecution, this drives the real Cogito/MCP
// dispatch path. These tools only increment counters: bash never runs a command
// and read never opens a file. Each veto has an otherwise identical nil-policy
// control that must reach the registered MCP body.
func TestToolPolicyRootDispatch(t *testing.T) {
	for _, route := range []string{"auto", "turn", "tool", "readonly", "nil prompt"} {
		t.Run(route, func(t *testing.T) {
			for _, denied := range []bool{false, true} {
				label := "allowed control"
				if denied {
					label = "policy denied"
				}
				t.Run(label, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					name := "bash"
					if route == "readonly" {
						name = "read"
					}
					var requests, executions, warmups, approvals, policies atomic.Int32
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost {
							http.NotFound(w, r)
							return
						}
						step := requests.Add(1)
						tool := ""
						if route == "turn" {
							if step == 1 {
								tool = "grant_probe"
							} else if step == 2 {
								tool = name
							}
						} else if step == 1 {
							tool = name
						}
						msg := map[string]any{"role": "assistant", "content": "done"}
						if tool != "" {
							msg["content"] = ""
							msg["tool_calls"] = []any{map[string]any{"id": "root-policy-call", "type": "function", "function": map[string]any{"name": tool, "arguments": "{}"}}}
						}
						w.Header().Set("Content-Type", "application/json")
						if err := json.NewEncoder(w).Encode(map[string]any{"id": "policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}}); err != nil {
							t.Error(err)
						}
					}))
					defer backend.Close()
					server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "root-policy", Version: "v0"}, nil)
					for _, tool := range []string{name, "grant_probe"} {
						sdkmcp.AddTool(server, &sdkmcp.Tool{Name: tool, Description: "counter only"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
							if tool == "grant_probe" {
								warmups.Add(1)
							} else {
								executions.Add(1)
							}
							return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "counter incremented"}}}, nil, nil
						})
					}
					mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
					defer mcpServer.Close()
					cb := Callbacks{}
					if route != "nil prompt" {
						cb.OnToolCall = func(r ToolCallRequest) ToolCallResponse {
							approvals.Add(1)
							if route != "turn" || r.Name != "grant_probe" || r.AgentID != "" {
								t.Errorf("unexpected approval prompt: %+v", r)
							}
							return ToolCallResponse{Approved: true, AllowAllTurn: true}
						}
					}
					if denied {
						cb.ToolPolicy = func(r ToolCallRequest) error {
							if r.Name != name {
								return nil
							}
							policies.Add(1)
							if r.AgentID != "" || r.Arguments != "{}" {
								t.Errorf("policy lost root call identity: %+v", r)
							}
							return errors.New("root tool veto")
						}
					}
					cfg := types.Config{Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent"}, AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}
					switch route {
					case "auto":
						cfg.ApprovalMode = "auto"
					case "tool":
						cfg.AllowedTools = []string{name}
					case "readonly":
						cfg.ApprovalMode = "prompt"
					}
					s, err := NewSession(ctx, cfg, cb)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					if err := s.ReconcileMCPServers(map[string]types.MCPServer{"policy": {URL: mcpServer.URL}}); err != nil {
						t.Fatal(err)
					}
					if _, err := s.SendMessage("root policy probe"); err != nil {
						t.Fatal(err)
					}
					wantExecutions, wantPolicies := int32(1), int32(0)
					if denied {
						wantExecutions, wantPolicies = 0, 1
					}
					wantWarmups, wantApprovals, wantRequests := int32(0), int32(0), int32(2)
					if route == "turn" {
						wantWarmups, wantApprovals, wantRequests = 1, 1, 3
					}
					if executions.Load() != wantExecutions || policies.Load() != wantPolicies || warmups.Load() != wantWarmups || approvals.Load() != wantApprovals || requests.Load() != wantRequests {
						t.Fatalf("executions=%d policies=%d warmups=%d approvals=%d requests=%d; want %d %d %d %d %d", executions.Load(), policies.Load(), warmups.Load(), approvals.Load(), requests.Load(), wantExecutions, wantPolicies, wantWarmups, wantApprovals, wantRequests)
					}
				})
			}
		})
	}
}
