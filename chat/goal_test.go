package chat

import (
	"strings"
	"testing"
)

func TestGoalDoneToolMarksDone(t *testing.T) {
	var got string
	def := goalDoneToolDefinition(func(justification string) string {
		got = justification
		return "ok"
	})
	if def.Tool().Function.Name != "goal_done" {
		t.Fatalf("tool name = %q, want goal_done", def.Tool().Function.Name)
	}

	tool := &goalDoneTool{onDone: func(j string) string { got = j; return "confirmed" }}
	out, _, err := tool.Run(map[string]any{"justification": "all tests pass"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "all tests pass" {
		t.Fatalf("justification not forwarded, got %q", got)
	}
	if out != "confirmed" {
		t.Fatalf("Run output = %q, want confirmed", out)
	}
}

func TestGoalReminderMentionsGoal(t *testing.T) {
	r := goalReminder("ship the thing")
	if !strings.Contains(r, "ship the thing") {
		t.Fatalf("reminder missing goal text: %q", r)
	}
	if !strings.Contains(r, "goal_done") {
		t.Fatalf("reminder should tell the model to call goal_done: %q", r)
	}
}
