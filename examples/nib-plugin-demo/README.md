# nib-demo — example plugin

A reference plugin exercising every nib contribution type:

- **mcp_servers** — `echo` (a standalone stdio MCP server in `cmd/echo-mcp`). The manifest
  references it as `${NIB_PLUGIN_ROOT}/bin/echo-mcp`, which resolves to the plugin's install
  dir — so just build it to `bin/echo-mcp` before installing (see below) and it travels with
  the plugin, no `PATH` setup needed.
- **agents** — `demo-researcher`, a sub-agent the main agent can `spawn_agent`.
- **prompt_fragments** — extra system-prompt text (one inline, one from `prompts/style.md`).
- **skills** — `demo-skill`, indexed in the prompt and loadable via `load_skill` / `/skill`.
- **commands** — `/demo-review <args>`, a slash command routed through `demo-researcher`.
- **hooks** — a `SessionStart` and a `PreToolUse` hook (shell scripts in `hooks/`).

## Install

    # From this directory: build the MCP server into bin/, then install.
    # `nib plugin install <dir>` copies the whole folder (bin/ included), and the
    # manifest's ${NIB_PLUGIN_ROOT}/bin/echo-mcp resolves at the install location.
    go build -o bin/echo-mcp ./cmd/echo-mcp
    nib plugin install .            # or: nib plugin install <git-url-of-this-plugin>
