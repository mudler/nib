package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
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
