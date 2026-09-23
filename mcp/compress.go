package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

// bashOutputBudget caps the bytes of stdout or stderr returned by the bash
// tools. Output beyond this is elided, keeping a head and a tail so the model
// sees both the start (command headers, test names) and the end (errors, exit
// status). 16 KB ≈ 4 K tokens — generous enough for normal command output,
// tight enough to stop a build log from flooding the context.
const bashOutputBudget = 16 * 1024

// bashHeadBudget is how many bytes of the head to retain when output exceeds
// the budget. The tail gets the rest.
const bashHeadBudget = 4 * 1024

// ansiRe matches CSI sequences, OSC sequences, and a few common single-char
// escapes (cursor save/restore, charset designation). Enough to clean typical
// terminal colour and cursor output from ls --color, grep --color, etc.
var ansiRe = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[a-zA-Z]" + // CSI: colors, cursor moves, clear
		"|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)" + // OSC: title, hyperlink
		"|\x1b[()][AB012]" + // Charset designation
		"|\x1b[=>]", // Keypad mode
)

// bashTruncationWarning is prepended to output that was truncated. It tells the
// model what happened and what to do instead, so it can self-correct on the
// next call rather than blindly re-running the same broad command.
const bashTruncationWarning = "⚠ Output was %s (%d bytes) — truncated to the first %s and last %s. To see what you need, re-run with a more targeted command: grep for a pattern, use head/tail with a line count, write to a file and read specific sections, or use bash_background + bash_job_output for paging."

// compressOutput strips ANSI escape codes, collapses runs of repeated lines,
// and applies a head+tail budget so that a single command cannot flood the
// context with megabytes of output.
func compressOutput(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	s = collapseRepeats(s)
	if len(s) <= bashOutputBudget {
		return s
	}

	total := len(s)
	tailBudget := bashOutputBudget - bashHeadBudget
	head := s[:bashHeadBudget]
	tail := s[total-tailBudget:]
	elided := total - bashHeadBudget - tailBudget

	return fmt.Sprintf(bashTruncationWarning+"\n… %d bytes elided\n%s\n… … …\n%s",
		humanBytes(total), total,
		humanBytes(bashHeadBudget), humanBytes(tailBudget),
		elided, head, tail)
}

// collapseRepeats replaces runs of 3+ identical consecutive lines with the
// first line and a "[N repeated lines]" note. Runs of 2 are left intact —
// a single duplicate is common and not worth a marker.
func collapseRepeats(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 2 {
		return s
	}
	var out []string
	i := 0
	for i < len(lines) {
		out = append(out, lines[i])
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		if dups := j - i - 1; dups >= 2 {
			out = append(out, fmt.Sprintf("  [%d repeated lines]", dups))
		} else if dups == 1 {
			out = append(out, lines[i])
		}
		i = j
	}
	return strings.Join(out, "\n")
}

// humanBytes renders a byte count in a human-readable form (e.g. "48 KB").
func humanBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%d MB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%d KB", n/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
