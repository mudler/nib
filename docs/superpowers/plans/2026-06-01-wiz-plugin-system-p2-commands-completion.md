# Wiz Plugin System — P2 (Commands + Unified `/` Completion) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add **slash commands** (plugin/user-defined prompt templates) and a **unified `/` completion surface** in the TUI over three registries — commands, skills, agents — with category tags, fuzzy filtering, Tab-complete, and a ghost-hint. Accepting an entry inserts its canonical token; submitting a `/`-line dispatches: a command expands its prompt template (optionally routed through a sub-agent), `/skill <name>` eagerly loads a skill body into the session prompt, `/agent <name> <task>` directs delegation to a sub-agent.

**Architecture:** Logic is split into pure, unit-tested pieces and a thin TUI wiring layer (mirroring the existing `tui/agents.go` + `tui/agents_test.go` pattern). A new `slash` package resolves a submitted input line into an `Action` (send text / load skill / error) and expands command templates. A new `tui/completion.go` holds the pure completion engine (build items, fuzzy filter, navigation, ghost). `chat.Session` gains `LoadSkill` (append a skill body to the live system prompt). `tui/model.go` wires keys (Tab/↑/↓/Enter) and renders the popup. Commands merge into `types.Config` exactly like skills/agents.

**Tech Stack:** Go 1.24, `text/template` (command expansion), `bubbletea`/`bubbles`/`lipgloss`, standard `testing`. Builds on P0 (merge engine) + P1 (skills).

**Branch:** `feat/plugin-system`. All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** hooks (P3), the real Claude adapter (P4), marketplace (P6). True inline ghost text inside the textarea is out — a dim ghost-hint line is the P2 form. Commands are NOT added to the LLM system prompt (they are a user-facing affordance only).

---

## File Structure

- `types/config.go` — **modify**: add `CommandConfig` type + `Config.Commands []CommandConfig`.
- `plugin/manifest.go` — **modify**: add `Manifest.Commands []types.CommandConfig`; extend `Validate` (command needs name + prompt).
- `plugin/discover.go` — **modify**: add `mergeCommands` (precedence plugins<user, last-wins), call it from `mergeManifests`.
- `plugin/discover_test.go` / `plugin/manifest_test.go` — **modify**: command merge + parse/validate tests.
- `slash/slash.go` — **new**: `Action`, `Expand`, `Resolve` (pure dispatch of a submitted line).
- `slash/slash_test.go` — **new**.
- `chat/session.go` — **modify**: store `skills`, add `LoadSkill(name) (string, error)`.
- `chat/session_skill_test.go` — **new**.
- `tui/completion.go` — **new**: completion engine (`compItem`, `compState`, build/filter/nav/ghost) + `renderCompletion`.
- `tui/completion_test.go` — **new**.
- `tui/model.go` — **modify**: add `completion compState`; wire Tab/↑/↓/Enter + `sync` + popup render + Enter→`slash.Resolve` dispatch.
- `plugin/e2e_p2_test.go` — **new**: real-git e2e for a command-contributing plugin.

---

## Task 1: Command config type + manifest + merge

**Files:**
- Modify: `types/config.go`, `plugin/manifest.go`, `plugin/discover.go`
- Test: `plugin/discover_test.go` (append), `plugin/manifest_test.go` (append)

**Context:** A command is a named prompt template with an optional sub-agent binding. Commands merge by name with precedence plugins<user, exactly like skills (Task P1-T4).

- [ ] **Step 1: Write the failing tests.**

Append to `plugin/manifest_test.go`:

```go
func TestParseAndValidateCommands(t *testing.T) {
	m, err := ParseManifest([]byte("name: demo\ncommands:\n  - name: review\n    description: review the diff\n    prompt: \"Review: {{.Args}}\"\n    agent: explore\n"))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Commands) != 1 || m.Commands[0].Name != "review" || m.Commands[0].Agent != "explore" {
		t.Fatalf("commands wrong: %+v", m.Commands)
	}
	// missing name
	if err := (Manifest{Name: "a", Commands: []types.CommandConfig{{Prompt: "x"}}}).Validate("0.9.0"); err == nil {
		t.Fatal("expected command with no name to be rejected")
	}
	// missing prompt
	if err := (Manifest{Name: "a", Commands: []types.CommandConfig{{Name: "c"}}}).Validate("0.9.0"); err == nil {
		t.Fatal("expected command with no prompt to be rejected")
	}
	// valid
	if err := (Manifest{Name: "a", Commands: []types.CommandConfig{{Name: "c", Prompt: "p"}}}).Validate("0.9.0"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}
```

