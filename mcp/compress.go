package mcp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mudler/nib/types"
)

// Default output-limit constants, used when the config block is absent or a
// field is zero. They are kept here rather than in types/config.go because
// they are the mcp package's policy, not the config struct's.
const (
	defaultBudget       = 16 * 1024 // ~4K tokens
	defaultHeadBudget   = 4 * 1024
	defaultMaxLineLen   = 2000
	defaultSpillThreshold = 64 * 1024
)

// ansiRe matches CSI sequences, OSC sequences, and a few common single-char
// escapes (cursor save/restore, charset designation). Enough to clean typical
// terminal colour and cursor output from ls --color, grep --color, etc.
var ansiRe = regexp.MustCompile(
	"\x1b\\[[0-9;?]*[a-zA-Z]" + // CSI: colors, cursor moves, clear
		"|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)" + // OSC: title, hyperlink
		"|\x1b[()][AB012]" + // Charset designation
		"|\x1b[=>]", // Keypad mode
)

// truncationWarning is prepended to output that was truncated. It tells the
// model what happened and what to do instead, so it can self-correct on the
// next call rather than blindly re-running the same broad command.
const truncationWarning = "⚠ Output was %s (%d bytes) — truncated to the first %s and last %s. To see what you need, re-run with a more targeted command: grep for a pattern, use head/tail with a line count, write to a file and read specific sections, or use bash_background + bash_job_output for paging."

// artifactSpillNotice is appended when the full output has been saved as an
// artifact. It tells the model how to page through the full output.
const artifactSpillNotice = "\n📦 Full output saved as %s — use the read tool with this path to page through it (with offset/limit)."

// OutputLimits is the resolved tool-output limits policy. Zero values are
// replaced with defaults by ResolveOutputLimits.
type OutputLimits struct {
	Disabled              bool
	Budget                int
	HeadBudget            int
	MaxLineLength         int
	ArtifactSpillThreshold int
}

// ResolveOutputLimits fills zero fields with defaults, matching the
// whole-block defaulting in config/config.go.
func ResolveOutputLimits(cfg types.ToolOutputLimitsConfig) OutputLimits {
	l := OutputLimits{
		Disabled:               cfg.Disabled,
		Budget:                 cfg.Budget,
		HeadBudget:             cfg.HeadBudget,
		MaxLineLength:          cfg.MaxLineLength,
		ArtifactSpillThreshold: cfg.ArtifactSpillThreshold,
	}
	if l.Budget == 0 {
		l.Budget = defaultBudget
	}
	if l.HeadBudget == 0 {
		l.HeadBudget = defaultHeadBudget
	}
	if l.MaxLineLength == 0 {
		l.MaxLineLength = defaultMaxLineLen
	}
	if l.ArtifactSpillThreshold == 0 {
		l.ArtifactSpillThreshold = defaultSpillThreshold
	}
	// -1 means "off": no artifact spill, output is truncated but never saved.
	if l.ArtifactSpillThreshold < 0 {
		l.ArtifactSpillThreshold = 0
	}
	if l.HeadBudget > l.Budget {
		l.HeadBudget = l.Budget / 4
	}
	return l
}

// LimitOutput applies the full output-limiting pipeline to s:
//  1. Strip ANSI escape codes
//  2. Collapse runs of repeated lines
//  3. Truncate lines longer than MaxLineLength
//  4. If the result fits the budget, return it as-is
//  5. If it exceeds the budget, keep head+tail and prepend a warning
//  6. If it exceeds the spill threshold, save the full output as an
//     artifact and append a recovery notice
//
// toolName is the name of the calling tool ("bash", "read", etc.), used
// only for artifact metadata. store may be nil, in which case no artifact
// is saved (the output is still truncated).
func LimitOutput(s, toolName string, limits OutputLimits, store *ArtifactStore) string {
	if limits.Disabled {
		return s
	}

	s = ansiRe.ReplaceAllString(s, "")
	s = collapseRepeats(s)
	s = truncateLongLines(s, limits.MaxLineLength)

	if len(s) <= limits.Budget {
		return s
	}

	total := len(s)
	tailBudget := limits.Budget - limits.HeadBudget
	if tailBudget < 0 {
		tailBudget = 0
	}
	head := s[:limits.HeadBudget]
	tail := s[total-tailBudget:]
	elided := total - limits.HeadBudget - tailBudget

	out := fmt.Sprintf(truncationWarning+"\n… %d bytes elided\n%s\n… … …\n%s",
		humanBytes(total), total,
		humanBytes(limits.HeadBudget), humanBytes(tailBudget),
		elided, head, tail)

	// Artifact spill: save the full output if it exceeds the threshold and
	// a store is available.
	if limits.ArtifactSpillThreshold > 0 && total >= limits.ArtifactSpillThreshold && store != nil {
		uri := store.Save(toolName, s)
		out += fmt.Sprintf(artifactSpillNotice, uri)
	}

	return out
}

// truncateLongLines truncates each line to maxLen characters. Lines that are
// cut get a " …" suffix so the model can tell the line continues.
func truncateLongLines(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Count runes, not bytes, so multi-byte characters are not split.
		runes := []rune(line)
		if len(runes) > maxLen {
			lines[i] = string(runes[:maxLen]) + " …"
		}
	}
	return strings.Join(lines, "\n")
}

// compressOutput is the legacy entry point for bash output. It applies the
// default limits with no artifact store. Kept for backward compatibility
// with existing tests.
func compressOutput(s string) string {
	return LimitOutput(s, "bash", OutputLimits{
		Budget:     defaultBudget,
		HeadBudget: defaultHeadBudget,
		MaxLineLength: defaultMaxLineLen,
		// No artifact spill in the legacy path.
		ArtifactSpillThreshold: 0,
	}, nil)
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
