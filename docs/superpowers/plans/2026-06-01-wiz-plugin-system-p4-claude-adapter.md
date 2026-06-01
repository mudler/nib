# Wiz Plugin System — P4 (Claude Code Compatibility Adapter) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the P0 `loadClaudeManifest` stub with a real adapter that maps a Claude Code plugin's `.claude-plugin/` layout into wiz's internal `Manifest`, so an unmodified Claude plugin installs and runs. Maps `plugin.json` (meta), `skills/<n>/SKILL.md`, `commands/*.md`, `agents/*.md`, `hooks/hooks.json`, and `.mcp.json`, with a best-effort tool-name alias map, `$ARGUMENTS` → `{{.Args}}` command translation, and hook event-name filtering. Unmappable items are skipped with a stderr warning.

**Architecture:** The internal `Manifest` is already format-agnostic; the adapter just produces one from the Claude layout (loading skill/command/agent bodies **inline**, so they flow through the existing resolve/merge path with no new runtime). The format-detection seam (`DetectFormat` → `loadClaudeManifest`) is already wired; this phase fills in the adapter. `${CLAUDE_PLUGIN_ROOT}` is already exported by the P3 hook dispatcher, so Claude hook scripts run unmodified. Pure helpers (tool alias, frontmatter split, per-directory loaders) are unit-tested; the orchestrator is proven by a real-git e2e installing a synthetic `.claude-plugin/` fixture.

**Tech Stack:** Go 1.24, `encoding/json` (plugin.json/hooks.json/.mcp.json), `gopkg.in/yaml.v3` (markdown frontmatter), `os`/`path/filepath` (directory walking), standard `testing`. Builds on P0 (detection seam + Manifest) + P1/P2/P3 (SkillSpec/CommandConfig/AgentTypeConfig/HookConfig).

**Branch:** `feat/plugin-system`. All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** Claude `marketplace.json` resolution (P7). `!`bash-exec / `@`file-ref command preprocessing and `$1..$n` positional args are a documented unsupported subset (only `$ARGUMENTS`/`$@` are translated). Subdirectory command namespacing is flattened to the base filename. `model:` aliases (opus/haiku) are carried through verbatim (the LLM factory resolves what it can; unknown → parent LLM).

---

## File Structure

- `plugin/claude_tools.go` — **new**: Claude→wiz tool-name alias map.
- `plugin/claude_md.go` — **new**: frontmatter splitter + `skills/`/`commands/`/`agents/` loaders.
- `plugin/claude.go` — **modify** (replace stub): `plugin.json`/`.mcp.json` loaders + `loadClaudeManifest` orchestrator.
- `plugin/claude_hooks.go` — **new**: `hooks/hooks.json` loader (flatten + event filter).
- `plugin/claude_tools_test.go`, `plugin/claude_md_test.go`, `plugin/claude_test.go`, `plugin/claude_hooks_test.go` — **new** tests.
- `plugin/detect_test.go` — **modify**: `TestLoadManifestClaudeStub` flips to assert a Claude plugin now loads.
- `plugin/e2e_p4_test.go` — **new**: real-git e2e installing a synthetic `.claude-plugin/` plugin.

---

## Task 1: Tool-alias map + frontmatter splitter

**Files:**
- Create: `plugin/claude_tools.go`, `plugin/claude_tools_test.go`
- Create: `plugin/claude_md.go` (the splitter only this task), `plugin/claude_md_test.go`

**Context:** Two pure helpers used by every later loader. The alias map translates Claude's PascalCase tool names to wiz's (`bash`/`read`/`write`/`edit`/`glob`/`grep`); unmapped tools are dropped. The frontmatter splitter separates a markdown file's `--- yaml ---` header from its body.

- [ ] **Step 1: Write the failing tests.**

Create `plugin/claude_tools_test.go`:

```go
package plugin

import "testing"

func TestAliasClaudeTools(t *testing.T) {
	got := aliasClaudeTools([]string{"Bash", "Read", "Edit", "MultiEdit", "Glob", "Grep", "Write", "Task", "WebFetch"})
	want := map[string]bool{"bash": true, "read": true, "edit": true, "glob": true, "grep": true, "write": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected mapped tool %q (got %v)", g, got)
		}
	}
	// single alias
	if w, ok := aliasClaudeTool("Bash"); !ok || w != "bash" {
		t.Fatalf("Bash -> %q ok=%v", w, ok)
	}
	if _, ok := aliasClaudeTool("Task"); ok {
		t.Fatal("Task should be unmapped")
	}
}
```