Append to `plugin/discover_test.go`:

```go
func TestMergeCommands(t *testing.T) {
	cfg := &types.Config{
		Commands: []types.CommandConfig{{Name: "shared", Prompt: "USER"}},
	}
	manifests := []Manifest{
		{Name: "p1", Commands: []types.CommandConfig{
			{Name: "shared", Prompt: "P1"}, // loses to user
			{Name: "p1cmd", Prompt: "one"},
		}},
		{Name: "p2", Commands: []types.CommandConfig{{Name: "p1cmd", Prompt: "two"}}}, // plugin-vs-plugin last wins
	}
	mergeManifests(cfg, manifests)

	var shared, p1cmd *types.CommandConfig
	for i := range cfg.Commands {
		switch cfg.Commands[i].Name {
		case "shared":
			shared = &cfg.Commands[i]
		case "p1cmd":
			p1cmd = &cfg.Commands[i]
		}
	}
	if shared == nil || shared.Prompt != "USER" {
		t.Fatalf("user command overwritten: %+v", shared)
	}
	if p1cmd == nil || p1cmd.Prompt != "two" {
		t.Fatalf("plugin-vs-plugin last-wins failed: %+v", p1cmd)
	}
	if len(cfg.Commands) != 2 {
		t.Fatalf("want 2 commands, got %d: %+v", len(cfg.Commands), cfg.Commands)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./plugin/ -run 'ParseAndValidateCommands|MergeCommands' -v` (CommandConfig/Commands undefined).

- [ ] **Step 3: Implement.**

In `types/config.go`, add the type (near `Skill`):

```go
// CommandConfig is a named slash command: a prompt template (text/template with
// {{.Args}} and {{.CurrentDirectory}}) optionally routed through a sub-agent.
type CommandConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Prompt      string `yaml:"prompt"`
	Agent       string `yaml:"agent,omitempty"`
}
```

Add the field to `Config`, after `Skills`:

```go
	Commands []CommandConfig `yaml:"commands"`
```

In `plugin/manifest.go`, add to the `Manifest` struct after `Skills`:

```go
	Commands []types.CommandConfig `yaml:"commands"`
```

And extend `Validate` (before the final `return checkWizVersion(...)`):

```go
	for i, c := range m.Commands {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("plugin manifest: command #%d missing name", i)
		}
		if strings.TrimSpace(c.Prompt) == "" {
			return fmt.Errorf("plugin manifest: command %q missing prompt", c.Name)
		}
	}
```

In `plugin/discover.go`, call `mergeCommands(cfg, manifests)` at the end of `mergeManifests` (after `mergeSkills`), and add the helper (mirror `mergeSkills`):

```go
// mergeCommands merges plugin commands into cfg with precedence plugins < user:
// a user command of the same name wins; plugin-vs-plugin clash is last-wins
// with a warning.
func mergeCommands(cfg *types.Config, manifests []Manifest) {
	userCmds := map[string]bool{}
	for _, c := range cfg.Commands {
		userCmds[c.Name] = true
	}
	order := []string{}
	byName := map[string]types.CommandConfig{}
	from := map[string]string{}

	for _, m := range manifests {
		for _, c := range m.Commands {
			if userCmds[c.Name] {
				continue
			}
			if _, ok := byName[c.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: command %q from plugin %q overrides plugin %q\n", c.Name, m.Name, from[c.Name])
			} else {
				order = append(order, c.Name)
			}
			byName[c.Name] = c
			from[c.Name] = m.Name
		}
	}
	for _, name := range order {
		cfg.Commands = append(cfg.Commands, byName[name])
	}
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./plugin/ -run 'ParseAndValidateCommands|MergeCommands' -v`. Then `go test ./plugin/ ./types/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add types/config.go plugin/manifest.go plugin/discover.go plugin/manifest_test.go plugin/discover_test.go
git commit -m "feat(plugin): slash command config type + manifest + merge"
```

---

## Task 2: `slash` package — Expand + Resolve

**Files:**
- Create: `slash/slash.go`, `slash/slash_test.go`

