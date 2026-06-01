# Wiz Plugin System — P0 (Spine) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the plugin spine — a `plugin` package with a format-agnostic manifest model, a format-detection seam (native `wiz-plugin.yaml` adapter live, Claude adapter stubbed), an on-disk registry, `wiz plugin install/list/update/enable/disable/remove`, and discovery that merges enabled plugins' `mcp_servers` + `agents` into the effective config — so installable native plugins can ship MCP servers and sub-agent types.

**Architecture:** A new `plugin` package owns everything (manifest parse/validate, detection, registry, install via `git`, discovery+merge). It imports only `types` (no import cycle: `config` imports `plugin`). `config.Load()` calls `plugin.Apply(&cfg, …)` to merge plugin contributions before the existing `MergeAgentTypes` runs, with precedence **built-in defaults < plugins < user**. A new `wiz plugin …` CLI subcommand (in `cmd`) is dispatched from `main.go` before flag parsing.

**Tech Stack:** Go 1.24, `gopkg.in/yaml.v3`, `github.com/Masterminds/semver/v3` (promote from indirect), `os/exec` + system `git`, standard `testing`. Reuses existing `types.MCPServer` and `types.AgentTypeConfig`.

**Branch:** `feat/plugin-system` (already created). All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** prompt fragments, skills/`load_skill`, commands/`/` completion, hooks, and the real Claude adapter are later phases (P1–P5). P0's manifest carries only `mcp_servers` + `agents`; later phases add fields additively.

---

## File Structure

- `plugin/manifest.go` — **new**: `Manifest` struct (format-agnostic model), `ParseManifest` (native YAML), `Validate`, `checkWizVersion`.
- `plugin/detect.go` — **new**: `Format` enum, `DetectFormat`, `LoadManifest` (detect → adapter → validate).
- `plugin/claude.go` — **new**: `loadClaudeManifest` P0 stub returning `ErrClaudeUnsupported`.
- `plugin/paths.go` — **new**: `BaseDir`, `PluginsDir`, `pluginDir`, `registryPath`.
- `plugin/registry.go` — **new**: `Entry`, `Registry`, `LoadRegistry`, `Save`, `Find`, `Upsert`, `Remove`.
- `plugin/manager.go` — **new**: `Manager`, `Install`, `Update`, `Remove`, `SetEnabled`, `List`; injectable `gitClone`/`gitPull` vars.
- `plugin/discover.go` — **new**: `EnabledManifests`, `Apply`, `mergeManifests` (precedence).
- `cmd/plugin.go` — **new**: `RunPluginCommand` + subcommand handlers + install summary + confirm.
- `config/config.go` — **modify**: call `plugin.Apply` in `Load()` before `MergeAgentTypes`.
- `main.go` — **modify**: dispatch `plugin` subcommand at the top of `main()`.
- `go.mod` — **modify**: promote `github.com/Masterminds/semver/v3` to a direct dependency.

Test files (created alongside): `plugin/manifest_test.go`, `plugin/detect_test.go`, `plugin/registry_test.go`, `plugin/manager_test.go`, `plugin/discover_test.go`, `plugin/e2e_test.go`, `cmd/plugin_test.go`, `config/plugin_load_test.go`.

---

## Task 1: Manifest model + native YAML parse

**Files:**
- Create: `plugin/manifest.go`
- Test: `plugin/manifest_test.go`

**Context:** The `Manifest` is the normalized, format-agnostic model both adapters (native + Claude) produce. P0 carries only `mcp_servers` and `agents`, reusing existing `types` structs.

- [ ] **Step 1: Write the failing test**

Create `plugin/manifest_test.go`:

```go
package plugin

import "testing"

func TestParseManifestNative(t *testing.T) {
	data := []byte(`
name: demo
version: 0.1.0
description: a demo plugin
wiz_version: ">=0.0.0"
mcp_servers:
  github:
    command: gh-mcp
    args: ["serve"]
    env: { TOKEN: abc }
agents:
  - name: researcher
    description: digs through docs
    system_prompt: be thorough
    tools: [bash]
`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "demo" || m.Version != "0.1.0" {
		t.Fatalf("meta wrong: %+v", m)
	}
	if got := m.MCPServers["github"].Command; got != "gh-mcp" {
		t.Fatalf("mcp command = %q", got)
	}
	if len(m.Agents) != 1 || m.Agents[0].Name != "researcher" {
		t.Fatalf("agents wrong: %+v", m.Agents)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run TestParseManifestNative -v`
Expected: FAIL — package/`ParseManifest` undefined (build error).

- [ ] **Step 3: Write minimal implementation**

Create `plugin/manifest.go`:

```go
// Package plugin loads, installs, and merges wiz plugins. A plugin is a git
// repo whose contributions (MCP servers, sub-agent types, and — in later
// phases — prompt fragments, skills, commands, hooks) are normalized into a
// format-agnostic Manifest and merged into the effective wiz config.
package plugin

import (
	"github.com/mudler/wiz/types"

	"gopkg.in/yaml.v3"
)

// Manifest is the normalized, format-agnostic representation of a plugin.
// Both the native (wiz-plugin.yaml) and Claude (.claude-plugin/) adapters
// produce a Manifest. P0 carries only the config-driven contributions.
type Manifest struct {
	Name        string                     `yaml:"name"`
	Version     string                     `yaml:"version"`
	Description string                     `yaml:"description"`
	WizVersion  string                     `yaml:"wiz_version"`
	MCPServers  map[string]types.MCPServer `yaml:"mcp_servers"`
	Agents      []types.AgentTypeConfig    `yaml:"agents"`

	// root is the plugin's install directory. Set by the loader, never parsed.
	// (Unexported: yaml ignores it; no struct tag, to keep `go vet` quiet.)
	root string
}

// ParseManifest parses a native wiz-plugin.yaml document.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run TestParseManifestNative -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add plugin/manifest.go plugin/manifest_test.go
git commit -m "feat(plugin): manifest model + native YAML parse"
```

