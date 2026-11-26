package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/hooks"
	"github.com/mudler/nib/types"
)

func TestDecideToolCallPreHook(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(block, []byte("#!/bin/sh\necho '{\"block\": true, \"reason\": \"denied\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	approved := false
	s, err := NewSession(context.Background(), types.Config{
		Hooks: []types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: block, Dir: dir}},
	}, Callbacks{
		OnToolCall: func(ToolCallRequest) ToolCallResponse {
			approved = true
			return ToolCallResponse{Approved: true}
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: "{}"})
	if dec.Approved {
		t.Fatalf("expected hook to deny bash, got approved")
	}
	if dec.Adjustment != "denied" {
		t.Fatalf("expected deny reason surfaced as adjustment, got %q", dec.Adjustment)
	}
	if approved {
		t.Fatal("user gate should not have been reached when a hook decides")
	}

	dec = s.decideToolCall(ToolCallRequest{Name: "other", Arguments: "{}"})
	if !dec.Approved {
		t.Fatal("expected non-matching tool to fall through to the user gate (approve)")
	}
}

func TestYoloWithExternalProvenanceStillHonorsDenyingHook(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(block, []byte("#!/bin/sh\necho '{\"block\": true, \"reason\": \"policy denied\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := newTestSessionWithExternalSource(t)
	s.SetAutoApprove(true)
	s.hooks = hooks.New([]types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: block, Dir: dir}})
	asked := false
	s.callbacks.OnToolCall = func(ToolCallRequest) ToolCallResponse {
		asked = true
		return ToolCallResponse{Approved: true}
	}

	dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"touch /tmp/x"}`})
	if dec.Approved {
		t.Fatal("yolo bypassed a denying PreToolUse hook")
	}
	if dec.Adjustment != "policy denied" {
		t.Fatalf("hook reason = %q, want %q", dec.Adjustment, "policy denied")
	}
	if asked {
		t.Fatal("denying hook fell through to the approval UI")
	}
}

func TestSessionStartHookContextReachesSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	start := filepath.Join(dir, "start.sh")
	if err := os.WriteFile(start, []byte("#!/bin/sh\necho 'Use the plugin skills.'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), types.Config{
		Prompt: "base prompt",
		Hooks: []types.HookConfig{{
			Event: "SessionStart", Matcher: "startup|clear|compact", Command: start, Dir: dir, Format: types.HookFormatClaude,
		}},
	}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	if !strings.Contains(s.systemPrompt, "Use the plugin skills.") {
		t.Fatalf("system prompt lacks the hook context:\n%s", s.systemPrompt)
	}
	if err := s.Reload(types.Config{Prompt: "base prompt"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.systemPrompt, "Use the plugin skills.") {
		t.Fatal("Reload dropped the SessionStart hook context")
	}
}

func TestDecideToolCallHookAskOverridesAutoApprove(t *testing.T) {
	dir := t.TempDir()
	ask := filepath.Join(dir, "ask.sh")
	if err := os.WriteFile(ask, []byte("#!/bin/sh\necho '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\",\"permissionDecisionReason\":\"risky\"}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var asked ToolCallRequest
	s, err := NewSession(context.Background(), types.Config{
		Hooks: []types.HookConfig{{Event: "PreToolUse", Command: ask, Dir: dir, Format: types.HookFormatClaude}},
	}, Callbacks{OnToolCall: func(r ToolCallRequest) ToolCallResponse {
		asked = r
		return ToolCallResponse{Approved: false, Adjustment: "no"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetAutoApprove(true)
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: "{}"}); dec.Approved {
		t.Fatal("auto-approve bypassed a hook that asked")
	}
	if asked.Name != "bash" || !strings.Contains(asked.Verdict, "risky") {
		t.Fatalf("user was not asked with the reason: %+v", asked)
	}
}

func TestUserPromptSubmitHookBlocks(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(block, []byte("#!/bin/sh\necho 'secret in prompt' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), types.Config{
		Hooks: []types.HookConfig{{Event: "UserPromptSubmit", Command: block, Dir: dir, Format: types.HookFormatClaude}},
	}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SendMessage("my key is sk-123"); err == nil || !strings.Contains(err.Error(), "secret in prompt") {
		t.Fatalf("err = %v", err)
	}
}
