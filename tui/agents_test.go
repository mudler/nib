package tui

import (
	"errors"
	"reflect"
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

func TestCompactTask(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     string
	}{
		{"empty", "", 72, ""},
		{"short single line unchanged", "find reporting code", 72, "find reporting code"},
		{"first line only", "find reporting code\nand also do X\nand Y", 72, "find reporting code"},
		{"ellipsized when too long", "abcdefghij", 5, "abcd…"},
		{"leading and trailing space trimmed", "  hello  ", 72, "hello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compactTask(c.in, c.max); got != c.want {
				t.Errorf("compactTask(%q,%d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}
}

func TestCapThreadLines(t *testing.T) {
	t.Run("under cap unchanged", func(t *testing.T) {
		in := []string{"a", "b", "c"}
		if got := capThreadLines(in, 8); !reflect.DeepEqual(got, in) {
			t.Errorf("got %v, want %v", got, in)
		}
	})
	t.Run("exactly cap unchanged", func(t *testing.T) {
		in := []string{"a", "b", "c"}
		if got := capThreadLines(in, 3); !reflect.DeepEqual(got, in) {
			t.Errorf("got %v, want %v", got, in)
		}
	})
	t.Run("over cap collapses older with count", func(t *testing.T) {
		in := []string{"a", "b", "c", "d", "e"}
		got := capThreadLines(in, 2)
		want := []string{"… +3 earlier", "d", "e"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("nil input", func(t *testing.T) {
		if got := capThreadLines(nil, 8); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}

func TestAgentTranscriptLineCompactsTask(t *testing.T) {
	long := "find the reporting code and also map every caller and rendering path across the whole tui package so we know what to change"
	ev := chat.AgentEvent{Type: "explore", Task: long, Status: chat.AgentStatusRunning}
	line := agentTranscriptLine(ev)
	if len([]rune(line)) > len("sub-agent explore started: ")+compactTaskWidth {
		t.Errorf("header not compacted: %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("expected ellipsis in compacted header, got %q", line)
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
