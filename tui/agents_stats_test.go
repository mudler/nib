package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestAgentTranscriptLineCompletedHasStats(t *testing.T) {
	line := agentTranscriptLine(chat.AgentEvent{
		Type:        "explore",
		Status:      chat.AgentStatusCompleted,
		ToolCount:   3,
		TotalTokens: 12400,
		Elapsed:     63 * time.Second,
	})
	for _, want := range []string{"finished", "3 tools", "12.4k tokens", "1m 03s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}