Create `plugin/claude_md_test.go`:

```go
package plugin

import "testing"

func TestSplitFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter([]byte("---\nname: foo\ndescription: bar\n---\nhello body\nmore\n"))
	if string(fm) != "name: foo\ndescription: bar\n" {
		t.Fatalf("frontmatter wrong: %q", fm)
	}
	if body != "hello body\nmore\n" {
		t.Fatalf("body wrong: %q", body)
	}
	// no frontmatter → empty fm, whole thing is body
	fm, body = splitFrontmatter([]byte("just body\n"))
	if len(fm) != 0 || body != "just body\n" {
		t.Fatalf("no-frontmatter wrong: fm=%q body=%q", fm, body)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./plugin/ -run 'AliasClaudeTools|SplitFrontmatter' -v`.

- [ ] **Step 3: Implement.**

Create `plugin/claude_tools.go`:

```go
package plugin

// claudeToolAliases maps Claude Code tool names to wiz's built-in tool names.
// Unlisted Claude tools (Task, WebFetch, WebSearch, TodoWrite, ...) have no wiz
// equivalent and are dropped during mapping.
var claudeToolAliases = map[string]string{
	"Bash":      "bash",
	"Read":      "read",
	"Write":     "write",
	"Edit":      "edit",
	"MultiEdit": "edit",
	"Glob":      "glob",
	"Grep":      "grep",
}

// aliasClaudeTool returns the wiz tool name for a Claude tool name.
func aliasClaudeTool(name string) (string, bool) {
	w, ok := claudeToolAliases[name]
	return w, ok
}

// aliasClaudeTools maps a list of Claude tool names to wiz tool names, dropping
// unmapped ones and de-duplicating (preserving first-seen order).
func aliasClaudeTools(tools []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tools {
		if w, ok := aliasClaudeTool(t); ok && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
```

Create `plugin/claude_md.go` (splitter only for now):

```go
package plugin

import (
	"bufio"
	"bytes"
	"strings"
)

// splitFrontmatter separates a markdown file's leading `--- ... ---` YAML
// frontmatter from its body. If there is no frontmatter, fm is empty and body
// is the whole input.
func splitFrontmatter(data []byte) (fm []byte, body string) {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !s.Scan() {
		return nil, string(data)
	}
	if strings.TrimRight(s.Text(), "\r") != "---" {
		return nil, string(data)
	}
	var fmBuf, bodyBuf bytes.Buffer
	inBody := false
	for s.Scan() {
		line := s.Text()
		if !inBody && strings.TrimRight(line, "\r") == "---" {
			inBody = true
			continue
		}
		if inBody {
			bodyBuf.WriteString(line)
			bodyBuf.WriteString("\n")
		} else {
			fmBuf.WriteString(line)
			fmBuf.WriteString("\n")
		}
	}
	if !inBody {
		// No closing '---' — treat the whole thing as body (no frontmatter).
		return nil, string(data)
	}
	return fmBuf.Bytes(), bodyBuf.String()
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./plugin/ -run 'AliasClaudeTools|SplitFrontmatter' -v`. Then `go test ./plugin/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add plugin/claude_tools.go plugin/claude_tools_test.go plugin/claude_md.go plugin/claude_md_test.go
git commit -m "feat(plugin): claude tool-alias map + markdown frontmatter splitter"
```

---

## Task 2: skills/, commands/, agents/ loaders

**Files:**
- Modify: `plugin/claude_md.go`
- Test: `plugin/claude_md_test.go` (append)

**Context:** Walk the Claude per-type directories and produce wiz spec types, loading bodies **inline**. Skills: `skills/<name>/SKILL.md` (frontmatter name/description/allowed-tools, body → instructions). Commands: `commands/*.md` (name = filename, frontmatter description, body → prompt with `$ARGUMENTS`→`{{.Args}}`). Agents: `agents/*.md` (frontmatter name/description/tools/model, body → system prompt). Tool lists run through the alias map. Missing directories yield empty slices (not errors).

