<h1 align="center">
  <br>
  <img width="350" alt="logo_wiz" src="https://github.com/user-attachments/assets/7b234b54-c228-4c2f-8bcc-524a9dafd7b1" />
<br>
</h1>

Feeling Lazy? ask it to Wiz.

Wiz is a small assistant living in your terminal that can be invoked with CTRL+space.

Wiz aims to be the `fzf` for llms living in your terminal that is portable and local-llm friendly.


<p align="center">
  <a href="#features">Features</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#tool-approval">Tool Approval</a>
</p>

---

## Features

🧙 **Terminal Keybindings** — Press `Ctrl+Space` anywhere to summon the wizard

⚡ **Dual modes** — Beautiful TUI or simple CLI, your choice

🔧 **Tool execution** — AI runs shell commands with your approval

✅ **Allow list** — Type `a` to trust a tool for the entire session

🔌 **MCP Protocol** — Connect external AI tool servers

🤖 **Sub-agents** — The assistant can delegate tasks to typed sub-agents (e.g. `explore`, `plan`), configurable via the `agents:` block

🪄 **Background jobs** — Run sub-agents in the background; press `Ctrl+B` to background a running foreground sub-agent, `Ctrl+J` to view the jobs footer

📟 **Tmux support** — Seamless splits and popups

🐚 **Multi-shell** — zsh, bash, and fish supported

📦 **0 dependencies** — Portable, single binary, easy to install and upgrade


## Installation

### Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/mudler/wiz/master/install.sh | bash
```

Or, if you use zsh:


```bash
curl -fsSL https://raw.githubusercontent.com/mudler/wiz/master/install.sh | zsh
```


### From Source

```bash
git clone https://github.com/mudler/wiz
cd wiz
go build -o wiz .
sudo mv wiz /usr/local/bin/
```

### Go Install

```bash
go install github.com/mudler/wiz@latest
```

## Usage

After installation, in your terminal, Press CTRL+Space to start `wiz.

You can also run wiz manually by running `wiz`.

### Manually install Shell Integration

Add to your shell config to enable `Ctrl+Space` (only needed if you did not install with `install.sh` and want to have shell bindings):

**zsh** (~/.zshrc):
```bash
eval "$(wiz --init zsh)"
```

**bash** (~/.bashrc):
```bash
eval "$(wiz --init bash)"
```

**fish** (~/.config/fish/config.fish):
```fish
wiz --init fish | source
```

Now `wiz` will be ready when you press `Ctrl+Space` anywhere in your terminal!

## Configuration

Create a config file at `~/.config/wiz/config.yaml`, `~/.wiz.yaml` or at `/etc/wiz/config.yaml` for global settings:

```yaml
# Required: Your LLM configuration
model: gpt-4o-mini
api_key: your-api-key
base_url: https://api.openai.com/v1

# Optional: Custom system prompt
prompt: |
  You are a helpful terminal wizard...

# Optional: Agent behavior
agent_options:
  iterations: 10
  max_attempts: 3
  max_retries: 3
  force_reasoning: false

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

When the wizard wants to run a command, you'll see a prompt:

```
┌──────────────────────────────────────┐
│ 🔧 bash                              │
│                                      │
│ Arguments: {"script": "ls -la"}      │
│ 💭 Listing directory contents...     │
│                                      │
│ [y]es  [a]lways  [n]o  or adjust     │
└──────────────────────────────────────┘
```

**Options:**
- `y` or `yes` — Approve this execution
- `a` or `always` — Approve and add to session allow list (won't ask again)
- `n` or `no` — Deny execution
- *anything else* — Treated as an adjustment to the command

## MCP Servers

Wiz uses the [Model Context Protocol](https://modelcontextprotocol.io/) for tool execution.

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

When running inside tmux, wiz automatically uses a split pane for the TUI. Use `--no-tmux` to disable this behavior.

## License

MIT
