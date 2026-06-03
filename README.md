<h1 align="center">
  <br>
  <img width="350" alt="logo_nib" src="https://github.com/user-attachments/assets/7b234b54-c228-4c2f-8bcc-524a9dafd7b1" />
<br>
</h1>

Feeling Lazy? ask it to nib.

nib is a small assistant living in your terminal that can be invoked with CTRL+space.

nib aims to be the `fzf` for llms living in your terminal that is portable and local-llm friendly.


<p align="center">
  <a href="#features">Features</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#tool-approval">Tool Approval</a>
</p>

---

## Features

**Terminal Keybindings** — Press `Ctrl+Space` anywhere to open nib

**Dual modes** — TUI or simple CLI, your choice

**Tool execution** — AI runs shell commands with your approval

**Allow list** — Type `a` to trust a tool for the entire session

**MCP Protocol** — Connect external AI tool servers

**Sub-agents** — The assistant can delegate tasks to typed sub-agents (e.g. `explore`, `plan`), configurable via the `agents:` block

**Background jobs** — Run sub-agents in the background; press `Ctrl+B` to background a running foreground sub-agent, `Ctrl+J` to view the jobs footer

**Tmux support** — Seamless splits and popups

**Multi-shell** — zsh, bash, and fish supported

**0 dependencies** — Portable, single binary, easy to install and upgrade


## Installation

### Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/mudler/nib/master/install.sh | bash
```

Or, if you use zsh:


```bash
curl -fsSL https://raw.githubusercontent.com/mudler/nib/master/install.sh | zsh
```


### From Source

```bash
git clone https://github.com/mudler/nib
cd nib
go build -o nib .
sudo mv nib /usr/local/bin/
```

### Go Install

```bash
go install github.com/mudler/nib@latest
```

## Usage

After installation, in your terminal, Press CTRL+Space to start `nib`.

You can also run nib manually by running `nib`.

### Manually install Shell Integration

Add to your shell config to enable `Ctrl+Space` (only needed if you did not install with `install.sh` and want to have shell bindings):

**zsh** (~/.zshrc):
```bash
eval "$(nib --init zsh)"
```

**bash** (~/.bashrc):
```bash
eval "$(nib --init bash)"
```

**fish** (~/.config/fish/config.fish):
```fish
nib --init fish | source
```

Now `nib` will be ready when you press `Ctrl+Space` anywhere in your terminal!

## Configuration

Create a config file at `~/.config/nib/config.yaml`, `~/.nib.yaml` or at `/etc/nib/config.yaml` for global settings:

```yaml
# Required: Your LLM configuration
model: gpt-4o-mini
api_key: your-api-key
base_url: https://api.openai.com/v1

# Optional: Custom system prompt
prompt: |
  You are a calm, helpful terminal assistant...

# Optional: Agent behavior
agent_options:
  iterations: 10
  max_attempts: 3
  max_retries: 3
  force_reasoning: false

# Optional: Tool-approval policy (default: prompt for every tool)
#   prompt    — ask before each tool call (default)
#   allowlist — auto-approve the tools in allowed_tools, prompt for the rest
#   auto      — approve every tool call without prompting
approval_mode: prompt
allowed_tools:
  - bash

# Optional: Additional MCP servers
mcp_servers:
  filesystem:
    command: npx
    args:
      - "-y"
      - "@anthropic/mcp-filesystem"
      - "/home/user"
    env:
      foo: bar
```

### Environment Variables

You can also configure via environment variables:

```bash
export MODEL=gpt-4o-mini
export API_KEY=your-api-key
export BASE_URL=https://api.openai.com/v1
```

## Tool Approval

When nib wants to run a command, you'll see a prompt:

```
▏ run  bash
▏ {
▏   "script": "ls -la"
▏ }
▏ listing directory contents...
▏ [y] yes  [a] always  [n] no  [e] edit  [A] all
```

Arguments are pretty-printed, and in the **TUI** approval is key-driven — the input
box is hidden and a single keypress answers (no Enter needed):

- `y` — approve this call
- `a` — always allow **this tool** for the session (incl. sub-agents — they share the allow list)
- `A` — allow **all** tool calls for the rest of this turn (handy when you've delegated a multi-step task to a sub-agent)
- `n` / `Esc` — deny
- `e` — edit: open a field to type a free-form change, then Enter (Esc cancels)

In the **CLI** (`--cli`) the prompt is line-based — type `y`, `a`, `all`, `n`, or a free-form change, then Enter.

To avoid prompting altogether, set `approval_mode` / `allowed_tools` in your config (see above).

## MCP Servers

nib uses the [Model Context Protocol](https://modelcontextprotocol.io/) for tool execution.

### Built-in Tools

- **bash** — Execute shell scripts

### Adding External MCP Servers

Add to your config:

```yaml
mcp_servers:
  my_server:
    command: /path/to/mcp-server
    args:
      - --some-flag
    env:
      API_KEY: secret
```

## Tmux Integration

When running inside tmux, nib automatically uses a split pane for the TUI. Use `--no-tmux` to disable this behavior.

## License

MIT
