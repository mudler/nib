# Cogito Sub-Agents & Background Integration — Design

**Date:** 2026-05-31
**Status:** Approved (pending spec review)
**Repos:** `wiz` (this repo) + `cogito` (`~/_git/cogito`)

## Goal

Update wiz to a newer cogito and bring wiz's agent capabilities to **Claude-Code-style
parity**: named sub-agent types, background execution, per-agent model selection,
continue/resume of agents, and a TUI job-control layer (Ctrl+B to background a running
foreground job). cogito gains the primitives; wiz supplies policy and UI. The two repos
are developed together — cogito changes land on a branch/PR, and wiz points its `go.mod`
at that branch until it is tagged.

## Decisions (locked during brainstorming)

1. **cogito dep:** develop against a cogito **branch/PR** (wiz `go.mod` pseudo-version points
   to the branch); drop to a tagged release once merged.
2. **Force reasoning:** keep the existing `force_reasoning` config field, **default off**
   (current behavior). Do **not** add force-reasoning-tool. No new reasoning surface.
3. **Agent spawning:** **always on**, no enable flag. `EnableAgentSpawning` is always passed.
4. **Claude-Code parity features (all in):** tool-approval propagation, named agent types,
   per-agent model override, continue/resume (**both** completed-agent resume and live
   running-agent injection).
5. **Agent types:** **hardcoded defaults in wiz**, overridable/extendable via a YAML
   `agents:` block in wiz config.
6. **Sub-agent LLM:** share the parent LLM by default; per-agent **model override** resolves
   through a wiz-supplied LLM factory (same base URL / API key, different model name).
7. **Job control (Ctrl+B):** **v1 covers sub-agents only.** Backgrounding a raw shell
   *command* (async/detachable tool execution in cogito) is a **documented follow-up**
   (see "Out of scope / follow-up").

## Background — current state

- wiz (`v0.8.1` cogito) wires `cogito.ExecuteTools` / `ExecutePlan` in `chat/session.go`,
  with `OnToolCall` approval, plan mode, reviewer LLM, and reasoning/status callbacks.
- Local cogito (`~/_git/cogito`, `v0.9.4+14`) already has the sub-agent machinery
  (`agent.go`: `spawn_agent` / `check_agent` / `get_agent_result`, `AgentManager`,
  `AgentState`, `WithAgentLLM`, `WithAgentCompletionCallback`). It is essentially
  `v0.10.0` minus `WithAgentCompletionFormatter`.
- Confirmed gaps vs Claude Code (grounded in cogito source):
  - `ToolChoice` / `SessionState` carry **no agent identity**.
  - Parent `WithToolCallBack` and MCP sessions are **not** propagated to sub-agents
    (`tools.go:1130` propagates only Iterations/MaxAttempts/MaxRetries) — so sub-agent
    tool calls currently bypass wiz's approval gate.
  - No named agent types / system prompts; spawn takes a bare task prompt.
  - `WithAgentLLM` is global, not per-spawn.
  - Foreground spawns run **synchronously** (`ExecuteTools` blocks) — not detachable.
  - Message injection already exists (`WithMessageInjectionChan` +
    `WithMessageInjectionResultChan`; the loop parks on it) — reusable for resume/inject.

## Architecture

```
wiz (TUI / CLI)
 ├─ hardcoded agent types + YAML override ─────► cogito.WithAgentDefinitions
 ├─ OnToolCall gate (now agent-aware) ◄────────  propagated WithToolCallBack + SessionState.AgentID
 ├─ agent LLM factory (parent base/key + model) ─► cogito.WithAgentLLMFactory
 ├─ jobs panel + Ctrl+B detach ◄───────────────► AgentManager.Detach / Inject / List
 └─ lifecycle callbacks ◄──────────────────────  WithAgentCompletionCallback / Formatter
```

cogito owns the primitives (spawn/background/poll/result/cancel/detach/inject and the
agent-definition registry). wiz owns policy (which agent types exist, approval, model
resolution) and presentation (TUI jobs panel, CLI notifications).

## Components & Tests (TDD breakdown)

Each component is built test-first. Tests below are the behavior contracts to write before
implementation.

### cogito (single branch → PR)

**C1 — Approval + MCP propagation**
- *Responsibility:* propagate parent `WithToolCallBack` and MCP client sessions into
  sub-agent options; add `AgentID string` to `SessionState` so the callback knows which
  agent is asking (empty = root).
- *Interface:* internal change in `spawnAgentRunner` building `subOpts`; new
  `SessionState.AgentID` field.
- *Tests:* sub-agent tool call invokes the parent callback; `AgentID` populated for sub,
  empty for root; `Approved:false` / `Skip` honored inside a sub-agent; MCP tools usable
  inside a sub-agent.

**C2 — Agent definitions (named types)**
- *Responsibility:* `AgentDefinition{Name, Description, SystemPrompt, Tools, Model}`,
  `WithAgentDefinitions(...defs)`; `spawn_agent` gains an `agent_type` argument; the chosen
  type seeds the sub-fragment's system prompt and restricts tools to the type's list.
- *Tests:* a known type resolves its system prompt + tool subset into the sub-fragment;
  unknown `agent_type` returns a clean error (no panic); empty `agent_type` preserves
  current/legacy behavior; the spawn tool's description enumerates available types.

