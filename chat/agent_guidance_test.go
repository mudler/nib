package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

const rootGuidanceHeading = "[Root delegation guidance]"
const childGuidanceHeading = "[Child task guidance]"

func messageText(ms []openai.ChatCompletionMessage) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}
func hasSchema(r openai.ChatCompletionRequest, name string) bool {
	for _, tool := range r.Tools {
		if tool.Function != nil && tool.Function.Name == name {
			return true
		}
	}
	return false
}
func guidanceConfig(url string) types.Config {
	return types.Config{Model: "fake-model", APIKey: "fake-key", BaseURL: url + "/v1", ApprovalMode: "auto", AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1}}
}
func guidanceReply(w http.ResponseWriter, name string, args any) {
	msg := map[string]any{"role": "assistant", "content": "done"}
	finish := "stop"
	if name != "" {
		data, _ := json.Marshal(args)
		msg["content"] = nil
		msg["tool_calls"] = []any{map[string]any{"id": "call_" + name, "type": "function", "index": 0, "function": map[string]any{"name": name, "arguments": string(data)}}}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "fake", "object": "chat.completion", "model": "fake", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}})
}

func TestRootDelegationGuidanceOutbound(t *testing.T) {
	for _, prompt := range []string{"", "CUSTOM PROMPT: spawn_agent stays verbatim"} {
		for _, allow := range [][]string{nil, {"spawn_agent"}, {"check_agent", "spawn_agent", "agent_logs"}, {"spawn_agent", "get_agent_result"}, {"spawn_agent", "send_agent_message"}, {"agent_logs"}, {"read"}} {
			t.Run(prompt+strings.Join(allow, ","), func(t *testing.T) {
				var requests []openai.ChatCompletionRequest
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						http.NotFound(w, r)
						return
					}
					var req openai.ChatCompletionRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					requests = append(requests, req)
					guidanceReply(w, "", nil)
				}))
				defer srv.Close()
				cfg := guidanceConfig(srv.URL)
				cfg.Prompt = prompt
				cfg.BuiltinTools = allow
				cfg.Agents = config.MergeAgentTypes(nil)
				s, err := NewSession(context.Background(), cfg, Callbacks{})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				for i := 0; i < 2; i++ {
					if err := s.Reload(cfg); err != nil {
						t.Fatal(err)
					}
					if _, err := s.SendMessage("root-only sentinel"); err != nil {
						t.Fatal(err)
					}
				}
				if len(requests) != 2 {
					t.Fatalf("requests = %d", len(requests))
				}
				for _, req := range requests {
					text := messageText(req.Messages)
					available := func(name string) bool { return len(allow) == 0 || slices.Contains(allow, name) }
					enabled := available("spawn_agent")
					want := 0
					if enabled {
						want = 1
					}
					if strings.Count(text, rootGuidanceHeading) != want {
						t.Errorf("root guidance count = %d, want %d", strings.Count(text, rootGuidanceHeading), want)
					}
					if prompt != "" && !strings.Contains(text, prompt) {
						t.Error("custom prompt lost")
					}
					for _, name := range []string{"spawn_agent", "check_agent", "get_agent_result", "send_agent_message"} {
						if hasSchema(req, name) != available(name) {
							t.Errorf("schema %s enabled = %v", name, hasSchema(req, name))
						}
					}
					logs := available("agent_logs")
					if hasSchema(req, "agent_logs") != logs {
						t.Error("agent_logs schema mismatch")
					}
					if enabled {
						for _, phrase := range []string{"acceptance", "working-directory", "worktree", "independently verified", "Available sub-agent types", "general:"} {
							if !strings.Contains(text, phrase) {
								t.Errorf("missing %q", phrase)
							}
						}
						block := text
						if idx := strings.Index(text, rootGuidanceHeading); idx >= 0 {
							block = text[idx:]
						}
						for _, name := range []string{"check_agent", "get_agent_result", "send_agent_message", "agent_logs"} {
							if strings.Contains(block, name) != available(name) {
								t.Errorf("%s guidance availability mismatch", name)
							}
						}
						if strings.Contains(block, "existing ID") != available("send_agent_message") {
							t.Error("follow-up guidance availability mismatch")
						}
					}
					if strings.Contains(text, childGuidanceHeading) {
						t.Error("child guidance leaked into root")
					}
				}
			})
		}
	}
}

