package tui

import (
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/tui/render"
	"strings"
	"testing"
)

func TestToolLifecycleTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{"failed", "denied", "skipped", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			m := runningTestModel()
			m.queueTool(chat.ToolStart{ID: "call", Name: "write", Arguments: "{}"})
			m.applyToolResult(chat.ToolResult{ID: "call", Name: "write", Arguments: "{}", Result: "Nothing to do", Outcome: outcome})
			if len(m.running) != 0 || len(m.messages) != 1 {
				t.Fatalf("unresolved terminal: %+v", m.running)
			}
			msg := m.messages[0]
			if msg.Status != render.ToolStatusFailed || !strings.Contains(msg.Meta, outcome) || msg.Content == "" {
				t.Fatalf("false success or lost outcome: %+v", msg)
			}
			if strings.Contains(msg.Meta, "s") && msg.Meta != outcome {
				t.Fatalf("queued call has duration: %q", msg.Meta)
			}
		})
	}
}

func TestToolLifecycleQueuedRunOwnership(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	oldQueue := m.queuedToolCallback()
	oldStart, oldResult := m.toolCallbacks()
	m.toolEvents.begin()
	oldQueue(chat.ToolStart{ID: "old", Name: "read"})
	oldStart(chat.ToolStart{ID: "old", Name: "read"})
	oldResult(chat.ToolResult{ID: "old", Name: "read", Outcome: "cancelled"})
	m = update(m, toolEventsReadyMsg{})
	if len(m.running) != 0 || len(m.messages) != 0 {
		t.Fatal("stale run leaked")
	}
	queue := m.queuedToolCallback()
	queue(chat.ToolStart{ID: "new", Name: "read"})
	m = update(m, toolEventsReadyMsg{})
	if len(m.running) != 1 || !m.running[0].started.IsZero() || !m.runningBlock(m.running[0]).Queued {
		t.Fatal("queued call started timer")
	}
	m.applyToolResult(chat.ToolResult{ID: "new", Name: "read", AgentID: "child", Outcome: "failed"})
	if len(m.running) != 1 {
		t.Fatal("child result consumed root call")
	}
}

func TestToolLifecycleOutcomeAuthorityAndLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, result string
		status                render.ToolStatus
	}{
		{"completed failure", "completed", `{"success":false,"error":"failed"}`, render.ToolStatusFailed},
		{"completed success", "completed", `{"success":true}`, render.ToolStatusOK},
		{"failed overrides output", "failed", `{"success":true}`, render.ToolStatusFailed},
		{"legacy failure", "", `{"success":false,"error":"failed"}`, render.ToolStatusFailed},
		{"legacy success", "", `{"success":true}`, render.ToolStatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := toolMessage(chat.ToolResult{Name: "write", Outcome: tc.outcome, Result: tc.result}, 0)
			if msg.Status != tc.status {
				t.Fatalf("status=%v, want %v", msg.Status, tc.status)
			}
		})
	}
}

func TestToolLifecycleCompletedSemanticFailure(t *testing.T) {
	for _, tc := range []struct {
		name, tool, result, content, meta string
	}{
		{"bash exit", "bash", `{"stdout":"","stderr":"boom","exit_code":1,"success":false}`, "boom", "exit 1"},
		{"bash timeout", "bash", `{"stdout":"","stderr":"command timed out","success":false}`, "command timed out", ""},
		{"write permission", "write", `{"success":false,"error":"permission denied"}`, "permission denied", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := runningTestModel()
			m.queueTool(chat.ToolStart{ID: "call", Name: tc.tool})
			m.applyToolResult(chat.ToolResult{ID: "call", Name: tc.tool, Outcome: "completed", Result: tc.result})
			if len(m.running) != 0 || len(m.messages) != 1 {
				t.Fatalf("unresolved completion: running=%+v messages=%+v", m.running, m.messages)
			}
			msg := m.messages[0]
			if msg.Status != render.ToolStatusFailed || msg.Content != tc.content || msg.Meta != tc.meta {
				t.Fatalf("got status %v content %q meta %q, want failed content %q meta %q", msg.Status, msg.Content, msg.Meta, tc.content, tc.meta)
			}
		})
	}
}
