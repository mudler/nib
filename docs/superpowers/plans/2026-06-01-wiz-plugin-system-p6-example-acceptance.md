# Wiz Plugin System — P6 (Example Plugin + e2e Acceptance Gate) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship one **fully runnable example plugin** that exercises *every* wiz contribution type (MCP server, sub-agent, prompt fragment, skill, command, hooks) and a **comprehensive e2e acceptance test** that installs it, enables it, and asserts all six contributions merge and run — plus installing a real Claude-format plugin to prove compatibility. This is the acceptance gate for the whole plugin system.

**Architecture:** The example lives at `examples/wiz-plugin-demo/` as a native `wiz-plugin.yaml` plugin with its asset files and a tiny standalone **echo MCP server** (`cmd/echo-mcp`, a `main` package in the wiz module using the go-sdk's `StdioTransport`) so `mcp_servers` is genuinely runnable. The Go acceptance test git-inits a copy of the example, installs it through the real `Manager`, `Apply`s it, and asserts every contribution type landed. The binary acceptance harness (controller step) builds `echo-mcp` onto `PATH`, installs the example via the real `wiz` binary, and drives a stub-LLM turn — confirming agents/fragments/skills reach the system prompt, the MCP echo tool + load_skill reach the LLM, and hooks fire.

**Tech Stack:** Go 1.24, `github.com/modelcontextprotocol/go-sdk/mcp` (`StdioTransport`), git, standard `testing`. Builds on P0–P5.

**Branch:** `feat/plugin-system`. All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** marketplace resolution (P7). The example is a single plugin; no registry/index.

---

## File Structure

- `examples/wiz-plugin-demo/wiz-plugin.yaml` — **new**: manifest with all six contribution types.
- `examples/wiz-plugin-demo/prompts/style.md` — **new**: a prompt-fragment body.
- `examples/wiz-plugin-demo/skills/demo.md` — **new**: a skill instruction body.
- `examples/wiz-plugin-demo/hooks/session.sh` — **new**: a SessionStart hook (records it fired).
- `examples/wiz-plugin-demo/hooks/audit.sh` — **new**: a PreToolUse hook (logs + approves).
- `examples/wiz-plugin-demo/cmd/echo-mcp/main.go` — **new**: standalone stdio MCP echo server.
- `examples/wiz-plugin-demo/README.md` — **new**: what it demonstrates + how to install.
- `plugin/e2e_p6_test.go` — **new**: the comprehensive Go acceptance test (install → merge → assert all six types) + a Claude-format plugin.

---

## Task 1: The example plugin (manifest + assets + echo MCP server)

**Files:**
- Create the seven `examples/wiz-plugin-demo/...` files above.
- Test: none yet (Task 2 is the test). This task verifies the example **builds + its manifest parses/validates**.

**Context:** A static reference artifact. The manifest demonstrates the native format for all six types; assets are referenced by relative path (resolved against the plugin root at merge time). The echo MCP server is a real `main` package so `mcp_servers` is runnable.

- [ ] **Step 1: Create `examples/wiz-plugin-demo/wiz-plugin.yaml`:**

```yaml
name: wiz-demo
version: 1.0.0
description: Demo plugin exercising every wiz contribution type
wiz_version: ">=0.0.0"

# 1) MCP server — a standalone stdio server shipped in cmd/echo-mcp (build it and
#    put it on PATH, or set command to its absolute path).
mcp_servers:
  echo:
    command: echo-mcp
    args: []

# 2) Sub-agent type
agents:
  - name: demo-researcher
    description: a demo research sub-agent for self-contained subtasks
    system_prompt: You are the demo researcher. Investigate the task and report a concise result.
    tools: [bash]

# 3) Prompt fragments (inline + file)
prompt_fragments:
  - "DEMO_FRAGMENT_INLINE: keep demo answers concise."
  - { file: prompts/style.md }

# 4) Skill (body loaded on demand via load_skill / eagerly via /skill)
skills:
  - name: demo-skill
    description: DEMO_SKILL_INDEX demonstrates a loadable skill
    instructions: { file: skills/demo.md }
    tools: [bash]

# 5) Slash command (optionally routed through a sub-agent)
commands:
  - name: demo-review
    description: a demo slash command
    prompt: "Demo review of the following: {{.Args}}"
    agent: demo-researcher

# 6) Hooks (shell commands on lifecycle events)
hooks:
  - event: SessionStart
    command: sh hooks/session.sh
  - event: PreToolUse
    matcher: bash
    command: sh hooks/audit.sh
```

> Hook commands use `sh hooks/xxx.sh` (not `./hooks/xxx.sh`) so they run without depending on the execute bit surviving a git clone; the hook `Dir` is the plugin root, so the relative path resolves.

- [ ] **Step 2: Create the asset files.**

`examples/wiz-plugin-demo/prompts/style.md`:
```md
DEMO_FRAGMENT_FILE: respond in a friendly, demonstrative tone.
```

`examples/wiz-plugin-demo/skills/demo.md`:
```md
# Demo Skill

DEMO_SKILL_BODY: when this skill is loaded, follow these steps:
1. Acknowledge the demo skill is active.
2. Use the bash tool to complete the task.
```

`examples/wiz-plugin-demo/hooks/session.sh`:
```sh
#!/bin/sh
# SessionStart hook — record that it fired (WIZ_PLUGIN_ROOT is the plugin dir).
echo "demo session-start fired" >> "${WIZ_PLUGIN_ROOT:-.}/demo-hooks.log"
```

`examples/wiz-plugin-demo/hooks/audit.sh`:
```sh
#!/bin/sh
# PreToolUse hook — log the requested tool (event JSON arrives on stdin) and approve.
cat >> "${WIZ_PLUGIN_ROOT:-.}/demo-hooks.log"
echo '{"approved": true}'
```

`examples/wiz-plugin-demo/README.md`:
```md
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
```

- [ ] **Step 3: Create the echo MCP server** `examples/wiz-plugin-demo/cmd/echo-mcp/main.go`:

```go
// Command echo-mcp is a minimal standalone MCP server exposing a single `echo`
// tool. It demonstrates how a wiz plugin ships an MCP server: a normal program
// that speaks MCP over stdio. wiz spawns it (per the plugin's mcp_servers entry)
// and connects as a client.
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Text string `json:"text" jsonschema:"the text to echo back"`
}

type echoOutput struct {
	Echoed string `json:"echoed" jsonschema:"the echoed text"`
}

func echo(ctx context.Context, req *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
	return nil, echoOutput{Echoed: in.Text}, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "echo", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echo back the provided text (demo MCP tool).",
	}, echo)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("echo-mcp: %v", err)
	}
}
```

- [ ] **Step 4: Verify it builds + the manifest validates.**

Run:
```bash
go build ./examples/wiz-plugin-demo/cmd/echo-mcp
go vet ./examples/...
```
Expected: both clean (the echo server compiles against the go-sdk).

Manifest sanity (parse + validate via a throwaway, then delete it):
```bash
cat > /tmp/manifest_check_test.go <<'EOF'
package plugin
import ("os";"testing")
func TestExampleManifestParses(t *testing.T) {
	data, err := os.ReadFile("../examples/wiz-plugin-demo/wiz-plugin.yaml")
	if err != nil { t.Fatal(err) }
	m, err := ParseManifest(data)
	if err != nil { t.Fatalf("parse: %v", err) }
	if err := m.Validate("0.9.0"); err != nil { t.Fatalf("validate: %v", err) }
	if m.Name != "wiz-demo" { t.Fatalf("name: %q", m.Name) }
}
EOF
cp /tmp/manifest_check_test.go plugin/zz_manifest_check_test.go
go test ./plugin/ -run TestExampleManifestParses -v
rm plugin/zz_manifest_check_test.go /tmp/manifest_check_test.go
```
Expected: PASS. (This is a scratch check — the real assertion lives in Task 2; remove the scratch file before committing.)

- [ ] **Step 5: Commit**

```bash
git add examples/wiz-plugin-demo
git commit -m "feat(examples): wiz-demo plugin exercising every contribution type"
```

---

## Task 2: Comprehensive Go acceptance e2e

**Files:**
- Create: `plugin/e2e_p6_test.go`

**Context:** The deterministic acceptance test. It copies `examples/wiz-plugin-demo/` into a temp git repo, installs it through the real `Manager`, enables it, `Apply`s to a `Config`, and asserts **every** contribution type merged and renders. It also installs a synthetic Claude-format plugin to reaffirm compatibility. No live LLM needed.

- [ ] **Step 1: Write the test** — create `plugin/e2e_p6_test.go`:

```go
package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

// copyTree recursively copies src into dst (files + dirs), preserving structure.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
}

func gitCommitDir(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestAcceptanceExamplePlugin is the plugin-system acceptance gate: the example
// plugin installs and contributes ALL six types.
func TestAcceptanceExamplePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Locate the committed example plugin relative to this test file's package dir.
	exampleSrc, err := filepath.Abs(filepath.Join("..", "examples", "wiz-plugin-demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(exampleSrc, "wiz-plugin.yaml")); err != nil {
		t.Fatalf("example plugin not found at %s: %v", exampleSrc, err)
	}

	// Copy it into a temp git repo to install from.
	repo := t.TempDir()
	copyTree(t, exampleSrc, repo)
	gitCommitDir(t, repo)

	base := t.TempDir()
	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "wiz-demo" {
		t.Fatalf("name = %q", m.Name)
	}
	if err := mgr.SetEnabled("wiz-demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}

	// 1) MCP server.
	if _, ok := cfg.MCPServers["echo"]; !ok {
		t.Fatalf("mcp server not merged: %+v", cfg.MCPServers)
	}
	// 2) Sub-agent.
	if !hasAgentNamed(cfg.Agents, "demo-researcher") {
		t.Fatalf("agent not merged: %+v", cfg.Agents)
	}
	// 3) Prompt fragments (inline + file) both merged.
	frags := strings.Join(cfg.PromptFragments, "\n")
	if !strings.Contains(frags, "DEMO_FRAGMENT_INLINE") || !strings.Contains(frags, "DEMO_FRAGMENT_FILE") {
		t.Fatalf("fragments not merged: %+v", cfg.PromptFragments)
	}
	// 4) Skill: merged with its file body, and surfaced in the prompt index.
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "demo-skill" || !strings.Contains(cfg.Skills[0].Instructions, "DEMO_SKILL_BODY") {
		t.Fatalf("skill not merged with body: %+v", cfg.Skills)
	}
	prompt := cfg.GetPrompt()
	if !strings.Contains(prompt, "demo-skill: DEMO_SKILL_INDEX") || !strings.Contains(prompt, "DEMO_FRAGMENT_INLINE") {
		t.Fatalf("prompt missing skill index / fragment:\n%s", prompt)
	}
	// 5) Command.
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "demo-review" || cfg.Commands[0].Agent != "demo-researcher" {
		t.Fatalf("command not merged: %+v", cfg.Commands)
	}
	// 6) Hooks: both events, Dir stamped to the plugin root.
	var sessionStart, preTool bool
	for _, h := range cfg.Hooks {
		if h.Dir == "" {
			t.Fatalf("hook Dir not stamped: %+v", h)
		}
		switch h.Event {
		case "SessionStart":
			sessionStart = true
		case "PreToolUse":
			preTool = true
		}
	}
	if !sessionStart || !preTool {
		t.Fatalf("hooks not merged (sessionStart=%v preTool=%v): %+v", sessionStart, preTool, cfg.Hooks)
	}
}

// TestAcceptanceClaudePlugin reaffirms a Claude-format plugin installs + maps.
func TestAcceptanceClaudePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-accept","version":"1.0.0","description":"d"}`,
		"skills/c/SKILL.md":          "---\nname: cskill\ndescription: claude skill\n---\nclaude body\n",
		"commands/cmd.md":            "---\ndescription: claude cmd\n---\nDo: $ARGUMENTS\n",
	})
	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("claude-accept", true); err != nil {
		t.Fatal(err)
	}
	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "cskill" {
		t.Fatalf("claude skill not mapped: %+v", cfg.Skills)
	}
	if len(cfg.Commands) != 1 || !strings.Contains(cfg.Commands[0].Prompt, "{{.Args}}") {
		t.Fatalf("claude command not mapped/translated: %+v", cfg.Commands)
	}
}

