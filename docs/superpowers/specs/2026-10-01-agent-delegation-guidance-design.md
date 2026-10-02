# Agent delegation guidance

## Scope and decision

Improve model-facing instructions for the existing agent tools. The root owns
orchestration and delegates bounded, self-contained tasks; children perform their
assigned work and return evidence, blockers, and follow-ups to the root.

This is prompt and documentation work, not a new execution model. No changes to
permissions, tool filtering, agent lifecycle, scheduling, approval, dependencies,
or tool schemas are proposed. Tool counting and MCP permission fixes are excluded.
This document is the only deliverable of the design change; implementation comes
later.

Use harness-injected root and child guidance, including custom personas. Updating
only built-in personas would miss overrides and untyped children. Editing Cogito
or adding enforcement would exceed scope. The harness approach covers the existing
paths without changing their execution semantics.

## Current seams and constraints

Inspected at nib `d781ca1`, with Cogito `dd8ba4df7d2a`:

- `chat/session.go`: `Reload` assembles the root prompt; `ensureSystemPrompt`
  installs/replaces it in the conversation. `toolOptions` registers the native
  agent family through `EnableAgentSpawning` when `toolEnabled("spawn_agent")`
  is true. That family includes `spawn_agent`, `check_agent`,
  `get_agent_result`, and `send_agent_message`. `agent_logs` is separately gated.
- `toCogitoDefinitions` copies configured personas, including custom overrides.
  Cogito seeds a typed child with its persona and the task, not the root
  conversation. An untyped child receives the task without a persona.
- `SendMessage` supplies the default child client through `WithAgentLLM`;
  `newAgentLLM` supplies model/persona overrides through `WithAgentLLMFactory`.
  Cogito uses the default child client for `send_agent_message` resumes.
- `config/agents.go` defines overridable `general`, `explore`, and `plan` personas.
  Their descriptions express intended behavior, not security boundaries.
- Cogito normally excludes its native agent-management tools from children.
  Its explicit requested-tool branch and inherited options are not a complete
  isolation boundary. Do not describe this as comprehensive enforcement.

## Guidance contract

### Root

The injected guidance must convey these rules concisely:

1. Keep the overall plan, integration, acceptance checks, and final answer at the
   root. Delegate only work with a clear independent outcome; do small or tightly
   coupled work directly. Children cannot dispatch implementation or review
   agents through the normal native tools: do not assign them orchestration.
   Collect setup/planning results, then dispatch implementation and independent
   review from the root when the workflow requires them. Parallelize independent
   tasks, not conflicting edits.
2. A child does not inherit this conversation. Its task brief must include the
   objective, relevant context, repository and working-directory/worktree paths,
   allowed scope and constraints, acceptance checks, and expected report. Specify
   ownership of files/worktrees when concurrent edits are possible; spawning does
   not create isolation. Include only context needed for the task.
3. Choose an advertised persona suitable for the task, but do not treat persona
   labels or the `tools` argument as a sandbox or guaranteed read-only mode.
4. `background=false` waits for the result; use it when the result is the next
   dependency. `background=true` returns an ID immediately; use it when useful
   independent work can continue. Keep that ID. A started job is not completed
   work; do not duplicate it because it has not replied yet.
5. Use `send_agent_message` with the existing ID for clarification, correction,
   or related follow-up. It injects into running work and resumes finished work
   from stored context. Spawn again only for a genuinely separate task or an
   explicitly justified restart. Report missing context or tool failures rather
   than silently assuming delivery or success.
6. `check_agent` reports lifecycle status; `agent_logs`, when available, shows
   recent activity; `get_agent_result` retrieves the final result and supports
   `wait=true`. Logs and status are not substitutes for the result. Avoid tight
   polling; continue independent work or wait for the needed result.
7. Review the returned evidence against acceptance criteria before integrating
   it. Report files/artifacts, checks and outcomes, failures, and remaining work.
   Distinguish child-reported checks from checks the root independently verified.
   Neither a successful spawn nor a completed status proves task correctness.

### Child

Every child request must receive a short harness-owned instruction block:

- You are a child working on the supplied task, not the overall orchestrator.
  Follow the brief and applicable repository instructions; do not assume access
  to the root conversation or invent missing requirements.
- Do not spawn or manage other agents. Normal native child tools do not provide
  that orchestration role. Do not run nested nib/other agent CLIs or use another
  tool as a workaround. Return needed delegation or follow-up work to the parent.
- Stay within the assigned scope. Report missing context, conflicts, unavailable
  capabilities, and blockers to the parent instead of expanding the task.
- Return a concise outcome with relevant paths/artifacts, checks actually run
  and their results, uncertainty, blockers, and suggested follow-ups. Do not claim
  completion without evidence. Read-only/planning roles are instructions, not
  promises that mutation is technically impossible.

