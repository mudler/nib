package chat

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/types"
)

func TestToolPolicyPrecedesApprovals(t *testing.T) {
	for _, name := range []string{"auto", "turn", "tool", "prefix", "readonly", "classifier", "hook", "prompt", "no callback", "external"} {
		t.Run(name, func(t *testing.T) {
			s := newTestSession(t)
			s.approvalMode = types.ApprovalAllowlist
			req := ToolCallRequest{Name: "bash", Arguments: `{"script":"go test ./..."}`, AgentID: "helper"}
			switch name {
			case "auto":
				s.SetAutoApprove(true)
			case "turn":
				s.currentApprovalTurn().allowAll = true
			case "tool":
				s.allowedTools[req.Name] = true
			case "prefix":
				s.allowedBashPrefixes = map[string]bool{"go": true}
			case "readonly":
				s.approvalMode = types.ApprovalPrompt
				req = ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`, AgentID: "helper"}
			case "classifier":
				s = classifySession(verdictFor("build_test", .95), nil)
			case "hook":
				s.hooks = hooks.New([]types.HookConfig{{Event: "PreToolUse", Command: `echo '{"approved":true}'`}})
			case "external":
				s = newTestSessionWithExternalSource(t)
			}
			asked := false
			if name != "no callback" {
				s.callbacks.OnToolCall = func(ToolCallRequest) ToolCallResponse { asked = true; return ToolCallResponse{Approved: true} }
			}
			// Prove the configured path really permits without policy first.
			if d := s.decideToolCall(req); !d.Approved {
				t.Fatal("control did not approve")
			}
			switch name {
			case "auto", "turn", "tool", "prefix", "readonly", "classifier", "hook":
				if asked {
					t.Fatal("control missed approval fast path")
				}
			}
			asked = false
			seen := false
			s.callbacks.ToolPolicy = func(got ToolCallRequest) error {
				seen = true
				if got.Name != req.Name || got.Arguments != req.Arguments || got.AgentID != "helper" {
					t.Error("policy lost call identity")
				}
				return errors.New("sensitive internal error")
			}
			d := s.decideToolCall(req)
			if d.Approved || !seen || asked {
				t.Fatalf("policy bypass: approved=%v seen=%v asked=%v", d.Approved, seen, asked)
			}
			if d.Adjustment != "tool call denied by embedder policy" {
				t.Fatalf("unsafe denial message: %q", d.Adjustment)
			}
		})
	}
}

func TestToolPolicyCannotGrant(t *testing.T) {
	for _, grant := range []ToolCallResponse{{Approved: true, AllowAllTurn: true}, {Approved: true, AlwaysAllow: true}} {
		asked := 0
		s := newDecideSession("allowlist", func(ToolCallRequest) ToolCallResponse { asked++; return grant })
		s.callbacks.ToolPolicy = func(r ToolCallRequest) error {
			if r.Name == "delegate_background" && r.AgentID != "" {
				return errors.New("unsupported helper context")
			}
			return nil
		}
		if s.ToolCallDenied(ToolCallRequest{Name: "delegate_background"}) || asked != 1 {
			t.Fatal("root did not reach ordinary approval")
		}
		if !s.ToolCallDenied(ToolCallRequest{Name: "delegate_background", AgentID: "helper"}) || asked != 1 {
			t.Fatal("grant bypassed helper veto")
		}
	}
	s := newDecideSession("allowlist", func(ToolCallRequest) ToolCallResponse { return ToolCallResponse{} })
	s.callbacks.ToolPolicy = func(ToolCallRequest) error { return nil }
	if !s.ToolCallDenied(ToolCallRequest{Name: "write"}) {
		t.Fatal("policy granted permission")
	}
}

func TestToolPolicyConcurrentReentry(t *testing.T) {
	s := newTestSession(t)
	s.callbacks.ToolPolicy = func(r ToolCallRequest) error {
		if r.Name == "outer" && !s.ToolCallDenied(ToolCallRequest{Name: "inner"}) {
			t.Error("inner not denied")
		}
		s.SetAutoApprove(true)
		return errors.New("denied")
	}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !s.ToolCallDenied(ToolCallRequest{Name: "outer"}) {
					t.Error("outer not denied")
				}
			}()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("policy deadlocked")
	}
}
