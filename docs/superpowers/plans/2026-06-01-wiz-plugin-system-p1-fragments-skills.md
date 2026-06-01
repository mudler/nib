# Wiz Plugin System — P1 (Prompt Fragments + Skills) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two plugin contribution types on top of the P0 spine: **prompt fragments** (extra system-prompt text appended at load time) and **skills** (a system-prompt index plus a `load_skill` MCP tool the agent calls to read a skill's full instructions on demand — progressive disclosure).

**Architecture:** Runtime config carries *resolved* contributions — `Config.PromptFragments []string` and `Config.Skills []Skill` (Name/Description/Instructions/Tools). The plugin manifest carries *authoring* forms — `FragmentSpec` (bare string or `{file}`) and `SkillSpec` (instructions inline or `{file}`) — which the merge resolves (reading files relative to the plugin root, with a traversal guard) into the runtime types. `Config.GetPrompt()` appends a skill index + fragments after the base prompt. A new in-memory `load_skill` MCP server (same pattern as the bash/filesystem servers) is started by `StartTransports` when skills exist and exposes the skill bodies by name.

**Tech Stack:** Go 1.24, `gopkg.in/yaml.v3` (incl. a custom `UnmarshalYAML`), `github.com/modelcontextprotocol/go-sdk/mcp`, standard `testing`. Builds on the P0 `plugin` package + merge engine.

**Branch:** `feat/plugin-system` (continue on it). All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** the `/skill` eager-load slash command and the `/` completion UI are **P2**; hooks are **P3**; the real Claude adapter is **P4**. User-config skills/fragments use *inline* forms only (file-based authoring is a plugin feature in P1).

---

## File Structure

- `types/config.go` — **modify**: add `PromptFragments []string` + `Skills []Skill` to `Config`, a `Skill` type, and append a skill index + fragments in `GetPrompt()`.
- `types/config_test.go` — **new**: tests for `GetPrompt` index/fragment appending.
- `plugin/manifest.go` — **modify**: add `PromptFragments []FragmentSpec` + `Skills []SkillSpec` to `Manifest`; add `FragmentSpec` (custom YAML unmarshal), `SkillSpec`, `InstructionsSpec`; extend `Validate` with structural checks.
- `plugin/resolve.go` — **new**: `resolveFragment`, `resolveSkill`, `readPluginFile` (root-relative read with traversal guard).
- `plugin/resolve_test.go` — **new**.
- `plugin/discover.go` — **modify**: add `mergePromptFragments` + `mergeSkills` helpers, call them from `mergeManifests`.
- `plugin/discover_test.go` — **modify**: add fragment/skill merge precedence tests.
- `mcp/skills.go` — **new**: `StartSkillsMCPServer` + `load_skill` tool (+ testable `skillIndex`/`loadSkillResult`).
- `mcp/skills_test.go` — **new**.
- `mcp/transport.go` — **modify**: start the skills server when `cfg.Skills` is non-empty.
- `plugin/manifest_test.go` — **modify**: add a parse test for fragments/skills authoring forms.

---

## Task 1: Runtime config types + GetPrompt rendering

**Files:**
- Modify: `types/config.go`
- Test: `types/config_test.go` (create)

**Context:** The running `Config` carries already-resolved fragments (plain strings) and skills (with their instruction bodies). `GetPrompt()` renders the base prompt template (unchanged), then appends a skill index and the fragments. The skill index lists name+description and tells the agent to call `load_skill`.

- [ ] **Step 1: Write the failing test** — create `types/config_test.go`:

```go
package types

import (
	"strings"
	"testing"
)

func TestGetPromptAppendsSkillsAndFragments(t *testing.T) {
	c := &Config{
		Prompt: "BASE PROMPT",
		Skills: []Skill{
			{Name: "git-commit", Description: "make a conventional commit"},
			{Name: "deploy", Description: "ship to prod"},
		},
		PromptFragments: []string{"Prefer small PRs.", "Use tabs."},
	}
	got := c.GetPrompt()

	if !strings.Contains(got, "BASE PROMPT") {
		t.Fatalf("base prompt missing:\n%s", got)
	}
	// Skill index: mentions load_skill + each skill name:description.
	if !strings.Contains(got, "load_skill") {
		t.Fatalf("skill index should mention load_skill:\n%s", got)
	}
	if !strings.Contains(got, "git-commit: make a conventional commit") ||
		!strings.Contains(got, "deploy: ship to prod") {
		t.Fatalf("skill index entries missing:\n%s", got)
	}
	// Fragments appended verbatim.
	if !strings.Contains(got, "Prefer small PRs.") || !strings.Contains(got, "Use tabs.") {
		t.Fatalf("fragments missing:\n%s", got)
	}
}

func TestGetPromptNoSkillsNoIndex(t *testing.T) {
	c := &Config{Prompt: "BASE"}
	got := c.GetPrompt()
	if strings.Contains(got, "load_skill") {
		t.Fatalf("should not mention load_skill when no skills:\n%s", got)
	}
	if strings.TrimSpace(got) != "BASE" {
		t.Fatalf("expected just the base prompt, got:\n%q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./types/ -run TestGetPrompt -v`
Expected: FAIL — `Skill`/`Skills`/`PromptFragments` undefined.

- [ ] **Step 3: Write the implementation**

In `types/config.go`, add `"fmt"` and `"strings"` to the imports (it currently imports `bytes`, `os`, `os/user`, `text/template`, and `github.com/Masterminds/sprig/v3`).

Add the `Skill` type near `AgentTypeConfig`:

```go
// Skill is a named, on-demand instruction set. Its Description is listed in the
// system prompt; the agent calls the load_skill tool to read Instructions.
type Skill struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Instructions string   `yaml:"instructions"` // resolved body (inline, or loaded from a plugin file)
	Tools        []string `yaml:"tools,omitempty"`
}
```

Add the two fields to `Config` (after `Agents`):

```go
	Agents          []AgentTypeConfig `yaml:"agents"`
	PromptFragments []string          `yaml:"prompt_fragments"`
	Skills          []Skill           `yaml:"skills"`
```

Replace the `return data.String()` tail of `GetPrompt()` with an append step. The full method becomes:

```go
func (c *Config) GetPrompt() string {
	tmpl, err := template.New("").Funcs(sprig.FuncMap()).Parse(c.Prompt)
	if err != nil {
		return ""
	}

	data := bytes.NewBuffer([]byte{})

	currentDirectory, err := os.Getwd()
	if err != nil {
		currentDirectory = ""
	}
	currentUser, err := user.Current()
	if err != nil {
		currentUser = &user.User{}
	}

	if err := tmpl.Execute(data, struct {
		Config           Config
		CurrentDirectory string
		CurrentUser      string
	}{
		Config:           *c,
		CurrentDirectory: currentDirectory,
		CurrentUser:      currentUser.Username,
	}); err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(data.String())

	if len(c.Skills) > 0 {
		b.WriteString("\n\nAvailable skills — call the load_skill tool with the skill name to read its full instructions before acting on a matching task:\n")
		for _, s := range c.Skills {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
	}

	for _, f := range c.PromptFragments {
		if strings.TrimSpace(f) == "" {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(f)
	}

	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./types/ -run TestGetPrompt -v`
Expected: PASS (both).

- [ ] **Step 5: Run config suite (no regressions)**

Run: `go test ./types/ ./config/ -v`
Expected: PASS — the existing `TestDefaultPromptListsAgentTypesAndDelegation` (config) still passes since the agent listing in the template is unchanged.

- [ ] **Step 6: Commit**

```bash
git add types/config.go types/config_test.go
git commit -m "feat(types): prompt fragments + skills in Config and GetPrompt"
```

---

## Task 2: Manifest authoring specs (fragments + skills)

**Files:**
- Modify: `plugin/manifest.go`
- Test: `plugin/manifest_test.go` (append a test)

**Context:** The manifest's authoring forms support bare-string OR `{file}` fragments and inline-OR-`{file}` skill instructions. `FragmentSpec` needs a custom `UnmarshalYAML` so a YAML scalar maps to its `Text`. `Validate` gains structural checks (skill needs a name + some instructions source; fragment needs text or file).

- [ ] **Step 1: Append the failing test** to `plugin/manifest_test.go`:

```go
func TestParseManifestFragmentsAndSkills(t *testing.T) {
	data := []byte(`
name: demo
prompt_fragments:
  - "bare string fragment"
  - { text: "explicit text fragment" }
  - { file: prompts/extra.md }
