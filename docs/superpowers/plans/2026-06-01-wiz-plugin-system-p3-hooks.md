# Wiz Plugin System — P3 (Hooks) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add **shell-command hooks** bound to lifecycle events. A hook is `{event, matcher?, command}`; on a matching event the dispatcher runs the command, passing event JSON on stdin and reading a JSON decision on stdout. Six events: `SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, AgentEvent, Stop`. `PreToolUse` hooks can **block/approve/adjust** a tool call (merging into the existing approval gate); the other five are observational. Hooks run with `${WIZ_PLUGIN_ROOT}` (and `${CLAUDE_PLUGIN_ROOT}`) set to the plugin dir.

**Architecture:** A new `hooks` package owns the dispatcher (pure exec + JSON, unit-tested with fake scripts) and the PreToolUse decision-combination logic. `chat.Session` holds a `*hooks.Dispatcher` built from `cfg.Hooks` and fires events at the six points. The tool-approval closure in `SendMessage` is refactored into a testable `Session.decideToolCall` that consults PreToolUse hooks before the existing allow-list + `OnToolCall` user gate. Hooks merge into `types.Config` by accumulation (like prompt fragments), with each plugin hook's `Dir` stamped to the plugin root.

**Tech Stack:** Go 1.24, `os/exec` (`sh -c`), `encoding/json`, `regexp` (matchers), standard `testing`. Builds on P0 (merge) + the existing approval gate.

**Branch:** `feat/plugin-system`. All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** the real Claude adapter / `hooks.json` mapping (P4) — but the dispatcher already sets `${CLAUDE_PLUGIN_ROOT}` so Claude hook scripts will run unmodified once P4 maps them. Rich `UserPromptSubmit` blocking/context-injection is a follow-up; in P3 only `PreToolUse` affects control flow. No new TUI surface.

---

## File Structure

- `types/config.go` — **modify**: add `HookConfig` type + `Config.Hooks []HookConfig`.
- `plugin/manifest.go` — **modify**: add `Manifest.Hooks []types.HookConfig`; extend `Validate` (hook needs event + command).
- `plugin/discover.go` — **modify**: add `mergeHooks` (accumulate, stamp `Dir`), call from `mergeManifests`.
- `plugin/discover_test.go` / `plugin/manifest_test.go` — **modify**: hook merge + validate tests.
- `hooks/hooks.go` — **new**: `Event` consts, `Decision`, `ToolDecision`, `Dispatcher` (`New`, `Fire`), `runHook`, `matchHook`, `CombineToolDecisions`.
- `hooks/hooks_test.go` — **new**: exec/stdin/env/exit-code/combine tests with fake scripts.
- `chat/session.go` — **modify**: hold the dispatcher, fire the six events, refactor the tool gate into `decideToolCall`.
- `chat/session_hooks_test.go` — **new**: `decideToolCall` PreToolUse block/approve/fallthrough.
- `plugin/e2e_p3_test.go` — **new**: real-git e2e for a PreToolUse-blocking hook plugin.

---

## Task 1: Hook config type + manifest + merge

**Files:**
- Modify: `types/config.go`, `plugin/manifest.go`, `plugin/discover.go`
- Test: `plugin/manifest_test.go` (append), `plugin/discover_test.go` (append)

**Context:** A hook carries an event, an optional matcher (matched against the tool name for PreToolUse/PostToolUse), and a shell command. Plugin hooks also carry a `Dir` (the plugin root), stamped during merge and used for `${WIZ_PLUGIN_ROOT}` + the command's working directory. Hooks accumulate (never override).

- [ ] **Step 1: Append failing tests.**

To `plugin/manifest_test.go`:

```go
func TestParseAndValidateHooks(t *testing.T) {
	m, err := ParseManifest([]byte("name: demo\nhooks:\n  - event: PreToolUse\n    matcher: bash\n    command: ./hooks/guard.sh\n"))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Hooks) != 1 || m.Hooks[0].Event != "PreToolUse" || m.Hooks[0].Matcher != "bash" {
		t.Fatalf("hooks wrong: %+v", m.Hooks)
	}
	if err := (Manifest{Name: "a", Hooks: []types.HookConfig{{Command: "x"}}}).Validate("0.9.0"); err == nil {
		t.Fatal("expected hook with no event to be rejected")
	}
	if err := (Manifest{Name: "a", Hooks: []types.HookConfig{{Event: "PreToolUse"}}}).Validate("0.9.0"); err == nil {
		t.Fatal("expected hook with no command to be rejected")
	}
	if err := (Manifest{Name: "a", Hooks: []types.HookConfig{{Event: "Stop", Command: "x"}}}).Validate("0.9.0"); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}
```

