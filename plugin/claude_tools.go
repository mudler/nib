package plugin

// claudeToolAliases maps Claude Code tool names to wiz's built-in tool names.
// Unlisted Claude tools (Task, TodoWrite, ...) have no wiz equivalent and are
// dropped during mapping.
var claudeToolAliases = map[string]string{
	"Bash":      "bash",
	"Read":      "read",
	"Write":     "write",
	"Edit":      "edit",
	"MultiEdit": "edit",
	"Glob":      "glob",
	"Grep":      "grep",
	"WebFetch":  "web_fetch",
	"WebSearch": "web_search",
}

// aliasClaudeTool returns the wiz tool name for a Claude tool name.
func aliasClaudeTool(name string) (string, bool) {
	w, ok := claudeToolAliases[name]
	return w, ok
}

// aliasClaudeTools maps a list of Claude tool names to wiz tool names, dropping
// unmapped ones and de-duplicating (preserving first-seen order).
func aliasClaudeTools(tools []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tools {
		if w, ok := aliasClaudeTool(t); ok && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
