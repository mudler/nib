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
// This is prose the model reads, not documentation. Keep it as flowing
// sentences: bullet lists and headings measurably shift how weaker local
// models weight instructions, and this text exists for weak local models.
const toolGuidance = `IMPORTANT: Always act by CALLING the available tools. Never narrate or describe a tool action (like reading files or "exploring") without actually invoking the corresponding tool — describing an action does not perform it.

Prefer the dedicated tools over shell equivalents: read, write and edit for files, glob to find files by name, grep to search file contents. Do not use cat, sed, head, tail, ls, find or shell grep for these — the dedicated tools are faster and return structured output.

read returns the whole file by default. Read a file once, in full, rather than requesting line ranges and re-reading it. Do not re-read a file you have already read in this conversation unless you have changed it.

After you edit or write a file, any earlier read of it is out of date. Re-read it before reasoning about its current contents.

edit requires that you have read the file first, and replaces one exact occurrence of the old string unless you pass all=true.`