---

## Task 2: Manifest validation + wiz_version constraint

**Files:**
- Modify: `plugin/manifest.go`
- Modify: `go.mod` (promote semver to direct)
- Test: `plugin/manifest_test.go`

**Context:** Validation rejects malformed manifests early so one bad plugin can't break the others. `wiz_version` is a semver constraint checked against the running build; dev builds (empty/non-semver `internal.Version`) skip the check.

- [ ] **Step 1: Write the failing test**

Append to `plugin/manifest_test.go`:

```go
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		m       Manifest
		wiz     string
		wantErr bool
	}{
		{"ok", Manifest{Name: "a"}, "0.9.0", false},
		{"missing name", Manifest{}, "0.9.0", true},
		{"mcp missing command", Manifest{Name: "a", MCPServers: map[string]types.MCPServer{"x": {}}}, "0.9.0", true},
		{"agent missing name", Manifest{Name: "a", Agents: []types.AgentTypeConfig{{}}}, "0.9.0", true},
		{"wiz constraint met", Manifest{Name: "a", WizVersion: ">=0.5.0"}, "0.9.0", false},
		{"wiz constraint unmet", Manifest{Name: "a", WizVersion: ">=1.0.0"}, "0.9.0", true},
		{"dev build skips constraint", Manifest{Name: "a", WizVersion: ">=1.0.0"}, "", false},
		{"prefixed v version", Manifest{Name: "a", WizVersion: ">=0.5.0"}, "v0.9.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.m.Validate(c.wiz)
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}
```

(Add `"github.com/mudler/wiz/types"` to the test file's imports.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run TestValidate -v`
Expected: FAIL — `Validate` undefined.

- [ ] **Step 3: Promote semver to a direct dependency**

Run:

```bash
go get github.com/Masterminds/semver/v3
```

Expected: `go.mod` lists `github.com/Masterminds/semver/v3` as a direct require (no `// indirect`).

- [ ] **Step 4: Write minimal implementation**

Add to `plugin/manifest.go` (extend imports to include `errors`, `fmt`, `strings`, and `github.com/Masterminds/semver/v3`):

```go
import (
	"errors"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/mudler/wiz/types"

	"gopkg.in/yaml.v3"
)

// Validate checks required fields and the wiz_version constraint. wizVersion is
// the running build version (internal.Version); empty or non-semver values
// (dev builds) skip the constraint check.
func (m Manifest) Validate(wizVersion string) error {
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("plugin manifest: name is required")
	}
	for k, s := range m.MCPServers {
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("plugin manifest: mcp server %q missing command", k)
		}
	}
	for i, a := range m.Agents {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("plugin manifest: agent #%d missing name", i)
		}
	}
	return checkWizVersion(m.WizVersion, wizVersion)
}

func checkWizVersion(constraint, current string) error {
	if strings.TrimSpace(constraint) == "" {
		return nil
	}
	cur, err := semver.NewVersion(strings.TrimPrefix(current, "v"))
	if err != nil {
		// Dev/unknown build: cannot evaluate, treat as satisfied.
		return nil
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return fmt.Errorf("plugin manifest: invalid wiz_version %q: %w", constraint, err)
	}
	if !c.Check(cur) {
		return fmt.Errorf("plugin requires wiz %s, running %s", constraint, current)
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./plugin/ -run TestValidate -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add plugin/manifest.go plugin/manifest_test.go go.mod go.sum
git commit -m "feat(plugin): manifest validation + wiz_version semver check"
```

---

## Task 3: Format detection + LoadManifest + Claude stub

**Files:**
- Create: `plugin/detect.go`
- Create: `plugin/claude.go`
- Test: `plugin/detect_test.go`

**Context:** The format-detection seam: a directory with `.claude-plugin/plugin.json` is Claude, one with `wiz-plugin.yaml` is native. `LoadManifest` dispatches to the right adapter, sets `root`, and validates. The Claude adapter is a P0 stub (P4 implements it).

- [ ] **Step 1: Write the failing test**

Create `plugin/detect_test.go`:

```go
package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectFormat(t *testing.T) {
	native := t.TempDir()
	writeFile(t, filepath.Join(native, "wiz-plugin.yaml"), "name: x")
	if got := DetectFormat(native); got != FormatNative {
		t.Fatalf("native detect = %v", got)
	}

	claude := t.TempDir()
	writeFile(t, filepath.Join(claude, ".claude-plugin", "plugin.json"), `{"name":"x"}`)
	if got := DetectFormat(claude); got != FormatClaude {
		t.Fatalf("claude detect = %v", got)
	}

	empty := t.TempDir()
	if got := DetectFormat(empty); got != FormatUnknown {
		t.Fatalf("unknown detect = %v", got)
	}
}

func TestLoadManifestNative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "wiz-plugin.yaml"), "name: demo\nversion: 1.0.0\n")
	m, err := LoadManifest(dir, "0.9.0")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "demo" || m.root != dir {
		t.Fatalf("loaded manifest wrong: %+v (root=%q)", m, m.root)
	}
}

func TestLoadManifestClaudeStub(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"x"}`)
	if _, err := LoadManifest(dir, "0.9.0"); err == nil {
		t.Fatal("expected ErrClaudeUnsupported for claude format in P0")
	}
}

