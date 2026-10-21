package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

type executionProbeLLM struct {
	captureLLM
	requests []openai.ChatCompletionRequest
}

func (l *executionProbeLLM) CreateChatCompletion(_ context.Context, r openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.requests = append(l.requests, r)
	m := openai.ChatCompletionMessage{Role: "assistant", Content: "done"}
	if len(l.requests) == 1 {
		m.Content = ""
		m.ToolCalls = []openai.ToolCall{{ID: "protocol-counter", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "counter", Arguments: `{"value":"private"}`}}}
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: m}}}}, cogito.LLMUsage{}, nil
}
func runExecutionProbe(t *testing.T, cb Callbacks, mode string) (*Session, *executionProbeLLM, *atomic.Int32, error) {
	t.Helper()
	ctx := context.Background()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "counter", Version: "1"}, nil)
	n := &atomic.Int32{}
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "counter", Description: "counter only"}, func(context.Context, *sdkmcp.CallToolRequest, map[string]any) (*sdkmcp.CallToolResult, map[string]any, error) {
		n.Add(1)
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "native-private"}}}, nil, nil
	})
	st, ct := sdkmcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	s, err := NewSession(ctx, types.Config{Model: "fake", APIKey: "fake", BaseDir: t.TempDir(), BuiltinTools: []string{"__no_builtins__"}, WorkingDir: t.TempDir(), ApprovalMode: types.ApprovalMode(mode), RequireToolExecutionBridge: true, AgentOptions: types.AgentOptions{Iterations: 3, MaxAttempts: 3, MaxRetries: 1, NoTitles: true}}, cb, ct)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.cfgClients["counter-fixture"] = s.clients[0]
	s.clients = nil
	l := &executionProbeLLM{}
	s.llm = l
	_, err = s.SendMessage("counter only")
	return s, l, n, err
}
func invokeExecutionProbe(ctx context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
	return b(ctx)
}
func TestExecutionBridgeRequiredMissing(t *testing.T) {
	s, e := NewSession(context.Background(), types.Config{RequireToolExecutionBridge: true}, Callbacks{})
	if s != nil {
		s.Close()
	}
	if e == nil {
		t.Fatal("required bridge absent")
	}
}
func TestExecutionBridgeNativeMCP(t *testing.T) {
	_, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: invokeExecutionProbe}, "auto")
	if e != nil || n.Load() != 1 {
		t.Fatalf("%v bodies=%d", e, n.Load())
	}
}
func TestExecutionBridgeCorrelation(t *testing.T) {
	var policy ToolCallRequest
	_, _, n, e := runExecutionProbe(t, Callbacks{ToolPolicy: func(r ToolCallRequest) error { policy = r; return nil }, ToolExecutionBridge: func(ctx context.Context, c ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		if c.ToolCallID() != "protocol-counter" || policy.ID != c.ToolCallID() || policy.InvocationID == "" || policy.InvocationID != c.InvocationID() || policy.AgentID != c.AgentID() || policy.Name != c.Name() {
			t.Error("candidate identity changed")
		}
		return b(ctx)
	}}, "auto")
	if e != nil || n.Load() != 1 {
		t.Fatalf("%v %d", e, n.Load())
	}
}
func TestExecutionBridgeArgumentIsolation(t *testing.T) {
	_, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(ctx context.Context, c ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		c.Arguments()["value"] = "changed"
		if c.Arguments()["value"] != "private" {
			t.Error("aliased")
		}
		return b(ctx)
	}}, "auto")
	if e != nil || n.Load() != 1 {
		t.Fatalf("%v %d", e, n.Load())
	}
}
func TestExecutionBridgeSubstitutionModel(t *testing.T) {
	_, l, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(context.Context, ToolExecutionCall, ToolExecutionBody) (ToolExecutionResult, error) {
		return ToolExecutionResult{Text: "authorized-replay"}, nil
	}}, "auto")
	if e != nil || n.Load() != 0 {
		t.Fatalf("%v %d", e, n.Load())
	}
	b, _ := json.Marshal(l.requests)
	if !strings.Contains(string(b), "authorized-replay") || strings.Contains(string(b), "native-private") {
		t.Fatal("missing substitution")
	}
}
func TestExecutionBridgeSubstitutionContext(t *testing.T) {
	s, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(context.Context, ToolExecutionCall, ToolExecutionBody) (ToolExecutionResult, error) {
		return ToolExecutionResult{Text: "authorized-replay"}, nil
	}}, "auto")
	b, _ := json.Marshal(s.fragment)
	if e != nil || n.Load() != 0 || !strings.Contains(string(b), "authorized-replay") {
		t.Fatalf("%v %d missing context", e, n.Load())
	}
}
func TestExecutionBridgeDuplicateSequential(t *testing.T) {
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(ctx context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		r, e := b(ctx)
		if _, e2 := b(ctx); e2 == nil {
			t.Error("duplicate admitted")
		}
		return r, e
	}}, "auto")
	if n.Load() != 1 {
		t.Fatalf("bodies=%d", n.Load())
	}
}
func TestExecutionBridgeDuplicateConcurrent(t *testing.T) {
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(ctx context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var result ToolExecutionResult
		var ok bool
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, e := b(ctx)
				if e == nil {
					mu.Lock()
					result = r
					ok = true
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if !ok {
			return ToolExecutionResult{}, errors.New("none")
		}
		return result, nil
	}}, "auto")
	if n.Load() != 1 {
		t.Fatalf("bodies=%d", n.Load())
	}
}
func TestExecutionBridgeExpiredContinuation(t *testing.T) {
	var retained ToolExecutionBody
	_, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(_ context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		retained = b
		return ToolExecutionResult{Text: "replay"}, nil
	}}, "auto")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := retained(context.Background()); e == nil || n.Load() != 0 {
		t.Fatal("expired continuation entered")
	}
}
func TestExecutionBridgeCancelledContinuation(t *testing.T) {
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(ctx context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return b(ctx)
	}}, "auto")
	if n.Load() != 0 {
		t.Fatal("cancelled body entered")
	}
}
func TestExecutionBridgeUnknownHolds(t *testing.T) {
	_, l, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(context.Context, ToolExecutionCall, ToolExecutionBody) (ToolExecutionResult, error) {
		return ToolExecutionResult{Text: "private-result"}, errors.New("private-error")
	}}, "auto")
	b, _ := json.Marshal(l.requests)
	if e == nil || n.Load() != 0 || strings.Contains(string(b), "private-result") || strings.Contains(string(b), "private-error") {
		t.Fatalf("unsafe publication %v bodies=%d", e, n.Load())
	}
}
func TestExecutionBridgeNoRetry(t *testing.T) {
	var bridges atomic.Int32
	_, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(ctx context.Context, _ ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		bridges.Add(1)
		_, _ = b(ctx)
		return ToolExecutionResult{}, errors.New("unknown")
	}}, "auto")
	if e == nil || bridges.Load() != 1 || n.Load() != 1 {
		t.Fatalf("retry %v bridges=%d bodies=%d", e, bridges.Load(), n.Load())
	}
}
func TestExecutionBridgePolicyBeforeApproval(t *testing.T) {
	var approvals, bridges atomic.Int32
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolPolicy: func(ToolCallRequest) error { return errors.New("veto") }, OnToolCall: func(ToolCallRequest) ToolCallResponse { approvals.Add(1); return ToolCallResponse{Approved: true} }, ToolExecutionBridge: func(ctx context.Context, c ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
		bridges.Add(1)
		return b(ctx)
	}}, "allowlist")
	if n.Load() != 0 || approvals.Load() != 0 || bridges.Load() != 0 {
		t.Fatal("veto bypass")
	}
}
func TestExecutionBridgeOrdinaryDeny(t *testing.T) {
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolPolicy: func(ToolCallRequest) error { return nil }, OnToolCall: func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{} }, ToolExecutionBridge: invokeExecutionProbe}, "allowlist")
	if n.Load() != 0 {
		t.Fatal("policy granted permission")
	}
}
func TestExecutionBridgeOrdinaryGrant(t *testing.T) {
	_, _, n, e := runExecutionProbe(t, Callbacks{OnToolCall: func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{Approved: true} }, ToolExecutionBridge: invokeExecutionProbe}, "allowlist")
	if e != nil || n.Load() != 1 {
		t.Fatalf("%v %d", e, n.Load())
	}
}
func TestExecutionBridgeHostValidationSeam(t *testing.T) {
	for _, reason := range []string{"stale attempt", "stale epoch", "forged caller", "revoked grant"} {
		t.Run(reason, func(t *testing.T) {
			_, _, n, e := runExecutionProbe(t, Callbacks{ToolExecutionBridge: func(context.Context, ToolExecutionCall, ToolExecutionBody) (ToolExecutionResult, error) {
				return ToolExecutionResult{}, errors.New(reason)
			}}, "auto")
			if e == nil || n.Load() != 0 {
				t.Fatal("host rejection bypass")
			}
		})
	}
}
func TestExecutionBridgeNilApprovalDefaultDeny(t *testing.T) {
	_, _, n, _ := runExecutionProbe(t, Callbacks{ToolExecutionBridge: invokeExecutionProbe}, "allowlist")
	if n.Load() != 0 {
		t.Fatal("secure mode without ordinary approval granted")
	}
}

