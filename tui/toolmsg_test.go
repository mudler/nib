package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/tui/render"
)

func TestToolMessageReadCollapsesToLineCount(t *testing.T) {
	msg := toolMessage(chat.ToolResult{
		Name:   "read",
		Result: `{"content":"   1| a\n   2| b\n   3| c","total_lines":3,"success":true}`,
	}, 0)
	if msg.Content != "" || msg.Meta != "3 lines" || msg.Status != render.ToolStatusOK {
		t.Fatalf("got content %q meta %q status %v, want a bare header with 3 lines", msg.Content, msg.Meta, msg.Status)
	}
}

func TestToolMessageWriteShowsDiffNotEnvelope(t *testing.T) {
	msg := toolMessage(chat.ToolResult{
		Name:   "write",
		Result: `{"success":true}`,
		Change: &chat.FileChange{Path: "a.go", After: "x\ny\n", Created: true},
	}, 0)
	if msg.Diff == nil || msg.Diff.Added != 2 {
		t.Fatalf("diff = %+v, want two added lines", msg.Diff)
	}
	if !strings.Contains(msg.Meta, "new file") || !strings.HasSuffix(msg.Meta, "+2") {
		t.Fatalf("meta = %q, want new file · +2", msg.Meta)
	}
	if strings.Contains(msg.Content, "success") {
		t.Fatalf("content = %q, want the envelope dropped", msg.Content)
	}
}

func TestToolMessageWriteWithoutChangeIsBare(t *testing.T) {
	msg := toolMessage(chat.ToolResult{Name: "write", Result: `{"success":true}`}, 0)
	if msg.Content != "" || msg.Diff != nil {
		t.Fatalf("got %+v, want a bare header", msg)
	}
}

func TestToolMessageFailedBashIsMarked(t *testing.T) {
	msg := toolMessage(chat.ToolResult{
		Name:   "bash",
		Result: `{"stdout":"","stderr":"boom","exit_code":1,"success":false}`,
	}, 0)
	if msg.Status != render.ToolStatusFailed || msg.Meta != "exit 1" || msg.Content != "boom" {
		t.Fatalf("got status %v meta %q content %q", msg.Status, msg.Meta, msg.Content)
	}
}

func TestToolMessageFailedEditKeepsErrorAndNoDiff(t *testing.T) {
	msg := toolMessage(chat.ToolResult{
		Name:   "edit",
		Result: `{"replacements":0,"success":false,"error":"old string not found"}`,
		Change: &chat.FileChange{Before: "a", After: "b", Fragment: true},
	}, 0)
	if msg.Status != render.ToolStatusFailed || msg.Diff != nil || msg.Content != "old string not found" {
		t.Fatalf("got %+v, want a failed block showing the error", msg)
	}
}

func TestShellJobsFooterHiddenWhenIdle(t *testing.T) {
	if _, ok := shellJobsFooterRow([]wizmcp.ShellJobInfo{{Status: "completed"}}); ok {
		t.Fatal("a footer row was shown with nothing running")
	}
	if _, ok := shellJobsFooterRow([]wizmcp.ShellJobInfo{{Status: "failed"}}); !ok {
		t.Fatal("a failed job should keep the row")
	}
	if _, ok := shellJobsFooterRow([]wizmcp.ShellJobInfo{{Status: "running"}}); !ok {
		t.Fatal("a running job should show the row")
	}
}

func TestJobsFooterHiddenWhenIdle(t *testing.T) {
	if _, ok := jobsFooterRow([]agentJob{{Status: chat.AgentStatusCompleted}}); ok {
		t.Fatal("a footer row was shown with nothing running")
	}
}

// A tool whose Execute returned an error reaches the transcript as cogito's
// plain-text "Error running tool: ..." result, not a JSON envelope. It must
// still be marked failed, not shown with a success mark.
func TestToolMessageExecuteErrorIsMarked(t *testing.T) {
	result := `Error running tool: calling "tools/call": invalid params: validating "arguments": validating root: unexpected additional properties ["pattern"]`
	msg := toolMessage(chat.ToolResult{Name: "glob", Result: result}, 0)
	if msg.Status != render.ToolStatusFailed || msg.Content != result {
		t.Fatalf("got status %v content %q, want a failed block showing the error", msg.Status, msg.Content)
	}
}
