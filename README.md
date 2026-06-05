<h1 align="center">
  <br>
  <img width="460" alt="nib" src="docs/images/logo.png" />
  <br>
</h1>

<p align="center">
  <b>A tiny, zero-dependency LLM agent harness that lives in your terminal.</b><br>
  One static binary. No runtime, no daemon, no cloud. Local-LLM friendly. Summon it anywhere with <code>Ctrl+Space</code>.
</p>

<p align="center">
  <img alt="License" src="https://img.shields.io/github/license/mudler/nib">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/mudler/nib">
  <img alt="Release" src="https://img.shields.io/github/v/release/mudler/nib?sort=semver">
  <img alt="CI" src="https://github.com/mudler/nib/actions/workflows/test.yml/badge.svg">
</p>

<p align="center">
  <a href="#why-nib">Why nib</a> •
  <a href="#quickstart">Quickstart</a> •
  <a href="#usage">Usage</a> •
  <a href="#plugins">Plugins</a> •
  <a href="#skills">Skills</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#tool-approval">Tool Approval</a>
</p>

<p align="center">
  <img alt="nib in the terminal: ask a question, approve a command, get an answer" src="docs/images/demo-tui.gif" width="800">
</p>

---

## Why nib

Most LLM coding agents are big: a Node/Python runtime, a pile of dependencies, a login, a
background service. **nib is the opposite.** It's a single ~20 MB Go binary you drop on any
machine — laptop, server, container, a box you SSH'd into — and it just runs. Point it at
any OpenAI-compatible endpoint (including a local model) and press `Ctrl+Space`.

Small doesn't mean toy. nib is a real agent harness:

- **Tool use with approval** — it runs shell commands, but every call passes an approval gate you control.
- **Sub-agents** — delegate self-contained subtasks (`explore`, `plan`, …) that run in the foreground or background.
- **MCP** — connect any [Model Context Protocol](https://modelcontextprotocol.io/) server for extra tools.
- **Plugins** — installable packages that add MCP servers, sub-agents, prompt fragments, skills, slash commands, and lifecycle hooks. **Claude Code plugins work too.**
- **Skills** — install skill packs (e.g. [`obra/superpowers`](https://github.com/obra/superpowers)) and let the agent load them on demand.

Think of it as the **`fzf` for LLMs**: portable, keyboard-driven, composable, and out of your way.

| | nib | typical agent CLIs |
|---|---|---|
| Install | one static binary | runtime + package tree |
| Dependencies | **zero** | many |
| Local LLMs | first-class | varies |
| Summon | `Ctrl+Space`, anywhere | launch a session |
| Extend | plugins · skills · MCP | varies |
| Footprint | ~20 MB | hundreds of MB |

## Features

- **`Ctrl+Space` anywhere** — summon nib straight from your shell prompt; inline like `fzf`, or a tmux split when you're in tmux.
- **Two modes** — a polished TUI, or a plain `--cli` mode for pipes and scripts.
- **Tool execution with approval** — the AI proposes commands; you approve, deny, edit, or trust for the session.
- **Sub-agents & background jobs** — delegate to typed sub-agents; background them (`Ctrl+B`) and watch the jobs footer (`Ctrl+J`).
- **Plugins** — `nib plugin install <git-url>`; six contribution types; Claude-Code-plugin compatible.
- **Skills** — `nib skill install <git-url>`; progressive-disclosure skill packs loaded on demand.
- **MCP protocol** — bring any external tool server.
- **tmux-native** — seamless splits and popups.
- **Multi-shell** — zsh, bash, and fish.
- **Zero dependencies** — one portable binary, trivial to install and upgrade.

## Quickstart

**1. Install**

```bash
curl -fsSL https://raw.githubusercontent.com/mudler/nib/master/install.sh | bash
```

<details>
<summary>Other ways to install</summary>

```bash
# zsh users
curl -fsSL https://raw.githubusercontent.com/mudler/nib/master/install.sh | zsh

# from source
git clone https://github.com/mudler/nib && cd nib && go build -o nib . && sudo mv nib /usr/local/bin/

# go install
go install github.com/mudler/nib@latest
```
</details>

**2. Configure** a model — `~/.config/nib/config.yaml`:

```yaml
model: gpt-4o-mini
api_key: your-api-key
base_url: https://api.openai.com/v1   # or your local endpoint, e.g. http://localhost:8080/v1
```

**3. Press `Ctrl+Space`** in your terminal (or just run `nib`). That's it.

## Usage

Run `nib` to open the TUI, or press `Ctrl+Space` from your shell. Use `--cli` for a plain,
pipe-friendly mode.

### Summon nib from your shell (`Ctrl+Space`)

The `install.sh` script wires this up for you. Inside tmux, nib opens in a split pane so it
never disturbs what you're doing:

<p align="center">
  <img alt="press Ctrl+Space to summon nib in a tmux split and ask for a command" src="docs/images/demo-tmux.gif" width="800">
</p>

To wire it up manually, add the line for your shell:

```bash
eval "$(nib --init zsh)"      # ~/.zshrc
eval "$(nib --init bash)"     # ~/.bashrc
nib --init fish | source      # ~/.config/fish/config.fish
```

### Sub-agents & background jobs

Ask nib to delegate, and it spawns a typed sub-agent (`explore`, `plan`, or any you
configure). Background a running job with `Ctrl+B` and watch the jobs footer with `Ctrl+J`:

<p align="center">
  <img alt="nib delegating to the explore sub-agent, with the jobs footer" src="docs/images/demo-agents.gif" width="800">
</p>

## Plugins

A **plugin** is a single installable unit — a git repo (or local dir) with a
`nib-plugin.yaml` manifest — that can contribute any combination of:

| Contribution | What it adds |
|---|---|
| `mcp_servers` | external MCP tool servers |
| `agents` | typed sub-agents the agent can spawn |
| `prompt_fragments` | extra system-prompt text (inline or from a file) |
| `skills` | skills indexed in the prompt, loaded on demand |
| `commands` | slash commands, optionally routed through a sub-agent |
| `hooks` | shell commands bound to lifecycle events (e.g. `SessionStart`, `PreToolUse`) |

```bash
nib plugin install <git-url|local-path>   # [--ref <tag|branch>] [--yes]
nib plugin list
nib plugin enable|disable <name>
nib plugin update|remove <name>
```

Install prints a summary of what the plugin contributes and asks for confirmation
(`--yes` to skip). Plugins install **disabled** by default; a disabled plugin contributes
nothing, and every tool call still passes the approval gate at runtime.

A minimal `nib-plugin.yaml`:

```yaml
name: my-plugin
version: 1.0.0
description: adds a sub-agent and a slash command

agents:
  - name: researcher
    description: investigates a self-contained subtask
    system_prompt: You are a focused research sub-agent.
    tools: [bash]

commands:
  - name: review
    description: review the given input
    prompt: "Review the following: {{.Args}}"
    agent: researcher
```

**Claude Code compatible.** `nib plugin install` also installs an unmodified Claude Code
plugin (`.claude-plugin/` layout) or marketplace, mapping its `plugin.json`, `skills/`,
`commands/`, `agents/`, `hooks/`, and `.mcp.json` into nib's model.

See **[`examples/nib-plugin-demo`](examples/nib-plugin-demo)** for a reference plugin that
exercises all six contribution types.

## Skills

A **skill pack** is a git repo (or local dir) containing a `skills/<name>/SKILL.md`
collection — for example [`obra/superpowers`](https://github.com/obra/superpowers). nib
harvests every skill, indexes it (name + description) in the system prompt, and the agent
pulls in a skill's full instructions on demand via the `load_skill` tool — or you inject one
eagerly for the session with `/skill <name>`.

```bash
nib skill install <git-url|local-path>    # [--ref <tag|branch>] [--yes]
nib skill list
nib skill enable|disable <name>
nib skill update|remove <name>
```

Like plugins, skill packs install **disabled**; enable the ones you want with
`nib skill enable <name>`. Skill packs carry their bundled files, so a skill can `Read` or
run scripts from its own directory at runtime.

## Configuration

nib looks for config (in order) in `./.nib.yaml`, `$XDG_CONFIG_HOME/nib/config.yaml`,
`~/.config/nib/config.yaml`, `~/.nib.yaml`, then `/etc/nib/config.yaml`.

```yaml
# Required: your LLM (any OpenAI-compatible endpoint, local or remote)
model: gpt-4o-mini
api_key: your-api-key
base_url: https://api.openai.com/v1

# Optional: custom system prompt
prompt: |
  You are a calm, helpful terminal assistant...

# Optional: per-request metadata sent verbatim on every LLM request (the OpenAI
# "metadata" object). Backends such as LocalAI use it for per-request flags —
# e.g. disable a reasoning model's thinking:
metadata:
  enable_thinking: "false"

# Optional: OpenAI-standard reasoning effort, sent on every request as
# "reasoning_effort" ("none"/"low"/"medium"/"high"). Unlike metadata.enable_thinking,
# this works even when the model's chat template has no enable_thinking toggle
# (e.g. LFM2.5) — so it's the reliable way to turn a reasoning model's thinking off:
reasoning_effort: "none"

# Optional: agent behavior
agent_options:
  iterations: 10
  max_attempts: 3
  max_retries: 3
  force_reasoning: false

# Optional: tool-approval policy (default: prompt for every tool)
#   prompt    — ask before each tool call (default)
#   allowlist — auto-approve the tools in allowed_tools, prompt for the rest
#   auto      — approve every tool call without prompting
approval_mode: prompt
allowed_tools:
  - bash

# Optional: extra sub-agent types (general, explore, plan are built in)
agents:
  - name: researcher
    description: investigates a self-contained subtask
    system_prompt: You are a focused research sub-agent.
    tools: [bash]
    # Per-agent metadata overlays the global metadata above (per key):
    metadata:
      enable_thinking: "true"

# Optional: external MCP servers
mcp_servers:
  filesystem:
    command: npx
    args: ["-y", "@anthropic/mcp-filesystem", "/home/user"]
    env:
      FOO: bar
```

You can also configure the essentials via environment variables:

```bash
export MODEL=gpt-4o-mini
export API_KEY=your-api-key
export BASE_URL=https://api.openai.com/v1
```

## Tool Approval

When nib wants to run a command, you decide:

```
▏ run: bash
▏ {
▏   "script": "df -h"
▏ }
▏ [y] yes  [a] always  [n] no  [e] edit  [A] all
```

In the **TUI**, approval is a single keypress (no Enter):

- `y` — approve this call
- `a` — always allow **this tool** for the session (sub-agents share the allow list)
- `A` — allow **all** tool calls for the rest of this turn (handy after delegating a multi-step task)
- `n` / `Esc` — deny
- `e` — edit the call, then submit

In the **CLI** (`--cli`) the prompt is line-based: type `y`, `a`, `all`, `n`, or a free-form
change, then Enter. To skip prompting entirely, set `approval_mode` / `allowed_tools` in
your config.

## MCP Servers

nib speaks the [Model Context Protocol](https://modelcontextprotocol.io/). `bash` is
built in; add any external server in your config:

```yaml
mcp_servers:
  my_server:
    command: /path/to/mcp-server
    args: ["--some-flag"]
    env:
      API_KEY: secret
```

## tmux

Inside tmux, nib automatically uses a split pane for the TUI. Pass `--no-tmux` to disable.

## License

MIT
