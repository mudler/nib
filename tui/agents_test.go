package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
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

func TestToolLabelWithAgent(t *testing.T) {
	got := buildApprovalContent(chat.ToolCallRequest{Name: "echo", AgentID: "a1"}).title
	if !strings.Contains(got, "a1") || !strings.HasSuffix(got, "echo") {
		t.Fatalf("expected agent-labeled approval header, got %q", got)
	}
	root := buildApprovalContent(chat.ToolCallRequest{Name: "echo"}).title
	if root != "echo" {
		t.Fatalf("expected plain header, got %q", root)
	}
}

// TestApprovalContentShowsChangeAsDiff: an edit approval with a predicted
// change is titled by its one-line summary, carries the diff and its stat, and
// drops the old -> new summary line the diff replaces.
func TestApprovalContentShowsChangeAsDiff(t *testing.T) {
	req := chat.ToolCallRequest{
		Name:      "edit",
		Arguments: `{"path":"main.go","old":"a","new":"b"}`,
		Change:    &chat.FileChange{Path: "main.go", Before: "x\na\ny\n", After: "x\nb\ny\n"},
	}
	c := buildApprovalContent(req)
	if c.title != "edit main.go" {
		t.Fatalf("title = %q, want %q", c.title, "edit main.go")
	}
	if c.diff == nil || c.diff.Added != 1 || c.diff.Removed != 1 {
		t.Fatalf("diff = %+v, want +1 -1", c.diff)
	}
	if c.meta != "+1 -1" {
		t.Fatalf("meta = %q, want %q", c.meta, "+1 -1")
	}
	if len(c.rows) != 0 {
		t.Fatalf("rows = %v, want none (the diff replaces the summary)", c.rows)
	}
}

// TestApprovalContentNewFile: a write that creates its file says so.
func TestApprovalContentNewFile(t *testing.T) {
	c := buildApprovalContent(chat.ToolCallRequest{
		Name:      "write",
		Arguments: `{"path":"hello.go","content":"package main\n"}`,
		Change:    &chat.FileChange{Path: "hello.go", After: "package main\n", Created: true},
	})
	if !strings.HasPrefix(c.meta, theme.DiffNewFile) || !strings.HasSuffix(c.meta, "+1") {
		t.Fatalf("meta = %q, want new file · +1", c.meta)
	}
}

// TestApprovalContentMultilineScriptKeepsRest: a multi-line bash script is
// titled by its first line; the rest follows as one prose row, and the first
// line is not repeated.
func TestApprovalContentMultilineScriptKeepsRest(t *testing.T) {
	c := buildApprovalContent(chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"cd x\nmake"}`})
	if c.title != "$ cd x" {
		t.Fatalf("title = %q", c.title)
	}
	if !c.unstructured || len(c.rows) != 1 || c.rows[0][1] != "make" {
		t.Fatalf("rows = %v, want the remaining line", c.rows)
	}
}