**Context:** `Resolve` turns a submitted input line into an `Action`. Non-slash input → send verbatim. `/skill <name>` → load that skill (if it exists). `/agent <name> <task>` → send a delegation directive. `/<cmd> <args>` → expand the command's prompt template; if the command binds an agent, wrap the expansion in a delegation directive. Unknown/invalid → error action. `Expand` runs the command's `text/template` with `{{.Args}}` and `{{.CurrentDirectory}}`.

- [ ] **Step 1: Write the failing test** — create `slash/slash_test.go`:

```go
package slash

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestExpand(t *testing.T) {
	out, err := Expand(types.CommandConfig{Prompt: "Review: {{.Args}}"}, "the diff")
	if err != nil || out != "Review: the diff" {
		t.Fatalf("expand: %q err %v", out, err)
	}
}

func TestResolve(t *testing.T) {
	cmds := []types.CommandConfig{
		{Name: "review", Prompt: "Review: {{.Args}}"},
		{Name: "scan", Prompt: "Scan it", Agent: "explore"},
	}
	skills := []types.Skill{{Name: "git-commit", Instructions: "body"}}
	agents := []types.AgentTypeConfig{{Name: "explore"}}

	// plain text → send verbatim
	if a := Resolve("hello world", cmds, skills, agents); a.Kind != KindSend || a.Text != "hello world" {
		t.Fatalf("plain: %+v", a)
	}
	// command expansion
	if a := Resolve("/review the diff", cmds, skills, agents); a.Kind != KindSend || a.Text != "Review: the diff" {
		t.Fatalf("command: %+v", a)
	}
	// command with agent binding → delegation directive containing the expansion + agent name
	a := Resolve("/scan", cmds, skills, agents)
	if a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "Scan it") {
		t.Fatalf("agent-bound command: %+v", a)
	}
	// /skill known → load
	if a := Resolve("/skill git-commit", cmds, skills, agents); a.Kind != KindLoadSkill || a.Skill != "git-commit" {
		t.Fatalf("skill: %+v", a)
	}
	// /skill unknown → error
	if a := Resolve("/skill nope", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown skill should error: %+v", a)
	}
	// /skill with no name → error
	if a := Resolve("/skill", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("skill with no name should error: %+v", a)
	}
	// /agent known → delegation directive
	if a := Resolve("/agent explore find bugs", cmds, skills, agents); a.Kind != KindSend || !strings.Contains(a.Text, "explore") || !strings.Contains(a.Text, "find bugs") {
		t.Fatalf("agent: %+v", a)
	}
	// /agent unknown → error
	if a := Resolve("/agent ghost x", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown agent should error: %+v", a)
	}
	// unknown slash command → error
	if a := Resolve("/bogus", cmds, skills, agents); a.Kind != KindError {
		t.Fatalf("unknown command should error: %+v", a)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./slash/ -v` (package undefined).

- [ ] **Step 3: Implement** — create `slash/slash.go`:

```go
// Package slash resolves a submitted TUI input line into an action: send text
// to the agent, eagerly load a skill, or report an error. It also expands a
// command's prompt template.
package slash

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/mudler/wiz/types"
)

// Kind enumerates the resolved action types.
type Kind int

const (
	KindSend      Kind = iota // send Text to the agent
	KindLoadSkill             // eagerly load Skill into the session prompt
	KindError                 // report Err to the user, send nothing
)

// Action is the resolved result of a submitted input line.
type Action struct {
	Kind  Kind
	Text  string // for KindSend: the message to send
	Skill string // for KindLoadSkill: the skill name
	Err   string // for KindError
}

// Expand renders a command's prompt template with the given args.
func Expand(c types.CommandConfig, args string) (string, error) {
	tmpl, err := template.New("cmd").Parse(c.Prompt)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	var b bytes.Buffer
	if err := tmpl.Execute(&b, struct {
		Args             string
		CurrentDirectory string
	}{Args: args, CurrentDirectory: cwd}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Resolve maps an input line to an Action. Non-slash input is sent verbatim.
func Resolve(input string, cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) Action {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return Action{Kind: KindSend, Text: input}
	}

	verb, rest := splitVerb(trimmed[1:]) // drop leading '/'

	switch verb {
	case "skill":
		name, _ := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /skill <name>"}
		}
		if !hasSkill(skills, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown skill %q", name)}
		}
		return Action{Kind: KindLoadSkill, Skill: name}
	case "agent":
		name, task := splitVerb(rest)
		if name == "" {
			return Action{Kind: KindError, Err: "usage: /agent <name> <task>"}
		}
		if !hasAgent(agents, name) {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown agent %q", name)}
		}
		return Action{Kind: KindSend, Text: delegation(name, task)}
	default:
		c, ok := findCommand(cmds, verb)
		if !ok {
			return Action{Kind: KindError, Err: fmt.Sprintf("unknown command %q", verb)}
		}
		text, err := Expand(c, rest)
		if err != nil {
			return Action{Kind: KindError, Err: fmt.Sprintf("command %q: %v", verb, err)}
		}
		if strings.TrimSpace(c.Agent) != "" {
			text = delegation(c.Agent, text)
		}
		return Action{Kind: KindSend, Text: text}
	}
}

// delegation builds a directive instructing the agent to delegate to a named
// sub-agent (the runtime already exposes spawn_agent + the agent-type list).
func delegation(agent, task string) string {
	return fmt.Sprintf("Use the %q sub-agent (spawn_agent) to handle the following task, then report its result:\n\n%s", agent, task)
}

// splitVerb splits s into the first whitespace-delimited token and the rest.
func splitVerb(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func hasSkill(skills []types.Skill, name string) bool {
	for _, s := range skills {
		if s.Name == name {
			return true
		}
	}
	return false
}

func hasAgent(agents []types.AgentTypeConfig, name string) bool {
	for _, a := range agents {
		if a.Name == name {
			return true
		}
	}
	return false
}

func findCommand(cmds []types.CommandConfig, name string) (types.CommandConfig, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return types.CommandConfig{}, false
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./slash/ -v` then `go vet ./slash/`.

