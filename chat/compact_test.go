package chat

import (
	"strings"
	"testing"
)

// TestFitSummaryHeadTail verifies that fitSummaryInput keeps both the head and
// the tail of an over-long piece, rather than only its first 512 bytes.
func TestFitSummaryHeadTail(t *testing.T) {
	// A piece well over summaryHeadBudget+summaryTailBudget (512) bytes.
	head := strings.Repeat("H", 300)
	middle := strings.Repeat("M", 600)
	tail := strings.Repeat("T", 300)
	long := head + middle + tail

	// One tool piece large enough to require cutting. maxTokens small enough to
	// force truncation (bytes/4 = 300/4 = 75 tokens; total is 1200 bytes = 300
	// tokens, so the limit must be < 300 to trigger a cut).
	pieces := []summaryPiece{{role: "tool", text: long, tool: true}}
	out := fitSummaryInput(pieces, 75)

	if !strings.Contains(out, "H"+strings.Repeat("H", 255)) {
		t.Errorf("expected head portion of piece to be preserved, got: %q...", out[:min(80, len(out))])
	}
	if !strings.Contains(out, strings.Repeat("T", 256)) {
		t.Errorf("expected tail portion of piece to be preserved, got: %q...", out[:min(80, len(out))])
	}
	if strings.Contains(out, "MMMM") {
		t.Errorf("expected middle to be omitted, but found 'MMMM' in output")
	}
	if !strings.Contains(out, "bytes omitted to fit the summary") {
		t.Errorf("expected omission marker in output")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
