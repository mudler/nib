package types

import "strings"

// actGuidance is the one paragraph of the tool guidance that is unconditional:
// it is about behavior, not about any particular tool, so it holds whatever the
// session exposes.
//
// It moved here from config.defaultPrompt for the same reason the rest of this
// text lives here: issue #53 reported a model announcing actions instead of
// taking them, from a deployment whose custom prompt had replaced the template
// that carried the instruction.
const actGuidance = `IMPORTANT: Always act by CALLING the available tools. Never narrate or describe a tool action (like reading files or "exploring") without actually invoking the corresponding tool — describing an action does not perform it.`

// toolGuidance renders the guidance appended to every system prompt, after the
// skills index and before the user's prompt fragments.
//
// It deliberately does NOT live in config.defaultPrompt. A `prompt:` in the
// user's config REPLACES that template outright, so anything written there
// reaches only users who never customized theirs — which excludes exactly the
// config-as-source-of-truth deployments most likely to hit the problem this
// text exists to fix (issue #53: the model shelling out to `sed -n '95,115p'`
// because nothing told it that read, glob and grep exist).
//
// Placed before prompt_fragments so a user's own fragment, which comes later,
// can still countermand any of it.
//
// builtinTools is Config.BuiltinTools, an ALLOWLIST: empty means every tool is
// exposed, non-empty means only the named ones are (see chat.Session.toolEnabled).
// The file-tools text is rendered per tool actually exposed, because a config
// like `builtin_tools: [bash]` would otherwise be told to prefer five tools it
// does not have and to avoid the shell commands that were its only remaining
// way to read a file. That knob exists to trim the prompt for small local
// models — the same audience this text is written for.
//
// Every factual claim below is checked against mcp/filesystem.go. types cannot
// import mcp (import cycle), so the two are kept in step by hand: if you change
// readFile, editFile or grepFiles, re-read this text.
//
// This is prose the model reads, not documentation. Keep it as flowing
// sentences: bullet lists and headings measurably shift how weaker local
// models weight instructions, and this text exists for weak local models.
func toolGuidance(builtinTools []string) string {
	read := toolExposed(builtinTools, "read")
	write := toolExposed(builtinTools, "write")
	edit := toolExposed(builtinTools, "edit")
	glob := toolExposed(builtinTools, "glob")
	grep := toolExposed(builtinTools, "grep")

	paragraphs := []string{actGuidance}

	// Which tools to name, and which shell commands to steer away from. A shell
	// command is only worth banning when the tool that replaces it is exposed.
	var fileTools, clauses, shellCmds []string
	for _, t := range []struct {
		on   bool
		name string
	}{{read, "read"}, {write, "write"}, {edit, "edit"}} {
		if t.on {
			fileTools = append(fileTools, t.name)
		}
	}
	if len(fileTools) > 0 {
		clauses = append(clauses, joinList(fileTools, "and")+" for files")
	}
	if glob {
		clauses = append(clauses, "glob to find files by name")
	}
	if grep {
		clauses = append(clauses, "grep to search file contents")
	}
	if read {
		shellCmds = append(shellCmds, "cat", "sed", "head", "tail")
	}
	if grep {
		shellCmds = append(shellCmds, "shell grep")
	}

	// ls and find are deliberately absent from that list: glob filters
	// directories out of its results (mcp/filesystem.go), so no dedicated tool
	// lists a directory or reveals subdirectories, and banning them would leave
	// the model no way to see what a tree contains.
	if len(clauses) > 0 {
		p := "Prefer the dedicated tools over shell equivalents: " + strings.Join(clauses, ", ") + "."
		if len(shellCmds) > 0 {
			p += " Do not use " + joinList(shellCmds, "or") +
				" for these — the dedicated tools are faster and return structured output."
		}
		paragraphs = append(paragraphs, p)
	}

	if read {
		paragraphs = append(paragraphs, "read returns the whole file by default. Read a file once, in full, rather than requesting line ranges and re-reading it; offset and limit are there for a file too large to read in one call. Do not re-read a file you have already read in this conversation unless you have changed it.")
	}

	if grep {
		paragraphs = append(paragraphs, "grep returns at most 50 matches, so a result that reaches 50 may be incomplete — treat it as a sample rather than every occurrence, and narrow the pattern or the search path to see the rest.")
	}

	// Only worth saying when a read can go stale, i.e. read plus a mutator.
	var mutators []string
	if edit {
		mutators = append(mutators, "edit")
	}
	if write {
		mutators = append(mutators, "write")
	}
	if read && len(mutators) > 0 {
		paragraphs = append(paragraphs, "After you "+joinList(mutators, "or")+" a file, any earlier read of it is out of date. Re-read it before reasoning about its current contents.")
	}

	if edit {
		seenBy := "read"
		if write {
			seenBy = "read or written"
		}
		paragraphs = append(paragraphs, "edit requires that you have "+seenBy+" the file first, and it replaces the old string only when that string appears exactly once in the file. Include enough surrounding context to make it unique, or pass all=true to replace every occurrence.")
	}

	return strings.Join(paragraphs, "\n\n")
}

// toolExposed reports whether a built-in tool reaches the model, mirroring
// chat.Session.toolEnabled: builtinTools is an allowlist, and an empty one means
// every tool is exposed rather than none.
func toolExposed(builtinTools []string, name string) bool {
	if len(builtinTools) == 0 {
		return true
	}
	for _, t := range builtinTools {
		if t == name {
			return true
		}
	}
	return false
}

// joinList renders items as prose: "a", "a and b", "a, b and c" (or "or").
func joinList(items []string, conj string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + conj + " " + items[len(items)-1]
}