func hasAgentNamed(agents []types.AgentTypeConfig, name string) bool {
	for _, a := range agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run** — `go test ./plugin/ -run 'TestAcceptance' -v`. Expected PASS. If a contribution type fails to merge, that's a real defect — investigate the relevant phase, report BLOCKED, do not weaken the test.

- [ ] **Step 3: Whole suite** — `go test ./...` and `go vet ./...` → all PASS.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_p6_test.go
git commit -m "test(plugin): acceptance e2e — example plugin exercises every contribution type"
```

---

## Task 3: Binary acceptance harness (controller step)

Not a code task — the controller runs this after Task 2 to prove the example runs through the real `wiz` binary:

- [ ] **Build the echo MCP server** onto a temp dir and prepend it to `PATH`:
  `go build -o "$TMPBIN/echo-mcp" ./examples/wiz-plugin-demo/cmd/echo-mcp`.
- [ ] **Install the example** via the real `wiz plugin install` (git-init a temp copy of `examples/wiz-plugin-demo`), `--yes`, into an isolated `XDG_CONFIG_HOME`. Confirm `wiz plugin list` shows it enabled (use a pipe/var, NOT `grep <(...)`).
- [ ] **Run one turn** against a stub OpenAI server with `PATH` including `$TMPBIN` (so the `echo` MCP server starts). Assert the captured request shows: the `demo-researcher` agent listed in the system prompt, both prompt fragments (`DEMO_FRAGMENT_INLINE`/`DEMO_FRAGMENT_FILE`), the skill index (`demo-skill: DEMO_SKILL_INDEX`), and the tools array includes the plugin's `echo` MCP tool + `load_skill`. Confirm the `SessionStart` hook fired (its `demo-hooks.log` side-effect exists in the installed plugin dir).
- [ ] **Full suite** `go test ./...` + `go vet ./...` green; push the branch.

---

## Self-Review (completed during planning)

**Spec coverage (P6 scope):**
- Example plugin exercising every contribution type → Task 1 ✓ (mcp_servers via a real echo server, agents, prompt_fragments, skills, commands, hooks)
- Comprehensive e2e installing it + asserting all six → Task 2 ✓
- Real Claude-format plugin compatibility → Task 2 (`TestAcceptanceClaudePlugin`) ✓
- Binary acceptance (live run through `wiz`) → Task 3 ✓

**Out of P6 (deferred):** marketplace (P7).

**Type/consistency:** the example produces a native `Manifest` flowing through the exact `Apply`/`GetPrompt` paths validated in P0–P5; the test reuses `gitInitRepoFiles` (P1) and asserts against `cfg.MCPServers/Agents/PromptFragments/Skills/Commands/Hooks`. The echo server uses the same go-sdk `AddTool`/`NewServer` pattern as `mcp/shell.go`, only with `StdioTransport` instead of in-memory.

**Risk notes:** (1) the example manifest is committed asset content — Task 1's scratch parse/validate check catches typos before Task 2. (2) hook commands use `sh hooks/x.sh` to avoid the git execute-bit dependency. (3) the echo server is a separate `main` package in the module; `go build ./examples/...` and `go vet ./...` must stay green (the binary acceptance builds it explicitly).
