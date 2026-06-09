package chat

import (
	"encoding/json"
	"strings"
)

// Scoped "always allow" grants for the bash tool. A grant covers a command
// prefix (the script's first word) rather than the whole tool, so approving
// `git push` can allow future `git …` calls without allowing arbitrary shell.
//
// Both derivation and matching go through BashGrantPrefix, which requires the
// script to be a single simple command. The check is deliberately
// conservative: a rejected token anywhere in the script — even inside quotes —
// disqualifies it. False negatives (prompting when a grant could have
// applied) are acceptable; false positives (a grant matching a smuggled
// command, e.g. `git push && rm -rf /` riding a `git` grant) are not.

// rejectedShellTokens disqualify a script from prefix grants: they chain,
// substitute, redirect, or background, so the first word no longer bounds
// what runs.
var rejectedShellTokens = []string{
	"&&", "||", ";", "|", "&", "$(", "${", "`", ">", "<", "\n",
}

// chainingCommands execute other commands, so granting their first word
// would grant arbitrary execution.
var chainingCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "eval": true, "exec": true,
	"source": true, ".": true, "env": true, "xargs": true, "sudo": true,
	"nohup": true, "time": true, "command": true,
}

// BashGrantPrefix derives the prefix-grant key for a bash tool call: the
// script's first word, when the script is a single simple command. ok is
// false when no safe prefix can be derived (compound or chaining commands,
// unparseable args) — callers fall back to a whole-tool grant.
func BashGrantPrefix(argsJSON string) (string, bool) {
	var args struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", false
	}
	script := strings.TrimSpace(args.Script)
	if script == "" {
		return "", false
	}
	for _, tok := range rejectedShellTokens {
		if strings.Contains(script, tok) {
			return "", false
		}
	}
	first := strings.Fields(script)[0]
	// Quotes, escapes, expansions, or assignments in the first word mean it
	// is not a plain command name (e.g. FOO=1 git push, "$CMD" args).
	if chainingCommands[first] || strings.ContainsAny(first, `"'\$=`) {
		return "", false
	}
	return first, true
}

// GrantScope describes what choosing "always allow" covers for this call:
// scope is the user-facing wording, prefix the grant key ("" = whole tool).
// Only bash gets prefix grants; every other tool (including bash_background)
// is granted whole.
func GrantScope(name, argsJSON string) (scope, prefix string) {
	if name != "bash" {
		return name, ""
	}
	if p, ok := BashGrantPrefix(argsJSON); ok {
		return "`" + p + " …`", p
	}
	return "any bash command", ""
}
