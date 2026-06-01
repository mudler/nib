# wiz-demo — example plugin

A reference plugin exercising every wiz contribution type:

- **mcp_servers** — `echo` (a standalone stdio MCP server in `cmd/echo-mcp`; build it
  and put it on your `PATH`, or set the manifest `command` to the built binary's path).
- **agents** — `demo-researcher`, a sub-agent the main agent can `spawn_agent`.
- **prompt_fragments** — extra system-prompt text (one inline, one from `prompts/style.md`).
- **skills** — `demo-skill`, indexed in the prompt and loadable via `load_skill` / `/skill`.
- **commands** — `/demo-review <args>`, a slash command routed through `demo-researcher`.
- **hooks** — a `SessionStart` and a `PreToolUse` hook (shell scripts in `hooks/`).

## Install

    go build -o ~/.local/bin/echo-mcp ./cmd/echo-mcp   # so `echo-mcp` is on PATH
    wiz plugin install <git-url-of-this-plugin>