func TestChildDelegationGuidanceOutbound(t *testing.T) {
	for _, tc := range []struct{ name, persona, model string }{
		{"", "", ""}, {"general", "You are a focused sub-agent. Complete the given task and report a concise result.", ""},
		{"general", "CUSTOM PERSONA verbatim", ""}, {"custom", "CUSTOM new persona", ""}, {"empty", "", ""}, {"override", "MODEL persona", "other-model"},
	} {
		t.Run(tc.name+tc.persona, func(t *testing.T) {
			var mu sync.Mutex
			var children []openai.ChatCompletionRequest
			rootCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				var req openai.ChatCompletionRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				isChild := false
				for _, m := range req.Messages {
					if m.Role == "user" && m.Content == "bounded task" {
						isChild = true
					}
				}
				if isChild {
					mu.Lock()
					children = append(children, req)
					mu.Unlock()
					guidanceReply(w, "", nil)
					return
				}
				rootCalls++
				if rootCalls == 1 {
					args := map[string]any{"task": "bounded task", "background": false}
					if tc.name != "" {
						args["agent_type"] = tc.name
					}
					guidanceReply(w, "spawn_agent", args)
					return
				}
				guidanceReply(w, "", nil)
			}))
			defer srv.Close()
			cfg := guidanceConfig(srv.URL)
			cfg.Prompt = "ROOT PRIVATE SENTINEL"
			cfg.Agents = config.MergeAgentTypes(nil)
			if tc.name != "" {
				cfg.Agents = config.MergeAgentTypes([]types.AgentTypeConfig{{Name: tc.name, SystemPrompt: tc.persona, Model: tc.model}})
			}
			s, err := NewSession(context.Background(), cfg, Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err = s.SendMessage("root-only conversation"); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(children) == 0 {
				t.Fatal("no child request")
			}
			for _, req := range children {
				assertChildRequest(t, req)
				text := messageText(req.Messages)
				if !strings.Contains(text, "bounded task") || !strings.Contains(text, tc.persona) {
					t.Error("task/persona lost")
				}
				if strings.Contains(text, "ROOT PRIVATE SENTINEL") || strings.Contains(text, "root-only conversation") || strings.Contains(text, rootGuidanceHeading) {
					t.Error("root context leaked")
				}
				if tc.model != "" && req.Model != tc.model {
					t.Errorf("model = %s", req.Model)
				}
			}
		})
	}
}
func assertChildRequest(t *testing.T, req openai.ChatCompletionRequest) {
	t.Helper()
	text := messageText(req.Messages)
	if strings.Count(text, childGuidanceHeading) != 1 {
		t.Errorf("child guidance count = %d", strings.Count(text, childGuidanceHeading))
	}
	for _, p := range []string{"Do not spawn or manage other agents", "nested nib/other agent CLIs", "blockers", "checks actually run"} {
		if !strings.Contains(text, p) {
			t.Errorf("missing %q", p)
		}
	}
	for _, name := range []string{"spawn_agent", "check_agent", "get_agent_result", "send_agent_message"} {
		if hasSchema(req, name) {
			t.Errorf("ordinary child exposed %s", name)
		}
	}
}

// Capture all three request APIs without relying on a provider's streaming adapter.
type guidanceCapture struct {
	cogito.LLM
	request  openai.ChatCompletionRequest
	fragment cogito.Fragment
}

func (f *guidanceCapture) CreateChatCompletion(_ context.Context, r openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	f.request = r
	return cogito.LLMReply{}, cogito.LLMUsage{}, nil
}
func (f *guidanceCapture) Ask(_ context.Context, r cogito.Fragment) (cogito.Fragment, error) {
	f.fragment = r
	return r, nil
}

type guidanceStreamCapture struct{ *guidanceCapture }

func (f *guidanceStreamCapture) CreateChatCompletionStream(_ context.Context, r openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	f.request = r
	ch := make(chan cogito.StreamEvent)
	close(ch)
	return ch, nil
}

func TestChildGuidanceRequestAPIs(t *testing.T) {
	for _, persona := range []string{"", "PERSONA"} {
		base := &guidanceCapture{}
		wrapped := guideChildLLM(base)
		if _, ok := wrapped.(cogito.StreamingLLM); ok {
			t.Fatal("invented streaming support")
		}
		msgs := []openai.ChatCompletionMessage{{Role: "user", Content: "TASK"}}
		if persona != "" {
			msgs = append([]openai.ChatCompletionMessage{{Role: "system", Content: persona}}, msgs...)
		}
		req := openai.ChatCompletionRequest{Model: "model", Temperature: 0.3, Messages: msgs, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "read"}}}}
		original, _ := json.Marshal(req)
		_, _, err := wrapped.CreateChatCompletion(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		assertChildRequest(t, base.request)
		first := base.request
		_, _, _ = wrapped.CreateChatCompletion(context.Background(), first)
		if !reflect.DeepEqual(first, base.request) {
			t.Error("not idempotent")
		}
		if base.request.Model != req.Model || base.request.Temperature != req.Temperature || !reflect.DeepEqual(base.request.Tools, req.Tools) {
			t.Error("changed request parameters")
		}
		after, _ := json.Marshal(req)
		if string(original) != string(after) {
			t.Error("mutated source request")
		}
		f := cogito.Fragment{Messages: msgs}
		_, _ = wrapped.Ask(context.Background(), f)
		assertChildRequest(t, openai.ChatCompletionRequest{Messages: base.fragment.Messages})
		if !reflect.DeepEqual(f.Messages, msgs) {
			t.Error("mutated fragment")
		}
		stream := guideChildLLM(&guidanceStreamCapture{base}).(cogito.StreamingLLM)
		_, _ = stream.CreateChatCompletionStream(context.Background(), req)
		assertChildRequest(t, base.request)
	}
}