- [ ] **Step 5: Commit**

```bash
git add slash/slash.go slash/slash_test.go
git commit -m "feat(slash): resolve submitted input to send/load-skill actions"
```

---

## Task 3: Session.LoadSkill (eager skill load)

**Files:**
- Modify: `chat/session.go`
- Test: `chat/session_skill_test.go` (create)

**Context:** `/skill <name>` eagerly injects a skill's body into the session's system prompt so the next turn includes it without a `load_skill` tool call. `NewSession` must capture `cfg.Skills`; `LoadSkill` finds the skill, appends its instructions to `systemPrompt`, and returns a short notice for the transcript.

- [ ] **Step 1: Write the failing test** — create `chat/session_skill_test.go`:

```go
package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestLoadSkill(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{
		Skills: []types.Skill{{Name: "git-commit", Description: "commit helper", Instructions: "SKILL BODY HERE"}},
	}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	before := s.systemPrompt

	notice, err := s.LoadSkill("git-commit")
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if !strings.Contains(notice, "git-commit") {
		t.Fatalf("notice should name the skill: %q", notice)
	}
	if !strings.Contains(s.systemPrompt, "SKILL BODY HERE") {
		t.Fatalf("skill body not appended to system prompt:\n%s", s.systemPrompt)
	}
	if len(s.systemPrompt) <= len(before) {
		t.Fatal("system prompt did not grow")
	}

	if _, err := s.LoadSkill("nope"); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./chat/ -run TestLoadSkill -v` (LoadSkill undefined / no skills field).

- [ ] **Step 3: Implement** in `chat/session.go`.

Add a `skills []types.Skill` field to the `Session` struct (near `systemPrompt`):

```go
	skills        []types.Skill
```

In `NewSession`, set it in the returned `&Session{...}` literal (add the field):

```go
		skills:          cfg.Skills,
```

Add the method (anywhere after `NewSession`):

```go
// LoadSkill appends a named skill's instructions to the session system prompt
// (eager load via /skill), so subsequent turns include it without a load_skill
// tool call. Returns a short notice for the transcript.
func (s *Session) LoadSkill(name string) (string, error) {
	for _, sk := range s.skills {
		if sk.Name == name {
			s.systemPrompt += "\n\n# Skill: " + sk.Name + "\n" + sk.Instructions
			return fmt.Sprintf("Loaded skill %q: %s", sk.Name, sk.Description), nil
		}
	}
	return "", fmt.Errorf("unknown skill %q", name)
}
```

Add `"fmt"` to the `chat/session.go` imports if not already present (it likely is; verify).

- [ ] **Step 4: Run, expect PASS** — `go test ./chat/ -run TestLoadSkill -v`. Then `go test ./chat/ -v` and `go vet ./chat/`.

- [ ] **Step 5: Commit**

```bash
git add chat/session.go chat/session_skill_test.go
git commit -m "feat(chat): Session.LoadSkill eager-injects a skill into the prompt"
```

