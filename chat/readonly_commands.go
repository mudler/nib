package chat

import "strings"

// readOnlyCommands classifies bash commands that only observe state. whole
// holds commands that are read-only at any arguments; pairs maps a command to
// the set of its read-only subcommands (for mixed tools like git/go where the
// first word alone is too coarse — `git status` is safe, `git push` is not).
type readOnlyCommands struct {
	whole map[string]bool
	pairs map[string]map[string]bool
}

// builtinWholeReadOnly are commands that only read/inspect, regardless of args.
//
// INVARIANT: every entry must be read-only at ALL arguments. No command with an
// output-file flag (e.g. -o), a positional output, a -delete/-exec form, or a
// config-write flag belongs here. The shell-syntax gate (safeCommand) does not
// catch a command's own mutating flags — so this list itself must stay
// conservative.
var builtinWholeReadOnly = []string{
	"ls", "cat", "head", "tail", "grep", "rg", "wc",
	"pwd", "stat", "file", "du", "df", "basename", "dirname", "echo",
	"printf", "whoami", "uname", "which", "type",
	"cut", "column", "nl", "less", "more",
}

// builtinReadOnlyPairs map a command to its read-only subcommands. Anything not
// listed (git push, go build, docker run, kubectl apply, npm install,
// cargo build, …) is excluded by construction and will prompt.
//
// The same invariant as builtinWholeReadOnly applies per subcommand: a listed
// `cmd subcmd` pair must be read-only regardless of any additional flags (e.g.
// `git branch -D`, `git tag`, `go env -w` mutate, so they are NOT listed).
var builtinReadOnlyPairs = map[string][]string{
	"git":     {"status", "log", "diff", "show", "blame", "describe", "rev-parse", "ls-files"},
	"go":      {"list", "version", "doc", "vet"},
	"docker":  {"ps", "images", "inspect", "logs", "version", "info"},
	"kubectl": {"get", "describe", "logs", "version"},
	"npm":     {"ls", "list", "view", "outdated"},
	"cargo":   {"tree", "metadata"},
}

// newReadOnlyCommands builds the set from the built-ins plus user extensions.
// An extra entry containing a space is a "cmd subcmd" pair; otherwise it is a
// whole-command entry. Extensions merge with the built-ins.
func newReadOnlyCommands(extra []string) readOnlyCommands {
	c := readOnlyCommands{
		whole: make(map[string]bool, len(builtinWholeReadOnly)),
		pairs: make(map[string]map[string]bool, len(builtinReadOnlyPairs)),
	}
	for _, w := range builtinWholeReadOnly {
		c.whole[w] = true
	}
	for cmd, subs := range builtinReadOnlyPairs {
		set := make(map[string]bool, len(subs))
		for _, s := range subs {
			set[s] = true
		}
		c.pairs[cmd] = set
	}
	for _, e := range extra {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		cmd, sub, isPair := strings.Cut(e, " ")
		if !isPair {
			c.whole[cmd] = true
			continue
		}
		sub = strings.TrimSpace(sub)
		if sub == "" {
			c.whole[cmd] = true
			continue
		}
		if c.pairs[cmd] == nil {
			c.pairs[cmd] = make(map[string]bool)
		}
		c.pairs[cmd][sub] = true
	}
	return c
}

// match reports whether the command (words[0]) — or command+subcommand
// (words[0] words[1]) — is read-only.
func (c readOnlyCommands) match(words []string) bool {
	if len(words) == 0 {
		return false
	}
	if c.whole[words[0]] {
		return true
	}
	if len(words) >= 2 {
		if subs := c.pairs[words[0]]; subs != nil && subs[words[1]] {
			return true
		}
	}
	return false
}