skills:
  - name: git-commit
    description: make a commit
    instructions: { inline: "do the thing" }
    tools: [bash]
  - name: deploy
    description: ship it
    instructions: { file: skills/deploy.md }
`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.PromptFragments) != 3 {
		t.Fatalf("want 3 fragments, got %d: %+v", len(m.PromptFragments), m.PromptFragments)
	}
	if m.PromptFragments[0].Text != "bare string fragment" {
		t.Fatalf("bare-string fragment not parsed to Text: %+v", m.PromptFragments[0])
	}
	if m.PromptFragments[1].Text != "explicit text fragment" {
		t.Fatalf("text-map fragment wrong: %+v", m.PromptFragments[1])
	}
	if m.PromptFragments[2].File != "prompts/extra.md" {
		t.Fatalf("file fragment wrong: %+v", m.PromptFragments[2])
	}
	if len(m.Skills) != 2 || m.Skills[0].Name != "git-commit" {
		t.Fatalf("skills wrong: %+v", m.Skills)
	}
	if m.Skills[0].Instructions.Inline != "do the thing" || m.Skills[1].Instructions.File != "skills/deploy.md" {
		t.Fatalf("skill instructions wrong: %+v / %+v", m.Skills[0].Instructions, m.Skills[1].Instructions)
	}
}

func TestValidateFragmentsAndSkills(t *testing.T) {
	// Fragment with neither text nor file → invalid.
	bad := Manifest{Name: "a", PromptFragments: []FragmentSpec{{}}}
	if err := bad.Validate("0.9.0"); err == nil {
		t.Fatal("expected empty fragment to be rejected")
	}
	// Skill with no name → invalid.
	bad = Manifest{Name: "a", Skills: []SkillSpec{{Description: "x", Instructions: InstructionsSpec{Inline: "y"}}}}
	if err := bad.Validate("0.9.0"); err == nil {
		t.Fatal("expected skill with no name to be rejected")
	}
	// Skill with no instructions → invalid.
	bad = Manifest{Name: "a", Skills: []SkillSpec{{Name: "s"}}}
	if err := bad.Validate("0.9.0"); err == nil {
		t.Fatal("expected skill with no instructions to be rejected")
	}
	// Valid.
	ok := Manifest{
		Name:            "a",
		PromptFragments: []FragmentSpec{{Text: "t"}, {File: "f.md"}},
		Skills:          []SkillSpec{{Name: "s", Instructions: InstructionsSpec{File: "s.md"}}},
	}
	if err := ok.Validate("0.9.0"); err != nil {
		t.Fatalf("expected valid manifest, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run 'ParseManifestFragmentsAndSkills|ValidateFragmentsAndSkills' -v`
Expected: FAIL — `FragmentSpec`/`SkillSpec`/`InstructionsSpec` undefined.

- [ ] **Step 3: Write the implementation** in `plugin/manifest.go`.

Add the two fields to the `Manifest` struct (after `Agents`):

```go
	Agents          []types.AgentTypeConfig `yaml:"agents"`
	PromptFragments []FragmentSpec          `yaml:"prompt_fragments"`
	Skills          []SkillSpec             `yaml:"skills"`
```

Add the spec types (place after the `Manifest` struct):

```go
// FragmentSpec is a prompt fragment in a manifest: either a bare YAML string
// (→ Text) or a mapping with text:/file:.
type FragmentSpec struct {
	Text string `yaml:"text"`
	File string `yaml:"file"`
}

// UnmarshalYAML lets a fragment be written as a bare string or a {text,file} map.
func (f *FragmentSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&f.Text)
	}
	type raw FragmentSpec // avoid recursion into this UnmarshalYAML
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*f = FragmentSpec(r)
	return nil
}

// SkillSpec is a skill declared in a manifest.
type SkillSpec struct {
	Name         string           `yaml:"name"`
	Description  string           `yaml:"description"`
	Instructions InstructionsSpec `yaml:"instructions"`
	Tools        []string         `yaml:"tools"`
}

// InstructionsSpec carries a skill body inline or via a file path (relative to
// the plugin root).
type InstructionsSpec struct {
	Inline string `yaml:"inline"`
	File   string `yaml:"file"`
}
```

Extend `Validate` — add these checks before the final `return checkWizVersion(...)`:

