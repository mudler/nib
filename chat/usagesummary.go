package chat

import "fmt"

// FormatSessionSummary renders the one-line spend report shown when a session
// ends. It returns "" when nothing was spent, so a session that exits before
// its first turn prints nothing rather than a row of zeroes.
//
// Turns and tokens are not in lockstep and the wording does not claim they are:
// every run is counted for tokens, including one the user interrupted, while
// Turns counts only completed exchanges. A session showing spend against fewer
// turns is reporting the truth, not a miscount.
//
// Callers must not send this to stdout in TUI mode: stdout carries the
// shell-capture line, and a summary there would be pasted into the user's
// command line. RunTUI writes it to /dev/tty and RunCLI to stderr.
func FormatSessionSummary(u SessionUsage) string {
	if u.TotalTokens <= 0 && u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return ""
	}
	turns := "turns"
	if u.Turns == 1 {
		turns = "turn"
	}
	return fmt.Sprintf("session: %d %s · %s prompt / %s completion · %s total",
		u.Turns, turns,
		HumanTokensOrZero(u.PromptTokens), HumanTokensOrZero(u.CompletionTokens), HumanTokensOrZero(u.TotalTokens))
}

// HumanTokensOrZero is HumanTokens with a floor of "0". HumanTokens renders ""
// for a zero count so a caller can drop the segment entirely, which suits a
// count that stands alone; it does not suit a fixed shape with a slot for every
// direction. A provider that reports no completion tokens would otherwise leave
// a hole where a number belongs.
//
// Exported because the TUI's footer badge prints that same fixed shape from
// package tui and must not grow a second copy of this rule: one formatter, one
// guard (TestFormatSessionSummaryFillsZeroSegments and its badge twin).
func HumanTokensOrZero(n int) string {
	if s := HumanTokens(n); s != "" {
		return s
	}
	return "0"
}
