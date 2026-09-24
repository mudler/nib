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
	index := toolExposed(builtinTools, "index")
	tree := toolExposed(builtinTools, "tree")
	repoMap := toolExposed(builtinTools, "repo_map")
	astGrep := toolExposed(builtinTools, "ast_grep")

	paragraphs := []string{actGuidance}

	// Navigation strategy: orient with tree first, then narrow with glob/grep,
	// then index for large files, then read specific ranges. Only when at
	// least 3 of the navigation tools are exposed.
	navCount := 0
	for _, on := range []bool{tree, index, glob, grep, read} {
		if on {
			navCount++
		}
	}
	if navCount >= 3 {
		paragraphs = append(paragraphs,
			"When you need to find something in the codebase, work from the top down: call tree on a directory you have not yet looked at to see its layout, then glob for files by name or grep for content you know is there. For a large source file, index it first to get its outline, then read only the lines you need. Do not start with grep when you do not know where to look — orient yourself with tree first.")
	}

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

	if tree {
		paragraphs = append(paragraphs,
			"tree renders a shallow directory listing (two levels by default, "+
				"twelve entries per directory) so you can see the layout of a path. "+
				"Call it on a directory you have not yet looked at, to find where things are, "+
				"before grepping or reading. It hides build output and VCS metadata; "+
				"use glob when you need every match for a pattern, and read or index for a file's contents.")
	}

	// index comes before read because it qualifies it. The read paragraph
	// used to say "read a file once, in full" with no exception, and since it
	// came last and spoke more firmly, the model never called index. Telling
	// it to index every unseen file overcorrected: it indexed instead of
	// reading. index is for large files, where it saves a full read.
	if index {
		paragraphs = append(paragraphs, "index returns a compact outline of a source file — imports, types, functions, and their line ranges — for a fraction of what reading it costs. Before reading a source file you suspect is large (over 200 lines), call index first: the outline tells you which lines to read, and you can then read only those ranges with offset and limit. For a file of ordinary size, read it directly without an index call.")
	}

	if repoMap {
		paragraphs = append(paragraphs,
			"repo_map returns a bird's-eye map of the whole codebase: a token-budgeted tree of the definitions "+
				"(types, functions, methods, classes) in every indexable file, each with its file and starting line. "+
				"Use it once, early, to learn which file owns a symbol without grepping, then read or index that file for the details. "+
				"It costs a fraction of reading every file, and when it would exceed its budget it drops the files with the fewest definitions and notes how many it omitted. "+
				"Do not call repo_map on a single file you already know; that is what index is for.")
	}

	if astGrep {
		paragraphs = append(paragraphs,
			"ast_grep searches code by structure, not text: pass an AST pattern with "+
				"metavariables ($NAME captures a node, $_ matches any single node, $$$NAME captures zero or more) "+
				"and it finds every match across the codebase. Use it when grep matches too much or too little "+
				"because the same identifier appears in different contexts. Requires the ast-grep binary on PATH.")
	}

	if read {
		p := "read returns the whole file by default. "
		if index {
			p += "When an outline shows you need only one function or type of a large file, read only the lines it gave, with offset and limit. Otherwise read a file once, in full, rather than requesting line ranges and re-reading it."
		} else {
			p += "Read a file once, in full, rather than requesting line ranges and re-reading it; offset and limit are there for a file too large to read in one call."
		}
		p += " A source file too large to return whole comes back as its outline instead; read the lines you need from it with offset and limit."
		p += " Do not re-read a file you have already read in this conversation unless you have changed it."
		paragraphs = append(paragraphs, p)
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

	if toolExposed(builtinTools, "todo_write") {
		paragraphs = append(paragraphs, "Use the todo_write tool to plan multi-step tasks (3+ steps) before starting work. Send the COMPLETE todo list on every call — it replaces the entire list, not a delta. Skip it for trivial tasks. Mark an item in_progress when you begin it and completed when done. Keep exactly one item in_progress at a time. Update the list after EACH completed step, not just at the end.")
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