```go
	for i, f := range m.PromptFragments {
		if strings.TrimSpace(f.Text) == "" && strings.TrimSpace(f.File) == "" {
			return fmt.Errorf("plugin manifest: prompt_fragment #%d has neither text nor file", i)
		}
	}
	for i, s := range m.Skills {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("plugin manifest: skill #%d missing name", i)
		}
		if strings.TrimSpace(s.Instructions.Inline) == "" && strings.TrimSpace(s.Instructions.File) == "" {
			return fmt.Errorf("plugin manifest: skill %q has no instructions (inline or file)", s.Name)
		}
	}
```

(`yaml.Node` needs the yaml import, already present as `gopkg.in/yaml.v3`. `strings`/`fmt` are already imported by Validate.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run 'ParseManifestFragmentsAndSkills|ValidateFragmentsAndSkills' -v`
Expected: PASS. Then full package: `go test ./plugin/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add plugin/manifest.go plugin/manifest_test.go
git commit -m "feat(plugin): manifest authoring specs for fragments + skills"
```

---

## Task 3: Resolution (specs → runtime, with traversal guard)

**Files:**
- Create: `plugin/resolve.go`
- Test: `plugin/resolve_test.go`

**Context:** During merge, manifest specs become runtime values: a `FragmentSpec` → a string, a `SkillSpec` → a `types.Skill` with its instructions body loaded. File paths are read relative to the plugin root, and a guard refuses paths that escape the root (defense-in-depth beyond the P0 name guard).

- [ ] **Step 1: Write the failing test** — create `plugin/resolve_test.go`:

```go
package plugin

import (
	"path/filepath"
	"testing"
)

func TestResolveFragment(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "frag.md"), "FILE BODY")

	// Inline text wins.
	got, err := resolveFragment(FragmentSpec{Text: "inline"}, root)
	if err != nil || got != "inline" {
		t.Fatalf("inline: got %q err %v", got, err)
	}
	// File read relative to root.
	got, err = resolveFragment(FragmentSpec{File: "frag.md"}, root)
	if err != nil || got != "FILE BODY" {
		t.Fatalf("file: got %q err %v", got, err)
	}
	// Neither → error.
	if _, err := resolveFragment(FragmentSpec{}, root); err == nil {
		t.Fatal("expected error for empty fragment")
	}
	// Traversal escape → error.
	if _, err := resolveFragment(FragmentSpec{File: "../escape.md"}, root); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestResolveSkill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "skills", "s.md"), "SKILL BODY")

	// Inline instructions.
	sk, err := resolveSkill(SkillSpec{Name: "a", Description: "d", Instructions: InstructionsSpec{Inline: "body"}, Tools: []string{"bash"}}, root)
	if err != nil || sk.Name != "a" || sk.Instructions != "body" || sk.Description != "d" || len(sk.Tools) != 1 {
		t.Fatalf("inline skill wrong: %+v err %v", sk, err)
	}
	// File instructions.
	sk, err = resolveSkill(SkillSpec{Name: "b", Instructions: InstructionsSpec{File: "skills/s.md"}}, root)
	if err != nil || sk.Instructions != "SKILL BODY" {
		t.Fatalf("file skill wrong: %+v err %v", sk, err)
	}
	// No instructions → error.
	if _, err := resolveSkill(SkillSpec{Name: "c"}, root); err == nil {
		t.Fatal("expected error for skill with no instructions")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run 'ResolveFragment|ResolveSkill' -v`
Expected: FAIL — `resolveFragment`/`resolveSkill` undefined.

- [ ] **Step 3: Write the implementation** — create `plugin/resolve.go`:

```go
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/wiz/types"
)