- [ ] **Step 1: Append failing tests** to `plugin/claude_md_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadClaudeSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "git-commit", "SKILL.md"),
		"---\nname: git-commit\ndescription: make a commit\nallowed-tools: Bash, Read\n---\nDo the commit.\n")
	skills := loadClaudeSkills(dir)
	if len(skills) != 1 || skills[0].Name != "git-commit" || skills[0].Description != "make a commit" {
		t.Fatalf("skills wrong: %+v", skills)
	}
	if skills[0].Instructions.Inline != "Do the commit.\n" {
		t.Fatalf("instructions wrong: %q", skills[0].Instructions.Inline)
	}
	if len(skills[0].Tools) != 2 { // Bash->bash, Read->read
		t.Fatalf("tools not aliased: %+v", skills[0].Tools)
	}
}

func TestLoadClaudeCommands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "commands", "review.md"),
		"---\ndescription: review the diff\n---\nReview this: $ARGUMENTS\n")
	cmds := loadClaudeCommands(dir)
	if len(cmds) != 1 || cmds[0].Name != "review" || cmds[0].Description != "review the diff" {
		t.Fatalf("commands wrong: %+v", cmds)
	}
	if cmds[0].Prompt != "Review this: {{.Args}}\n" {
		t.Fatalf("$ARGUMENTS not translated: %q", cmds[0].Prompt)
	}
}

func TestLoadClaudeAgents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "agents", "researcher.md"),
		"---\nname: researcher\ndescription: digs\ntools: Read, Grep\nmodel: sonnet\n---\nYou research.\n")
	agents := loadClaudeAgents(dir)
	if len(agents) != 1 || agents[0].Name != "researcher" || agents[0].SystemPrompt != "You research.\n" {
		t.Fatalf("agents wrong: %+v", agents)
	}
	if len(agents[0].Tools) != 2 || agents[0].Model != "sonnet" {
		t.Fatalf("agent tools/model wrong: %+v", agents[0])
	}
}
```

