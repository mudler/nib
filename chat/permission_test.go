package chat

import (
	"context"
	"testing"
)

func TestDecideToolCallAllowlist(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:          context.Background(),
		allowedTools: map[string]bool{"shell": true},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: false}
			},
		},
	}

	dec := s.decideToolCall(ToolCallRequest{Name: "shell", Arguments: "{}"})
	if !dec.Approved {
		t.Fatal("expected allowlisted tool to be approved")
	}
	if calls != 0 {
		t.Fatalf("expected OnToolCall not to be invoked, got %d calls", calls)
	}
}

func TestDecideToolCallAuto(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:          context.Background(),
		autoApprove:  true,
		allowedTools: map[string]bool{},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: false}
			},
		},
	}

	dec := s.decideToolCall(ToolCallRequest{Name: "anything", Arguments: "{}"})
	if !dec.Approved {
		t.Fatal("expected auto-approve to approve any tool")
	}
	if calls != 0 {
		t.Fatalf("expected OnToolCall not to be invoked, got %d calls", calls)
	}
}

func TestDecideToolCallAllowAllTurn(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:          context.Background(),
		allowedTools: map[string]bool{},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: true, AllowAllTurn: true}
			},
		},
	}

	if dec := s.decideToolCall(ToolCallRequest{Name: "first", Arguments: "{}"}); !dec.Approved {
		t.Fatal("expected first call approved")
	}
	if calls != 1 {
		t.Fatalf("expected spy invoked once, got %d", calls)
	}

	// A different tool should now be approved without invoking the spy.
	if dec := s.decideToolCall(ToolCallRequest{Name: "second", Arguments: "{}"}); !dec.Approved {
		t.Fatal("expected second (different) call approved via allow-all-turn")
	}
	if calls != 1 {
		t.Fatalf("expected spy still invoked once after allow-all-turn, got %d", calls)
	}

	// Simulate a new turn: flag reset, spy should be consulted again.
	s.allowAllTurn = false
	if dec := s.decideToolCall(ToolCallRequest{Name: "third", Arguments: "{}"}); !dec.Approved {
		t.Fatal("expected third call approved")
	}
	if calls != 2 {
		t.Fatalf("expected spy invoked again after turn reset, got %d", calls)
	}
}

func TestDecideToolCallAlwaysAllow(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:          context.Background(),
		allowedTools: map[string]bool{},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: true, AlwaysAllow: true}
			},
		},
	}

	if dec := s.decideToolCall(ToolCallRequest{Name: "read_file", Arguments: "{}"}); !dec.Approved {
		t.Fatal("expected first read_file approved")
	}
	if calls != 1 {
		t.Fatalf("expected spy invoked once, got %d", calls)
	}

	if dec := s.decideToolCall(ToolCallRequest{Name: "read_file", Arguments: "{}"}); !dec.Approved {
		t.Fatal("expected second read_file approved via always-allow")
	}
	if calls != 1 {
		t.Fatalf("expected spy not invoked again for always-allowed tool, got %d", calls)
	}
}
