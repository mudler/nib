package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

// TestEmitAgentEventMapsRunningSpawn proves the spawn-callback path: a running
// *cogito.AgentState is mapped to a chat.AgentEvent (preserving ID, Type, Task
// and a running Status) and forwarded to Callbacks.OnAgentEvent.
func TestEmitAgentEventMapsRunningSpawn(t *testing.T) {
	var got AgentEvent
	called := false
	s := &Session{
		callbacks: Callbacks{
			OnAgentEvent: func(ev AgentEvent) {
				got = ev
				called = true
			},
		},
	}

	s.emitAgentEvent(&cogito.AgentState{
		ID:     "agent-123",
		Type:   "explore",
		Task:   "investigate the codebase",
		Status: cogito.AgentStatusRunning,
	})

	if !called {
		t.Fatal("OnAgentEvent was not called")
	}
	if got.ID != "agent-123" {
		t.Errorf("ID = %q, want %q", got.ID, "agent-123")
	}
	if got.Type != "explore" {
		t.Errorf("Type = %q, want %q", got.Type, "explore")
	}
	if got.Task != "investigate the codebase" {
		t.Errorf("Task = %q, want %q", got.Task, "investigate the codebase")
	}
	if got.Status != AgentStatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, AgentStatusRunning)
	}
}

// TestEmitAgentEventNilCallbackNoPanic ensures the helper is safe when no
// OnAgentEvent callback is registered.
func TestEmitAgentEventNilCallbackNoPanic(t *testing.T) {
	s := &Session{}
	s.emitAgentEvent(&cogito.AgentState{ID: "x", Status: cogito.AgentStatusRunning})
}
