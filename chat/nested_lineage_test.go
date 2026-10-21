package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

// Only the provider is scripted. Spawn, resume, registry, policy, MCP bodies,
// and captured lifecycle sinks all use the production SendMessage path.
func TestNestedLineageLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name                                                  string
		nested, parentBG, leafBG, concurrent, reverse, resume bool
	}{
		{name: "foreground"}, {name: "background", leafBG: true},
		{name: "nested foreground", nested: true}, {name: "nested background leaf", nested: true, leafBG: true},
		{name: "nested background parent", nested: true, parentBG: true}, {name: "nested both background", nested: true, parentBG: true, leafBG: true},
		{name: "concurrent forward", nested: true, parentBG: true, leafBG: true, concurrent: true},
		{name: "concurrent reverse", nested: true, parentBG: true, leafBG: true, concurrent: true, reverse: true},
		{name: "resume original parent", nested: true, resume: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var mu sync.Mutex
			var events []AgentEvent
			var captures, bodies, approvals atomic.Int32
			var resumedID string
			var rootCalls int
			var resumeSent bool
			arrived := make(chan string, 2)
			gates := map[string]chan struct{}{}
			var s *Session
			spawn := func(kind string, bg bool) string {
				tools := []string{"probe"}
				if kind == "branch" {
					tools = append(tools, "spawn_agent")
				}
				b, _ := json.Marshal(map[string]any{"task": "identical", "agent_type": kind, "background": bg, "tools": tools})
				return string(b)
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Model    string `json:"model"`
					Messages []struct {
						Role    string `json:"role"`
						Content any    `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				toolResults := 0
				for _, m := range req.Messages {
					if m.Role == "tool" {
						toolResults++
					}
				}
				type call struct{ Name, Args string }
				var calls []call
				switch req.Model {
				case "root":
					mu.Lock()
					rootCalls++
					n := rootCalls
					id := resumedID
					sendResume := tc.resume && id != "" && !resumeSent
					if sendResume {
						resumeSent = true
					}
					mu.Unlock()
					if n == 1 {
						kind := "leaf"
						bg := tc.leafBG
						if tc.nested {
							kind = "branch"
							bg = tc.parentBG
						}
						calls = append(calls, call{"spawn_agent", spawn(kind, bg)})
						if tc.concurrent {
							calls = append(calls, call{"spawn_agent", spawn(kind, bg)})
						}
					}
					if sendResume {
						calls = append(calls, call{"send_agent_message", fmt.Sprintf(`{"agent_id":%q,"message":"continue"}`, id)})
					}
				case "branch":
					if toolResults == 0 {
						calls = append(calls, call{"spawn_agent", spawn("leaf", tc.leafBG)})
					}
				case "leaf":
					// Each fresh generation (including resume) executes the MCP body once.
					lastUser, lastTool := -1, -1
					for i, m := range req.Messages {
						if m.Role == "user" {
							lastUser = i
						}
						if m.Role == "tool" {
							lastTool = i
						}
					}
					if lastTool < lastUser {
						calls = append(calls, call{"probe", "{}"})
					}
				}
				// Cogito resumes with the caller's LLM while retaining the child's
				// fragment and captured execution options. Route that provider request
				// by its explicit follow-up message, never by lifecycle inference.
				if tc.resume && req.Model == "root" {
					lastUser, lastTool := -1, -1
					followup := false
					for i, m := range req.Messages {
						if m.Role == "user" {
							lastUser = i
							followup = m.Content == "continue"
						}
						if m.Role == "tool" {
							lastTool = i
						}
					}
					if followup && lastUser > lastTool {
						calls = []call{{"probe", "{}"}}
					}
				}
				msg := map[string]any{"role": "assistant", "content": "done"}
				if len(calls) > 0 {
					msg["content"] = ""
					var out []any
					for i, c := range calls {
						out = append(out, map[string]any{"id": fmt.Sprintf("identical-%d", i), "type": "function", "function": map[string]any{"name": c.Name, "arguments": c.Args}})
					}
					msg["tool_calls"] = out
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"id": "lineage", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
			}))
			defer backend.Close()
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "lineage", Version: "v0"}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "probe", Description: "lineage probe"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
				bodies.Add(1)
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, nil, nil
			})
			mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
			defer mcpServer.Close()
			cb := Callbacks{
				ToolExecutionBridge: invokeExecutionProbe,
				AgentCallbacks: func() func(AgentEvent) {
					generation := uint64(captures.Add(1))
					return func(e AgentEvent) {
						if a, ok := s.AgentManager().Get(e.ID); !ok || a.ParentID != e.ParentID && e.ParentID != "" {
							t.Errorf("callback lacks matching native registry entry: %+v", e)
						}
						e.ObservationScope.Generation = generation
						mu.Lock()
						events = append(events, e)
						mu.Unlock()
					}
				},
				OnAgentEvent: func(AgentEvent) { t.Error("uncaptured fallback invoked") },
				OnToolCall: func(ToolCallRequest) ToolCallResponse {
					approvals.Add(1)
					return ToolCallResponse{Approved: true, AllowAllTurn: true}
				},
				ToolPolicy: func(r ToolCallRequest) error {
					if r.AgentID == "" {
						return nil
					}
					a, ok := s.AgentManager().Get(r.AgentID)
					if !ok {
						return fmt.Errorf("native policy ID absent: %s", r.AgentID)
					}
					mu.Lock()
					seen := false
					for _, e := range events {
						if e.ID == a.ID && e.Status == AgentStatusRunning {
							seen = true
						}
					}
					mu.Unlock()
					if !seen {
						return fmt.Errorf("policy before captured running: %s", a.ID)
					}
					if a.ParentID != "" {
						if _, ok := s.AgentManager().Get(a.ParentID); !ok {
							return fmt.Errorf("native parent absent: %s", a.ParentID)
						}
					}
					if tc.concurrent && r.Name == "probe" {
						gate := make(chan struct{})
						mu.Lock()
						gates[a.ID] = gate
						mu.Unlock()
						arrived <- a.ID
						select {
						case <-gate:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				},
			}
			var err error
			s, err = NewSession(ctx, types.Config{Model: "root", APIKey: "fake", BaseURL: backend.URL + "/v1", WorkingDir: t.TempDir(), ApprovalMode: "allowlist", RequireToolExecutionBridge: true, BuiltinTools: []string{"spawn_agent", "send_agent_message"}, Agents: []types.AgentTypeConfig{{Name: "branch", Model: "branch", Tools: []string{"spawn_agent", "probe"}}, {Name: "leaf", Model: "leaf", Tools: []string{"probe"}}}, AgentOptions: types.AgentOptions{Iterations: 8, MaxAttempts: 1, MaxRetries: 1, NoTitles: true}}, cb)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.ReconcileMCPServers(map[string]types.MCPServer{"lineage": {URL: mcpServer.URL}}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := s.SendMessage("identical"); done <- err }()
			if tc.concurrent {
				ids := []string{}
				for i := 0; i < 2; i++ {
					select {
					case id := <-arrived:
						ids = append(ids, id)
					case <-ctx.Done():
						t.Fatal("concurrent leaves did not arrive")
					}
				}
				if tc.reverse {
					ids[0], ids[1] = ids[1], ids[0]
				}
				for _, id := range ids {
					mu.Lock()
					close(gates[id])
					mu.Unlock()
					if _, err := s.AgentManager().Wait(id); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("SendMessage did not finish")
			}
			agents := s.AgentManager().List()
			want := 1
			if tc.nested {
				want = 2
			}
			if tc.concurrent {
				want *= 2
			}
			if len(agents) != want {
				t.Fatalf("registry agents=%d want=%d", len(agents), want)
			}
			for _, a := range agents {
				if _, err := s.AgentManager().Wait(a.ID); err != nil {
					t.Fatal(err)
				}
			}
			originalParents := map[string]string{}
			for _, a := range agents {
				originalParents[a.ID] = a.ParentID
			}
			if tc.resume {
				for _, a := range agents {
					if a.ParentID != "" {
						mu.Lock()
						resumedID = a.ID
						mu.Unlock()
					}
				}
				if resumedID == "" {
					t.Fatal("no genuine nested child to resume")
				}
				if _, err = s.SendMessage("resume"); err != nil {
					t.Fatal(err)
				}
				if _, err = s.AgentManager().Wait(resumedID); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(s.AgentManager().List()) != want {
				t.Fatal("resume replaced native ID")
			}
			roots, leaves := 0, 0
			for _, a := range agents {
				if a.ParentID != originalParents[a.ID] {
					t.Errorf("resume changed original parent: ID=%s before=%s after=%s", a.ID, originalParents[a.ID], a.ParentID)
				}
				if a.ParentID == "" {
					roots++
				} else {
					leaves++
				}
				running, terminal := 0, 0
				for _, e := range events {
					if e.ID != a.ID {
						continue
					}
					if e.ParentID != a.ParentID {
						t.Errorf("event %s status=%s parent=%q native=%q", e.ID, e.Status, e.ParentID, a.ParentID)
					}
					if e.Task != "identical" {
						t.Errorf("task=%q", e.Task)
					}
					if e.ObservationScope.Generation != 1 {
						t.Errorf("generation changed on original native lifetime: %+v", e)
					}
					switch e.Status {
					case AgentStatusRunning:
						running++
					case AgentStatusCompleted:
						terminal++
					default:
						t.Errorf("unexpected lifecycle %+v", e)
					}
				}
				count := 1
				if a.ID == resumedID {
					count = 2
				}
				if running != count || terminal != count {
					t.Errorf("ID=%s running=%d terminal=%d want=%d", a.ID, running, terminal, count)
				}
			}
			expectedRoots := 1
			if tc.concurrent {
				expectedRoots = 2
			}
			if roots != expectedRoots || (tc.nested && leaves != expectedRoots) {
				t.Errorf("roots=%d nested=%d", roots, leaves)
			}
			expectedBodies := int32(1)
			if tc.concurrent {
				expectedBodies = 2
			}
			if tc.resume {
				expectedBodies++
			}
			if bodies.Load() != expectedBodies {
				t.Errorf("MCP bodies=%d want=%d", bodies.Load(), expectedBodies)
			}
			if approvals.Load() == 0 {
				t.Error("approval grant path not exercised")
			}
			expectedCaptures := int32(1)
			if tc.resume {
				expectedCaptures = 2
			}
			if captures.Load() != expectedCaptures {
				t.Errorf("captures=%d", captures.Load())
			}
		})
	}
}
