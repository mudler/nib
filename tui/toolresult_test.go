package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/mudler/nib/chat"
)

// TestToolResultMessageRenders verifies that a root-agent tool result is shown
// inline as a dim block carrying the tool name and its output, and that a
// sub-agent result is skipped (stays in the Ctrl+J log instead).
func TestToolResultMessageRenders(t *testing.T) {
	t.Run("renders tool role inline", func(t *testing.T) {
		m := Model{viewport: viewport.New(80, 10)}
		m.messages = append(m.messages, ChatMessage{Role: "tool", Name: "bash", Content: "hello\nworld"})
		m.updateViewport()

		out := m.viewport.View()
		if !strings.Contains(out, "bash") {
			t.Errorf("rendered viewport should contain the tool name %q, got:\n%s", "bash", out)
		}
		if !strings.Contains(out, "hello") {
			t.Errorf("rendered viewport should contain the tool output %q, got:\n%s", "hello", out)
		}
	})

	t.Run("sub-agent result is skipped", func(t *testing.T) {
		m := Model{
			ctx:            context.Background(),
			viewport:       viewport.New(80, 10),
			toolResultChan: make(chan chat.ToolResult, 1),
		}
		before := len(m.messages)
		next, _ := m.Update(toolResultMsg{Name: "x", Result: "y", AgentID: "agent1"})
		nm := next.(Model)
		if len(nm.messages) != before {
			t.Fatalf("sub-agent result must not append a message; messages %d -> %d", before, len(nm.messages))
		}
	})

	t.Run("root result is appended via Update", func(t *testing.T) {
		m := Model{
			ctx:            context.Background(),
			viewport:       viewport.New(80, 10),
			toolResultChan: make(chan chat.ToolResult, 1),
		}
		next, _ := m.Update(toolResultMsg{Name: "bash", Result: "hi"})
		nm := next.(Model)
		if len(nm.messages) != 1 {
			t.Fatalf("root result should append one message, got %d", len(nm.messages))
		}
		if nm.messages[0].Role != "tool" || nm.messages[0].Name != "bash" {
			t.Fatalf("appended message wrong: %+v", nm.messages[0])
		}
	})
}
