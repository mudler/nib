package chat

import (
	"testing"
)

// newDecideSession builds a minimal Session exercising only decideToolCall's
// approval logic — no MCP/agent wiring needed.
func newDecideSession(mode string, onCall func(ToolCallRequest) ToolCallResponse) *Session {
	return &Session{
		allowedTools:     map[string]bool{},
		approvalMode:     mode,
		autoApprove:      mode == "auto",
		readOnlyCommands: newReadOnlyCommands(nil),
		callbacks:        Callbacks{OnToolCall: onCall},
	}
}

func TestDecideReadOnlyAutoApprovesInPromptMode(t *testing.T) {
	called := false
	s := newDecideSession("", func(ToolCallRequest) ToolCallResponse {
		called = true
		return ToolCallResponse{Approved: false}
	})
	d := s.decideToolCall(ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`})
	if !d.Approved {
		t.Error("read should auto-approve in prompt mode")
	}
	if called {
		t.Error("OnToolCall must not be invoked for a read-only call")
	}
}

func TestDecideMutatingStillPrompts(t *testing.T) {
	called := false
	s := newDecideSession("", func(ToolCallRequest) ToolCallResponse {
		called = true
		return ToolCallResponse{Approved: true}
	})
	s.decideToolCall(ToolCallRequest{Name: "write", Arguments: `{"path":"x","content":"y"}`})
	if !called {
		t.Error("write must still invoke OnToolCall")
	}
}

func TestDecideStrictModePromptsForReadOnly(t *testing.T) {
	called := false
	s := newDecideSession("strict", func(ToolCallRequest) ToolCallResponse {
		called = true
		return ToolCallResponse{Approved: true}
	})
	s.decideToolCall(ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`})
	if !called {
		t.Error("strict mode must prompt even for read-only calls")
	}
}

func TestDecideAllowlistModeDoesNotGetReadOnlyFreebie(t *testing.T) {
	called := false
	s := newDecideSession("allowlist", func(ToolCallRequest) ToolCallResponse {
		called = true
		return ToolCallResponse{Approved: false}
	})
	s.decideToolCall(ToolCallRequest{Name: "read", Arguments: `{"path":"x"}`})
	if !called {
		t.Error("allowlist mode must prompt for read tool not in allowed_tools")
	}
}
