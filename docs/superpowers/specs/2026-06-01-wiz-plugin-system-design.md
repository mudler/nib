# Wiz Plugin System — Design

**Date:** 2026-06-01
**Status:** Draft (pending spec review)
**Repo:** `wiz`

## Goal

Give wiz a **Claude-Code-style plugin system**: a single installable unit (a git repo
with a manifest) that can contribute MCP servers, sub-agent types, system-prompt
fragments, skills, slash commands, and lifecycle hooks. Plugins are discovered, merged
into the runtime config, and exercised through the existing agent loop with no change to
the "single binary, zero-dependency" ethos.

The acceptance gate is an **example plugin** that exercises every contribution type, plus
an **end-to-end harness** that installs it and drives wiz through each feature.

wiz is additionally **compatible with Claude Code plugins**: the same loader installs and
runs an unmodified Claude Code plugin (and Claude marketplace), mapping its layout into
wiz's internal contribution model with documented, best-effort semantics.

## Decisions (locked during brainstorming)

1. **Scope:** full Claude-Code parity, but staged. One architecture spec, a phased
   implementation roadmap; each phase becomes its own spec-driven plan.
2. **Packaging:** a plugin is a **git repo**. Two native-supported formats, detected by the
   loader and feeding one format-agnostic internal contribution model:
   (a) a concise **`wiz-plugin.yaml`** manifest at the root (YAML-native, single-file), and
   (b) the full **Claude Code `.claude-plugin/` layout**. Both via `wiz plugin install <git-url>`.
3. **Claude Code compatibility:** an unmodified Claude Code plugin/marketplace installs and
   runs. The `.claude-plugin/` adapter maps `plugin.json`, `skills/`, `commands/`, `agents/`,
   `hooks/hooks.json`, and `.mcp.json` into the internal model, with a tool-name alias map,
   command-syntax translation (`$ARGUMENTS`/`$n`), `${CLAUDE_PLUGIN_ROOT}` hook env, and
   event-name mapping. Unmappable items are skipped with a warning (documented gaps).
4. **Sub-agents are already done.** The merged subagent subsystem (`AgentTypeConfig`,
   `MergeAgentTypes`, full cogito runtime, TUI jobs footer) is reused as-is. Plugins
   contribute `agents:` entries; no new subagent runtime is built here.
5. **Merge convention:** mirror the existing `MergeAgentTypes` — merge named items by name,
   user config always wins, plugin-vs-plugin clash = last-loaded wins **with a warning**;
   hooks and prompt-fragments accumulate.
6. **Skills:** NOT cogito guidelines. A skill is indexed in the system prompt
   (name + description); a `load_skill` tool lets the agent read a skill body on demand
   (progressive disclosure); `/skill <name>` eagerly injects a skill body into the system
   prompt for the session.
7. **Hooks:** shell commands bound to events, receiving event JSON on stdin and returning a
   JSON decision on stdout. `PreToolUse` reuses the existing `OnToolCall` approve/deny/adjust
   contract.
8. **Security consent point:** `wiz plugin install` prints a contribution summary and
   requires confirmation (`--yes` to skip). Disabled plugins contribute nothing. All tool
   calls still pass the existing approval gate at runtime.
9. **Acceptance:** an example plugin + e2e harness driven through wiz **CLI mode** against a
   stub/local LLM, plus installing a real Claude Code plugin to prove compatibility.
10. **Execution:** spec-driven, sub-agent-dispatched on Opus 4.8.

## Background — current state (post `feat: subagent integration (#6)`)

- `types.Config` already carries `MCPServers map[string]MCPServer` and
  `Agents []AgentTypeConfig`. `config.Load()` calls `MergeAgentTypes(cfg.Agents)`.
- The system prompt (`config.defaultPrompt`) is a Go `text/template` that already
  `range`s over `.Config.Agents` to list available sub-agent types — the exact pattern
  skills and prompt-fragments reuse.
- In-memory MCP tools are registered via `mcp/shell.go` (`startBashMCPServer`) and
  `mcp/filesystem.go` (`StartFileSystemMCPServer`) over `mcp.NewInMemoryTransports()` — the
  pattern the `load_skill` tool reuses.