---

## Task 4: Completion engine (pure)

**Files:**
- Create: `tui/completion.go`, `tui/completion_test.go`

**Context:** The pure heart of the `/` popup: flatten the three registries into tagged items whose `Insert` is the canonical token to place in the input on accept; fuzzy-filter by the typed query; navigate; compute the ghost suffix. No bubbletea here — only data + strings, so it is fully unit-testable (mirrors `tui/agents_test.go`).

- [ ] **Step 1: Write the failing test** — create `tui/completion_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

func sampleRegistries() ([]types.CommandConfig, []types.Skill, []types.AgentTypeConfig) {
	return []types.CommandConfig{{Name: "review", Description: "review diff"}},
		[]types.Skill{{Name: "reviewer", Description: "guidelines"}},
		[]types.AgentTypeConfig{{Name: "explore", Description: "read-only"}}
}

func TestBuildAndFilter(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	items := buildCompItems(cmds, skills, agents)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}

	// "rev" matches review (cmd) + reviewer (skill), not explore.
	got := filterComp(items, "rev")
	if len(got) != 2 {
		t.Fatalf("want 2 matches for 'rev', got %d: %+v", len(got), got)
	}
	// canonical Insert tokens carry the right prefix per category.
	for _, it := range items {
		switch it.Cat {
		case compCmd:
			if it.Insert != "/review " {
				t.Fatalf("cmd insert wrong: %q", it.Insert)
			}
		case compSkill:
			if it.Insert != "/skill reviewer " {
				t.Fatalf("skill insert wrong: %q", it.Insert)
			}
		case compAgent:
			if it.Insert != "/agent explore " {
				t.Fatalf("agent insert wrong: %q", it.Insert)
			}
		}
	}
}

func TestCompStateSyncAndAccept(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)

	c.sync("/rev")
	if !c.active || len(c.matches) != 2 {
		t.Fatalf("expected active with 2 matches, got active=%v matches=%d", c.active, len(c.matches))
	}
	// ghost completes the selected (first) match token beyond the typed input.
	if g := c.ghost("/rev"); g == "" || !strings.HasPrefix("/review ", "/rev") {
		t.Fatalf("ghost wrong: %q", g)
	}

	// Space after the verb closes the popup (verb chosen).
	c.sync("/review the diff")
	if c.active {
		t.Fatal("popup should be inactive once a space is typed")
	}

	// Non-slash input is inactive.
	c.sync("hello")
	if c.active {
		t.Fatal("popup should be inactive for non-slash input")
	}

	// Accept returns the selected item's Insert.
	c.sync("/rev")
	got, ok := c.accept()
	if !ok || got != "/review " {
		t.Fatalf("accept wrong: %q ok=%v", got, ok)
	}
}

func TestCompStateNavigation(t *testing.T) {
	cmds, skills, agents := sampleRegistries()
	var c compState
	c.setRegistries(cmds, skills, agents)
	c.sync("/rev") // 2 matches, sel=0
	c.down()
	if c.sel != 1 {
		t.Fatalf("down: sel=%d", c.sel)
	}
	c.down() // clamp at last
	if c.sel != 1 {
		t.Fatalf("down clamp: sel=%d", c.sel)
	}
	c.up()
	c.up() // clamp at 0
	if c.sel != 0 {
		t.Fatalf("up clamp: sel=%d", c.sel)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./tui/ -run 'BuildAndFilter|CompState' -v` (undefined symbols).

