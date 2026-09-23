package chat

import "testing"

// TestEmitToolStart pins when the UI hears that a tool began: an approved
// root-agent call, before it runs. A sub-agent's call already has its own
// thread line (emitSubAgentToolLine), and a denied call never runs.
func TestEmitToolStart(t *testing.T) {
	var got []ToolStart
	s := &Session{callbacks: Callbacks{OnToolStart: func(ts ToolStart) { got = append(got, ts) }}}

	t.Run("approved root tool emits name and args", func(t *testing.T) {
		got = nil
		s.emitToolStart(true, "", "bash", `{"script":"go test ./..."}`)
		if len(got) != 1 {
			t.Fatalf("expected 1 emit, got %d", len(got))
		}
		if got[0].Name != "bash" || got[0].Arguments != `{"script":"go test ./..."}` {
			t.Fatalf("wrong ToolStart: %+v", got[0])
		}
	})

	t.Run("sub-agent tool does not emit", func(t *testing.T) {
		got = nil
		s.emitToolStart(true, "agent-1", "read", "{}")
		if len(got) != 0 {
			t.Fatalf("sub-agent tool must not emit; got %d", len(got))
		}
	})

	t.Run("denied tool does not emit", func(t *testing.T) {
		got = nil
		s.emitToolStart(false, "", "bash", "{}")
		if len(got) != 0 {
			t.Fatalf("denied tool must not emit; got %d", len(got))
		}
	})

	t.Run("nil callback is a safe no-op", func(t *testing.T) {
		(&Session{}).emitToolStart(true, "", "bash", "{}")
	})
}