- `chat.Callbacks` exposes `OnStatus, OnReasoning, OnToolCall, OnPlan, OnResponse, OnError,
  OnAgentEvent`. `OnToolCall` returns `ToolCallResponse{Approved, Adjustment, AlwaysAllow}`
  — the contract `PreToolUse` hooks reuse.
- The TUI (`tui/model.go`, `tui/agents.go`) handles special keys and an agents surface;
  there is **no** slash-command surface yet.
- cogito's `WithGuidelines` exists but is **not** used (and, per decision 5, will not be).

## Architecture

```
git repo (plugin) ──install──► ~/.config/wiz/plugins/<name>/   registry: ~/.config/wiz/plugins.yaml
                                     │                             {name, source_url, ref, enabled}
                          ┌──────────┴───────────┐  format detection
                  wiz-plugin.yaml          .claude-plugin/       → both produce the SAME
                   (native adapter)        (Claude adapter)        []Contribution model
config.Load()
  ├─ load user config (highest precedence)
  ├─ discover enabled plugins (registry) → detect format → adapter → []Contribution
  └─ merge contributions into types.Config:
        mcp_servers ──► Config.MCPServers ──────► WithMCPs            (exists)
        agents ───────► MergeAgentTypes ────────► WithAgentDefinitions (exists)
        prompt_fragments ► Config.PromptFragments ► GetPrompt() append (new compose)
        skills ───────► Config.Skills ──┬────────► system-prompt index (new template)
                                        └────────► load_skill in-memory MCP tool (new)
        commands ─────► Config.Commands ────────► TUI slash palette   (new surface)
        hooks ────────► Config.Hooks ───────────► HookDispatcher       (new, via Callbacks)
```

The plugin loader is a pure transform: `(user Config, enabled plugins) → effective Config`.
Everything downstream consumes the effective `Config` exactly as it does today. New runtime
pieces (`load_skill` tool, slash palette, `HookDispatcher`) are the only behavioral
additions; the merge itself touches no runtime.

## Plugin anatomy

A plugin is either **native** (concise `wiz-plugin.yaml`) or **Claude Code layout**
(`.claude-plugin/plugin.json` + per-type directories); the loader detects which. Native:

```
my-plugin/
  wiz-plugin.yaml          # manifest (root)
  prompts/style.md         # prompt fragment bodies
  skills/git-commit.md     # skill instruction bodies
  hooks/guard.sh           # hook scripts
```

Claude Code layout (also natively supported — see "Claude Code plugin compatibility"):

```
my-plugin/
  .claude-plugin/plugin.json   # manifest
  skills/<name>/SKILL.md        # skill (frontmatter + body)
  commands/*.md                 # slash commands
  agents/*.md                   # sub-agent types
  hooks/hooks.json              # hook config
  .mcp.json                     # MCP servers
```

### Manifest schema (`wiz-plugin.yaml`)

```yaml
name: my-plugin
version: 0.1.0
description: Git workflow helpers
wiz_version: ">=0.9.0"        # semver constraint; load fails with a clear error if unmet

mcp_servers:                 # → merged into Config.MCPServers (by server key)
  github: { command: gh-mcp, args: [], env: {} }

agents:                      # → MergeAgentTypes (by name)
  - { name: researcher, description: "...", system_prompt: "...", tools: [bash] }

prompt_fragments:            # → appended to system prompt, in load order (accumulate)
  - "Prefer conventional commits."
  - { file: prompts/style.md }

skills:                      # → system-prompt index + load_skill tool (by name)
  - name: git-commit
    description: when the user wants to create a git commit
    instructions: { file: skills/git-commit.md }   # or `inline: "..."`
    tools: [bash]                                   # optional advisory list

commands:                    # → TUI slash commands (by name)
  - name: review
    description: review the current diff
    prompt: "Review the staged diff. Focus: {{.Args}}"
    agent: explore           # optional: route the turn through this sub-agent type

hooks:                       # → shell command per event (accumulate)
  - { event: PreToolUse, matcher: bash, command: ./hooks/guard.sh }
```

Every block is optional. Relative paths (`file`, `command`) resolve against the plugin's
install directory.

## Install & lifecycle