func TestLoadManifestUnknown(t *testing.T) {
	if _, err := LoadManifest(t.TempDir(), "0.9.0"); err == nil {
		t.Fatal("expected error for directory with no manifest")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run 'Detect|LoadManifest' -v`
Expected: FAIL — `DetectFormat`/`LoadManifest`/`FormatNative` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `plugin/claude.go`:

```go
package plugin

import "errors"

// ErrClaudeUnsupported is returned by the Claude adapter until P4 implements it.
var ErrClaudeUnsupported = errors.New("claude code plugin format not yet supported (planned: P4)")

// loadClaudeManifest is a P0 stub. P4 maps .claude-plugin/ (plugin.json,
// skills/, commands/, agents/, hooks.json, .mcp.json) into a Manifest.
func loadClaudeManifest(dir string) (Manifest, error) {
	return Manifest{}, ErrClaudeUnsupported
}
```

Create `plugin/detect.go`:

```go
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Format identifies a plugin's on-disk layout.
type Format int

const (
	FormatUnknown Format = iota
	FormatNative         // wiz-plugin.yaml
	FormatClaude         // .claude-plugin/plugin.json
)

// NativeManifestFile is the native manifest filename at a plugin's root.
const NativeManifestFile = "wiz-plugin.yaml"

// DetectFormat inspects a plugin directory and reports its layout.
func DetectFormat(dir string) Format {
	if _, err := os.Stat(filepath.Join(dir, NativeManifestFile)); err == nil {
		return FormatNative
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		return FormatClaude
	}
	return FormatUnknown
}

// LoadManifest detects a plugin's format, loads it via the matching adapter,
// stamps its install dir as root, and validates it against wizVersion.
func LoadManifest(dir string, wizVersion string) (Manifest, error) {
	var (
		m   Manifest
		err error
	)
	switch DetectFormat(dir) {
	case FormatNative:
		var data []byte
		data, err = os.ReadFile(filepath.Join(dir, NativeManifestFile))
		if err != nil {
			return Manifest{}, err
		}
		m, err = ParseManifest(data)
	case FormatClaude:
		m, err = loadClaudeManifest(dir)
	default:
		return Manifest{}, fmt.Errorf("no plugin manifest found in %s", dir)
	}
	if err != nil {
		return Manifest{}, err
	}
	m.root = dir
	if err := m.Validate(wizVersion); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run 'Detect|LoadManifest' -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
git add plugin/detect.go plugin/claude.go plugin/detect_test.go
git commit -m "feat(plugin): format detection seam + native loader + claude stub"
```

---

## Task 4: Paths + registry read/write

**Files:**
- Create: `plugin/paths.go`
- Create: `plugin/registry.go`
- Test: `plugin/registry_test.go`

**Context:** The registry (`<baseDir>/plugins.yaml`) records each installed plugin's source, ref, and enabled state. `baseDir` is parameterized for testability; `BaseDir()` resolves the real location (XDG-aware).

- [ ] **Step 1: Write the failing test**

Create `plugin/registry_test.go`:

```go
package plugin

import (
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	base := t.TempDir()

	reg, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry (empty): %v", err)
	}
	if len(reg.Plugins) != 0 {
		t.Fatalf("expected empty registry, got %+v", reg.Plugins)
	}

	reg.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v1", Enabled: true})
	reg.Upsert(Entry{Name: "b", SourceURL: "u2", Enabled: false})
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg2, err := LoadRegistry(base)
	if err != nil {
		t.Fatalf("LoadRegistry (reload): %v", err)
	}
	if len(reg2.Plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(reg2.Plugins))
	}

	// Upsert updates in place (no duplicate).
	reg2.Upsert(Entry{Name: "a", SourceURL: "u1", Ref: "v2", Enabled: false})
	if len(reg2.Plugins) != 2 {
		t.Fatalf("upsert duplicated entry: %d", len(reg2.Plugins))
	}
	if e := reg2.Find("a"); e == nil || e.Ref != "v2" || e.Enabled {
		t.Fatalf("upsert did not update: %+v", e)
	}

	// Find returns a pointer into the slice (mutation persists after Save).
	reg2.Find("b").Enabled = true
	if err := reg2.Save(); err != nil {
		t.Fatal(err)
	}
	reg3, _ := LoadRegistry(base)
	if !reg3.Find("b").Enabled {
		t.Fatal("in-place mutation via Find not persisted")
	}

	if !reg3.Remove("a") || reg3.Find("a") != nil {
		t.Fatal("Remove failed")
	}
	if reg3.Remove("missing") {
		t.Fatal("Remove of missing entry should return false")
	}

	// Sanity: registry file path is under baseDir.
	if registryPath(base) != filepath.Join(base, "plugins.yaml") {
		t.Fatalf("registryPath = %q", registryPath(base))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run TestRegistryRoundTrip -v`
Expected: FAIL — `LoadRegistry`/`Entry`/`registryPath` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `plugin/paths.go`:

```go
package plugin

import (
	"os"
	"path/filepath"
)

// BaseDir resolves wiz's config base directory (XDG-aware), where the plugin
// registry and installed plugins live.
func BaseDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "wiz")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "wiz")
	}
	return ".wiz"
}

// PluginsDir is where plugin git checkouts live.
func PluginsDir(baseDir string) string { return filepath.Join(baseDir, "plugins") }