**C3 — Per-agent model override**
- *Responsibility:* `WithAgentLLMFactory(func(model string) LLM)`; resolution order for a
  spawn's model = spawn `model` arg → definition `Model` → factory; fall back to parent LLM
  when none set.
- *Tests:* factory called with the resolved model name; spawn-arg model beats definition
  model; no factory + no model → parent LLM used.

**C4 — Resume / inject**
- *Responsibility:* per-agent injection channel; `AgentManager.Inject(id, msg)` for a live
  running agent; resume-completed (append message to the agent's stored `Fragment` and
  re-run); LLM-facing `send_agent_message` tool covering both.
- *Tests:* injected message reaches a running sub-agent's loop (parks then resumes);
  resuming a completed agent re-runs with prior `Fragment` context and updates `Result`;
  injecting to an unknown id errors cleanly.

**C5 — Detach (job control primitive)**
- *Responsibility:* foreground spawn becomes interruptible — its `Run` selects on
  `agent.done` / a detach signal / `ctx.Done()`; `AgentManager.Detach(id)` promotes a
  running foreground agent to background and returns its ID immediately.
- *Tests:* `Detach` returns the ID without waiting for completion; the goroutine keeps
  running after detach; status transitions foreground→running/background; completion still
  fires the completion callback + injection.

### wiz (this repo)

**W1 — Dependency bump**
- *Responsibility:* point `go.mod` at the cogito branch pseudo-version; build green.
- *Tests:* `go build ./...` passes; `force_reasoning` still defaults off and no
  reasoning-tool option is wired (assert in a config/session test).

**W2 — Agent registry (defaults + YAML override)**
- *Responsibility:* hardcoded default agent types in wiz; merge with an `agents:` block in
  `types.Config` (override by name, add new). Convert to `[]cogito.AgentDefinition`.
- *Tests:* defaults present when no config; YAML entry with an existing name overrides;
  new name adds; referenced tool names validated against known tools.

**W3 — Session wiring**
- *Responsibility:* always pass `EnableAgentSpawning`; construct a shared `AgentManager`;
  supply `WithAgentLLMFactory` that builds `cogito.NewOpenAILLM(model, parentKey, parentURL)`;
  pass `WithAgentDefinitions`, `WithAgentCompletionCallback`, `WithAgentCompletionFormatter`.
- *Tests:* options assembled correctly; factory builds an OpenAI LLM with parent base/key
  and the requested model; `OnToolCall` receives `AgentID` for sub-agent calls.

**W4 — Lifecycle callbacks**
- *Responsibility:* extend `chat.Callbacks` with an agent-lifecycle hook
  (`OnAgentEvent` covering spawn/update/complete/fail) fed by the completion callback and
  sub-agent stream events; thread `AgentID` into `ToolCallRequest`.
- *Tests:* events emitted on spawn/complete/fail; `ToolCallRequest` carries the agent id;
  no events when no agents spawned.

**W5 — TUI jobs panel + Ctrl+B**
- *Responsibility:* render a jobs/agents panel (running/done, task, status) from
  `AgentManager.List`; bind Ctrl+B to background the current foreground job via
  `AgentManager.Detach`; show completion toasts.
- *Tests:* Ctrl+B on a running foreground sub-agent calls `Detach`; panel reflects manager
  state; completion toast appears on the completion event.

**W6 — CLI notifications**
- *Responsibility:* in CLI mode, print agent spawn/complete/fail lines.
- *Tests:* lifecycle lines emitted in CLI mode for spawn and completion.

## Sequencing

1. **cogito branch:** C1 (safety foundation) → C2 → C3 → C4 → C5. Open PR.
2. **wiz:** W1 → W2 → W3 → W4 (point `go.mod` at the cogito branch), then W5 (TUI) → W6 (CLI).

## Data flow — sub-agent tool approval (the safety-critical path)

1. LLM calls `spawn_agent(agent_type, task, background, model?)`.
2. cogito resolves the definition (system prompt + tools), resolves the LLM via factory,
   builds `subOpts` **including** the propagated `WithToolCallBack` and MCP sessions (C1).
3. Sub-agent selects a tool → cogito calls wiz's `OnToolCall` with
   `SessionState.AgentID` set → wiz UI prompts (labeled with the agent id) or auto-allows
   per the session allow-list.
4. Background completion injects a message back into the parent loop (existing mechanism),
   and `WithAgentCompletionCallback` fires the wiz lifecycle event for UI.

## Error handling

- Unknown `agent_type` / unknown agent id on inject/resume → tool returns an error string
  the parent LLM can read; no panic.
- Sub-agent failure → `AgentStatusFailed`, error surfaced via completion message + event.
- Approval rejection inside a sub-agent → same `ToolCallDecision` semantics as the root.

## Out of scope / follow-up

- **Backgrounding a raw shell command via Ctrl+B** (async/detachable *tool* execution in
  cogito + a command job registry). Requires changing cogito's synchronous in-loop tool
  execution model. Tracked as a separate follow-up spec.
- **Worktree / filesystem isolation** for sub-agents (harness concern, not cogito).

## Risks

- C1 changes a security-sensitive path (sub-agent tool calls). Must have explicit tests
  proving the approval gate fires for sub-agents before wiz ships always-on spawning.
- C4 live-inject touches the loop's parking logic; reuse the existing injection channel
  machinery rather than inventing a parallel path.
- C5 changes the foreground spawn from sync to selectable — ensure non-detached foreground
  behavior is byte-for-byte unchanged when Ctrl+B is never pressed.