- **Install dir:** `~/.config/wiz/plugins/<name>/` (the git clone).
- **Registry:** `~/.config/wiz/plugins.yaml` — a list of `{name, source_url, ref, enabled}`.
- **Commands:**

  ```
  wiz plugin install <git-url> [--ref <tag>] [--yes]
  wiz plugin list
  wiz plugin update <name>
  wiz plugin enable|disable <name>
  wiz plugin remove <name>
  ```

  `install` clones, validates the manifest (schema + `wiz_version`), prints a **contribution
  summary** (counts + hook commands + MCP server commands), asks for confirmation unless
  `--yes`, then records the plugin as enabled in the registry.
- **Discovery:** `config.Load()`, after loading user config and applying env overrides,
  enumerates enabled registry entries, parses each manifest, and merges contributions into
  the in-memory `types.Config` before defaults/merges that already run.

### Precedence & merge

Order: **built-in defaults → plugins (registry/install order) → user config (wins)**.

- Named items (`mcp_servers` keys, `agents`, `skills`, `commands`) merge by name; a user
  config entry with the same name overrides the plugin's.
- Plugin-vs-plugin name clash → last-loaded wins, emit a warning to stderr/log.
- `prompt_fragments` and `hooks` **accumulate** (never overwrite).
- This is the same shape as `MergeAgentTypes`, generalized per contribution type.

## Component subsystems

### MCP servers
Plugin `mcp_servers` entries merge into `Config.MCPServers`; the existing transport startup
(`mcp.StartTransports`) and `WithMCPs` wiring consume them unchanged.

### Sub-agents
Plugin `agents` entries flow through the existing `MergeAgentTypes` and
`toCogitoDefinitions` path. No new code beyond including plugin agents in the merge input.

### Prompt fragments
New `Config.PromptFragments []PromptFragment` (`inline` string or `{file}`). `GetPrompt()`
loads each fragment body and appends it after the base prompt, in load order, then runs the
existing `text/template` pass over the whole thing.

### Skills (index + load-tool + `/skill`)
- New `Config.Skills []SkillConfig {Name, Description, Instructions (file|inline), Tools}`.
- **Index:** the prompt template gains a `{{- if .Config.Skills }} Available skills: … {{- end}}`
  block listing `name: description`, plus a line instructing the agent to call `load_skill`
  to read one when relevant.
- **`load_skill` tool:** an in-memory MCP server (pattern of `startBashMCPServer`) exposing
  `load_skill(name string) -> instructions`. It reads from the loaded skills registry and
  returns the skill body; unknown name returns a clean error string.
- **`/skill <name>`** (built-in command, P2): eagerly injects the named skill's body into
  the session system prompt, so the agent has it without a `load_skill` call.

### Commands (TUI slash palette)
- New `Config.Commands []CommandConfig {Name, Description, Prompt, Agent}`.
- Typing `/` at the start of TUI input opens a filterable palette (name + description),
  using the same `tui/model.go` update pattern as the agents surface.
- Selecting `/<name> <args>` expands the command's `prompt` template (`{{.Args}}`,
  `{{.CurrentDirectory}}`) into a user message and sends it. If `agent:` is set, the turn is
  routed through that sub-agent type via the existing spawn path.
- Built-in commands: `/skill` (P2). Plugin/user commands extend the same registry.
- A command name clash with a built-in → built-in wins, warn.

### Hooks (event bus + shell dispatch)
- New `Config.Hooks []HookConfig {Event, Matcher, Command}`.
- Events: `SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, OnAgentEvent, Stop`.
- A `HookDispatcher` fires every hook whose `event` matches (and whose optional `matcher`
  matches the tool name / event subtype), passing event JSON on stdin and reading a JSON
  decision on stdout. Wired **through `chat.Callbacks`**:
  - `PreToolUse` wraps `OnToolCall` — a hook may return `{approved, adjustment, reason}`,
    reusing the `ToolCallResponse` contract (deny short-circuits; approve may auto-allow;
    adjust modifies the call).
  - `PostToolUse` observes the tool result.
  - `UserPromptSubmit` fires at the start of `SendMessage`; `SessionStart`/`Stop` at session
    boundaries; `OnAgentEvent` on sub-agent lifecycle.
- Hook stdout schema (subset honored per event): `{ "approved": bool, "adjustment": string,
  "reason": string, "block": bool }`. Malformed/empty stdout = no decision (pass through).

