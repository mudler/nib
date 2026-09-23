package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mudler/nib/theme"
)

// numbered returns n lines "line 1" … "line n".
func numbered(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return strings.Join(lines, "\n")
}

func TestToolBlockFoldsLongOutput(t *testing.T) {
	out := ansi.Strip(ToolBlock(Message{Role: RoleTool, Label: "$ ls", Content: numbered(40)}, 80))
	if !strings.Contains(out, "line 12") || strings.Contains(out, "line 13") {
		t.Fatalf("folded block should show the first %d lines only:\n%s", ToolFoldLines, out)
	}
	if !strings.Contains(out, "… 28 more lines · "+theme.ReasoningExpand) {
		t.Fatalf("folded block should say how many lines are hidden and how to expand:\n%s", out)
	}
}

func TestToolBlockExpandedShowsAll(t *testing.T) {
	out := ansi.Strip(ToolBlock(Message{Role: RoleTool, Label: "$ ls", Content: numbered(40), Expanded: true}, 80))
	if !strings.Contains(out, "line 40") {
		t.Fatalf("expanded block should show every line:\n%s", out)
	}
	if !strings.Contains(out, theme.ReasoningCollapse) {
		t.Fatalf("expanded block should offer to collapse:\n%s", out)
	}
}

func TestToolBlockShortOutputHasNoFoldRow(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		out := ansi.Strip(ToolBlock(Message{Role: RoleTool, Label: "$ ls", Content: numbered(3), Expanded: expanded}, 80))
		if strings.Contains(out, "ctrl+r") {
			t.Fatalf("a block with nothing to fold needs no hint (expanded=%v):\n%s", expanded, out)
		}
	}
}

func TestRunningToolHeader(t *testing.T) {
	out := ansi.Strip(RunningToolBlock(RunningTool{Label: "$ go test ./...", Elapsed: 12 * time.Second, Hint: theme.ToolBackgroundHint}, 80))
	head := strings.SplitN(out, "\n", 2)[0]
	for _, want := range []string{theme.RunningDot, "go test ./...", "12s", theme.ToolBackgroundHint} {
		if !strings.Contains(head, want) {
			t.Fatalf("running header %q is missing %q", head, want)
		}
	}
}

func TestRunningToolHidesElapsedUnderASecond(t *testing.T) {
	out := ansi.Strip(RunningToolBlock(RunningTool{Label: "grep foo", Elapsed: 300 * time.Millisecond}, 80))
	if strings.Contains(out, "1s") {
		t.Fatalf("a call under a second should show no elapsed time: %q", out)
	}
}

func TestRunningToolTailsOutput(t *testing.T) {
	out := ansi.Strip(RunningToolBlock(RunningTool{Label: "$ make", Elapsed: 3 * time.Second, Output: numbered(20)}, 80))
	if !strings.Contains(out, "… 15 more lines · "+theme.ReasoningExpand) {
		t.Fatalf("collapsed running block should count the hidden lines:\n%s", out)
	}
	if strings.Contains(out, "line 15\n") || !strings.Contains(out, "line 16") || !strings.Contains(out, "line 20") {
		t.Fatalf("collapsed running block should show the last %d lines:\n%s", RunningTailLines, out)
	}
	// The fold row sits above the tail, so the newest line stays last.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.Contains(lines[len(lines)-1], "line 20") {
		t.Fatalf("newest output should be the last row:\n%s", out)
	}
}

func TestRunningToolExpandedShowsAllOutput(t *testing.T) {
	out := ansi.Strip(RunningToolBlock(RunningTool{Label: "$ make", Output: numbered(20), Expanded: true}, 80))
	if !strings.Contains(out, "line 1\n") || !strings.Contains(out, "line 20") {
		t.Fatalf("expanded running block should show all output:\n%s", out)
	}
}
