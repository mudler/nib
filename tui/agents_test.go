package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
)

func TestAgentTranscriptLine(t *testing.T) {
	if got := agentTranscriptLine(chat.AgentEvent{Type: "explore", Task: "scan repo", Status: chat.AgentStatusRunning}); !strings.Contains(got, "explore") || !strings.Contains(got, "started") || !strings.Contains(got, "scan repo") {
		t.Fatalf("running line wrong: %q", got)
	}
	if got := agentTranscriptLine(chat.AgentEvent{Type: "explore", Status: chat.AgentStatusCompleted}); !strings.Contains(got, "finished") {
		t.Fatalf("completed line wrong: %q", got)
	}
	if got := agentTranscriptLine(chat.AgentEvent{Status: chat.AgentStatusFailed, Err: errors.New("boom")}); !strings.Contains(got, "failed") || !strings.Contains(got, "boom") {
		t.Fatalf("failed line wrong: %q", got)
	}
	// Empty Type falls back to "agent".
	if got := agentTranscriptLine(chat.AgentEvent{Status: chat.AgentStatusRunning}); !strings.Contains(got, "agent") {
		t.Fatalf("empty-type fallback wrong: %q", got)
	}
	// Unknown status produces no line.
	if got := agentTranscriptLine(chat.AgentEvent{Status: chat.AgentStatus("weird")}); got != "" {
		t.Fatalf("unknown status should be empty, got %q", got)
	}
}

func TestRenderJobsFooterEmpty(t *testing.T) {
	if got := renderJobsFooter(nil, 80); got != "" {
		t.Fatalf("empty jobs should render nothing, got %q", got)
	}
}

func TestRenderJobsFooterCounts(t *testing.T) {
	jobs := []agentJob{
		{ID: "a1", Type: "explore", Task: "scan repo", Status: chat.AgentStatusRunning},
		{ID: "b2", Type: "plan", Task: "draft", Status: chat.AgentStatusCompleted},
	}
	out := renderJobsFooter(jobs, 80)
	if !strings.Contains(out, "1 running") {
		t.Fatalf("footer should report running count, got %q", out)
	}
}

func TestToolLabelWithAgent(t *testing.T) {
	got := toolApprovalLabel(chat.ToolCallRequest{Name: "echo", AgentID: "a1"})
	if !strings.Contains(got, "a1") || !strings.Contains(got, "echo") {
		t.Fatalf("expected agent-labeled approval, got %q", got)
	}
	root := toolApprovalLabel(chat.ToolCallRequest{Name: "echo"})
	if strings.Contains(root, "→") {
		t.Fatalf("root tool should not show an agent arrow, got %q", root)
	}
}