To `plugin/discover_test.go`:

```go
func TestMergeHooksStampsDir(t *testing.T) {
	cfg := &types.Config{
		Hooks: []types.HookConfig{{Event: "Stop", Command: "user.sh"}}, // user hook, Dir stays ""
	}
	manifests := []Manifest{
		{Name: "p1", root: "/plugins/p1", Hooks: []types.HookConfig{{Event: "PreToolUse", Command: "g.sh"}}},
		{Name: "p2", root: "/plugins/p2", Hooks: []types.HookConfig{{Event: "Stop", Command: "s.sh"}}},
	}
	mergeManifests(cfg, manifests)

	if len(cfg.Hooks) != 3 {
		t.Fatalf("want 3 hooks (accumulate), got %d: %+v", len(cfg.Hooks), cfg.Hooks)
	}
	// user hook keeps empty Dir
	if cfg.Hooks[0].Dir != "" {
		t.Fatalf("user hook Dir should be empty: %+v", cfg.Hooks[0])
	}
	// plugin hooks get their plugin root stamped
	var p1, p2 *types.HookConfig
	for i := range cfg.Hooks {
		switch cfg.Hooks[i].Command {
		case "g.sh":
			p1 = &cfg.Hooks[i]
		case "s.sh":
			p2 = &cfg.Hooks[i]
		}
	}
	if p1 == nil || p1.Dir != "/plugins/p1" || p2 == nil || p2.Dir != "/plugins/p2" {
		t.Fatalf("plugin hook Dir not stamped: p1=%+v p2=%+v", p1, p2)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./plugin/ -run 'ParseAndValidateHooks|MergeHooksStampsDir' -v`.

- [ ] **Step 3: Implement.**

In `types/config.go`, add the type (near `CommandConfig`):

```go
// HookConfig is a shell command bound to a lifecycle event. Matcher (optional)
// is matched against the tool name for PreToolUse/PostToolUse. Dir is the
// plugin root (set during merge); it is the command's working directory and is
// exported as ${WIZ_PLUGIN_ROOT}/${CLAUDE_PLUGIN_ROOT}.
type HookConfig struct {
	Event   string `yaml:"event"`
	Matcher string `yaml:"matcher,omitempty"`
	Command string `yaml:"command"`
	Dir     string `yaml:"-"` // plugin root; set during merge, not parsed
}
```

Add to `Config`, after `Commands`:

```go
	Hooks []HookConfig `yaml:"hooks"`
```

In `plugin/manifest.go`, add to `Manifest` after `Commands`:

```go
	Hooks []types.HookConfig `yaml:"hooks"`
```

Extend `Validate` (before the final `return checkWizVersion(...)`):

```go
	for i, h := range m.Hooks {
		if strings.TrimSpace(h.Event) == "" {
			return fmt.Errorf("plugin manifest: hook #%d missing event", i)
		}
		if strings.TrimSpace(h.Command) == "" {
			return fmt.Errorf("plugin manifest: hook #%d (%s) missing command", i, h.Event)
		}
	}
```

In `plugin/discover.go`, call `mergeHooks(cfg, manifests)` as the LAST call in `mergeManifests` (after `mergeCommands`), and add:

```go
// mergeHooks accumulates each enabled plugin's hooks into cfg, stamping the
// plugin root as the hook's Dir (working directory + ${WIZ_PLUGIN_ROOT}). Hooks
// never override — they all fire.
func mergeHooks(cfg *types.Config, manifests []Manifest) {
	for _, m := range manifests {
		for _, h := range m.Hooks {
			h.Dir = m.root
			cfg.Hooks = append(cfg.Hooks, h)
		}
	}
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./plugin/ -run 'ParseAndValidateHooks|MergeHooksStampsDir' -v`. Then `go test ./plugin/ ./types/ -v` and `go vet ./plugin/`.