(The `writeFile` helper already exists in `plugin/detect_test.go`. Add the `import` block to `claude_md_test.go` if the file doesn't already import `os`/`path/filepath`.)

- [ ] **Step 2: Run, expect FAIL** — `go test ./plugin/ -run 'LoadClaudeSkills|LoadClaudeCommands|LoadClaudeAgents' -v`.

- [ ] **Step 3: Implement** — add to `plugin/claude_md.go` (extend imports to add `os`, `path/filepath`, `strings`, `github.com/mudler/wiz/types`, `gopkg.in/yaml.v3`):

```go
// claudeToolsField parses a Claude `tools`/`allowed-tools` frontmatter value,
// which may be a YAML list or a comma-separated string, into wiz tool names.
type claudeToolsField struct{ tools []string }

func (f *claudeToolsField) UnmarshalYAML(value *yaml.Node) error {
	var list []string
	if err := value.Decode(&list); err == nil {
		f.tools = aliasClaudeTools(list)
		return nil
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parts := strings.Split(s, ",")
	raw := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			raw = append(raw, p)
		}
	}
	f.tools = aliasClaudeTools(raw)
	return nil
}

// loadClaudeSkills reads skills/<name>/SKILL.md files into SkillSpecs (bodies inline).
func loadClaudeSkills(root string) []SkillSpec {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return nil
	}
	var out []SkillSpec
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "skills", e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(data)
		var meta struct {
			Name        string           `yaml:"name"`
			Description string           `yaml:"description"`
			Tools       claudeToolsField `yaml:"allowed-tools"`
		}
		_ = yaml.Unmarshal(fm, &meta)
		name := meta.Name
		if name == "" {
			name = e.Name()
		}
		out = append(out, SkillSpec{
			Name:         name,
			Description:  meta.Description,
			Instructions: InstructionsSpec{Inline: body},
			Tools:        meta.Tools.tools,
		})
	}
	return out
}

// loadClaudeCommands reads commands/*.md files into CommandConfigs.
func loadClaudeCommands(root string) []types.CommandConfig {
	entries, err := os.ReadDir(filepath.Join(root, "commands"))
	if err != nil {
		return nil
	}
	var out []types.CommandConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "commands", e.Name()))
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(data)
		var meta struct {
			Description string `yaml:"description"`
		}
		_ = yaml.Unmarshal(fm, &meta)
		out = append(out, types.CommandConfig{
			Name:        strings.TrimSuffix(e.Name(), ".md"),
			Description: meta.Description,
			Prompt:      strings.ReplaceAll(body, "$ARGUMENTS", "{{.Args}}"),
		})
	}
	return out
}

// loadClaudeAgents reads agents/*.md files into AgentTypeConfigs.
func loadClaudeAgents(root string) []types.AgentTypeConfig {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return nil
	}
	var out []types.AgentTypeConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "agents", e.Name()))
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(data)
		var meta struct {
			Name        string           `yaml:"name"`
			Description string           `yaml:"description"`
			Tools       claudeToolsField `yaml:"tools"`
			Model       string           `yaml:"model"`
		}
		_ = yaml.Unmarshal(fm, &meta)
		name := meta.Name
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, types.AgentTypeConfig{
			Name:         name,
			Description:  meta.Description,
			SystemPrompt: body,
			Tools:        meta.Tools.tools,
			Model:        meta.Model,
		})
	}
	return out
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./plugin/ -run 'LoadClaudeSkills|LoadClaudeCommands|LoadClaudeAgents' -v`. Then `go test ./plugin/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add plugin/claude_md.go plugin/claude_md_test.go
git commit -m "feat(plugin): claude skills/commands/agents directory loaders"
```

---

## Task 3: plugin.json + hooks.json + .mcp.json + assemble

**Files:**
- Modify: `plugin/claude.go` (replace the stub)
- Create: `plugin/claude_hooks.go`
- Test: `plugin/claude_test.go`, `plugin/claude_hooks_test.go`
- Modify: `plugin/detect_test.go` (flip the stub test)

**Context:** Parse `.claude-plugin/plugin.json` (meta), `.mcp.json` (`mcpServers`), and `hooks/hooks.json` (flatten the nested structure, keeping only events wiz supports), then assemble everything into a `Manifest`. Replace `loadClaudeManifest` so a Claude plugin actually loads.

- [ ] **Step 1: Write failing tests.**

Create `plugin/claude_hooks_test.go`:

```go
package plugin

import (
	"path/filepath"
	"testing"
)

func TestLoadClaudeHooks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}] }
    ],
    "PreCompact": [
      { "hooks": [{"type": "command", "command": "ignored.sh"}] }
    ]
  }
}`)
	hooks := loadClaudeHooks(dir)
	if len(hooks) != 1 { // PreCompact is unsupported and skipped
		t.Fatalf("want 1 hook (PreCompact skipped), got %d: %+v", len(hooks), hooks)
	}
	if hooks[0].Event != "PreToolUse" || hooks[0].Matcher != "Bash" || hooks[0].Command != "guard.sh" {
		t.Fatalf("hook mapped wrong: %+v", hooks[0])
	}
}
```

Create `plugin/claude_test.go`:

```go
package plugin

import (
	"path/filepath"
	"testing"
)

