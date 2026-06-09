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
// "(" and ")" cover subshells; "\r" is rejected because Go's strings.Fields
// splits on it while bash does not, so allowing it would let the derived
// first word diverge from what bash actually runs.
var rejectedShellTokens = []string{
	"&&", "||", ";", "|", "&", "$(", "${", "`", ">", "<", "\n", "\r",
	"(", ")",
}

// chainingCommands execute other commands, so granting their first word
// would grant arbitrary execution. This denylist of wrappers is inherently
// incomplete (any installed binary can exec another); the conservative
// rejected-token checks above remain the primary boundary.
var chainingCommands = map[string]bool{
	// shells and shell builtins that run arbitrary code
	"sh": true, "bash": true, "zsh": true, "eval": true, "exec": true,
	"source": true, ".": true, "command": true, "!": true,
	// environment / privilege wrappers
	"env": true, "sudo": true, "doas": true, "su": true, "runuser": true,
	// process / scheduling wrappers
	"nohup": true, "time": true, "timeout": true, "nice": true,
	"ionice": true, "chrt": true, "setsid": true, "stdbuf": true,
	"flock": true, "watch": true, "setarch": true, "taskset": true,
	// namespace / multiplexer wrappers
	"unshare": true, "nsenter": true, "busybox": true, "xargs": true,
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
	// strings.Fields splits on all unicode whitespace, a superset of bash's
	// default word separators (space, tab, newline). Splitting on more can
	// only shorten the first word, never merge two bash words, so the
	// derived prefix is at most narrower than what bash runs — safe.
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
