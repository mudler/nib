package tui

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/chat"
)

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