// Exercise the native message tool, not SendToAgent (which deliberately refuses
// finished agents). The live case uses the same request barrier and harmless
// tool step as agentInputBackend so delivery happens before the child's next step.
func TestChildDelegationGuidanceBackgroundFollowupOutbound(t *testing.T) {
	for _, live := range []bool{true, false} {
		name := "finished_resume"
		if live {
			name = "live_send_agent_message"
		}
		t.Run(name, func(t *testing.T) {
			const task = "bounded background task verbatim"
			const persona = "CUSTOM background persona verbatim"
			const followup = "related follow-up verbatim"
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			waiting, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			ids := make(chan string, 1)
			var mu sync.Mutex
			var children []openai.ChatCompletionRequest
			rootCalls := 0
			id := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				var req openai.ChatCompletionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				isChild := false
				for _, m := range req.Messages {
					if m.Role == "user" && m.Content == task {
						isChild = true
					}
				}
				if isChild {
					mu.Lock()
					children = append(children, req)
					first := len(children) == 1
					mu.Unlock()
					if first {
						close(waiting)
						if live {
							select {
							case <-release:
							case <-ctx.Done():
								return
							}
							guidanceReply(w, "agent_logs", map[string]any{"agent_id": "none"})
							return
						}
					}
					guidanceReply(w, "", nil)
					return
				}
				rootCalls++
				switch rootCalls {
				case 1:
					guidanceReply(w, "spawn_agent", map[string]any{"task": task, "agent_type": "custom", "background": true})
				case 2:
					select {
					case id = <-ids:
					case <-ctx.Done():
						return
					}
					select {
					case <-waiting:
					case <-ctx.Done():
						return
					}
					if live {
						guidanceReply(w, "send_agent_message", map[string]any{"agent_id": id, "message": followup})
					} else {
						guidanceReply(w, "get_agent_result", map[string]any{"agent_id": id, "wait": true})
					}
				case 3:
					if live {
						if !strings.Contains(messageText(req.Messages), "Message delivered to running agent "+id) {
							t.Error("native send_agent_message did not confirm live delivery")
						}
						unblock()
						guidanceReply(w, "get_agent_result", map[string]any{"agent_id": id, "wait": true})
					} else {
						guidanceReply(w, "send_agent_message", map[string]any{"agent_id": id, "message": followup})
					}
				default:
					guidanceReply(w, "", nil)
				}
			}))
			defer srv.Close()
			defer unblock()
			cfg := guidanceConfig(srv.URL)
			cfg.Prompt = "ROOT PRIVATE SENTINEL"
			cfg.Agents = config.MergeAgentTypes([]types.AgentTypeConfig{{Name: "custom", SystemPrompt: persona}})
			s, err := NewSession(ctx, cfg, Callbacks{OnAgentEvent: func(ev AgentEvent) {
				if ev.Status == AgentStatusRunning {
					select {
					case ids <- ev.ID:
					default:
					}
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.SendMessage("root-only conversation"); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(children) != 2 {
				t.Fatalf("child requests = %d, want initial and follow-up", len(children))
			}
			for i, req := range children {
				assertChildRequest(t, req)
				text := messageText(req.Messages)
				if !strings.Contains(text, persona) {
					t.Errorf("request %d lost custom persona", i)
				}
				foundTask := false
				for _, m := range req.Messages {
					foundTask = foundTask || (m.Role == "user" && m.Content == task)
				}
				if !foundTask {
					t.Errorf("request %d lost verbatim task", i)
				}
				for _, sentinel := range []string{"ROOT PRIVATE SENTINEL", "root-only conversation", rootGuidanceHeading} {
					if strings.Contains(text, sentinel) {
						t.Errorf("request %d leaked %q", i, sentinel)
					}
				}
				if strings.Contains(text, followup) != (i == 1) {
					t.Errorf("request %d has incorrect follow-up presence", i)
				}
			}
		})
	}
}