func TestLoadClaudeManifestFull(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name": "demo", "version": "1.2.3", "description": "a claude plugin"}`)
	writeFile(t, filepath.Join(dir, "skills", "s1", "SKILL.md"),
		"---\nname: s1\ndescription: skill one\n---\nbody one\n")
	writeFile(t, filepath.Join(dir, ".mcp.json"),
		`{"mcpServers": {"srv": {"command": "mcp-srv", "args": ["serve"]}}}`)

	m, err := loadClaudeManifest(dir)
	if err != nil {
		t.Fatalf("loadClaudeManifest: %v", err)
	}
	if m.Name != "demo" || m.Version != "1.2.3" || m.Description != "a claude plugin" {
		t.Fatalf("meta wrong: %+v", m)
	}
	if len(m.Skills) != 1 || m.Skills[0].Name != "s1" {
		t.Fatalf("skills wrong: %+v", m.Skills)
	}
	if m.MCPServers["srv"].Command != "mcp-srv" {
		t.Fatalf("mcp wrong: %+v", m.MCPServers)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./plugin/ -run 'LoadClaudeHooks|LoadClaudeManifestFull' -v`.

- [ ] **Step 3: Implement.**

Create `plugin/claude_hooks.go`:

```go
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/wiz/types"
)

// claudeSupportedEvents are the hook events wiz maps from Claude (the names
// already match wiz's events). Claude-only events are skipped with a warning.
var claudeSupportedEvents = map[string]bool{
	"SessionStart":     true,
	"UserPromptSubmit": true,
	"PreToolUse":       true,
	"PostToolUse":      true,
	"Stop":             true,
}

// loadClaudeHooks flattens hooks/hooks.json into wiz HookConfigs, keeping only
// supported events.
func loadClaudeHooks(root string) []types.HookConfig {
	data, err := os.ReadFile(filepath.Join(root, "hooks", "hooks.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "wiz: claude hooks.json parse error: %v\n", err)
		return nil
	}
	var out []types.HookConfig
	for event, groups := range doc.Hooks {
		if !claudeSupportedEvents[event] {
			fmt.Fprintf(os.Stderr, "wiz: skipping unsupported claude hook event %q\n", event)
			continue
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type != "" && h.Type != "command" {
					continue
				}
				out = append(out, types.HookConfig{Event: event, Matcher: g.Matcher, Command: h.Command})
			}
		}
	}
	return out
}

// loadClaudeMCP reads .mcp.json (mcpServers) into wiz MCP server configs.
func loadClaudeMCP(root string) map[string]types.MCPServer {
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]types.MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "wiz: claude .mcp.json parse error: %v\n", err)
		return nil
	}
	return doc.MCPServers
}
```

> NOTE: `types.MCPServer` has yaml tags but is unmarshaled here from JSON. Confirm it has `json` tags or that the field names match the JSON keys (`command`/`args`/`env`). If `MCPServer` lacks json tags, the JSON keys are lowercase (`command`) but Go's `encoding/json` matches case-insensitively against exported field names `Command`/`Args`/`Env` — so `command`/`args`/`env` map correctly without explicit json tags. Verify with the test; if `env` doesn't populate, add `json:"..."` tags to `types.MCPServer` (additive, keep the yaml tags).

Replace the body of `plugin/claude.go` (drop the `ErrClaudeUnsupported` stub) with:

```go
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// loadClaudeManifest maps a Claude Code plugin's .claude-plugin/ layout into a
// wiz Manifest: plugin.json meta, skills/, commands/, agents/, hooks/hooks.json,
// and .mcp.json. Bodies are loaded inline; unmappable items are skipped with a
// stderr warning.
func loadClaudeManifest(dir string) (Manifest, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("claude plugin: reading plugin.json: %w", err)
	}
	var meta struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return Manifest{}, fmt.Errorf("claude plugin: parsing plugin.json: %w", err)
	}

	return Manifest{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		MCPServers:  loadClaudeMCP(dir),
		Agents:      loadClaudeAgents(dir),
		Skills:      loadClaudeSkills(dir),
		Commands:    loadClaudeCommands(dir),
		Hooks:       loadClaudeHooks(dir),
	}, nil
}
```

- [ ] **Step 4: Update the stub test.** In `plugin/detect_test.go`, the existing `TestLoadManifestClaudeStub` asserts a Claude-format dir returns an error. It now LOADS. Replace that test with one that asserts a Claude plugin loads:

```go
func TestLoadManifestClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"x","version":"1.0.0"}`)
	m, err := LoadManifest(dir, "0.9.0")
	if err != nil {
		t.Fatalf("expected claude plugin to load, got %v", err)
	}
	if m.Name != "x" || m.root != dir {
		t.Fatalf("claude manifest wrong: %+v (root=%q)", m, m.root)
	}
}
```

(If `ErrClaudeUnsupported` is referenced anywhere else, remove those references — grep `ErrClaudeUnsupported`.)

- [ ] **Step 5: Run, expect PASS** — `go test ./plugin/ -run 'LoadClaudeHooks|LoadClaudeManifestFull|LoadManifestClaude' -v`. Then the FULL package `go test ./plugin/ -v` (no regressions), `go vet ./plugin/`, `go build ./...`.

- [ ] **Step 6: Commit**

```bash
git add plugin/claude.go plugin/claude_hooks.go plugin/claude_test.go plugin/claude_hooks_test.go plugin/detect_test.go
git commit -m "feat(plugin): claude adapter assembles plugin.json/hooks/mcp into a Manifest"
```

---

## Task 4: e2e — install a real Claude-format plugin

**Files:**
- Create: `plugin/e2e_p4_test.go`

**Context:** Real-git proof that an unmodified `.claude-plugin/` layout installs, detects as Claude, maps into the merged config (skills + hooks), and that the hook's `Dir` is stamped to the plugin root (so `${CLAUDE_PLUGIN_ROOT}` resolves). Reuses `gitInitRepoFiles` (P1 e2e, same package).

