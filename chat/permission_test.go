package chat

import (
	"context"
	"testing"

	"github.com/mudler/nib/provenance"
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
		allowedTools: map[string]bool{},
		callbacks: Callbacks{
			OnToolCall: func(ToolCallRequest) ToolCallResponse {
				calls++
				return ToolCallResponse{Approved: false}
			},
		},
	}
	s.autoApprove.Store(true)

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
	s.newApprovalTurn()
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

	// A bash command with a different first word still prompts — and the
	// spy approves it, so the decision itself must come back approved.
	dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"rm -rf /"}`})
	if calls != 2 {
		t.Fatalf("expected spy consulted for non-go command, got %d calls", calls)
	}
	if !dec.Approved {
		t.Fatal("expected spy-approved non-go command to be approved")
	}
}

func TestDecideToolCallAlwaysPrefixValidation(t *testing.T) {
	t.Run("non-bash tool cannot mint a prefix grant", func(t *testing.T) {
		calls := 0
		s := &Session{
			ctx:          context.Background(),
			allowedTools: map[string]bool{},
			callbacks: Callbacks{
				OnToolCall: func(ToolCallRequest) ToolCallResponse {
					calls++
					return ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: "git"}
				},
			},
		}

		if dec := s.decideToolCall(ToolCallRequest{Name: "bash_background", Arguments: `{"script":"git status"}`}); !dec.Approved {
			t.Fatal("expected first bash_background call approved")
		}
		if calls != 1 {
			t.Fatalf("expected spy invoked once, got %d", calls)
		}
		if len(s.allowedBashPrefixes) != 0 {
			t.Fatalf("non-bash approval must not mint a bash prefix grant, got %v", s.allowedBashPrefixes)
		}
		if s.allowedTools["bash_background"] {
			t.Fatal("AlwaysPrefix response must not fall back to a whole-tool grant")
		}

		// No grant of any kind was minted: an identical bash_background
		// request still prompts (the whole tool was not allowlisted) …
		if dec := s.decideToolCall(ToolCallRequest{Name: "bash_background", Arguments: `{"script":"git status"}`}); !dec.Approved {
			t.Fatal("expected second bash_background call approved by the spy")
		}
		if calls != 2 {
			t.Fatalf("expected spy consulted again for bash_background, got %d calls", calls)
		}

		// … and no bash "git" prefix grant exists either: a plain bash
		// `git status` still prompts. (This call legitimately mints a git
		// grant of its own — bash request, matching prefix — so the grant
		// maps are not re-checked afterwards.)
		if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`}); !dec.Approved {
			t.Fatal("expected bash git command approved by the spy")
		}
		if calls != 3 {
			t.Fatalf("expected spy consulted for bash git command, got %d calls", calls)
		}
	})

	t.Run("prefix not derivable from the approved request mints nothing", func(t *testing.T) {
		calls := 0
		s := &Session{
			ctx:          context.Background(),
			allowedTools: map[string]bool{},
			callbacks: Callbacks{
				OnToolCall: func(ToolCallRequest) ToolCallResponse {
					calls++
					// Claims an "rm" grant while the approved script is `git status`.
					return ToolCallResponse{Approved: true, AlwaysAllow: true, AlwaysPrefix: "rm"}
				},
			},
		}

		if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`}); !dec.Approved {
			t.Fatal("expected first git command approved")
		}
		if calls != 1 {
			t.Fatalf("expected spy invoked once, got %d", calls)
		}
		// The mismatch must mint nothing: no "rm" grant, no "git" grant in
		// its place, and no whole-tool fallback.
		if len(s.allowedBashPrefixes) != 0 {
			t.Fatalf("expected no prefix grants minted, got %v", s.allowedBashPrefixes)
		}
		if s.allowedTools["bash"] {
			t.Fatal("mismatched AlwaysPrefix must not fall back to a whole-tool grant")
		}

		// The mismatched "rm" grant must not exist: rm still prompts.
		if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"rm -rf /"}`}); !dec.Approved {
			t.Fatal("expected rm command approved by the spy")
		}
		if calls != 2 {
			t.Fatalf("expected spy consulted for rm command, got %d calls", calls)
		}

		// And no "git" grant was minted in its place either: git still prompts.
		if dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`}); !dec.Approved {
			t.Fatal("expected git command approved by the spy")
		}
		if calls != 3 {
			t.Fatalf("expected spy consulted again for git command, got %d calls", calls)
		}
	})
}

// newTestSession builds a minimal Session for decideToolCall tests, mirroring
// newDecideSession in readonly_decide_test.go.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	return &Session{
		ctx:              context.Background(),
		allowedTools:     map[string]bool{},
		readOnlyCommands: newReadOnlyCommands(nil),
	}
}

// newTestSessionWithExternalSource builds a Session that already has one
// externally-influenced source recorded directly (bypassing the provenance
// classifier, which is irrelevant to the approval-gate ordering under test),
// so decideToolCall's externally-influenced gate is live.
func newTestSessionWithExternalSource(t *testing.T) *Session {
	t.Helper()
	s := newTestSession(t)
	s.recordExternalEnvelope(provenance.NewExternal(s.ctx, "", "tool-result", "web_fetch", "external page content", nil))
	return s
}

// TestSetAutoApproveBypassesExternalInfluencePrompt pins yolo's user-facing
// contract: every tool call is approved without opening the approval UI, even
// after external content enters the conversation.
func TestSetAutoApproveBypassesExternalInfluencePrompt(t *testing.T) {
	s := newTestSessionWithExternalSource(t)
	s.SetAutoApprove(true)

	asked := false
	s.callbacks.OnToolCall = func(ToolCallRequest) ToolCallResponse {
		asked = true
		return ToolCallResponse{Approved: false}
	}

	decision := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"script":"rm -rf /tmp/x"}`})
	if !decision.Approved {
		t.Fatal("yolo did not approve an externally influenced tool call")
	}
	if asked {
		t.Fatal("yolo opened the approval UI for an externally influenced tool call")
	}
}

// TestYoloOffKeepsNarrowerGrants: turning the broad switch off must not silently
// revoke the specific grants the user minted with "always allow".
func TestYoloOffKeepsNarrowerGrants(t *testing.T) {
	s := newTestSession(t)
	s.allowedTools = map[string]bool{"read": true}
	s.SetAutoApprove(true)
	s.SetAutoApprove(false)

	if !s.allowedTools["read"] {
		t.Error("turning yolo off revoked an unrelated always-allow grant")
	}
}
