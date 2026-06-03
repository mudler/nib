package tui

import "strings"

// lastSuggestedCommand returns the command nib most recently suggested, suitable
// for inserting at the user's shell prompt (the Ctrl+Y "yank" flow).
//
// It looks at the last assistant message and returns the content of its last
// fenced ``` code block. If the message has no code block but is a single line,
// that line is returned verbatim. Otherwise it returns "" (nothing to yank).
func lastSuggestedCommand(messages []ChatMessage) string {
	var content string
	found := false
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			content = messages[i].Content
			found = true
			break
		}
	}
	if !found {
		return ""
	}

	if block := lastFencedBlock(content); block != "" {
		return block
	}

	// No code block: accept a single-line answer, unwrapping any surrounding
	// inline-code backticks (models often reply with `the command`). Never
	// yank multi-line prose.
	trimmed := strings.TrimSpace(content)
	if trimmed != "" && !strings.Contains(trimmed, "\n") {
		return strings.TrimSpace(strings.Trim(trimmed, "`"))
	}
	return ""
}

// lastFencedBlock returns the content of the last complete ```-fenced block in
// s, trimmed of surrounding whitespace. Returns "" if there is no closed block.
func lastFencedBlock(s string) string {
	lines := strings.Split(s, "\n")
	var last string
	var cur []string
	inBlock := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inBlock {
				// Closing fence: this block is complete.
				last = strings.TrimSpace(strings.Join(cur, "\n"))
				cur = nil
				inBlock = false
			} else {
				// Opening fence (any language tag after ``` is ignored).
				inBlock = true
				cur = nil
			}
			continue
		}
		if inBlock {
			cur = append(cur, line)
		}
	}
	return last
}