- [ ] **Step 3: Implement** — create `tui/completion.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/wiz/types"

	"github.com/charmbracelet/lipgloss"
)

// compCategory tags a completion item by source registry.
type compCategory string

const (
	compCmd   compCategory = "cmd"
	compSkill compCategory = "skill"
	compAgent compCategory = "agent"
)

// compItem is one entry in the unified `/` completion list.
type compItem struct {
	Cat    compCategory
	Name   string
	Desc   string
	Insert string // canonical token placed in the input on accept (trailing space)
}

// buildCompItems flattens the three registries into tagged completion items.
func buildCompItems(cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) []compItem {
	items := make([]compItem, 0, len(cmds)+len(skills)+len(agents))
	for _, c := range cmds {
		items = append(items, compItem{Cat: compCmd, Name: c.Name, Desc: c.Description, Insert: "/" + c.Name + " "})
	}
	for _, s := range skills {
		items = append(items, compItem{Cat: compSkill, Name: s.Name, Desc: s.Description, Insert: "/skill " + s.Name + " "})
	}
	for _, a := range agents {
		items = append(items, compItem{Cat: compAgent, Name: a.Name, Desc: a.Description, Insert: "/agent " + a.Name + " "})
	}
	return items
}

// filterComp returns items whose name contains the query (case-insensitive
// substring). An empty query returns all items. Order is preserved (cmds, then
// skills, then agents).
func filterComp(items []compItem, query string) []compItem {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]compItem, len(items))
		copy(out, items)
		return out
	}
	var out []compItem
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), q) {
			out = append(out, it)
		}
	}
	return out
}

// compState holds the live `/` completion popup state.
type compState struct {
	all     []compItem
	active  bool
	matches []compItem
	sel     int
}

// setRegistries seeds the completion source from the three registries.
func (c *compState) setRegistries(cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) {
	c.all = buildCompItems(cmds, skills, agents)
}

// sync recomputes active/matches from the current input. The popup is active
// while the user is still typing the verb: input starts with '/' and contains
// no space yet. Once a space is typed (args begin) it deactivates.
func (c *compState) sync(input string) {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t") {
		c.active = false
		c.matches = nil
		c.sel = 0
		return
	}
	c.active = true
	c.matches = filterComp(c.all, input[1:])
	if len(c.matches) == 0 {
		c.active = false
	}
	if c.sel >= len(c.matches) {
		c.sel = 0
	}
}

func (c *compState) up() {
	if c.sel > 0 {
		c.sel--
	}
}

func (c *compState) down() {
	if c.sel < len(c.matches)-1 {
		c.sel++
	}
}

// current returns the selected match.
func (c *compState) current() (compItem, bool) {
	if !c.active || c.sel < 0 || c.sel >= len(c.matches) {
		return compItem{}, false
	}
	return c.matches[c.sel], true
}

// accept returns the selected item's Insert token.
func (c *compState) accept() (string, bool) {
	it, ok := c.current()
	if !ok {
		return "", false
	}
	return it.Insert, true
}

// ghost returns the suffix of the selected item's Insert beyond the current
// input (the dim hint shown to the user). Empty if no clean continuation.
func (c *compState) ghost(input string) string {
	it, ok := c.current()
	if !ok {
		return ""
	}
	if strings.HasPrefix(it.Insert, input) {
		return it.Insert[len(input):]
	}
	return ""
}

// renderCompletion renders the popup: a tagged, selectable list plus a ghost hint.
func renderCompletion(c compState, input string, width int) string {
	if !c.active || len(c.matches) == 0 {
		return ""
	}
	var b strings.Builder
	for i, it := range c.matches {
		tag := completionTagStyle.Render(fmt.Sprintf("[%s]", it.Cat))
		line := fmt.Sprintf("%s %-16s %s", tag, it.Name, dimmedStyle.Render(it.Desc))
		if i == c.sel {
			line = completionSelectedStyle.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if g := c.ghost(input); g != "" {
		b.WriteString(dimmedStyle.Render("Tab → " + input + g))
		b.WriteString("\n")
	}
	return completionBoxStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// completion styles (kept here so the engine file is self-contained).
var (
	completionBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	completionTagStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	completionSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)
```

> NOTE: `dimmedStyle` already exists in `tui/styles.go` (used elsewhere in the package). Reuse it. If a name collision occurs, check `tui/styles.go` and reuse the existing style rather than redefining it.

- [ ] **Step 4: Run, expect PASS** — `go test ./tui/ -run 'BuildAndFilter|CompState' -v`. Then `go test ./tui/ -v` and `go vet ./tui/`.

- [ ] **Step 5: Commit**

```bash
git add tui/completion.go tui/completion_test.go
git commit -m "feat(tui): unified / completion engine (build/filter/nav/ghost)"
```

---

## Task 5: Wire completion + slash dispatch into the TUI model

**Files:**
- Modify: `tui/model.go`

**Context:** Add the completion state to `Model`, seed it from `cfg`, sync it on every keystroke, intercept Tab/↑/↓/Enter when the popup is active, render the popup above the input, and route a submitted line through `slash.Resolve`. This is integration glue — the logic is already tested in Tasks 2 & 4; here we wire and build-verify.

- [ ] **Step 1: Add the field + seed it.**

In the `Model` struct (`tui/model.go`), add near the other UI state:

