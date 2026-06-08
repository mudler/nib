package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/mudler/nib/theme"
)

func renderMsgs(msgs []ChatMessage) string {
	m := Model{viewport: viewport.New(80, 40)}
	m.jobs = []agentJob{{ID: "a1", Type: "explore"}, {ID: "a2", Type: "review"}}
	m.messages = msgs
	m.updateViewport()
	return m.viewport.View()
}

func TestThreadRunIndentsToolLinesUnderHeader(t *testing.T) {
	out := renderMsgs([]ChatMessage{
		{Role: "agent", AgentID: "a1", Content: "sub-agent explore started: find reporting"},
		{Role: "agent_tool", AgentID: "a1", Content: `grep "subagent"`},
		{Role: "agent_tool", AgentID: "a1", Content: "read tui/model.go"},
	})
	if !strings.Contains(out, "started: find reporting") {
		t.Fatalf("missing header, got:\n%s", out)
	}
	// Tool labels must now appear (previously dropped). Assert bare tokens only —
	// lipgloss may insert styling codes between indent and text.
	for _, want := range []string{`grep "subagent"`, "read tui/model.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool line %q should be present, got:\n%s", want, out)
		}
	}
}

func TestThreadRunReprintsHeaderOnAgentSwitch(t *testing.T) {
	out := renderMsgs([]ChatMessage{
		{Role: "agent", AgentID: "a1", Content: "sub-agent explore started: x"},
		{Role: "agent_tool", AgentID: "a1", Content: "read a.go"},
		{Role: "agent_tool", AgentID: "a2", Content: "read b.go"},
		{Role: "agent_tool", AgentID: "a1", Content: "read c.go"},
	})
	if !strings.Contains(out, theme.SubAgent+" review") {
		t.Errorf("expected continuation header for switched agent, got:\n%s", out)
	}
}

func TestThreadRunCapsInlineLines(t *testing.T) {
	msgs := []ChatMessage{{Role: "agent", AgentID: "a1", Content: "sub-agent explore started: x"}}
	for i := 0; i < agentThreadInlineCap+3; i++ {
		msgs = append(msgs, ChatMessage{Role: "agent_tool", AgentID: "a1", Content: "tool line"})
	}
	out := renderMsgs(msgs)
	if !strings.Contains(out, "+3 earlier") {
		t.Errorf("expected '+3 earlier' collapse marker, got:\n%s", out)
	}
}

func TestAgentResultRendersIndentedWithArrow(t *testing.T) {
	out := renderMsgs([]ChatMessage{
		{Role: "agent", AgentID: "a1", Content: "sub-agent explore finished · 1 tool"},
		{Role: "agent_result", AgentID: "a1", Content: "Found it at model.go:1386"},
	})
	if !strings.Contains(out, theme.Arrow) || !strings.Contains(out, "Found it at model.go:1386") {
		t.Errorf("expected arrow-marked indented result, got:\n%s", out)
	}
}