func pluginDir(baseDir, name string) string { return filepath.Join(PluginsDir(baseDir), name) }

func registryPath(baseDir string) string { return filepath.Join(baseDir, "plugins.yaml") }
```

Create `plugin/registry.go`:

```go
package plugin

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Entry is one installed plugin's registry record.
type Entry struct {
	Name      string `yaml:"name"`
	SourceURL string `yaml:"source_url"`
	Ref       string `yaml:"ref"`
	Enabled   bool   `yaml:"enabled"`
}

// Registry is the persisted list of installed plugins.
type Registry struct {
	Plugins []Entry `yaml:"plugins"`

	// baseDir is unexported (yaml ignores it; no struct tag, to keep `go vet` quiet).
	baseDir string
}

// LoadRegistry reads <baseDir>/plugins.yaml, returning an empty registry if the
// file does not exist yet.
func LoadRegistry(baseDir string) (*Registry, error) {
	r := &Registry{baseDir: baseDir}
	data, err := os.ReadFile(registryPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, r); err != nil {
		return nil, err
	}
	r.baseDir = baseDir
	return r, nil
}

// Save writes the registry back to disk, creating baseDir if needed.
func (r *Registry) Save() error {
	if err := os.MkdirAll(r.baseDir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(r.baseDir), data, 0o644)
}

// Find returns a pointer to the entry with the given name, or nil. Mutating the
// returned entry mutates the registry in place.
func (r *Registry) Find(name string) *Entry {
	for i := range r.Plugins {
		if r.Plugins[i].Name == name {
			return &r.Plugins[i]
		}
	}
	return nil
}

// Upsert replaces an existing entry by name, or appends a new one.
func (r *Registry) Upsert(e Entry) {
	if existing := r.Find(e.Name); existing != nil {
		*existing = e
		return
	}
	r.Plugins = append(r.Plugins, e)
}