```go
	// Unified `/` completion state
	completion compState
```

In `NewModel`, before the `return Model{...}`, the model is built inline — add the field to the returned literal and seed registries right after constructing it. Simplest: after the `return Model{...}` is changed to a named var. Replace `return Model{` with `m := Model{` … and at the end (before the final brace’s `}`) keep fields, then after the literal add seeding and `return m`. Concretely, change the tail of `NewModel` from:

```go
	return Model{
		...
		planMode:         false,
	}
}
```

to:

```go
	m := Model{
		...
		planMode:         false,
	}
	m.completion.setRegistries(cfg.Commands, cfg.Skills, cfg.Agents)
	return m
}
```

- [ ] **Step 2: Add the import.**

Add `"github.com/mudler/wiz/slash"` to the `tui/model.go` imports.

- [ ] **Step 3: Intercept popup keys.**

In `Update`, inside `case tea.KeyMsg:` `switch msg.Type {`, add these cases BEFORE `case tea.KeyEnter:` (they only consume when the popup is active; otherwise they fall through to the normal textarea handling after the switch):

```go
		case tea.KeyTab:
			if m.completion.active {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				return m, nil
			}

		case tea.KeyUp:
			if m.completion.active {
				m.completion.up()
				return m, nil
			}

		case tea.KeyDown:
			if m.completion.active {
				m.completion.down()
				return m, nil
			}
```

(Do NOT add a `return` for the inactive branch — letting control fall past the switch runs the normal `m.textarea.Update(msg)` so ↑/↓/Tab still work in the textarea when the popup is closed.)

- [ ] **Step 4: Handle Enter with the popup open, and route via slash.Resolve.**

Replace the body of `case tea.KeyEnter:` so it (a) accepts the completion if the popup is open, and (b) otherwise resolves the input. Find the existing block:

```go
		case tea.KeyEnter:
			if m.loading || !m.sessionReady {
				return m, nil
			}

			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			// Check if we're in plan approval mode
			if m.awaitingPlanApproval {
				return m.handlePlanApproval(true)
			}

			// Check if we're in tool approval mode
			if m.awaitingApproval {
				return m.handleToolApproval(input)
			}

			// Add user message
			m.messages = append(m.messages, ChatMessage{
				Role:    "user",
				Content: input,
			})
			m.textarea.Reset()
			m.loading = true
			m.status = "Thinking..."
			m.updateViewport()

			return m, m.sendMessage(input)
```

Replace it with:

```go
		case tea.KeyEnter:
			// Accept an open completion instead of submitting.
			if m.completion.active {
				if ins, ok := m.completion.accept(); ok {
					m.textarea.SetValue(ins)
					m.completion.sync(ins)
				}
				return m, nil
			}

			if m.loading || !m.sessionReady {
				return m, nil
			}

			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			// Check if we're in plan approval mode
			if m.awaitingPlanApproval {
				return m.handlePlanApproval(true)
			}

			// Check if we're in tool approval mode
			if m.awaitingApproval {
				return m.handleToolApproval(input)
			}

			// Echo the user's literal input, then resolve it (command/skill/agent).
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
			m.textarea.Reset()
			m.completion.sync("")

			action := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
			switch action.Kind {
			case slash.KindError:
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: action.Err})
				m.updateViewport()
				return m, nil
			case slash.KindLoadSkill:
				notice, err := m.session.LoadSkill(action.Skill)
				if err != nil {
					m.messages = append(m.messages, ChatMessage{Role: "error", Content: err.Error()})
				} else {
					m.messages = append(m.messages, ChatMessage{Role: "agent", Content: notice})
				}
				m.updateViewport()
				return m, nil
			default: // slash.KindSend
				m.loading = true
				m.status = "Thinking..."
				m.updateViewport()
				return m, m.sendMessage(action.Text)
			}
```

- [ ] **Step 5: Sync completion after the textarea consumes a keystroke.**

Find this block (around the end of `Update`):

```go
	// Update textarea (only if not loading)
	// Note: We allow updates during approval states so users can type their response
	if !m.loading {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
```

Change it to also re-sync the completion from the (possibly changed) textarea value:

```go
	// Update textarea (only if not loading)
	// Note: We allow updates during approval states so users can type their response
	if !m.loading {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.completion.sync(m.textarea.Value())
	}
```

- [ ] **Step 6: Render the popup above the input.**