These are model-facing rules. They do not establish behavioral or security
 guarantees, even when a custom persona conflicts with them.

## Precise injection design

### Root prompt assembly

Add a dedicated delegation-guidance builder in `chat`, alongside the existing
`agentModelGuidance` pattern. Append its output as harness-owned text during
`Reload`, after configured prompt/skills and before identity suffixes. Preserve
custom prompt content and compose from source text on each reload, not from the
previous composed prompt. Ensure an empty configured prompt does not suppress
applicable harness guidance; avoid accumulating suffixes across reloads.

Emit the root delegation block only when native spawning is registered. Use the
same existing `toolEnabled("spawn_agent")` decision as `toolOptions`, not a new
interpretation of per-tool permissions. Mention the four bundled native tools
under that condition. Include the `agent_logs` clause only when its separate
registration is enabled. Do not add scheduler instructions or advertise tools
based on persona names, configured models, or similarly named MCP tools.
Remove the unconditional delegation advice from the built-in default prompt in
`config/config.go`, so disabling spawning does not leave contradictory built-in
instructions. Preserve user-authored custom prompt text verbatim; the harness
cannot guarantee that custom text never mentions unavailable tools. The disabled
case tests therefore assert absence of harness-owned and built-in delegation
advice, not arbitrary user-authored text. Keep advertised persona information in
the capability-gated root guidance.
`ensureSystemPrompt` remains the existing installation/replacement mechanism.

### Child request boundary

Use a small child-only LLM decorator in `chat`, separate from retry policy. Wrap
both the default `WithAgentLLM` client in `SendMessage` and the client returned by
`newAgentLLM`, including its fallback. This covers untyped children, custom
personas (including empty prompts), model overrides, and resumed children without
changing Cogito or synthesizing agent definitions.

For each outgoing child request, copy the messages and append one clearly marked
system instruction containing the child contract to the existing system prompt,
or prepend a system message when none exists. Preserve the persona and task
verbatim. Make the transformation idempotent and do not mutate shared requests
or persisted fragments. Apply the same transformation to completion, streaming,
and `Ask` paths; preserve optional streaming support rather than advertising it
for a non-streaming client. Leave tools, parameters, results, retries, and
lifecycle untouched. The decorator is prompt assembly only, not a tool filter.
Do not attach it to the root or Cogito's internal reviewer client. A review
sub-agent spawned through the normal child path still receives this guidance.
No persona-file rewrite is needed to make this guidance apply.

## Documentation and verification

During implementation, update README's “Sub-agents & background jobs” section
with the root/child boundary, self-contained briefs, background/foreground choice,
existing-ID follow-ups, evidence expectations, and non-sandbox caveat. README is
the user-facing source of truth. Run `make sync-readme` and commit
`README.md` with `selfdoc/README.md`; do not separately author embedded guidance.

Use a deterministic fake backend following `chat/agent_input_test.go`. Capture
actual outbound messages and tool schemas, not only helper strings:

- Root requests advertise and describe the bundled tools when spawning is
  enabled. When disabled, neither the bundle nor delegation instructions appear;
  separately exercise enabled/disabled `agent_logs`. Custom and empty prompts,
  repeated turns, and reloads retain the correct guidance exactly once.
- Script typed default, custom/overridden, empty-persona, untyped, and
  model-override children. Each receives the child block and task, preserves any
  persona, and excludes a sentinel placed only in the root conversation.
- Exercise foreground and background spawn, live follow-up, and finished-agent
  resume. Capture the resulting child requests to verify guidance survives those
  paths. Cover streaming and non-streaming clients, the `Ask` adapter, and
  idempotence without mutating the source request.
- Capture normal child schemas and assert native spawn/check/result/message
  tools are absent on the ordinary path. Check that this change preserves the
  existing schemas; do not generalize that assertion to explicit-tool overrides,
  inherited MCP capabilities, shell commands, or all configurations.
- Assert the guidance text contains the brief, no-nesting/no-CLI-workaround,
  blockers, existing-ID follow-up, and evidence requirements. Run targeted chat
  tests and `go test ./...`, including embedded-documentation checks.

These tests establish what the harness sends and which ordinary native schemas
it exposes. A scripted backend cannot prove that real models delegate wisely,
obey the guidance, avoid nested CLIs, or produce correct work. No such guarantee
is an acceptance criterion.

## Design review

Scope remains instructions, their delivery, documentation, and deterministic
coverage. The normal native-tool boundary is explicitly distinguished from full
enforcement. The child-client decorator is chosen over persona-only injection to
cover generic and resumed children. Follow-up uses the actual
`send_agent_message` tool, not a hypothetical `followup_agent`. No permissions,
runtime lifecycle changes, unrelated fixes, or implementation are included here.