// Remove deletes an entry by name, reporting whether one was removed.
func (r *Registry) Remove(name string) bool {
	for i := range r.Plugins {
		if r.Plugins[i].Name == name {
			r.Plugins = append(r.Plugins[:i], r.Plugins[i+1:]...)
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run TestRegistryRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add plugin/paths.go plugin/registry.go plugin/registry_test.go
git commit -m "feat(plugin): config paths + on-disk registry"
```

---

## Task 5: Manager — install/update/remove/enable via git

**Files:**
- Create: `plugin/manager.go`
- Test: `plugin/manager_test.go`

**Context:** `Manager` clones plugins with system `git` (injectable `gitClone`/`gitPull` vars for unit tests), loads+validates the manifest, and records the plugin **disabled** (the CLI enables it after the user consents to the contribution summary). Clone goes to a temp dir first, then renames to `plugins/<manifest.Name>` once the real name is known.

- [ ] **Step 1: Write the failing test**

Create `plugin/manager_test.go`:

```go
package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// withFakeGit replaces gitClone to materialize a fixed manifest, so Manager
// tests don't depend on a network or real remote.
func withFakeGit(t *testing.T, manifestBody string) {
	t.Helper()
	origClone := gitClone
	gitClone = func(url, ref, dest string) error {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, NativeManifestFile), []byte(manifestBody), 0o644)
	}
	t.Cleanup(func() { gitClone = origClone })
}

func TestManagerInstallRegistersDisabled(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\nversion: 1.2.3\n")

	mgr := NewManager(base)
	m, err := mgr.Install("https://example.com/demo.git", "v1", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "demo" {
		t.Fatalf("manifest name = %q", m.Name)
	}
	// Files landed at plugins/<name>.
	if _, err := os.Stat(filepath.Join(pluginDir(base, "demo"), NativeManifestFile)); err != nil {
		t.Fatalf("plugin files not at expected dir: %v", err)
	}
	// Registry records it, disabled, with source+ref.
	reg, _ := LoadRegistry(base)
	e := reg.Find("demo")
	if e == nil || e.Enabled {
		t.Fatalf("expected disabled registry entry, got %+v", e)
	}
	if e.SourceURL != "https://example.com/demo.git" || e.Ref != "v1" {
		t.Fatalf("registry source/ref wrong: %+v", e)
	}
}

func TestManagerInstallRejectsBadManifest(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "version: 1.0.0\n") // no name → invalid
	if _, err := NewManager(base).Install("u", "", "0.9.0"); err == nil {
		t.Fatal("expected install to reject manifest with no name")
	}
	// No temp dirs left behind.
	entries, _ := os.ReadDir(PluginsDir(base))
	if len(entries) != 0 {
		t.Fatalf("temp clone not cleaned up: %v", entries)
	}
}

func TestManagerSetEnabledRemoveList(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\n")
	mgr := NewManager(base)
	if _, err := mgr.Install("u", "", "0.9.0"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.SetEnabled("demo", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	list, _ := mgr.List()
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("List after enable: %+v", list)
	}

	if err := mgr.SetEnabled("missing", true); err == nil {
		t.Fatal("SetEnabled on missing plugin should error")
	}

	if err := mgr.Remove("demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(pluginDir(base, "demo")); !os.IsNotExist(err) {
		t.Fatal("plugin dir not removed")
	}
	list, _ = mgr.List()
	if len(list) != 0 {
		t.Fatalf("registry not cleared after remove: %+v", list)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run TestManager -v`
Expected: FAIL — `NewManager`/`gitClone` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `plugin/manager.go`:

```go
package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gitClone clones url (optionally at ref) into dest. Var for test injection.
var gitClone = func(url, ref, dest string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dest)
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitPull fast-forwards an existing checkout. Var for test injection.
var gitPull = func(dir string) error {
	cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Manager performs plugin install/update/remove against a base directory.
type Manager struct{ baseDir string }

// NewManager returns a Manager rooted at baseDir (use plugin.BaseDir() in prod).
func NewManager(baseDir string) *Manager { return &Manager{baseDir: baseDir} }

// Install clones a plugin, validates its manifest, places it at
// plugins/<name>, and records it in the registry as DISABLED. The caller (CLI)
// enables it after presenting the contribution summary for consent.
func (mgr *Manager) Install(url, ref, wizVersion string) (Manifest, error) {
	pluginsDir := PluginsDir(mgr.baseDir)
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return Manifest{}, err
	}
	tmp, err := os.MkdirTemp(pluginsDir, ".tmp-")
	if err != nil {
		return Manifest{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(tmp)
		}
	}()

	if err := gitClone(url, ref, tmp); err != nil {
		return Manifest{}, fmt.Errorf("git clone: %w", err)
	}
	m, err := LoadManifest(tmp, wizVersion)
	if err != nil {
		return Manifest{}, err
	}

	dest := filepath.Join(pluginsDir, m.Name)
	if err := os.RemoveAll(dest); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Manifest{}, err
	}
	cleanup = false
	m.root = dest

	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return Manifest{}, err
	}
	reg.Upsert(Entry{Name: m.Name, SourceURL: url, Ref: ref, Enabled: false})
	if err := reg.Save(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Update fast-forwards an installed plugin's checkout.
func (mgr *Manager) Update(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	return gitPull(pluginDir(mgr.baseDir, name))
}

// Remove deletes an installed plugin's files and registry record.
func (mgr *Manager) Remove(name string) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	if reg.Find(name) == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	if err := os.RemoveAll(pluginDir(mgr.baseDir, name)); err != nil {
		return err
	}
	reg.Remove(name)
	return reg.Save()
}

// SetEnabled flips a plugin's enabled flag in the registry.
func (mgr *Manager) SetEnabled(name string, enabled bool) error {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return err
	}
	e := reg.Find(name)
	if e == nil {
		return fmt.Errorf("plugin %q not installed", name)
	}
	e.Enabled = enabled
	return reg.Save()
}

// List returns all installed plugins from the registry.
func (mgr *Manager) List() ([]Entry, error) {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		return nil, err
	}
	return reg.Plugins, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run TestManager -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add plugin/manager.go plugin/manager_test.go
git commit -m "feat(plugin): manager install/update/remove/enable via git"
```

---

## Task 6: Discovery + merge into config (precedence)

**Files:**
- Create: `plugin/discover.go`
- Test: `plugin/discover_test.go`

**Context:** `Apply` loads every *enabled* plugin's manifest and merges contributions into `*types.Config` with precedence **plugins < user**: user MCP keys win; plugin agents are **prepended** to user agents so the later `MergeAgentTypes` (defaults overlaid by list order) yields defaults < plugins < user. Plugin-vs-plugin clashes warn to stderr. A plugin that fails to load is skipped (one bad plugin never breaks the rest).

- [ ] **Step 1: Write the failing test**

Create `plugin/discover_test.go`:

```go
package plugin

import (
	"testing"

	"github.com/mudler/wiz/types"
)

func TestMergeManifestsPrecedence(t *testing.T) {
	cfg := &types.Config{
		MCPServers: map[string]types.MCPServer{
			"shared": {Command: "user-cmd"}, // user-defined; must survive
		},
		Agents: []types.AgentTypeConfig{
			{Name: "explore", SystemPrompt: "USER"}, // user override; must stay last
		},
	}
	manifests := []Manifest{
		{
			Name:       "p1",
			MCPServers: map[string]types.MCPServer{"shared": {Command: "p1-cmd"}, "only1": {Command: "c1"}},
			Agents:     []types.AgentTypeConfig{{Name: "explore", SystemPrompt: "P1"}, {Name: "p1agent"}},
		},
		{
			Name:       "p2",
			MCPServers: map[string]types.MCPServer{"only2": {Command: "c2"}},
			Agents:     []types.AgentTypeConfig{{Name: "p2agent"}},
		},
	}

	mergeManifests(cfg, manifests)

	// User MCP key wins over plugin.
	if cfg.MCPServers["shared"].Command != "user-cmd" {
		t.Fatalf("user mcp overwritten: %q", cfg.MCPServers["shared"].Command)
	}
	// Plugin-only MCP keys added.
	if cfg.MCPServers["only1"].Command != "c1" || cfg.MCPServers["only2"].Command != "c2" {
		t.Fatalf("plugin mcp not merged: %+v", cfg.MCPServers)
	}
	// Plugin agents prepended; user 'explore' override is LAST so it wins in MergeAgentTypes.
	last := cfg.Agents[len(cfg.Agents)-1]
	if last.Name != "explore" || last.SystemPrompt != "USER" {
		t.Fatalf("user agent not last: %+v", cfg.Agents)
	}
	// Plugin agents present.
	var names []string
	for _, a := range cfg.Agents {
		names = append(names, a.Name)
	}
	if len(cfg.Agents) != 4 { // p1.explore, p1agent, p2agent, user.explore
		t.Fatalf("expected 4 agents, got %d: %v", len(cfg.Agents), names)
	}
}

func TestApplyEnabledOnly(t *testing.T) {
	base := t.TempDir()
	withFakeGit(t, "name: demo\nagents:\n  - name: fromplugin\n")
	mgr := NewManager(base)
	if _, err := mgr.Install("u", "", "0.9.0"); err != nil {
		t.Fatal(err)
	}

	// Disabled: contributes nothing.
	cfg := &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(cfg.Agents) != 0 {
		t.Fatalf("disabled plugin contributed: %+v", cfg.Agents)
	}

	// Enabled: contributes its agent.
	if err := mgr.SetEnabled("demo", true); err != nil {
		t.Fatal(err)
	}
	cfg = &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "fromplugin" {
		t.Fatalf("enabled plugin agent missing: %+v", cfg.Agents)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugin/ -run 'MergeManifests|ApplyEnabled' -v`
Expected: FAIL — `mergeManifests`/`Apply` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `plugin/discover.go`:

```go
package plugin

import (
	"fmt"
	"os"

	"github.com/mudler/wiz/types"
)

// EnabledManifests loads the manifest of every enabled plugin in the registry,
// in registry order. A plugin that fails to load is skipped with a warning.
func (mgr *Manager) EnabledManifests(wizVersion string) []Manifest {
	reg, err := LoadRegistry(mgr.baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wiz: plugin registry: %v\n", err)
		return nil
	}
	var out []Manifest
	for _, e := range reg.Plugins {
		if !e.Enabled {
			continue
		}
		m, err := LoadManifest(pluginDir(mgr.baseDir, e.Name), wizVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "wiz: skipping plugin %q: %v\n", e.Name, err)
			continue
		}
		out = append(out, m)
	}
	return out
}

// Apply merges all enabled plugins' contributions into cfg. Precedence is
// plugins < user; user config (already in cfg) always wins.
func Apply(cfg *types.Config, baseDir, wizVersion string) error {
	mergeManifests(cfg, NewManager(baseDir).EnabledManifests(wizVersion))
	return nil
}

func mergeManifests(cfg *types.Config, manifests []Manifest) {
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]types.MCPServer{}
	}
	userKeys := map[string]bool{}
	for k := range cfg.MCPServers {
		userKeys[k] = true
	}

	mcpFrom := map[string]string{}   // mcp key   -> plugin that set it
	agentFrom := map[string]string{} // agent name -> plugin that set it
	var pluginAgents []types.AgentTypeConfig

	for _, m := range manifests {
		for k, v := range m.MCPServers {
			if userKeys[k] {
				continue // user wins
			}
			if prev, ok := mcpFrom[k]; ok {
				fmt.Fprintf(os.Stderr, "wiz: mcp server %q from plugin %q overrides plugin %q\n", k, m.Name, prev)
			}
			cfg.MCPServers[k] = v
			mcpFrom[k] = m.Name
		}
		for _, a := range m.Agents {
			if prev, ok := agentFrom[a.Name]; ok {
				fmt.Fprintf(os.Stderr, "wiz: agent %q from plugin %q overrides plugin %q\n", a.Name, m.Name, prev)
			}
			agentFrom[a.Name] = m.Name
			pluginAgents = append(pluginAgents, a)
		}
	}

	// Prepend plugin agents so user agents stay last → user wins when
	// config.MergeAgentTypes overlays the list (defaults < plugins < user).
	cfg.Agents = append(pluginAgents, cfg.Agents...)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugin/ -run 'MergeManifests|ApplyEnabled' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add plugin/discover.go plugin/discover_test.go
git commit -m "feat(plugin): discovery + merge into config with precedence"
```

---

## Task 7: Wire plugin.Apply into config.Load

**Files:**
- Modify: `config/config.go`
- Test: `config/plugin_load_test.go`

**Context:** `config.Load()` must merge plugin contributions *before* `MergeAgentTypes(cfg.Agents)` so the final agent list is defaults < plugins < user, and so plugin MCP servers reach `cfg.MCPServers`. Use `XDG_CONFIG_HOME` to point the test at a temp base dir.

- [ ] **Step 1: Write the failing test**

Create `config/plugin_load_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesEnabledPlugin(t *testing.T) {
	base := t.TempDir() // becomes <base>/wiz via XDG
	t.Setenv("XDG_CONFIG_HOME", base)

	wizBase := filepath.Join(base, "wiz")
	pluginRoot := filepath.Join(wizBase, "plugins", "demo")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: demo\n" +
		"mcp_servers:\n  pluginmcp:\n    command: pmcp\n" +
		"agents:\n  - name: fromplugin\n    description: d\n"
	if err := os.WriteFile(filepath.Join(pluginRoot, "wiz-plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := "plugins:\n  - name: demo\n    source_url: u\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(wizBase, "plugins.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()

	if _, ok := cfg.MCPServers["pluginmcp"]; !ok {
		t.Fatalf("plugin mcp server not merged: %+v", cfg.MCPServers)
	}
	var found bool
	for _, a := range cfg.Agents {
		if a.Name == "fromplugin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin agent not merged into cfg.Agents: %+v", cfg.Agents)
	}
	// Built-in defaults still present (MergeAgentTypes ran after plugin merge).
	if findType(cfg.Agents, "explore") == nil {
		t.Fatal("built-in agent types lost")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./config/ -run TestLoadMergesEnabledPlugin -v`
Expected: FAIL — plugin contributions absent (Apply not wired).

- [ ] **Step 3: Add the wiring**

In `config/config.go`, add imports `"github.com/mudler/wiz/internal"` and `"github.com/mudler/wiz/plugin"`, and insert the plugin merge in `Load()` immediately before the existing `cfg.Agents = MergeAgentTypes(cfg.Agents)` line:

```go
	// Merge enabled plugin contributions (mcp servers + agents) before the
	// agent default-merge, so precedence is built-in defaults < plugins < user.
	if err := plugin.Apply(&cfg, plugin.BaseDir(), internal.Version); err != nil {
		fmt.Fprintf(os.Stderr, "wiz: plugin load: %v\n", err)
	}

	// Merge user-provided agent types with the built-in defaults.
	cfg.Agents = MergeAgentTypes(cfg.Agents)
```

Add `"fmt"` to the `config` imports if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./config/ -run TestLoadMergesEnabledPlugin -v`
Expected: PASS.

- [ ] **Step 5: Run the full config + plugin suites**

Run: `go test ./config/ ./plugin/ -v`
Expected: PASS (no regressions in existing agent/config tests).

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/plugin_load_test.go
git commit -m "feat(config): merge enabled plugin contributions in Load"
```

---

## Task 8: `wiz plugin` CLI subcommand

**Files:**
- Create: `cmd/plugin.go`
- Test: `cmd/plugin_test.go`

**Context:** `RunPluginCommand(args)` dispatches `install/list/update/enable/disable/remove`, returns a process exit code, and on install prints a contribution summary then enables only on confirmation (`--yes` skips). Confirmation reads stdin; the prompt source is an injectable var so it can be tested.

- [ ] **Step 1: Write the failing test**

Create `cmd/plugin_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunPluginCommandLifecycle(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	// Seed an installed-but-disabled plugin directly via the registry + files,
	// avoiding git in this CLI-level test.
	wizBase := filepath.Join(base, "wiz")
	pdir := filepath.Join(wizBase, "plugins", "demo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "wiz-plugin.yaml"), []byte("name: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wizBase, "plugins.yaml"),
		[]byte("plugins:\n  - name: demo\n    enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := RunPluginCommand([]string{"list"}); code != 0 {
		t.Fatalf("list exit = %d", code)
	}
	if code := RunPluginCommand([]string{"enable", "demo"}); code != 0 {
		t.Fatalf("enable exit = %d", code)
	}
	if code := RunPluginCommand([]string{"enable", "missing"}); code == 0 {
		t.Fatal("enable missing should fail")
	}
	if code := RunPluginCommand([]string{"disable", "demo"}); code != 0 {
		t.Fatalf("disable exit = %d", code)
	}
	if code := RunPluginCommand([]string{"remove", "demo"}); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	if code := RunPluginCommand([]string{"bogus"}); code == 0 {
		t.Fatal("unknown subcommand should fail")
	}
	if code := RunPluginCommand(nil); code == 0 {
		t.Fatal("no subcommand should fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestRunPluginCommandLifecycle -v`
Expected: FAIL — `RunPluginCommand` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/plugin.go`:

```go
package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mudler/wiz/internal"
	"github.com/mudler/wiz/plugin"
)

// confirmFn reads a yes/no answer. Var for test injection.
var confirmFn = func(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// RunPluginCommand dispatches `wiz plugin <sub> ...` and returns an exit code.
func RunPluginCommand(args []string) int {
	if len(args) == 0 {
		pluginUsage()
		return 1
	}
	mgr := plugin.NewManager(plugin.BaseDir())
	switch args[0] {
	case "install":
		return pluginInstall(mgr, args[1:])
	case "list":
		return pluginList(mgr)
	case "update":
		return pluginByName(args, "update", mgr.Update)
	case "remove":
		return pluginByName(args, "remove", mgr.Remove)
	case "enable":
		return pluginSetEnabled(mgr, args[1:], true)
	case "disable":
		return pluginSetEnabled(mgr, args[1:], false)
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin command: %s\n", args[0])
		pluginUsage()
		return 1
	}
}

func pluginUsage() {
	fmt.Fprintln(os.Stderr, "usage: wiz plugin <install|list|update|enable|disable|remove> ...")
}

func pluginInstall(mgr *plugin.Manager, args []string) int {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	ref := fs.String("ref", "", "git ref (tag or branch) to install")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: wiz plugin install [--ref REF] [--yes] <git-url>")
		return 1
	}

	m, err := mgr.Install(fs.Arg(0), *ref, internal.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}

	fmt.Printf("Installed %q v%s — %s\n", m.Name, m.Version, m.Description)
	fmt.Printf("Contributes: %d MCP server(s), %d sub-agent(s)\n", len(m.MCPServers), len(m.Agents))

	if *yes || confirmFn("Enable this plugin?") {
		if err := mgr.SetEnabled(m.Name, true); err != nil {
			fmt.Fprintf(os.Stderr, "enable failed: %v\n", err)
			return 1
		}
		fmt.Printf("Plugin %q enabled.\n", m.Name)
		return 0
	}
	fmt.Printf("Plugin %q installed but left disabled. Enable later: wiz plugin enable %s\n", m.Name, m.Name)
	return 0
}

func pluginList(mgr *plugin.Manager) int {
	entries, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("No plugins installed.")
		return 0
	}
	for _, e := range entries {
		status := "disabled"
		if e.Enabled {
			status = "enabled"
		}
		fmt.Printf("%-20s %-9s %s\n", e.Name, status, e.SourceURL)
	}
	return 0
}

func pluginByName(args []string, verb string, fn func(string) error) int {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: wiz plugin %s <name>\n", verb)
		return 1
	}
	if err := fn(args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", verb, err)
		return 1
	}
	fmt.Printf("Plugin %q %sd.\n", args[1], verb) // "updated" / "removed"
	return 0
}

func pluginSetEnabled(mgr *plugin.Manager, args []string, enabled bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: wiz plugin enable|disable <name>")
		return 1
	}
	if err := mgr.SetEnabled(args[0], enabled); err != nil {
		fmt.Fprintf(os.Stderr, "failed: %v\n", err)
		return 1
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Printf("Plugin %q %s.\n", args[0], state)
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/ -run TestRunPluginCommandLifecycle -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/plugin.go cmd/plugin_test.go
git commit -m "feat(cmd): wiz plugin install/list/update/enable/disable/remove"
```

---

## Task 9: Dispatch the `plugin` subcommand from main

**Files:**
- Modify: `main.go`

**Context:** `main()` uses the `flag` package; the `plugin` subcommand and its own args must be intercepted before `flag.Parse()` so flags don't choke on them.

- [ ] **Step 1: Add the dispatch**

In `main.go`, add `os` is already imported. Insert as the very first statements inside `func main() {`, before the flag definitions:

```go
func main() {
	// Subcommand dispatch (must precede flag parsing).
	if len(os.Args) >= 2 && os.Args[1] == "plugin" {
		os.Exit(cmd.RunPluginCommand(os.Args[2:]))
	}

	// Parse command line arguments
	heightFlag := flag.String("height", "", "Height of the TUI (e.g., '40%' or '20')")
	// ... rest unchanged ...
```

(`cmd` is already imported in `main.go`.)

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Manually verify the subcommand routes**

Run: `go run . plugin list`
Expected: prints `No plugins installed.` (or the current list) and exits 0 — confirming the dispatch reaches `RunPluginCommand` rather than launching the TUI.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(cmd): route 'wiz plugin' subcommand from main"
```

---

## Task 10: End-to-end smoke — install a real git plugin and merge it

**Files:**
- Create: `plugin/e2e_test.go`

**Context:** A real-`git` integration test proving the whole spine: init a local git repo containing a native plugin, install it (real clone), enable it, then `Apply` to a `Config` and assert the MCP server + agent landed. This guards the install→registry→discover→merge path against regressions and is the P0 acceptance check.

- [ ] **Step 1: Write the failing test**

Create `plugin/e2e_test.go`:

```go
package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
)

func gitInitRepo(t *testing.T, body string) string {
	t.Helper()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, NativeManifestFile), body)
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

func TestEndToEndInstallEnableMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepo(t,
		"name: e2e\nversion: 0.1.0\n"+
			"mcp_servers:\n  e2emcp:\n    command: e2ecmd\n"+
			"agents:\n  - name: e2eagent\n    description: d\n")

	mgr := NewManager(base)
	m, err := mgr.Install(repo, "", "0.9.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if m.Name != "e2e" {
		t.Fatalf("name = %q", m.Name)
	}

	// Disabled by default → no contribution.
	cfg := &types.Config{}
	_ = Apply(cfg, base, "0.9.0")
	if len(cfg.Agents) != 0 || len(cfg.MCPServers) != 0 {
		t.Fatalf("disabled plugin contributed: %+v / %+v", cfg.Agents, cfg.MCPServers)
	}

	// Enable → contributes mcp + agent.
	if err := mgr.SetEnabled("e2e", true); err != nil {
		t.Fatal(err)
	}
	cfg = &types.Config{}
	if err := Apply(cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if cfg.MCPServers["e2emcp"].Command != "e2ecmd" {
		t.Fatalf("mcp not merged: %+v", cfg.MCPServers)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "e2eagent" {
		t.Fatalf("agent not merged: %+v", cfg.Agents)
	}

	// Clone really happened on disk.
	if _, err := os.Stat(filepath.Join(pluginDir(base, "e2e"), NativeManifestFile)); err != nil {
		t.Fatalf("plugin files missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails, then passes**

Run: `go test ./plugin/ -run TestEndToEndInstallEnableMerge -v`
Expected: PASS (the spine is already implemented by Tasks 1–6; this test exercises it end-to-end). If it fails, fix the offending unit before proceeding.

- [ ] **Step 3: Run the entire suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_test.go
git commit -m "test(plugin): end-to-end install+enable+merge smoke"
```

---

## Self-Review (completed during planning)

**Spec coverage (P0 scope):**
- Manifest schema + parse/validate → Tasks 1–2 ✓
- Format-detection seam (native live, Claude stub) → Task 3 ✓
- Registry → Task 4 ✓
- `install/list/update/enable/disable/remove` → Tasks 5 (engine) + 8 (CLI) + 9 (dispatch) ✓
- Discovery + generalized merge wiring `mcp_servers` + `agents` with precedence → Tasks 6–7 ✓
- Security consent (summary + confirm before enable) → Task 8 (`pluginInstall`) ✓
- "Installable native plugins shipping MCP servers + sub-agent types" outcome → Task 10 e2e ✓

**Out of P0 (correctly deferred):** prompt fragments, skills/`load_skill`, commands/`/` completion, hooks, real Claude adapter, marketplace — later phases. Manifest fields for those are added additively then.

**Type consistency:** `Manifest`, `Entry`, `Registry`, `Manager`, `Format`, `LoadManifest`, `Apply`, `mergeManifests`, `RunPluginCommand` names are used identically across tasks. `Install(url, ref, wizVersion)` and `SetEnabled(name, enabled)` signatures match every call site. `gitClone`/`gitPull`/`confirmFn` are the injectable vars referenced by tests.

**Precedence invariant** (asserted in Tasks 6, 7, 10): user MCP keys and user agents always win; built-in agent defaults survive because `MergeAgentTypes` runs after `plugin.Apply`.