- [ ] **Step 5: Commit**

```bash
git add types/config.go plugin/manifest.go plugin/discover.go plugin/manifest_test.go plugin/discover_test.go
git commit -m "feat(plugin): hook config type + manifest + merge (Dir stamping)"
```

---

## Task 2: `hooks` package — dispatcher + decision combine

**Files:**
- Create: `hooks/hooks.go`, `hooks/hooks_test.go`

**Context:** The dispatcher runs each hook whose `Event` matches and whose `Matcher` matches the supplied name (empty matcher = match all; otherwise regexp match, falling back to exact equality). It marshals the payload to stdin, runs `sh -c <command>` with `cwd=Dir` and `WIZ_PLUGIN_ROOT`/`CLAUDE_PLUGIN_ROOT=Dir`, and parses stdout as a `Decision`. A non-zero exit (or timeout) is treated as `Block` (Claude-style blocking). `CombineToolDecisions` reduces PreToolUse decisions: any block/deny wins; else any approve approves; else undecided.

- [ ] **Step 1: Write the failing test** — create `hooks/hooks_test.go`:

```go
package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func boolp(b bool) *bool { return &b }

func TestFireMatchingAndStdinEnv(t *testing.T) {
	dir := t.TempDir()
	// Hook records its stdin and ${WIZ_PLUGIN_ROOT} to files, then approves.
	script := writeScript(t, dir, "h.sh",
		"cat > \"$WIZ_PLUGIN_ROOT/stdin.txt\"; echo \"$WIZ_PLUGIN_ROOT\" > \"$WIZ_PLUGIN_ROOT/root.txt\"; echo '{\"approved\": true}'")
	d := New([]types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: script, Dir: dir}})

	// Non-matching event → no hooks fire.
	if got := d.Fire(context.Background(), EventStop, "bash", map[string]any{"x": 1}); len(got) != 0 {
		t.Fatalf("non-matching event fired: %+v", got)
	}
	// Non-matching matcher → no fire.
	if got := d.Fire(context.Background(), EventPreToolUse, "other", nil); len(got) != 0 {
		t.Fatalf("non-matching matcher fired: %+v", got)
	}
	// Matching → fires, approves.
	got := d.Fire(context.Background(), EventPreToolUse, "bash", map[string]any{"tool": "bash"})
	if len(got) != 1 || got[0].Approved == nil || !*got[0].Approved {
		t.Fatalf("expected one approve decision, got %+v", got)
	}
	// stdin + env were delivered.
	if b, _ := os.ReadFile(filepath.Join(dir, "stdin.txt")); len(b) == 0 {
		t.Fatal("hook did not receive payload on stdin")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "root.txt")); string(b) == "\n" || len(b) == 0 {
		t.Fatal("WIZ_PLUGIN_ROOT not set for the hook")
	}
}

func TestFireNonZeroExitBlocks(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "deny.sh", "echo 'no way' >&2; exit 2")
	d := New([]types.HookConfig{{Event: "PreToolUse", Command: script, Dir: dir}})
	got := d.Fire(context.Background(), EventPreToolUse, "bash", nil)
	if len(got) != 1 || !got[0].Block {
		t.Fatalf("non-zero exit should block: %+v", got)
	}
}

func TestCombineToolDecisions(t *testing.T) {
	// deny wins over approve
	td := CombineToolDecisions([]Decision{{Approved: boolp(true)}, {Block: true, Reason: "nope"}})
	if !td.Decided || td.Approve {
		t.Fatalf("block should deny: %+v", td)
	}
	// approve when no deny
	td = CombineToolDecisions([]Decision{{Approved: boolp(true), Adjustment: "use -n"}})
	if !td.Decided || !td.Approve || td.Adjustment != "use -n" {
		t.Fatalf("should approve with adjustment: %+v", td)
	}
	// undecided when no opinions
	td = CombineToolDecisions([]Decision{{}})
	if td.Decided {
		t.Fatalf("should be undecided: %+v", td)
	}
	// explicit approved:false denies
	td = CombineToolDecisions([]Decision{{Approved: boolp(false)}})
	if !td.Decided || td.Approve {
		t.Fatalf("approved:false should deny: %+v", td)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./hooks/ -v` (package undefined).

