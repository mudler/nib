package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
)

func TestUnifiedJobsAgents(t *testing.T) {
	m := Model{
		jobs: []agentJob{
			{ID: "agent-123456789", Type: "explore", Task: "look around", Status: chat.AgentStatusRunning},
		},
		// shellJobs left nil — List has a nil guard.
	}

	jobs := m.unifiedJobs()
	if len(jobs) != 1 || jobs[0].Kind != "agent" || jobs[0].ID != "agent-123456789" {
		t.Fatalf("unifiedJobs = %+v", jobs)
	}
}

func TestLastLinesAndClip(t *testing.T) {
	got := lastLines("a\nb\nc\nd\n", 2)
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("lastLines = %v", got)
	}
	if lastLines("", 3) != nil {
		t.Fatal("empty input should yield nil")
	}
	if clipLine("hello world", 8) != "hello w…" {
		t.Fatalf("clipLine = %q", clipLine("hello world", 8))
	}
}

func TestKillSelectedOutOfRangeIsNoop(t *testing.T) {
	m := Model{jobs: []agentJob{{ID: "a1", Status: chat.AgentStatusRunning}}}
	// These must not panic and must not set a "Killed" status.
	m.killSelected(0)
	m.killSelected(99)
	if strings.HasPrefix(m.status, "Killed") {
		t.Fatalf("out-of-range selection should be a no-op, status = %q", m.status)
	}
}