## Claude Code plugin compatibility

The internal contribution model is format-agnostic. A format-detection step picks an adapter:
a directory with `.claude-plugin/plugin.json` is loaded as a Claude plugin; one with
`wiz-plugin.yaml` as native wiz. (Both may coexist; native wins for overlapping declarations.)

### Structural mapping (Claude → internal model)

| Claude Code path | Internal target | Notes |
|---|---|---|
| `.claude-plugin/plugin.json` | manifest meta | `name`/`version`/`description`/`author` (JSON) |
| `.claude-plugin/marketplace.json` | marketplace index | `plugins[].source` (incl. `{source:github,repo}`) — P6 |
| `skills/<n>/SKILL.md` | `SkillConfig` | frontmatter `name`/`description` → index; body → `load_skill`; sibling files kept on disk |
| `commands/*.md` | `CommandConfig` | frontmatter `description` → desc; body → `prompt` |
| `agents/*.md` | `AgentTypeConfig` | frontmatter `name`/`description`/`tools`/`model` → fields; body → `system_prompt` |
| `hooks/hooks.json` | `[]HookConfig` | `{hooks:{Event:[{matcher,hooks:[{type:command,command}]}]}}` flattened |
| `.mcp.json` (`mcpServers`) | `Config.MCPServers` | same `command`/`args`/`env` shape |

### Semantic mapping (best-effort, documented gaps)

- **Tool namespace:** a built-in alias map translates Claude tool names to wiz tools
  (`Bash→bash`; `Read`/`Edit`/`Write`/`Glob`/`Grep`→ wiz filesystem MCP tools). Unmapped
  names (`Task`, `WebFetch`, …) are dropped from `tools:`/`matcher` with a warning.
- **Command syntax:** `$ARGUMENTS` → the full arg string; `$1..$n` → positional args. Claude's
  `!`bash-exec and `@`file-ref injection are a documented subset (may be unsupported in v1).
  Frontmatter `allowed-tools`/`model` map to the command's tool scope / agent model where
  resolvable; otherwise ignored with a warning.
- **Hook env:** the dispatcher exports `${CLAUDE_PLUGIN_ROOT}` (and `${WIZ_PLUGIN_ROOT}`) set
  to the plugin install dir, so Claude hook commands run unmodified.
- **Hook events:** the six wiz events are mapped from their Claude equivalents; Claude-only
  events (`SubagentStop`, `PreCompact`, `Notification`, …) are skipped with a warning.
- **Model aliases:** Claude `model: opus|haiku|sonnet` cannot resolve to the user's endpoint;
  falls back to the parent LLM (warning), unless a user-config alias table maps it.

Compatibility is **structural + best-effort semantic**, not behavioral parity: a Claude plugin
whose value depends on tools wiz lacks will load but under-deliver. This is documented, not
hidden — the install summary and load warnings make the gaps visible.

## Security model

- Plugins ship executable code (MCP servers, hook scripts). The **consent point** is
  `wiz plugin install`: it prints the contribution summary (N MCP servers + their commands,
  N hooks + their commands, N agents/skills/commands) and requires confirmation unless
  `--yes`.
- Disabled plugins contribute nothing (skipped at discovery).
- At runtime, **every** tool call (plugin MCP, sub-agent, built-in) still passes the
  existing `OnToolCall` approval gate.
- Trusted `PreToolUse` hooks may auto-approve/deny (parity with Claude Code). This power is
  the reason install is an explicit, reviewed step; documented in the plugin authoring docs.

## Data flow — a turn with an enabled plugin

1. `config.Load()` produces the effective `Config` (user config + merged plugin
   contributions).
2. Session start: `SessionStart` hooks fire; in-memory MCP servers (incl. `load_skill`) and
   plugin MCP transports start; system prompt is composed (base + fragments + skill index).
3. User submits a prompt → `UserPromptSubmit` hooks fire (may modify/inject).
4. Agent reasons; may call `load_skill(name)` to pull a skill body, or `spawn_agent` for a
   plugin-contributed agent type.
5. Before any tool runs → `PreToolUse` hook(s) → existing `OnToolCall` gate. After → result
   to `PostToolUse`.
6. `/skill <name>` (if used) injected the skill body into the system prompt up front.
7. Session end → `Stop` hooks fire.

