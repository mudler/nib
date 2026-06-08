package chat

import "testing"

// TestEmitSubAgentToolLine pins the call-callback emission that surfaces a
// sub-agent's tool as a compact inline thread line. The result callback never
// fires for sub-agent tools (cogito propagates only the call callback into
// sub-agents), so this is the sole source of the thread's tool lines.
func TestEmitSubAgentToolLine(t *testing.T) {
	var got []ToolResult
	s := &Session{callbacks: Callbacks{OnToolResult: func(r ToolResult) { got = append(got, r) }}}

	t.Run("approved sub-agent tool emits with agent id, name, args", func(t *testing.T) {
		got = nil
		s.emitSubAgentToolLine(true, "agent-1", "read", `{"path":"go.mod"}`)
		if len(got) != 1 {
			t.Fatalf("expected 1 emit, got %d", len(got))
		}
		if got[0].AgentID != "agent-1" || got[0].Name != "read" || got[0].Arguments != `{"path":"go.mod"}` {
			t.Fatalf("wrong ToolResult: %+v", got[0])
		}
	})

	t.Run("root tool does not emit from the call callback", func(t *testing.T) {
		got = nil
		s.emitSubAgentToolLine(true, "", "bash", "{}")
		if len(got) != 0 {
			t.Fatalf("root tool must not emit here (streams via result callback); got %d", len(got))
		}
	})

	t.Run("denied sub-agent tool does not emit", func(t *testing.T) {
		got = nil
		s.emitSubAgentToolLine(false, "agent-1", "read", "{}")
		if len(got) != 0 {
			t.Fatalf("denied tool must not emit; got %d", len(got))
		}
	})

	t.Run("nil callback is a safe no-op", func(t *testing.T) {
		(&Session{}).emitSubAgentToolLine(true, "agent-1", "read", "{}")
	})
}