- [ ] **Step 3: Implement** — create `hooks/hooks.go`:

```go
// Package hooks runs shell-command hooks bound to lifecycle events. Each hook
// receives the event payload as JSON on stdin and may return a JSON Decision on
// stdout; PreToolUse decisions gate tool execution.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/wiz/types"
)

// Event identifies a lifecycle hook point.
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventAgentEvent       Event = "AgentEvent"
	EventStop             Event = "Stop"
)

// Decision is a hook's JSON stdout response.
type Decision struct {
	Approved   *bool  `json:"approved"`
	Block      bool   `json:"block"`
	Adjustment string `json:"adjustment"`
	Reason     string `json:"reason"`
}

// ToolDecision is the combined PreToolUse verdict.
type ToolDecision struct {
	Decided    bool
	Approve    bool
	Adjustment string
	Reason     string
}

// Dispatcher fires hooks for events.
type Dispatcher struct {
	hooks   []types.HookConfig
	timeout time.Duration
}

// New builds a dispatcher over the given hooks (a nil/empty slice is a no-op).
func New(hooks []types.HookConfig) *Dispatcher {
	return &Dispatcher{hooks: hooks, timeout: 30 * time.Second}
}

// Fire runs every hook whose Event matches and whose Matcher matches name
// (empty matcher matches all), passing payload as JSON on stdin, and returns
// each hook's Decision in order.
func (d *Dispatcher) Fire(ctx context.Context, event Event, name string, payload any) []Decision {
	if d == nil || len(d.hooks) == 0 {
		return nil
	}
	data, _ := json.Marshal(payload)
	var out []Decision
	for _, h := range d.hooks {
		if Event(h.Event) != event || !matchHook(h.Matcher, name) {
			continue
		}
		out = append(out, runHook(ctx, h, data, d.timeout))
	}
	return out
}

// matchHook reports whether a hook matcher matches name. Empty matches all; a
// valid regexp is matched; otherwise exact equality.
func matchHook(pattern, name string) bool {
	if strings.TrimSpace(pattern) == "" {
		return true
	}
	if pattern == name {
		return true
	}
	if re, err := regexp.Compile(pattern); err == nil {
		return re.MatchString(name)
	}
	return false
}

// runHook executes one hook and returns its Decision. A non-zero exit (or
// timeout) is treated as Block, with stderr as the reason.
func runHook(ctx context.Context, h types.HookConfig, stdin []byte, timeout time.Duration) Decision {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", h.Command)
	if h.Dir != "" {
		cmd.Dir = h.Dir
	}
	cmd.Env = append(os.Environ(), "WIZ_PLUGIN_ROOT="+h.Dir, "CLAUDE_PLUGIN_ROOT="+h.Dir)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var dec Decision
	_ = json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &dec) // lenient: empty/garbage → zero Decision

	if err != nil {
		dec.Block = true
		if dec.Reason == "" {
			if r := strings.TrimSpace(stderr.String()); r != "" {
				dec.Reason = r
			} else {
				dec.Reason = err.Error()
			}
		}
	}
	return dec
}

// CombineToolDecisions reduces PreToolUse decisions: any block / explicit
// approved:false denies (first one wins); otherwise any explicit approved:true
// approves (carrying its adjustment); otherwise undecided.
func CombineToolDecisions(ds []Decision) ToolDecision {
	for _, d := range ds {
		if d.Block || (d.Approved != nil && !*d.Approved) {
			return ToolDecision{Decided: true, Approve: false, Reason: d.Reason}
		}
	}
	res := ToolDecision{}
	for _, d := range ds {
		if d.Approved != nil && *d.Approved {
			res = ToolDecision{Decided: true, Approve: true, Adjustment: d.Adjustment}
		}
	}
	return res
}
```

- [ ] **Step 4: Run, expect PASS** — `go test ./hooks/ -v` then `go vet ./hooks/`.

- [ ] **Step 5: Commit**

```bash
git add hooks/hooks.go hooks/hooks_test.go
git commit -m "feat(hooks): shell-command hook dispatcher + PreToolUse combine"
```