## Error handling

- Manifest invalid / `wiz_version` unmet → that plugin is skipped with a clear logged error;
  other plugins and wiz still load.
- Unknown skill in `load_skill` / `/skill` → clean error string (no panic).
- Hook command missing/non-zero/malformed stdout → logged; treated as no-decision
  (pass-through), except a hook explicitly returning `block:true`.
- Plugin-vs-plugin name clashes → last wins + warning.

## Testing strategy

- **Unit (mirror `agents_test.go`, table-driven):**
  - merge engine per contribution type (override-by-name, accumulate, clash warning)
  - manifest parse + validation + `wiz_version` check
  - prompt-fragment compose order
  - skill index rendering + `load_skill` tool (known/unknown name)
  - command expansion (`{{.Args}}`, agent routing)
  - hook dispatch with fake shell scripts (approve/deny/adjust/malformed)
  - install/registry over temp dirs (install/list/enable/disable/remove)
- **E2E (P4):** the example plugin driven through wiz **CLI mode** against a stub/local LLM,
  asserting each feature fires (MCP tool callable, agent spawnable, fragment present in
  prompt, skill loadable, command expands, PreToolUse hook blocks/adjusts).

## Phased roadmap (each phase = one spec-driven plan, sub-agent executed on Opus 4.8)

- **P0 — Spine:** the format-agnostic `[]Contribution` model + a **format-detection loader
  seam** (native adapter present; Claude adapter stubbed); manifest schema + parse/validate;
  `wiz plugin install/list/update/enable/disable/remove`; registry; discovery + generalized
  merge engine wiring the already config-driven contributions (`mcp_servers`, `agents`).
  Outcome: installable native plugins shipping MCP servers and sub-agent types.
- **P1 — Prompt fragments + Skills:** `Config.PromptFragments` compose; `Config.Skills` +
  system-prompt index + `load_skill` in-memory MCP tool.
- **P2 — Commands:** TUI slash palette + command registry + expansion + agent routing;
  built-in `/skill` eager-load command.
- **P3 — Hooks:** `HookDispatcher` + six events + JSON stdin/stdout, wired through
  `chat.Callbacks` (incl. `PreToolUse` → approval gate).
- **P4 — Claude Code compatibility:** the `.claude-plugin/` adapter mapping
  `plugin.json`/`skills/`/`commands/`/`agents/`/`hooks.json`/`.mcp.json` into the internal
  model; tool-name alias map; command-syntax translation (`$ARGUMENTS`/`$n`);
  `${CLAUDE_PLUGIN_ROOT}` hook env; hook event-name mapping with skip-warnings.
- **P5 — Example plugin + e2e harness:** a native plugin repo exercising every contribution
  type, **plus installing a real Claude Code plugin** (e.g. a skills-only one) to prove
  compatibility; CLI-mode e2e harness against a stub LLM. **Acceptance gate.**
- **P6 (later) — Marketplace:** wiz index repos + Claude `marketplace.json` import +
  `wiz plugin install <name>` resolution.

## Out of scope / follow-up

- Marketplace/registry resolution (P6, designed but not built in the first pass).
- Sandboxing/isolation of plugin code beyond the install-time consent + runtime approval
  gate.
- Hot-reload of plugins within a running session (load is at `config.Load()` time).

## Risks

- **Security:** hooks and MCP servers run with user privileges. Mitigation: explicit
  reviewed install, contribution summary, disabled-contributes-nothing, runtime approval
  gate unchanged.
- **Merge ambiguity:** silent overrides confuse users. Mitigation: warn on every
  plugin-vs-plugin clash; document precedence.
- **TUI slash surface:** first interactive command palette in wiz; keep the model-update
  changes isolated and well-tested (precedent: `tui/agents_test.go`).
- **E2E determinism:** LLM-driven flows are nondeterministic. Mitigation: stub/local LLM and
  assert on observable side effects (tool invoked, prompt composed) rather than model text.
- **Claude-compat over-promising:** users may expect a Claude plugin to behave identically.
  Mitigation: structural+best-effort framing, install summary + load warnings surface dropped
  tools/events, and the docs state compatibility limits plainly. The P5 e2e installs a real
  Claude plugin so regressions in the adapter are caught.
