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

func TestDecideToolCallBashPrefixGrant(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:                 context.Background(),
		allowedTools:        map[string]bool{},
		allowedBashPrefixes: map[string]bool{"git": true},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: false}
			},
		},
	}

	// A simple command whose first word matches the grant is auto-approved.
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`}); !dec.Approved {
		t.Fatal("expected granted prefix to approve simple git command")
	}
	if calls != 0 {
		t.Fatalf("expected OnToolCall not to be invoked, got %d calls", calls)
	}

	// A compound command can never ride a prefix grant.
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status && rm -rf /"}`}); dec.Approved {
		t.Fatal("expected compound command to be denied by the spy")
	}
	if calls != 1 {
		t.Fatalf("expected spy consulted for compound command, got %d calls", calls)
	}

	// A different first word still prompts.
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"gitx whatever"}`}); dec.Approved {
		t.Fatal("expected non-matching prefix to be denied by the spy")
	}
	if calls != 2 {
		t.Fatalf("expected spy consulted for non-matching prefix, got %d calls", calls)
	}

	// Prefix grants apply only to the bash tool.
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash_background", Arguments: `{"script":"git status"}`}); dec.Approved {
		t.Fatal("expected bash_background to be denied by the spy (no prefix grants)")
	}
	if calls != 3 {
		t.Fatalf("expected spy consulted for bash_background, got %d calls", calls)
	}
}

func TestDecideToolCallAlwaysPrefixResponse(t *testing.T) {
	calls := 0
	s := &Session{
		ctx:          context.Background(),
		allowedTools: map[string]bool{},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: "go"}
			},
		},
	}

	// First call prompts; the response grants the "go" prefix (note: the
	// allowedBashPrefixes map starts nil here — the grant must lazily init it).
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"go build ./..."}`}); !dec.Approved {
		t.Fatal("expected first go command approved")
	}
	if calls != 1 {
		t.Fatalf("expected spy invoked once, got %d", calls)
	}

	// A later simple go command is approved without prompting.
	if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"go test ./..."}`}); !dec.Approved {
		t.Fatal("expected second go command approved via prefix grant")
	}
	if calls != 1 {
		t.Fatalf("expected spy not invoked again, got %d", calls)
	}

	// The whole bash tool must NOT have been allowlisted.
	if s.allowedTools["bash"] {
		t.Fatal("AlwaysPrefix grant must not allowlist the whole bash tool")
	}

	// A bash command with a different first word still prompts.
	if s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"rm -rf /"}`}); calls != 2 {
		t.Fatalf("expected spy consulted for non-go command, got %d calls", calls)
	}
}