- [ ] **Step 1: Create `plugin/e2e_p4_test.go`:**

```go
package plugin

import (
	"os/exec"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestEndToEndClaudePlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"claudedemo","version":"0.1.0","description":"d"}`,
		"skills/helper/SKILL.md":     "---\nname: helper\ndescription: a claude skill\n---\nHelp body.\n",
		"hooks/hooks.json":           `{"hooks":{"PreToolUse":[{"matcher":"bash","hooks":[{"type":"command","command":"echo ok"}]}]}}`,
	})

	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "claudedemo" {
		t.Fatalf("claude plugin name wrong: %q", m.Name)
	}
	if err := mgr.SetEnabled("claudedemo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{Prompt: "BASE"}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	// Skill mapped + surfaced in the prompt index.
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "helper" || cfg.Skills[0].Instructions != "Help body.\n" {
		t.Fatalf("claude skill not mapped: %+v", cfg.Skills)
	}
	if !contains(cfg.GetPrompt(), "helper: a claude skill") {
		t.Fatalf("claude skill not in prompt:\n%s", cfg.GetPrompt())
	}
	// Hook mapped with Dir stamped to the plugin root.
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Event != "PreToolUse" || cfg.Hooks[0].Dir == "" {
		t.Fatalf("claude hook not mapped with Dir: %+v", cfg.Hooks)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

> NOTE: `contains`/`indexOf` above avoid adding a `strings` import collision if one already exists in another e2e test file in the package. If `strings` is already imported package-wide via another _test.go and there's no collision, you MAY instead use `strings.Contains(cfg.GetPrompt(), "helper: a claude skill")` and delete the helpers. Pick whichever compiles cleanly; do not duplicate a `contains` that already exists.

- [ ] **Step 2: Run** — `go test ./plugin/ -run TestEndToEndClaudePlugin -v`. Expected PASS. If it fails, investigate the failing unit (Tasks 1–3), report BLOCKED, do not weaken the test.

- [ ] **Step 3: Whole suite** — `go test ./...` and `go vet ./...` → all PASS.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_p4_test.go
git commit -m "test(plugin): e2e install a real .claude-plugin/ layout"
```

---

## Self-Review (completed during planning)

**Spec coverage (P4 scope):**
- `.claude-plugin/plugin.json` meta + `.mcp.json` + `hooks/hooks.json` → Task 3 ✓
- `skills/`/`commands/`/`agents/` directory mapping → Task 2 ✓
- Tool-name alias map → Task 1 ✓
- `$ARGUMENTS` → `{{.Args}}` command translation → Task 2 ✓
- `${CLAUDE_PLUGIN_ROOT}` hook env → already done in P3 (hook `Dir` stamped at merge; dispatcher exports the env) ✓
- Hook event-name filtering with skip-warnings → Task 3 ✓
- e2e (real git, `.claude-plugin/` layout) → Task 4 ✓; binary validation (install a real Claude plugin) is the controller step after Task 4.

**Out of P4 (documented gaps):** `marketplace.json` (P7); `!`bash-exec/`@`file-ref/`$1..$n` command preprocessing (only `$ARGUMENTS`/`$@`); subdir command namespacing (flattened); `model:` aliases carried verbatim. All surface as no-ops/warnings, not failures.

**Type consistency:** the adapter produces the existing internal types — `Manifest{Name,Version,Description,MCPServers,Agents,Skills,Commands,Hooks}`, `SkillSpec`/`InstructionsSpec`, `types.CommandConfig`, `types.AgentTypeConfig`, `types.HookConfig`, `types.MCPServer` — so everything flows through the existing `Validate` + merge + runtime path unchanged. `aliasClaudeTool`/`aliasClaudeTools`/`splitFrontmatter`/`loadClaude{Skills,Commands,Agents,Hooks,MCP}`/`loadClaudeManifest` names are consistent across files and tests.

**Risk note:** Task 3 removes `ErrClaudeUnsupported` and flips `TestLoadManifestClaudeStub` — confirm no other reference to the error remains (grep). The adapter loads bodies inline, so no new resolve/merge runtime is introduced; a Claude plugin is, after loading, indistinguishable from a native one.