---

## Task 3: Wire the dispatcher into the session

**Files:**
- Modify: `chat/session.go`
- Test: `chat/session_hooks_test.go` (create)

**Context:** `Session` holds a `*hooks.Dispatcher` and fires the six events. The tool-approval closure in `SendMessage` is refactored into a testable `decideToolCall` method that consults PreToolUse hooks first, then the existing allow-list + user gate. `SessionStart` fires in `NewSession`; `UserPromptSubmit` and `Stop` in `SendMessage`; `PostToolUse` via a new `WithToolCallResultCallback`; `AgentEvent` in `emitAgentEvent`.

- [ ] **Step 1: Write the failing test** — create `chat/session_hooks_test.go`:

```go
package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestDecideToolCallPreHook(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(block, []byte("#!/bin/sh\necho '{\"block\": true, \"reason\": \"denied\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	approved := false
	s, err := NewSession(context.Background(), types.Config{
		Hooks: []types.HookConfig{{Event: "PreToolUse", Matcher: "bash", Command: block, Dir: dir}},
	}, Callbacks{
		OnToolCall: func(ToolCallRequest) ToolCallResponse {
			approved = true // the user gate; should NOT be reached for "bash"
			return ToolCallResponse{Approved: true}
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// "bash" matches the blocking PreToolUse hook → denied, user gate skipped.
	dec := s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: "{}"})
	if dec.Approved {
		t.Fatalf("expected hook to deny bash, got approved")
	}
	if approved {
		t.Fatal("user gate should not have been reached when a hook decides")
	}

	// A non-matching tool falls through to the user gate (approve).
	dec = s.decideToolCall(ToolCallRequest{Name: "other", Arguments: "{}"})
	if !dec.Approved {
		t.Fatal("expected non-matching tool to fall through to the user gate (approve)")
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./chat/ -run TestDecideToolCallPreHook -v` (decideToolCall undefined).

- [ ] **Step 3: Implement** in `chat/session.go`.

(a) Add the import `"github.com/mudler/wiz/hooks"` to the import block.

(b) Add a field to the `Session` struct (near `allowedTools`):

```go
	hooks         *hooks.Dispatcher
```

(c) In `NewSession`, build the session into a variable, fire `SessionStart`, and return it. Change the final `return &Session{...}, nil` to:

```go
	s := &Session{
		ctx:             ctx,
		llm:             llm,
		reviewerLLM:     reviewerLLM,
		clients:         clients,
		fragment:        cogito.NewEmptyFragment(),
		messages:        []openai.ChatCompletionMessage{},
		callbacks:       callbacks,
		systemPrompt:    cfg.GetPrompt(),
		skills:          cfg.Skills,
		cogitoOptions:   cfg.AgentOptions,
		allowedTools:    make(map[string]bool),
		hooks:           hooks.New(cfg.Hooks),
		reviewerEnabled: reviewerEnabled,
		agentManager:    agentManager,
		agentDefs:       toCogitoDefinitions(cfg.Agents),
		llmModel:        cfg.Model,
		apiKey:          cfg.APIKey,
		baseURL:         cfg.BaseURL,
	}
	s.hooks.Fire(ctx, hooks.EventSessionStart, "", map[string]any{"event": "SessionStart"})
	return s, nil
```

(d) Add the `decideToolCall` method (place after `LoadSkill`):

```go
// decideToolCall resolves a tool-call request: PreToolUse hooks first (a hook
// may block/approve/adjust), then the session allow-list, then the user gate.
func (s *Session) decideToolCall(req ToolCallRequest) cogito.ToolCallDecision {
	if s.hooks != nil {
		decisions := s.hooks.Fire(s.ctx, hooks.EventPreToolUse, req.Name, map[string]any{
			"event":     "PreToolUse",
			"tool":      req.Name,
			"arguments": req.Arguments,
			"reasoning": req.Reasoning,
			"agent_id":  req.AgentID,
		})
		if td := hooks.CombineToolDecisions(decisions); td.Decided {
			return cogito.ToolCallDecision{Approved: td.Approve, Adjustment: td.Adjustment}
		}
	}

	if s.allowedTools[req.Name] {
		return cogito.ToolCallDecision{Approved: true}
	}
	if s.callbacks.OnToolCall == nil {
		return cogito.ToolCallDecision{Approved: true}
	}
	resp := s.callbacks.OnToolCall(req)
	if resp.AlwaysAllow && resp.Approved {
		s.allowedTools[req.Name] = true
	}
	return cogito.ToolCallDecision{Approved: resp.Approved, Adjustment: resp.Adjustment}
}
```