In `View`, find the input-area block:

```go
	// Input area
	if m.sessionReady {
		sb.WriteString(m.textarea.View())
	} else {
		sb.WriteString(m.spinner.View() + " Summoning the wizard...")
	}
```

Insert the popup render immediately BEFORE it:

```go
	// `/` completion popup (above the input)
	if comp := renderCompletion(m.completion, strings.TrimSpace(m.textarea.Value()), m.width); comp != "" {
		sb.WriteString(comp)
		sb.WriteString("\n")
	}

	// Input area
	if m.sessionReady {
		sb.WriteString(m.textarea.View())
	} else {
		sb.WriteString(m.spinner.View() + " Summoning the wizard...")
	}
```

- [ ] **Step 7: Build + manual smoke.**

Run: `go build ./...`
Expected: clean build.

Run: `go test ./tui/ -v` and `go vet ./...`
Expected: existing tui tests still pass; vet clean.

- [ ] **Step 8: Commit**

```bash
git add tui/model.go
git commit -m "feat(tui): wire / completion popup + slash dispatch into the model"
```

---

## Task 6: e2e — plugin contributes a command

**Files:**
- Create: `plugin/e2e_p2_test.go`

**Context:** Real-git proof that a command-contributing plugin merges into the config and that `slash.Resolve` expands it. Reuses `gitInitRepoFiles` from the P1 e2e (same package).

- [ ] **Step 1: Create `plugin/e2e_p2_test.go`:**

```go
package plugin

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mudler/wiz/slash"
	"github.com/mudler/wiz/types"
)

func TestEndToEndCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p2demo\n" +
			"commands:\n  - name: review\n    description: review the diff\n    prompt: \"Please review: {{.Args}}\"\n",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p2demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "review" {
		t.Fatalf("command not merged: %+v", cfg.Commands)
	}

	// The command resolves + expands through the slash dispatcher.
	a := slash.Resolve("/review the auth changes", cfg.Commands, cfg.Skills, cfg.Agents)
	if a.Kind != slash.KindSend || !strings.Contains(a.Text, "Please review: the auth changes") {
		t.Fatalf("command did not expand: %+v", a)
	}
}
```

- [ ] **Step 2: Run** — `go test ./plugin/ -run TestEndToEndCommand -v`. Expected PASS. If it fails, investigate the failing unit (Tasks 1–2), report BLOCKED, do not weaken the test.

- [ ] **Step 3: Whole suite** — `go test ./...` and `go vet ./...` → all PASS.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_p2_test.go
git commit -m "test(plugin): e2e command plugin install/merge/expand"
```

---

## Self-Review (completed during planning)

**Spec coverage (P2 scope):**
- Command config + manifest + merge → Task 1 ✓
- Command expansion (`{{.Args}}`/`{{.CurrentDirectory}}`) + slash dispatch (cmd/skill/agent, agent-bound commands) → Task 2 ✓
- `/skill` eager-load (Session.LoadSkill) → Task 3 ✓
- Unified `/` completion engine (3 registries, category tags, fuzzy filter, navigation, ghost) → Task 4 ✓
- TUI wiring: `/` trigger, Tab/↑/↓/Enter, popup render, Enter→Resolve dispatch, built-in `/skill` + `/agent` → Task 5 ✓
- e2e (real git, command plugin) → Task 6 ✓; binary validation is the controller step after Task 6.

**Out of P2 (correctly deferred):** hooks (P3), Claude adapter (P4), marketplace (P6). True inline ghost-in-textarea (P2 uses a dim ghost-hint line). Commands intentionally NOT added to the LLM system prompt.

**Type consistency:** `types.CommandConfig{Name,Description,Prompt,Agent}` used in types/manifest/discover/slash/completion. `slash.Action{Kind,Text,Skill,Err}` with `KindSend/KindLoadSkill/KindError` consistent across slash + model. `compState`/`compItem`/`compCmd|compSkill|compAgent` + `buildCompItems`/`filterComp`/`renderCompletion` consistent across completion + model. `Session.LoadSkill(name) (string, error)` matches its model call site. Merge precedence (user wins; plugin-vs-plugin last-wins) mirrors P0/P1.

**Risk note:** Task 5 is the first interactive popup in `tui/model.go`. All decision logic is in tested pure functions (Tasks 2,4); Task 5 is wiring + build/vet verification. The controller runs a binary smoke (drive the TUI / inspect View) after Task 6.
