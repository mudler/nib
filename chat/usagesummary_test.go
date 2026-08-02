package chat

import (
	"strings"
	"testing"
)

// Nothing spent, nothing to say — a session that exits before its first turn
// should not print a row of zeroes.
func TestFormatSessionSummaryEmptyWhenUnused(t *testing.T) {
	if got := FormatSessionSummary(SessionUsage{}); got != "" {
		t.Fatalf("summary for an unused session = %q, want empty", got)
	}
}

// The shape the spec fixed: turns, both directions, and the total.
func TestFormatSessionSummaryReportsTurnsAndTokens(t *testing.T) {
	got := FormatSessionSummary(SessionUsage{
		PromptTokens:     312400,
		CompletionTokens: 18400,
		TotalTokens:      330800,
		Turns:            14,
	})
	for _, want := range []string{"14 turns", "312.4k", "18.4k", "330.8k"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q is missing %q", got, want)
		}
	}
}

// One turn is a turn, not "1 turns".
func TestFormatSessionSummarySingularTurn(t *testing.T) {
	got := FormatSessionSummary(SessionUsage{PromptTokens: 10, TotalTokens: 10, Turns: 1})
	if strings.Contains(got, "1 turns") {
		t.Fatalf("summary should say \"1 turn\": %q", got)
	}
	if !strings.Contains(got, "1 turn") {
		t.Fatalf("summary does not report the turn count: %q", got)
	}
}

// A session can spend tokens without completing a turn: Task 5 counts tokens
// for every run including an interrupted one, but Turns only for completed
// exchanges. That must still render as a sentence, not as "0 turns" suppressing
// the spend.
func TestFormatSessionSummaryInterruptedBeforeFirstTurn(t *testing.T) {
	got := FormatSessionSummary(SessionUsage{
		PromptTokens:     1200,
		CompletionTokens: 30,
		TotalTokens:      1230,
		Turns:            0,
	})
	if got == "" {
		t.Fatal("tokens were spent but the summary is empty")
	}
	for _, want := range []string{"0 turns", "1.2k", "30"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q is missing %q", got, want)
		}
	}
}

// HumanTokens renders "" for a zero count, which would leave a hole in the
// sentence ("... /  completion ..."). Every segment must carry a number.
func TestFormatSessionSummaryFillsZeroSegments(t *testing.T) {
	got := FormatSessionSummary(SessionUsage{PromptTokens: 10, TotalTokens: 10, Turns: 1})
	if !strings.Contains(got, "0 completion") {
		t.Fatalf("a zero segment must still render a number: %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("summary has a hole where a count should be: %q", got)
	}
}