// resolveFragment returns a fragment's text: inline Text when set, otherwise the
// contents of File read relative to the plugin root.
func resolveFragment(f FragmentSpec, root string) (string, error) {
	if strings.TrimSpace(f.Text) != "" {
		return f.Text, nil
	}
	if f.File == "" {
		return "", fmt.Errorf("prompt fragment has neither text nor file")
	}
	b, err := readPluginFile(root, f.File)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveSkill converts a manifest SkillSpec into a runtime types.Skill, loading
// the instructions body (inline, or from a file relative to the plugin root).
func resolveSkill(s SkillSpec, root string) (types.Skill, error) {
	body := s.Instructions.Inline
	if strings.TrimSpace(body) == "" {
		if s.Instructions.File == "" {
			return types.Skill{}, fmt.Errorf("skill %q has no instructions", s.Name)
		}
		b, err := readPluginFile(root, s.Instructions.File)
		if err != nil {
			return types.Skill{}, err
		}
		body = string(b)
	}
	return types.Skill{
		Name:         s.Name,
		Description:  s.Description,
		Instructions: body,
		Tools:        s.Tools,
	}, nil
}

// readPluginFile reads a path relative to a plugin root, refusing paths that
// escape the root (defense in depth against ../ traversal in a manifest).
func readPluginFile(root, rel string) ([]byte, error) {
	rootAbs := filepath.Clean(root)
	full := filepath.Clean(filepath.Join(rootAbs, rel))
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+string(os.PathSeparator)) {
		return nil, fmt.Errorf("file %q escapes plugin directory", rel)
	}
	return os.ReadFile(full)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run 'ResolveFragment|ResolveSkill' -v`
Expected: PASS. Then `go test ./plugin/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add plugin/resolve.go plugin/resolve_test.go
git commit -m "feat(plugin): resolve fragment/skill specs with traversal guard"
```

---

## Task 4: Merge fragments + skills into Config

**Files:**
- Modify: `plugin/discover.go`
- Test: `plugin/discover_test.go` (append tests)

**Context:** `mergeManifests` already merges MCP servers + agents. Add two helpers that accumulate prompt fragments and merge skills with precedence **plugins < user** (user skills, already in `cfg.Skills`, win; plugin-vs-plugin name clash → last wins with a warning). A spec that fails to resolve is skipped with a warning.

- [ ] **Step 1: Append the failing test** to `plugin/discover_test.go`:

```go
func TestMergeFragmentsAndSkills(t *testing.T) {
	root := t.TempDir() // plugins share a root here for simplicity
	cfg := &types.Config{
		Skills: []types.Skill{{Name: "shared", Instructions: "USER BODY"}},
	}
	manifests := []Manifest{
		{
			Name:            "p1",
			root:            root,
			PromptFragments: []FragmentSpec{{Text: "frag-from-p1"}},
			Skills: []SkillSpec{
				{Name: "shared", Instructions: InstructionsSpec{Inline: "P1 BODY"}}, // loses to user
				{Name: "p1skill", Instructions: InstructionsSpec{Inline: "p1 body"}},
			},
		},
		{
			Name:            "p2",
			root:            root,
			PromptFragments: []FragmentSpec{{Text: "frag-from-p2"}},
			Skills:          []SkillSpec{{Name: "p1skill", Instructions: InstructionsSpec{Inline: "p2 overrides"}}}, // plugin-vs-plugin: last wins
		},
	}

	mergeManifests(cfg, manifests)

	// Fragments accumulated in order.
	if len(cfg.PromptFragments) != 2 || cfg.PromptFragments[0] != "frag-from-p1" || cfg.PromptFragments[1] != "frag-from-p2" {
		t.Fatalf("fragments wrong: %+v", cfg.PromptFragments)
	}
	// User skill "shared" preserved with its body (plugin did not override).
	var shared *types.Skill
	var p1skill *types.Skill
	for i := range cfg.Skills {
		switch cfg.Skills[i].Name {
		case "shared":
			shared = &cfg.Skills[i]
		case "p1skill":
			p1skill = &cfg.Skills[i]
		}
	}
	if shared == nil || shared.Instructions != "USER BODY" {
		t.Fatalf("user skill overwritten: %+v", shared)
	}
	// plugin-vs-plugin: p2 won.
	if p1skill == nil || p1skill.Instructions != "p2 overrides" {
		t.Fatalf("plugin-vs-plugin last-wins failed: %+v", p1skill)
	}
	// exactly the user skill + one merged plugin skill.
	if len(cfg.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(cfg.Skills), cfg.Skills)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run TestMergeFragmentsAndSkills -v`
Expected: FAIL — fragments/skills not merged (helpers don't exist yet).

- [ ] **Step 3: Write the implementation** in `plugin/discover.go`.

Add `"strings"` to the imports. Call the two new helpers at the end of `mergeManifests`, immediately after the existing `cfg.Agents = append(pluginAgents, cfg.Agents...)` line:

```go
	cfg.Agents = append(pluginAgents, cfg.Agents...)

	mergePromptFragments(cfg, manifests)
	mergeSkills(cfg, manifests)
}
```

Then add the helpers below `mergeManifests`:

```go
// mergePromptFragments appends each enabled plugin's resolved prompt fragments
// to cfg (accumulate; fragments never override).
func mergePromptFragments(cfg *types.Config, manifests []Manifest) {
	for _, m := range manifests {
		for _, fs := range m.PromptFragments {
			text, err := resolveFragment(fs, m.root)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wiz: plugin %q prompt fragment: %v\n", m.Name, err)
				continue
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			cfg.PromptFragments = append(cfg.PromptFragments, text)
		}
	}
}

// mergeSkills merges plugin skills into cfg with precedence plugins < user: a
// user skill of the same name wins; a plugin-vs-plugin name clash is last-wins
// with a warning. Resolution failures are skipped with a warning.
func mergeSkills(cfg *types.Config, manifests []Manifest) {
	userSkills := map[string]bool{}
	for _, s := range cfg.Skills {
		userSkills[s.Name] = true
	}
	order := []string{}
	byName := map[string]types.Skill{}
	from := map[string]string{}

	for _, m := range manifests {
		for _, ss := range m.Skills {
			if userSkills[ss.Name] {
				continue // user wins
			}
			skill, err := resolveSkill(ss, m.root)
			if err != nil {
				fmt.Fprintf(os.Stderr, "wiz: plugin %q skill %q: %v\n", m.Name, ss.Name, err)
				continue
			}
			if _, ok := byName[ss.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: skill %q from plugin %q overrides plugin %q\n", ss.Name, m.Name, from[ss.Name])
			} else {
				order = append(order, ss.Name)
			}
			byName[ss.Name] = skill
			from[ss.Name] = m.Name
		}
	}
	for _, name := range order {
		cfg.Skills = append(cfg.Skills, byName[name])
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run TestMergeFragmentsAndSkills -v`
Expected: PASS. Then full package + the existing precedence test: `go test ./plugin/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add plugin/discover.go plugin/discover_test.go
git commit -m "feat(plugin): merge prompt fragments + skills with precedence"
```

---

## Task 5: load_skill in-memory MCP server

**Files:**
- Create: `mcp/skills.go`
- Test: `mcp/skills_test.go`

**Context:** A new in-memory MCP server (same pattern as `mcp/shell.go`) exposes a single `load_skill(name)` tool returning a skill's instructions. The core logic is factored into pure functions (`skillIndex`, `loadSkillResult`) so it is unit-testable directly, mirroring how `mcp/filesystem_test.go` calls handlers.

- [ ] **Step 1: Write the failing test** — create `mcp/skills_test.go`:

```go
package mcp

import (
	"testing"

	"github.com/mudler/wiz/types"
)

func TestLoadSkillResult(t *testing.T) {
	index, names := skillIndex([]types.Skill{
		{Name: "git-commit", Instructions: "COMMIT BODY"},
		{Name: "deploy", Instructions: "DEPLOY BODY"},
	})
	if len(names) != 2 {
		t.Fatalf("want 2 names, got %v", names)
	}

	out := loadSkillResult(index, loadSkillInput{Name: "git-commit"})
	if !out.Found || out.Instructions != "COMMIT BODY" {
		t.Fatalf("known skill: %+v", out)
	}

	out = loadSkillResult(index, loadSkillInput{Name: "nope"})
	if out.Found || out.Error == "" {
		t.Fatalf("unknown skill should report not found: %+v", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./mcp/ -run TestLoadSkillResult -v`
Expected: FAIL — `skillIndex`/`loadSkillResult`/`loadSkillInput` undefined.

- [ ] **Step 3: Write the implementation** — create `mcp/skills.go`:

```go
package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/wiz/types"
)

// loadSkillInput is the argument to the load_skill tool.
type loadSkillInput struct {
	Name string `json:"name" jsonschema:"the name of the skill to load"`
}

// loadSkillOutput is the result of the load_skill tool.
type loadSkillOutput struct {
	Name         string `json:"name" jsonschema:"the requested skill name"`
	Instructions string `json:"instructions" jsonschema:"the skill's full instructions"`
	Found        bool   `json:"found" jsonschema:"whether the skill was found"`
	Error        string `json:"error,omitempty" jsonschema:"error message if not found"`
}

// skillIndex builds a name→instructions map and the ordered list of names.
func skillIndex(skills []types.Skill) (map[string]string, []string) {
	index := make(map[string]string, len(skills))
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		index[s.Name] = s.Instructions
		names = append(names, s.Name)
	}
	return index, names
}

// loadSkillResult looks a skill up by name in the index.
func loadSkillResult(index map[string]string, in loadSkillInput) loadSkillOutput {
	body, ok := index[in.Name]
	if !ok {
		return loadSkillOutput{Name: in.Name, Found: false, Error: fmt.Sprintf("unknown skill %q", in.Name)}
	}
	return loadSkillOutput{Name: in.Name, Instructions: body, Found: true}
}

// StartSkillsMCPServer runs an in-memory MCP server exposing a load_skill tool
// that returns a skill's full instructions by name.
func StartSkillsMCPServer(ctx context.Context, transport mcp.Transport, skills []types.Skill) error {
	index, names := skillIndex(skills)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "skills",
		Version: "v1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "load_skill",
		Description: fmt.Sprintf("Load the full instructions for a skill by name, then follow them. Available skills: %v.", names),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in loadSkillInput) (*mcp.CallToolResult, loadSkillOutput, error) {
		return nil, loadSkillResult(index, in), nil
	})

	return server.Run(ctx, transport)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./mcp/ -run TestLoadSkillResult -v`
Expected: PASS. Then `go test ./mcp/ -v` and `go vet ./mcp/`.

- [ ] **Step 5: Commit**

```bash
git add mcp/skills.go mcp/skills_test.go
git commit -m "feat(mcp): load_skill in-memory MCP server"
```

---

## Task 6: Wire the skills server into StartTransports

**Files:**
- Modify: `mcp/transport.go`
- Test: `mcp/transport_test.go` (create)

**Context:** `StartTransports` starts the in-memory bash + filesystem servers, then external plugin MCP servers. Add the skills server when `cfg.Skills` is non-empty, so `load_skill` becomes available to the agent exactly when there are skills to load.

- [ ] **Step 1: Write the failing test** — create `mcp/transport_test.go`:

```go
package mcp

import (
	"context"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestStartTransportsIncludesSkillsWhenPresent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No skills → bash + filesystem only (2 transports).
	base, err := StartTransports(ctx, types.Config{})
	if err != nil {
		t.Fatalf("StartTransports (no skills): %v", err)
	}
	withoutSkills := len(base)

	// With skills → exactly one more transport (the skills server).
	withSkills, err := StartTransports(ctx, types.Config{
		Skills: []types.Skill{{Name: "s", Instructions: "body"}},
	})
	if err != nil {
		t.Fatalf("StartTransports (skills): %v", err)
	}
	if len(withSkills) != withoutSkills+1 {
		t.Fatalf("expected one extra transport for skills, got %d vs %d", len(withSkills), withoutSkills)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./mcp/ -run TestStartTransportsIncludesSkillsWhenPresent -v`
Expected: FAIL — skills transport not added (counts equal).

- [ ] **Step 3: Write the implementation** in `mcp/transport.go`.

After the filesystem server block (where `transports := []mcp.Transport{bashMCPServerClient, filesystemMCPServerClient}` is built), and BEFORE the `for _, c := range cfg.MCPServers` loop, insert:

```go
	// Skills server (load_skill tool) — only when the config carries skills.
	if len(cfg.Skills) > 0 {
		skillsServerTransport, skillsServerClient := mcp.NewInMemoryTransports()
		go func() {
			if err := StartSkillsMCPServer(ctx, skillsServerTransport, cfg.Skills); err != nil {
				fmt.Fprintf(os.Stderr, "Skills MCP server error: %v\n", err)
			}
		}()
		transports = append(transports, skillsServerClient)
	}
```

(`fmt` and `os` are already imported in `transport.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./mcp/ -run TestStartTransportsIncludesSkillsWhenPresent -v`
Expected: PASS. Then `go test ./mcp/ -v` and `go vet ./mcp/`.

- [ ] **Step 5: Commit**

```bash
git add mcp/transport.go mcp/transport_test.go
git commit -m "feat(mcp): start load_skill server when config has skills"
```

---

## Task 7: End-to-end — plugin contributes a fragment + file-based skill

**Files:**
- Create: `plugin/e2e_p1_test.go`

**Context:** A real-`git` integration test proving the P1 path: a plugin repo with a prompt fragment and a file-based skill is installed, enabled, and `Apply`-ed; the resolved fragment and skill (with its file body) land in the config and surface in `GetPrompt()`. Reuses the `gitInitRepoFiles` style from the P0 e2e (here generalized to write multiple files).

- [ ] **Step 1: Write the test** — create `plugin/e2e_p1_test.go`:

```go
package plugin

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/wiz/types"
)

func gitInitRepoFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for rel, content := range files {
		writeFile(t, filepath.Join(repo, rel), content)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestEndToEndFragmentAndSkill(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p1demo\n" +
			"prompt_fragments:\n  - \"FRAGMENT MARKER\"\n" +
			"skills:\n  - name: demoskill\n    description: a demo skill\n    instructions: { file: skills/demo.md }\n",
		"skills/demo.md": "SKILL FILE BODY",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p1demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := &types.Config{Prompt: "BASE"}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}

	// Fragment resolved + merged.
	if len(cfg.PromptFragments) != 1 || cfg.PromptFragments[0] != "FRAGMENT MARKER" {
		t.Fatalf("fragment not merged: %+v", cfg.PromptFragments)
	}
	// Skill resolved with its FILE body.
	if len(cfg.Skills) != 1 || cfg.Skills[0].Name != "demoskill" || cfg.Skills[0].Instructions != "SKILL FILE BODY" {
		t.Fatalf("skill not merged with file body: %+v", cfg.Skills)
	}
	// Both surface in the rendered prompt.
	prompt := cfg.GetPrompt()
	if !strings.Contains(prompt, "FRAGMENT MARKER") {
		t.Fatalf("fragment not in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "demoskill: a demo skill") || !strings.Contains(prompt, "load_skill") {
		t.Fatalf("skill index not in prompt:\n%s", prompt)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./plugin/ -run TestEndToEndFragmentAndSkill -v`
Expected: PASS. If it FAILS, do not weaken the test — investigate the failing unit (Tasks 1–6), report BLOCKED with detail, and stop.

- [ ] **Step 3: Run the whole suite**

Run: `go test ./...` and `go vet ./...`
Expected: ALL PASS, vet clean.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_p1_test.go
git commit -m "test(plugin): e2e fragment + file-based skill install/merge/prompt"
```

---

## Self-Review (completed during planning)

**Spec coverage (P1 scope):**
- Prompt fragments: `Config.PromptFragments`, manifest `FragmentSpec` (bare-string or file), resolution, merge (accumulate), `GetPrompt` append → Tasks 1,2,3,4 ✓
- Skills: `Config.Skills`/`Skill`, system-prompt **index** (Task 1 GetPrompt), manifest `SkillSpec` + `InstructionsSpec`, resolution (inline/file + traversal guard), merge (plugins < user, plugin-vs-plugin last-wins) → Tasks 1,2,3,4 ✓
- `load_skill` in-memory MCP tool + wiring into `StartTransports` → Tasks 5,6 ✓
- e2e proof (real git, file-based skill) → Task 7 ✓

**Out of P1 (correctly deferred):** `/skill` eager-load + `/` completion (P2), hooks (P3), Claude adapter (P4). User-config file-based authoring (P1 supports inline for user config; plugin files only).

**Type consistency:** `types.Skill{Name,Description,Instructions,Tools}` used identically in types, resolve, discover, mcp. Manifest specs `FragmentSpec{Text,File}`, `SkillSpec{Name,Description,Instructions,Tools}`, `InstructionsSpec{Inline,File}` consistent across manifest/resolve/discover. `StartSkillsMCPServer(ctx, transport, []types.Skill)` and `loadSkillResult(index, loadSkillInput)` signatures match call sites. The merge precedence (user wins; plugin-vs-plugin last-wins) mirrors the P0 agent/mcp convention.

**Note for execution:** there is a separate binary-e2e validation step after Task 7 (mirroring P0) — install a fragment+skill plugin into a real `wiz` and confirm the fragment appears in the system prompt sent to a stub LLM and that the `load_skill` tool returns the body. That is driven by the controller, not a plan task.
