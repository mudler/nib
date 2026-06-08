package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// TestToolResultMessageRenders verifies that a tool result is shown inline as a
// dim block carrying the tool name and its output, and that a sub-agent result
// is shown inline too, labeled with its agent id.
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

	t.Run("sub-agent tool result appends a compact agent_tool line", func(t *testing.T) {
		m := Model{
			ctx:            context.Background(),
			viewport:       viewport.New(80, 10),
			toolResultChan: make(chan chat.ToolResult, 1),
		}
		before := len(m.messages)
		next, _ := m.Update(toolResultMsg{Name: "bash", Arguments: `{"command":"go build ./..."}`, Result: "y", AgentID: "agent1234"})
		nm := next.(Model)
		if len(nm.messages) != before+1 {
			t.Fatalf("sub-agent tool result should append one agent_tool line; messages %d -> %d", before, len(nm.messages))
		}
		got := nm.messages[len(nm.messages)-1]
		if got.Role != "agent_tool" || got.AgentID != "agent1234" {
			t.Fatalf("wrong message appended: %+v", got)
		}
		if got.Content == "y" {
			t.Fatalf("agent_tool line must carry the tool label, not the result body: %q", got.Content)
		}
	})

	t.Run("sub-agent final result renders indented under its thread", func(t *testing.T) {
		m := Model{viewport: viewport.New(80, 10)}
		m.messages = append(m.messages, ChatMessage{Role: "agent_result", Name: "explore", AgentID: "agent1234", Content: "done"})
		m.updateViewport()
		out := m.viewport.View()
		if !strings.Contains(out, theme.Arrow) {
			t.Errorf("agent result should render the %q marker, got:\n%s", theme.Arrow, out)
		}
		if !strings.Contains(out, "done") {
			t.Errorf("agent result should render its content, got:\n%s", out)
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
