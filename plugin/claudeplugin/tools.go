package claudeplugin

import "strings"

// ToolMap maps Claude Code tool names (Bash, Read, Task...) to a host's tool
// names. A name mapped to "" is known to the host and deliberately not
// offered. Plugins keep the names as written; a host applies its own map.
type ToolMap map[string]string

// ToolName returns the tool name of a tool rule: "Bash(git add:*)" gives
// "Bash".
func ToolName(rule string) string {
	if i := strings.IndexByte(rule, '('); i > 0 {
		return strings.TrimSpace(rule[:i])
	}
	return strings.TrimSpace(rule)
}

// Map maps tool rules to host names, de-duplicated in first-seen order. A
// rule's argument part is dropped. unmapped lists the tool names the host
// does not offer, so a host can show them instead of dropping them in
// silence. MCP tool names (mcp__...) pass through unchanged.
func (m ToolMap) Map(rules []string) (mapped, unmapped []string) {
	seen := map[string]bool{}
	missed := map[string]bool{}
	for _, r := range rules {
		name := ToolName(r)
		if name == "" {
			continue
		}
		host, ok := m[name]
		if !ok && strings.HasPrefix(name, "mcp__") {
			host, ok = name, true
		}
		if !ok || host == "" {
			if !missed[name] {
				missed[name] = true
				unmapped = append(unmapped, name)
			}
			continue
		}
		if !seen[host] {
			seen[host] = true
			mapped = append(mapped, host)
		}
	}
	return mapped, unmapped
}

// Reverse returns the host-to-Claude map. When several Claude names map to
// one host name, the first in ClaudeTools order wins.
func (m ToolMap) Reverse() map[string]string {
	out := map[string]string{}
	for _, c := range ClaudeTools {
		if h, ok := m[c]; ok && h != "" {
			if _, dup := out[h]; !dup {
				out[h] = c
			}
		}
	}
	return out
}

// ClaudeTools lists the Claude Code built-in tool names that plugins
// reference.
var ClaudeTools = []string{
	"Bash", "BashOutput", "KillShell", "Read", "Write", "Edit", "MultiEdit",
	"NotebookEdit", "NotebookRead", "Glob", "Grep", "LS", "WebFetch", "WebSearch",
	"Task", "Agent", "Skill", "TodoWrite", "TodoRead", "SlashCommand",
	"ExitPlanMode", "AskUserQuestion", "LSP", "Monitor",
}
