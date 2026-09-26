package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
)

// A generated title replaces the task's first sentence on the agent's chip;
// before it arrives, the first sentence stands in.
func TestAgentTitleOnChipAndLog(t *testing.T) {
	m := newQueueTestModel()
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Task: "Look through backend/go/sd and find every option. More.", Status: chat.AgentStatusRunning}}
	if got := m.agentChipText(m.jobs[0]); !strings.HasPrefix(got, "explore: Look through backend/go/sd") {
		t.Fatalf("chip before the title = %q", got)
	}

	next, _ := m.Update(agentTitleMsg{id: "a1", title: "Map the LoRA options"})
	m = next.(Model)
	if got := m.agentChipText(m.jobs[0]); !strings.HasPrefix(got, "explore: Map the LoRA options") {
		t.Fatalf("chip after the title = %q", got)
	}

	// A title for an agent the UI has not seen yet is kept for when it shows.
	next, _ = m.Update(agentTitleMsg{id: "b2", title: "Draft the plan"})
	m = next.(Model)
	m.applyAgentEvent(chat.AgentEvent{ID: "b2", Type: "plan", Task: "draft", Status: chat.AgentStatusRunning})
	if j, _ := m.jobByID("b2"); j.Title != "Draft the plan" {
		t.Fatalf("early title lost: %+v", j)
	}
}