// Approval mutation audit: use nib's actual option builder and Cogito approval
// seam (nib's public ToolCallResponse deliberately has no Modified field).
func TestExecutionBridgeApprovalModifiedReevaluation(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		t.Run(map[bool]string{false: "returned", true: "in_place"}[inPlace], func(t *testing.T) {
			s := newWarmTestSession(t)
			s.toolAllow = map[string]bool{"counter_0": true}
			var candidates []ToolCallRequest
			var bodies atomic.Int32
			s.callbacks.ToolExecutionBridge = func(ctx context.Context, c ToolExecutionCall, b ToolExecutionBody) (ToolExecutionResult, error) {
				bodies.Add(1)
				if c.Arguments()["value"] != "approved" {
					t.Error("unevaluated arguments")
				}
				return ToolExecutionResult{Text: "projected"}, nil
			}
			s.callbacks.ToolPolicy = func(r ToolCallRequest) error { candidates = append(candidates, r); return nil }
			l := &executionProbeLLM{}
			// Discover the real in-process MCP counter used by the session fixture.
			client := connectTestMCP(t, "counter", 1, 10)
			// That helper names its tool counter_0; use an explicit model adapter below.
			opts := s.toolOptions(context.Background(), "", "")
			opts = append(opts, cogito.WithMCPs(client), cogito.WithIterations(2), cogito.WithToolCallBack(func(c *cogito.ToolChoice, _ *cogito.SessionState) cogito.ToolCallDecision {
				if c.Arguments["value"] == "private" {
					if inPlace {
						c.Arguments["value"] = "approved"
						return cogito.ToolCallDecision{Approved: true}
					}
					modified := *c
					modified.Arguments = map[string]any{"value": "approved"}
					return cogito.ToolCallDecision{Approved: true, Modified: &modified}
				}
				return cogito.ToolCallDecision{Approved: true}
			}))
			_, err := cogito.ExecuteTools(&executionRenameLLM{executionProbeLLM: l}, cogito.NewEmptyFragment(), opts...)
			if err != nil || bodies.Load() != 1 || len(candidates) != 2 {
				t.Fatalf("err=%v bridges=%d policies=%d", err, bodies.Load(), len(candidates))
			}
			if candidates[0].ID != candidates[1].ID || candidates[0].InvocationID == "" || candidates[0].InvocationID != candidates[1].InvocationID {
				t.Fatal("reevaluation dropped IDs")
			}
		})
	}
}

type executionRenameLLM struct{ *executionProbeLLM }

func (l *executionRenameLLM) CreateChatCompletion(ctx context.Context, r openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	reply, u, e := l.executionProbeLLM.CreateChatCompletion(ctx, r)
	if len(reply.ChatCompletionResponse.Choices) > 0 && len(reply.ChatCompletionResponse.Choices[0].Message.ToolCalls) > 0 {
		reply.ChatCompletionResponse.Choices[0].Message.ToolCalls[0].Function.Name = "counter_0"
	}
	return reply, u, e
}
