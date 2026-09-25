# Plan: AGENTS.md post-compaction nudge, boot screen LSP + context files, sub-agent log label

**Date:** 2026-09-26
**Status:** Approved
**Branch:** `sdd/agents-md-enforcement-boot-lsp`

## Context

Three independent improvements:

1. After end-of-turn compaction, the model's earlier `read` of AGENTS.md is in
   the compacted-away head. The system prompt still says "read it before acting"
   but the model may start tool-calling without re-reading. We add a one-time
   soft nudge (user-role message) on the first tool call after compaction.

2. The boot screen shows no LSP or context-file information. LSP detection
   results go only to `xlog.Info`. Context-file detection is baked into the
   system prompt but invisible in the boot log.

3. The sub-agent logs viewer (ctrl+o) labels the task text as `prompt:`, which
   is misleading — it's the task/prompt given to the sub-agent, not a shell
   prompt. Rename to `task:`.

## Task 1: Add LSP entry to boot screen

**File:** `tui/boot.go`

Add a `lsp.detect` entry to `bootScript()` after `mcp.connect` (delay 460ms,
shifting `tools.mount` and later entries later by 60ms each).

Add an `lsp.detect` case to `bootDetail()` that reads detected servers from the
session. The session needs a method to expose detected server lines.

**File:** `chat/session.go`

Add a `DetectedLSPServers() []string` method on `*Session` that returns
human-readable lines (e.g. `"go: gopls serve"`) from the LSP manager's configs.
If no manager or no configs, returns nil.

**Verify:** Boot screen shows `lsp.detect` line with detected servers or "none".

## Task 2: Add context files entry to boot screen

**File:** `tui/boot.go`

Add a `context.files` entry to `bootScript()` after `skills.index` (delay 580ms,
shifting `session` and `memory` later by 60ms each).

Add a `context.files` case to `bootDetail()` that calls
`types.DetectContextFiles(workingDir)` and renders the result.

**File:** `types/config.go`

Export `DetectContextFiles` (rename from `detectContextFiles`, keep wrapper for
back-compat if needed). Already returns `[]string` of detected file names.

**Verify:** Boot screen shows `context.files` line with "AGENTS.md" or "none".

## Task 3: Post-compaction AGENTS.md soft nudge

**Files:** `chat/session.go`, `chat/compact.go`

### Mechanism

Add a `contextFilesRead` bool field on `Session` (guarded by `historyMu`).
After end-of-turn `compactHistory` succeeds, reset it to false.

In `SendMessage`'s tool-call processing loop (the `ExecuteTools` path), before
executing the first tool call in a turn:
- If `contextFilesRead` is false AND context files were detected (check via
  `types.DetectContextFiles`), inject a user-role nudge message into the
  fragment: "You haven't re-read AGENTS.md since the conversation was compacted.
  Read it before continuing with tool calls."
- Set `contextFilesRead` to true immediately (nudge once per compaction cycle).

Watch `read` tool calls: when the path argument matches a detected context
file, set `contextFilesRead` to true (so no nudge if the model already read it).

**Only end-of-turn compaction triggers this** — mid-turn compaction
(`turnCompactor`) does not reset the flag.

### Where to hook

The nudge injection goes in `SendMessage` right before the `ExecuteTools` call
(session.go ~line 1900), guarded by a check on `s.contextFilesRead` and
`s.compactionHappened` (a second bool set at the end of `compactHistory`).

The read-tracking goes in the tool-result processing path where tool names and
arguments are available.

**Verify:** Unit test: after `compactHistory`, first tool call gets a nudge
message in the fragment. After a `read` of AGENTS.md, no nudge.

## Task 4: Rename sub-agent log label from "prompt" to "task"

**File:** `tui/jobskill.go`

Line 45: change `"prompt:\n"` to `"task:\n"`.

**File:** `tui/jobskill_test.go`

Update `TestJobActivityTailPrependsPrompt` to expect `"task:"` instead of
`"prompt:"`. Rename test to `TestJobActivityTailPrependsTask`.

**Verify:** `go test ./tui/...` passes.
