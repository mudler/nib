package chat

// readOnlyTools are built-in tools that only observe state, regardless of
// their arguments. Anything not listed here (write, edit, cron, spawn_agent,
// schedule_wakeup, load_skill, ask_user, and all MCP/plugin tools) is treated
// as potentially mutating and will prompt. bash/bash_background are handled
// separately by inspecting the command.
var readOnlyTools = map[string]bool{
	"read":             true,
	"grep":             true,
	"glob":             true,
	"bash_jobs":        true,
	"bash_job_output":  true,
	"agent_logs":       true,
	"check_agent":      true,
	"get_agent_result": true,
	"cron_list":        true,
}

// IsReadOnly reports whether a tool call only observes state and is therefore
// safe to auto-approve in the default prompt mode. It is deliberately
// conservative: unknown tools and any non-trivial bash return false. cmds is
// the read-only bash command set (built-ins plus user config).
func IsReadOnly(name, argsJSON string, cmds readOnlyCommands) bool {
	if readOnlyTools[name] {
		return true
	}
	if name == "bash" || name == "bash_background" {
		words, ok := safeCommand(argsJSON)
		if !ok {
			return false
		}
		return cmds.match(words)
	}
	return false
}
