package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

// Like TestToolPolicySpawnedHelperExecution, this uses only a local completion
// endpoint and inherited fake MCP tools. IDs come from the real spawn registry,
// never from a synthetic AgentState or an assumption that nonempty means child.
func TestExecutionBridgeHelperExecution(t *testing.T) {
	for _, route := range []string{"foreground", "resume", "injection", "spawn veto"} {
		for _, deny := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deny=%t", route, deny), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				const secret = "PRIVATE-helper-policy-detail"
				var mu sync.Mutex
				var captured []string
				record := func(v any) { b, _ := json.Marshal(v); mu.Lock(); captured = append(captured, string(b)); mu.Unlock() }
				var rootCalls, childCalls, bodies, seedBodies, policies atomic.Int32
				var childID atomic.Value
				waiting, release := make(chan struct{}), make(chan struct{})
				completed := make(chan string, 8)
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
					record(req)
					child := false
					for _, m := range req.Messages {
						if m.Role == "user" {
							child = m.Content == "policy helper task"
							break
						}
					}
					name, args := "", "{}"
					if child {
						n := childCalls.Add(1)
						switch route {
						case "resume":
							if n == 2 {
								name = "helper_probe"
							}
						case "injection":
							if n == 1 {
								close(waiting)
								select {
								case <-release:
								case <-ctx.Done():
								}
								name = "helper_seed"
							}
							if n == 2 {
								b, _ := json.Marshal(req)
								if !strings.Contains(string(b), "injected policy instruction") {
									t.Error("subsequent decision lacks injected message")
								}
								name = "helper_probe"
							}
						default:
							if n == 1 {
								name = "helper_probe"
							}
						}
						if name == "helper_probe" {
							args = `{"value":"preserved helper arguments"}`
						}
					} else {
						switch rootCalls.Add(1) {
						case 1:
							name = "spawn_agent"
							args = fmt.Sprintf(`{"task":"policy helper task","background":%t}`, route != "foreground")
						case 3:
							if route == "resume" {
								name = "send_agent_message"
								args = fmt.Sprintf(`{"agent_id":%q,"message":"continue"}`, childID.Load().(string))
							}
						}
					}
					msg := map[string]any{"role": "assistant", "content": "done"}
					if name != "" {
						msg["content"] = ""
						msg["tool_calls"] = []any{map[string]any{"id": fmt.Sprintf("%s-%d-%d", name, rootCalls.Load(), childCalls.Load()), "type": "function", "function": map[string]any{"name": name, "arguments": args}}}
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"id": "helper-policy", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
				}))
				defer backend.Close()
				server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "policy", Version: "v0"}, nil)
				sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "helper_probe", Description: "fake counter only"}, func(_ context.Context, _ *sdkmcp.CallToolRequest, args struct {
					Value string `json:"value"`
				}) (*sdkmcp.CallToolResult, any, error) {
					if args.Value != "preserved helper arguments" {
						t.Errorf("body arguments=%q", args.Value)
					}
					bodies.Add(1)
					return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "executed"}}}, nil, nil
				})
				sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "helper_seed", Description: "fake injection step"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
					seedBodies.Add(1)
					return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "step"}}}, nil, nil
				})
				serverT, clientT := sdkmcp.NewInMemoryTransports()
				serverSession, connectErr := server.Connect(ctx, serverT, nil)
				if connectErr != nil {
					t.Fatal(connectErr)
				}
				defer serverSession.Close()

				var session *Session
				cb := Callbacks{
					ToolExecutionBridge: invokeExecutionProbe,
					OnAgentEvent: func(e AgentEvent) {
						record(e)
						if e.Status == AgentStatusRunning {
							childID.Store(e.ID)
						}
						if e.Status == AgentStatusCompleted || e.Status == AgentStatusFailed {
							completed <- e.ID
						}
					},
					OnToolStart: func(e ToolStart) {
						record(e)
						if deny && ((route == "spawn veto" && e.Name == "spawn_agent") || (route != "spawn veto" && e.Name == "helper_probe")) {
							t.Errorf("denied tool started: %+v", e)
						}
					},
					OnStepContent: func(s string) { record(s) },
					OnResponse:    func(s string) { record(s) },
					OnError:       func(err error) { record(err.Error()) },
					OnToolResult: func(e ToolResult) {
						record(e)
						if deny && route != "spawn veto" && e.Name == "helper_probe" {
							t.Errorf("denied helper emitted start line: %+v", e)
						}
					},
					ObservationCallbacks: func() func(Observation) { return func(e Observation) { record(e) } },
					ToolPolicy: func(r ToolCallRequest) error {
						if r.Name == "spawn_agent" || r.Name == "send_agent_message" {
							if r.AgentID != "" {
								t.Error("root special tool has child identity")
							}
							if route == "spawn veto" && r.Name == "spawn_agent" {
								policies.Add(1)
								if deny {
									return errors.New(secret)
								}
							}
						}
						if r.Name == "helper_probe" {
							policies.Add(1)
							id, _ := childID.Load().(string)
							if id == "" || r.AgentID != id {
								t.Errorf("policy identity=%q runtime=%q", r.AgentID, id)
							}
							if _, ok := session.agentManager.Get(r.AgentID); !ok {
								t.Error("policy identity absent from actual registry")
							}
							var args map[string]string
							if err := json.Unmarshal([]byte(r.Arguments), &args); err != nil || args["value"] != "preserved helper arguments" {
								t.Errorf("policy arguments=%s error=%v", r.Arguments, err)
							}
							if deny && route != "spawn veto" {
								return errors.New(secret)
							}
						}
						return nil
					},
				}
				var err error
				session, err = NewSession(ctx, types.Config{RequireToolExecutionBridge: true, BaseDir: t.TempDir(), Model: "policy", APIKey: "policy", BaseURL: backend.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), BuiltinTools: []string{"spawn_agent", "send_agent_message"}, AgentOptions: types.AgentOptions{Iterations: 6, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}}, cb, clientT)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				session.cfgClients["counter-fixture"] = session.clients[0]
				session.clients = nil
				done := make(chan error, 1)
				go func() { response, err := session.SendMessage("root"); record(response); done <- err }()
				if route == "injection" {
					select {
					case <-waiting:
					case <-ctx.Done():
						t.Fatal("helper never waited")
					}
					id, _ := childID.Load().(string)
					if id == "" {
						t.Fatal("no runtime ID")
					}
					if err := session.SendToAgent(id, "injected policy instruction"); err != nil {
						t.Fatal(err)
					}
					close(release)
				}
				select {
				case err := <-done:
					if err != nil && !deny {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("root timed out")
				}
				waitChild := func() {
					select {
					case id := <-completed:
						if id == "" {
							t.Error("empty completed ID")
						}
					case <-ctx.Done():
						t.Fatal("helper timed out")
					}
				}
				if route != "spawn veto" || !deny {
					waitChild()
				}
				if route == "resume" {
					id := childID.Load().(string)
					if err := session.SendToAgent(id, "finished"); !errors.Is(err, ErrAgentFinished) {
						t.Fatalf("expected genuinely finished helper, got %v", err)
					}
					if response, err := session.SendMessage("resume"); err != nil && !deny {
						t.Fatal(err)
					} else {
						record(response)
					}
				}
				want := int32(1)
				if deny {
					want = 0
				}
				if bodies.Load() != want {
					t.Errorf("fake body=%d want=%d", bodies.Load(), want)
				}
				wantPolicies := int32(1)
				if route == "spawn veto" && !deny {
					wantPolicies = 2
				}
				if policies.Load() != wantPolicies {
					t.Errorf("policy calls=%d want=%d", policies.Load(), wantPolicies)
				}
				if route == "spawn veto" && deny {
					if len(session.agentManager.List()) != 0 || childCalls.Load() != 0 {
						t.Error("veto created or executed helper")
					}
				} else {
					agents := session.agentManager.List()
					id, _ := childID.Load().(string)
					if len(agents) != 1 || id == "" || agents[0].ID != id {
						t.Errorf("registry/runtime mismatch: %v %q", agents, id)
					}
				}
				if route == "injection" && seedBodies.Load() != 1 {
					t.Error("injection step did not execute")
				}
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(strings.Join(captured, "\n"), secret) {
					t.Error("private policy error leaked to model/events")
				}
			})
		}
	}
}