(e) In `emitAgentEvent`, after forwarding to the callback, fire the AgentEvent hook. Add at the end of the method:

```go
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventAgentEvent, string(a.Status), map[string]any{
			"event":  "AgentEvent",
			"id":     a.ID,
			"type":   a.Type,
			"status": string(a.Status),
		})
	}
```

(f) In `SendMessage`, fire `UserPromptSubmit` at the very top (before the `if s.systemPrompt != ""` block):

```go
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventUserPromptSubmit, "", map[string]any{"event": "UserPromptSubmit", "prompt": text})
	}
```

(g) In `SendMessage`, replace the `cogito.WithToolCallBack(func(tool *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision { ... })` block (the entire closure body currently doing allow-list + OnToolCall) with one that delegates to `decideToolCall`, and add a `WithToolCallResultCallback` for PostToolUse right after it:

```go
		cogito.WithToolCallBack(func(tool *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			args, err := json.Marshal(tool.Arguments)
			if err != nil {
				return cogito.ToolCallDecision{Approved: false}
			}
			return s.decideToolCall(ToolCallRequest{
				Name:      tool.Name,
				Arguments: string(args),
				Reasoning: tool.Reasoning,
				AgentID:   state.AgentID,
			})
		}),
		cogito.WithToolCallResultCallback(func(status cogito.ToolStatus) {
			if s.hooks != nil {
				s.hooks.Fire(s.ctx, hooks.EventPostToolUse, "", map[string]any{"event": "PostToolUse", "result": status})
			}
		}),
```

> NOTE: confirm the `cogito.ToolStatus` type name and that `WithToolCallResultCallback(func(cogito.ToolStatus))` is the correct signature in the pinned cogito version (run `go doc github.com/mudler/cogito.WithToolCallResultCallback` or grep the module). If the field/type differs, marshal whatever the callback actually receives — the payload just needs to be JSON-serializable; do not block on its exact shape.

(h) In `SendMessage`, fire `Stop` immediately before the final `return response, nil`:

```go
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventStop, "", map[string]any{"event": "Stop"})
	}

	return response, nil
```

- [ ] **Step 4: Run, expect PASS** — `go test ./chat/ -run TestDecideToolCallPreHook -v`. Then `go test ./chat/ -v` and `go vet ./chat/` and `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add chat/session.go chat/session_hooks_test.go
git commit -m "feat(chat): fire lifecycle hooks; PreToolUse gates tool calls"
```

---

## Task 4: e2e — plugin contributes a blocking PreToolUse hook

**Files:**
- Create: `plugin/e2e_p3_test.go`

**Context:** Real-git proof that a hook-contributing plugin installs, merges (with `Dir` stamped to the plugin root so the hook script is found), and that a built `Session.decideToolCall` denies a matching tool via the plugin's PreToolUse hook. Reuses `gitInitRepoFiles` (P1 e2e, same package). Note `chat` importing `plugin` would be a cycle — instead this test lives in `plugin` and builds the session from the merged config via the `chat` package (plugin→chat is fine: chat does not import plugin).

- [ ] **Step 1: Create `plugin/e2e_p3_test.go`:**

