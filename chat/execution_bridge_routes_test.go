package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/types"
)

// Like TestToolPolicySpawnedHelperExecution, this drives the real Cogito/MCP
// dispatch path. These tools only increment counters: bash never runs a command
// and read never opens a file. Each veto has an otherwise identical nil-policy
// control that must reach the registered MCP body.
func TestExecutionBridgeRootDispatch(t *testing.T) {
	for _, route := range []string{"auto", "turn", "tool", "readonly", "nil prompt", "prefix", "classifier", "hook"} {
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
					const privateError = "sensitive host policy error: credential=never-expose"
					var requests, executions, warmups, approvals, policies, hookCalls, autoApprovals atomic.Int32
					checkOutput := func(output string) {
						if strings.Contains(output, privateError) {
							t.Error("private policy error leaked to model or event output")
						}
					}
					arguments := "{}"
					if route == "prefix" || route == "classifier" {
						arguments = `{"script":"go test ./..."}`
					}
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost {
							http.NotFound(w, r)
							return
						}
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						checkOutput(string(body))
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
							msg["tool_calls"] = []any{map[string]any{"id": "root-policy-call", "type": "function", "function": map[string]any{"name": tool, "arguments": arguments}}}
						}
						w.Header().Set("Content-Type", "application/json")
						if err := json.NewEncoder(w).Encode(map[string]any{"id": "policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}}); err != nil {
							t.Error(err)
						}
					}))
					defer backend.Close()
					server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "root-policy", Version: "v0"}, nil)
					for _, tool := range []string{name, "grant_probe"} {
						sdkmcp.AddTool(server, &sdkmcp.Tool{Name: tool, Description: "counter only"}, func(context.Context, *sdkmcp.CallToolRequest, struct {
							Script string `json:"script,omitempty"`
						}) (*sdkmcp.CallToolResult, any, error) {
							if tool == "grant_probe" {
								warmups.Add(1)
							} else {
								executions.Add(1)
							}
							return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "counter incremented"}}}, nil, nil
						})
					}
					serverT, clientT := sdkmcp.NewInMemoryTransports()
					serverSession, connectErr := server.Connect(ctx, serverT, nil)
					if connectErr != nil {
						t.Fatal(connectErr)
					}
					defer serverSession.Close()

					cb := Callbacks{
						ToolExecutionBridge: invokeExecutionProbe,
						OnStatus:            checkOutput,
						OnReasoning:         checkOutput,
						OnResponse:          checkOutput,
						OnStepContent:       checkOutput,
						OnError:             func(err error) { checkOutput(err.Error()) },
						OnToolStart:         func(v ToolStart) { b, _ := json.Marshal(v); checkOutput(string(b)) },
						OnToolResult:        func(v ToolResult) { b, _ := json.Marshal(v); checkOutput(string(b)) },
						OnAgentEvent:        func(v AgentEvent) { b, _ := json.Marshal(v); checkOutput(string(b)) },
						ObservationCallbacks: func() func(Observation) {
							return func(v Observation) { b, _ := json.Marshal(v); checkOutput(string(b)) }
						},
						OnAutoApproved: func(ToolCallRequest, Verdict) { autoApprovals.Add(1) },
					}
					{
						cb.OnToolCall = func(r ToolCallRequest) ToolCallResponse {
							approvals.Add(1)
							if route != "nil prompt" && (route != "turn" || r.Name != "grant_probe" || r.AgentID != "") {
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
							if r.AgentID != "" || r.Arguments != arguments {
								t.Errorf("policy lost root call identity: %+v", r)
							}
							return errors.New(privateError)
						}
					}
					cfg := types.Config{RequireToolExecutionBridge: true, BaseDir: t.TempDir(), Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent"}, AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}
					switch route {
					case "auto":
						cfg.ApprovalMode = "auto"
					case "tool":
						cfg.AllowedTools = []string{name}
					case "readonly":
						cfg.ApprovalMode = "prompt"
					}
					s, err := NewSession(ctx, cfg, cb, clientT)
					if err != nil && !denied {
						t.Fatal(err)
					}
					defer s.Close()
					s.cfgClients["counter-fixture"] = s.clients[0]
					s.clients = nil
					classifier := verdictFor("build_test", .95)
					switch route {
					case "prefix":
						s.allowedBashPrefixes = map[string]bool{"go": true}
					case "classifier":
						s.setClassifierState(&classifierState{approver: NewApprover(classifier, types.AutoApproveConfig{}, cfg.WorkingDir)})
						s.SetApprovalMode(types.ApprovalClassify)
					case "hook":
						s.hooks = policyHookFunc(func(_ context.Context, event hooks.Event, tool string, payload any) []hooks.Decision {
							b, _ := json.Marshal(payload)
							checkOutput(string(b))
							if event != hooks.EventPreToolUse {
								return nil
							}
							hookCalls.Add(1)
							if tool != name {
								t.Errorf("unexpected hook tool %q", tool)
							}
							approved := true
							return []hooks.Decision{{Approved: &approved}}
						})
					}
					if reply, err := s.SendMessage("root policy probe"); err != nil && !denied {
						t.Fatal(err)
					} else {
						checkOutput(reply)
					}
					wantExecutions, wantPolicies := int32(1), int32(0)
					if denied {
						wantExecutions, wantPolicies = 0, 1
					}
					wantHooks, wantClassifier := int32(0), 0
					if !denied && route == "hook" {
						wantHooks = 1
					}
					if !denied && route == "classifier" {
						wantClassifier = 1
					}
					if hookCalls.Load() != wantHooks || classifier.calls != wantClassifier || autoApprovals.Load() != int32(wantClassifier) {
						t.Fatalf("hooks=%d classifier=%d auto approvals=%d; want %d %d %d", hookCalls.Load(), classifier.calls, autoApprovals.Load(), wantHooks, wantClassifier, wantClassifier)
					}
					wantWarmups, wantApprovals, wantRequests := int32(0), int32(0), int32(2)
					if route == "turn" {
						wantWarmups, wantApprovals, wantRequests = 1, 1, 3
					}
					if route == "nil prompt" && !denied {
						wantApprovals = 1
					}
					if denied {
						wantRequests--
					}
					if executions.Load() != wantExecutions || policies.Load() != wantPolicies || warmups.Load() != wantWarmups || approvals.Load() != wantApprovals || requests.Load() != wantRequests {
						t.Fatalf("executions=%d policies=%d warmups=%d approvals=%d requests=%d; want %d %d %d %d %d", executions.Load(), policies.Load(), warmups.Load(), approvals.Load(), requests.Load(), wantExecutions, wantPolicies, wantWarmups, wantApprovals, wantRequests)
					}
				})
			}
		})
	}
}
