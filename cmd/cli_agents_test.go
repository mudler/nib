package cmd

import (
	"strings"
	"testing"

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