```go
package plugin

import (
	"context"
	"os/exec"
	"testing"

	"github.com/mudler/wiz/chat"
	"github.com/mudler/wiz/types"
)

func TestEndToEndHookBlocksTool(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	repo := gitInitRepoFiles(t, map[string]string{
		"wiz-plugin.yaml": "name: p3demo\n" +
			"hooks:\n  - event: PreToolUse\n    matcher: bash\n    command: ./guard.sh\n",
		// guard blocks any bash tool call
		"guard.sh": "#!/bin/sh\necho '{\"block\": true, \"reason\": \"bash blocked by plugin\"}'\n",
	})

	mgr := NewManager(base)
	if _, err := mgr.Install(repo, "", "0.9.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := mgr.SetEnabled("p3demo", true); err != nil {
		t.Fatal(err)
	}

	cfg := types.Config{}
	if err := Apply(&cfg, base, "0.9.0"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Event != "PreToolUse" || cfg.Hooks[0].Dir == "" {
		t.Fatalf("hook not merged with Dir stamped: %+v", cfg.Hooks)
	}

	// Build a real session from the merged config; the PreToolUse hook must deny bash.
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{
		OnToolCall: func(chat.ToolCallRequest) chat.ToolCallResponse {
			return chat.ToolCallResponse{Approved: true} // would approve, but the hook should pre-empt
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if !s.ToolCallDenied(chat.ToolCallRequest{Name: "bash", Arguments: "{}"}) {
		t.Fatal("expected the plugin PreToolUse hook to deny the bash tool")
	}
}
```

> This test calls a small exported test-friendly helper `Session.ToolCallDenied`. Add it to `chat/session.go` in this task (it wraps `decideToolCall` so the e2e doesn't need an unexported method):
> ```go
> // ToolCallDenied reports whether the given tool call would be denied (used to
> // verify PreToolUse hook gating end-to-end).
> func (s *Session) ToolCallDenied(req ToolCallRequest) bool {
> 	return !s.decideToolCall(req).Approved
> }
> ```

- [ ] **Step 2: Add `ToolCallDenied`** to `chat/session.go` (the snippet above, after `decideToolCall`), then run `go test ./plugin/ -run TestEndToEndHookBlocksTool -v`. Expected PASS. If it fails, investigate the failing unit (Tasks 1–3), report BLOCKED, do not weaken the test.

- [ ] **Step 3: Whole suite** — `go test ./...` and `go vet ./...` → all PASS.

- [ ] **Step 4: Commit**

```bash
git add plugin/e2e_p3_test.go chat/session.go
git commit -m "test(plugin): e2e PreToolUse hook blocks a tool end-to-end"
```

---

## Self-Review (completed during planning)

**Spec coverage (P3 scope):**
- Hook config + manifest + merge (accumulate, Dir stamping) → Task 1 ✓
- Dispatcher: events, stdin JSON, `${WIZ_PLUGIN_ROOT}`/`${CLAUDE_PLUGIN_ROOT}`, matcher, exit-code-blocks, PreToolUse combine → Task 2 ✓
- Six events fired from the session; PreToolUse merges into the existing approval gate (deny/approve/adjust); refactor to `decideToolCall` → Task 3 ✓
- e2e (real git, blocking PreToolUse hook through a real Session) → Task 4 ✓; binary validation is the controller step after Task 4.

**Out of P3 (correctly deferred):** Claude `hooks.json` mapping (P4) — but the dispatcher already exports `${CLAUDE_PLUGIN_ROOT}`. Rich UserPromptSubmit blocking/context injection (observational only in P3). No TUI changes.

**Type consistency:** `types.HookConfig{Event,Matcher,Command,Dir}` used in types/manifest/discover/hooks/chat. `hooks.Event` constants + `hooks.Decision{Approved *bool,Block,Adjustment,Reason}` + `hooks.ToolDecision{Decided,Approve,Adjustment,Reason}` + `Dispatcher.Fire(ctx,Event,name,payload)` + `CombineToolDecisions` consistent across hooks + chat. `Session.decideToolCall(req) cogito.ToolCallDecision` and `Session.ToolCallDenied(req) bool` match their call sites. Merge accumulation mirrors P1 prompt fragments.

**Security note:** hooks execute arbitrary shell with user privileges — the trust boundary is install-time consent (P0). PreToolUse hooks can auto-approve/deny (trusted-plugin model), documented. Non-zero exit = block is fail-safe (a broken/timing-out hook denies rather than silently allowing).

**Risk note:** Task 3 edits the security-sensitive tool gate. The refactor preserves the exact existing allow-list + `OnToolCall` semantics (verified by the existing chat tests staying green) and only prepends the PreToolUse consultation; `decideToolCall` is unit-tested for both the gated and fall-through paths.
