package types

// toolGuidance is appended to every system prompt, after the skills index and
// before the user's prompt fragments.
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
// The leading act-don't-narrate sentence moved here from config.defaultPrompt
// for the same reason the rest of this text lives here: issue #53 reported a
// model announcing actions instead of taking them, from a deployment whose
// custom prompt had replaced the template that carried the instruction.
//
// Every factual claim below is checked against mcp/filesystem.go. types cannot
// import mcp (import cycle), so the two are kept in step by hand: if you change
// readFile, editFile or grepFiles, re-read this text. A prompt that confidently
// states something false is worse than no prompt — the model plans around the
// claim and burns a turn on the error.
//
// This is prose the model reads, not documentation. Keep it as flowing
// sentences: bullet lists and headings measurably shift how weaker local
// models weight instructions, and this text exists for weak local models.
const toolGuidance = `IMPORTANT: Always act by CALLING the available tools. Never narrate or describe a tool action (like reading files or "exploring") without actually invoking the corresponding tool — describing an action does not perform it.

Prefer the dedicated tools over shell equivalents: read, write and edit for files, glob to find files by name, grep to search file contents. Do not use cat, sed, head, tail or shell grep for these — the dedicated tools are faster and return structured output.

read returns the whole file by default. Read a file once, in full, rather than requesting line ranges and re-reading it; offset and limit are there for a file too large to read in one call. Do not re-read a file you have already read in this conversation unless you have changed it.

grep returns at most 50 matches, so a result that reaches 50 may be incomplete — treat it as a sample rather than every occurrence, and narrow the pattern or the search path to see the rest.

After you edit or write a file, any earlier read of it is out of date. Re-read it before reasoning about its current contents.

edit requires that you have read or written the file first, and it replaces the old string only when that string appears exactly once in the file. Include enough surrounding context to make it unique, or pass all=true to replace every occurrence.`
