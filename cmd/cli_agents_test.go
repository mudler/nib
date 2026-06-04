package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestFormatAgentEventLine(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{
		ID: "abcd1234ef", Type: "explore", Task: "scan", Status: chat.AgentStatusCompleted, Result: "found 3 files",
	})
	if !strings.Contains(line, "abcd1234") || !strings.Contains(line, "completed") {
		t.Fatalf("unexpected line: %q", line)
	}
}

func TestFormatAgentEventLineFailure(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{ID: "x", Status: chat.AgentStatusFailed})
	if !strings.Contains(line, "failed") {
		t.Fatalf("expected failed marker, got %q", line)
	}
}

func TestFormatAgentEventLineHasStats(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{
		ID: "abcd1234ef", Type: "explore", Status: chat.AgentStatusCompleted,
		Result: "ok", ToolCount: 3, TotalTokens: 12400, Elapsed: 63 * time.Second,
	})
	for _, want := range []string{"completed", "3 tools", "12.4k tokens", "1m 03s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}
