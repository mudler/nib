package chat

import "testing"

func TestToolCallRequestCarriesAgentID(t *testing.T) {
	r := ToolCallRequest{Name: "echo", AgentID: "a1"}
	if r.AgentID != "a1" {
		t.Fatalf("AgentID not set, got %q", r.AgentID)
	}
}

func TestAgentEventFields(t *testing.T) {
	e := AgentEvent{ID: "a1", Type: "explore", Task: "look", Status: AgentStatusRunning}
	if e.ID != "a1" || e.Status != AgentStatusRunning {
		t.Fatalf("AgentEvent fields wrong: %+v", e)
	}
}
