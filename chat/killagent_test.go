package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestKillAgent(t *testing.T) {
	s := &Session{agentManager: cogito.NewAgentManager()}

	killed := false
	s.agentManager.Register(&cogito.AgentState{ID: "x", Cancel: func() { killed = true }})

	if !s.KillAgent("x") {
		t.Fatal("KillAgent should return true for a known agent")
	}
	if !killed {
		t.Fatal("KillAgent should call the agent's Cancel")
	}
	if s.KillAgent("nope") {
		t.Fatal("KillAgent should return false for an unknown agent")
	}
}
