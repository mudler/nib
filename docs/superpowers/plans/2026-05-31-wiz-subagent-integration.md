# Wiz Sub-Agent Integration Implementation Plan (Plan B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire wiz to the enhanced cogito (Plan A): always-on agent spawning with hardcoded-default agent types overridable via YAML, per-agent model+temperature via an LLM factory, sub-agent tool approval routed through wiz's existing gate (labeled per agent), a persistent TUI jobs footer with Ctrl+B to background a running foreground sub-agent, and CLI lifecycle notifications. Keep `force_reasoning` (default off); add no reasoning-tool.

**Architecture:** wiz owns policy + UI; cogito owns mechanism. `chat.Session` constructs a shared `cogito.AgentManager`, registers `cogito.AgentDefinition`s built from wiz config, supplies a `WithAgentLLMFactory` that reuses the parent endpoint/credentials with a per-agent model+temperature, and exposes agent lifecycle to the UI through new callbacks. The TUI consumes lifecycle events over a channel (mirroring the existing status/reasoning channels) and calls `AgentManager.Detach` on Ctrl+B.

**Tech Stack:** Go 1.24, `github.com/mudler/cogito` (+ `cogito/clients`), `bubbletea`/`bubbles`/`lipgloss`, `gopkg.in/yaml.v3`, standard `testing`.

**Depends on:** Plan A merged (or its branch). Task B1 pins `go.mod` to the cogito branch commit recorded at Plan A Task A10 Step 3. All file references are relative to `~/_git/wiz` (current branch `feat/cogito-subagents`).

---

## File Structure

- `go.mod` — point `github.com/mudler/cogito` at the Plan A branch commit.
- `chat/session.go` — modify: import `cogito/clients`; build `AgentManager`, definitions, factory; always-on `EnableAgentSpawning`; agent-aware `OnToolCall`; expose `AgentManager()` accessor.
- `chat/callbacks.go` — **new file** (extract + extend `Callbacks`, `ToolCallRequest`, new `AgentEvent`). Keeps `session.go` focused.
- `types/config.go` — modify: add `AgentTypeConfig` and `Agents []AgentTypeConfig` to `Config`.
- `config/agents.go` — **new file**: hardcoded default agent types + merge-with-YAML.
- `config/config.go` — modify: call the agent-defaults merge.
- `tui/model.go` — modify: agent-event channel, listener, jobs-footer state, Ctrl+B handling.
- `tui/agents.go` — **new file**: jobs-footer rendering + agent-aware tool/approval labels.
- `cmd/cli.go` — modify: print agent lifecycle lines.

---

## Task B1: Bump cogito to the enhanced branch + migrate import

**Files:**
- Modify: `go.mod`
- Modify: `chat/session.go` (import path for `NewOpenAILLM`)
- Test: `chat/session_build_test.go` (create)

**Context:** In v0.8.1 `NewOpenAILLM` lived in the root `cogito` package; from v0.10.0 it lives in `cogito/clients`. Bumping requires migrating the two call sites in `chat/session.go`.

- [ ] **Step 1: Point go.mod at the Plan A branch**

Run (replace `<SHA>` with the commit recorded at Plan A Task A10 Step 3):

```bash
cd ~/_git/wiz
go get github.com/mudler/cogito@<SHA>
```

Expected: `go.mod` now shows a pseudo-version like `v0.10.1-0.YYYYMMDD...-<SHA>`. If `go get` cannot resolve the unpushed branch, add a temporary replace directive instead:

```bash
go mod edit -replace github.com/mudler/cogito=../cogito
go mod tidy
```

> If you use the replace directive, leave a `// TODO: drop replace once cogito <branch> is tagged` comment in `go.mod` and note it in the PR.

- [ ] **Step 2: Migrate the NewOpenAILLM import**

In `chat/session.go`, add the clients import:

```go
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
```

Change both constructor call sites:

```go
	llm := cogito.NewOpenAILLM(cfg.Model, cfg.APIKey, cfg.BaseURL)
```
→
```go
	llm := clients.NewOpenAILLM(cfg.Model, cfg.APIKey, cfg.BaseURL)
```

and

```go
			reviewerLLM = cogito.NewOpenAILLM(cfg.ReviewerLLM.Model, reviewerAPIKey, reviewerBaseURL)
```
→
```go
			reviewerLLM = clients.NewOpenAILLM(cfg.ReviewerLLM.Model, reviewerAPIKey, reviewerBaseURL)
```

- [ ] **Step 3: Write a build/guard test**

Create `chat/session_build_test.go`:

```go
package chat

import (
	"testing"

	"github.com/mudler/wiz/types"
)

// TestForceReasoningDefaultsOff guards the spec decision: force_reasoning stays
// opt-in (default off) and no reasoning-tool option is introduced.
func TestForceReasoningDefaultsOff(t *testing.T) {
	var opts types.AgentOptions
	if opts.ForceReasoning {
		t.Fatal("ForceReasoning must default to false")
	}
}
```

- [ ] **Step 4: Build + test**

Run: `go build ./... && go test ./chat/ -run TestForceReasoningDefaultsOff`
Expected: BUILD OK, PASS.

- [ ] **Step 5: Confirm no reasoning-tool option is wired**

Run: `grep -rn "WithForceReasoningTool\|ForceReasoningTool" . --include=*.go`
Expected: no matches in wiz (cogito may define it; wiz must not call it).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum chat/session.go chat/session_build_test.go
git commit -m "build: bump cogito to sub-agent branch; migrate NewOpenAILLM to clients pkg"
```

---

## Task B2: Config — agent types (defaults + YAML override)

**Files:**
- Modify: `types/config.go`
- Create: `config/agents.go`
- Modify: `config/config.go`
- Test: `config/agents_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `config/agents_test.go`:

```go
package config

import "testing"

func TestDefaultAgentTypesPresent(t *testing.T) {
	defs := MergeAgentTypes(nil)
	if len(defs) == 0 {
		t.Fatal("expected built-in agent types")
	}
	if findType(defs, "explore") == nil || findType(defs, "plan") == nil {
		t.Fatalf("expected default explore+plan types, got %v", names(defs))
	}
}

func TestYAMLOverridesByName(t *testing.T) {
	override := []AgentTypeConfig{
		{Name: "explore", SystemPrompt: "CUSTOM EXPLORE", Model: "small"},
		{Name: "custom", Description: "user type", SystemPrompt: "hi"},
	}
	defs := MergeAgentTypes(override)
	ex := findType(defs, "explore")
	if ex == nil || ex.SystemPrompt != "CUSTOM EXPLORE" || ex.Model != "small" {
		t.Fatalf("explore not overridden: %+v", ex)
	}
	if findType(defs, "custom") == nil {
		t.Fatal("custom type not added")
	}
	// Non-overridden defaults survive.
	if findType(defs, "plan") == nil {
		t.Fatal("plan default lost after override")
	}
}

// test helpers
func findType(defs []AgentTypeConfig, name string) *AgentTypeConfig {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}
func names(defs []AgentTypeConfig) []string {
	var n []string
	for _, d := range defs {
		n = append(n, d.Name)
	}
	return n
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./config/ -run 'TestDefaultAgentTypes|TestYAMLOverrides'`
Expected: FAIL — `AgentTypeConfig` and `MergeAgentTypes` undefined.

- [ ] **Step 3: Add the config type**

In `types/config.go`, add the type and a field on `Config`:

```go
// AgentTypeConfig is a wiz-facing sub-agent type. It maps 1:1 to a
// cogito.AgentDefinition. Zero-valued numeric fields mean "inherit".
type AgentTypeConfig struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	SystemPrompt string   `yaml:"system_prompt"`
	Tools        []string `yaml:"tools"`
	Model        string   `yaml:"model"`
	Temperature  float32  `yaml:"temperature"`
	Iterations   int      `yaml:"iterations"`
	MaxAttempts  int      `yaml:"max_attempts"`
	MaxRetries   int      `yaml:"max_retries"`
}
```

Add to the `Config` struct:

```go
	Agents         []AgentTypeConfig    `yaml:"agents"`
```

- [ ] **Step 4: Add the defaults + merge**

Create `config/agents.go`:

```go
package config

import "github.com/mudler/wiz/types"

// AgentTypeConfig is re-exported for ergonomic use within the config package.
type AgentTypeConfig = types.AgentTypeConfig

// defaultAgentTypes are the built-in sub-agent personas. They are overridable
// and extendable via the `agents:` block in wiz config (see MergeAgentTypes).
func defaultAgentTypes() []AgentTypeConfig {
	return []AgentTypeConfig{
		{
			Name:         "general",
			Description:  "general-purpose helper for self-contained subtasks",
			SystemPrompt: "You are a focused sub-agent. Complete the given task and report a concise result.",
			Iterations:   15,
		},
		{
			Name:         "explore",
			Description:  "read-only codebase/file exploration; returns findings",
			SystemPrompt: "You are an exploration sub-agent. Investigate and summarize findings. Prefer read-only tools. Return the conclusion, not raw dumps.",
			Iterations:   25,
		},
		{
			Name:         "plan",
			Description:  "produce a step-by-step plan for a goal without executing it",
			SystemPrompt: "You are a planning sub-agent. Produce a concrete, ordered plan. Do not execute irreversible actions.",
			Iterations:   15,
		},
	}
}

// MergeAgentTypes returns the built-in types with any user entries merged in:
// an entry whose Name matches a default overrides it field-for-field; a new
// Name is appended. Defaults not mentioned by the user are preserved.
func MergeAgentTypes(user []AgentTypeConfig) []AgentTypeConfig {
	merged := defaultAgentTypes()
	for _, u := range user {
		replaced := false
		for i := range merged {
			if merged[i].Name == u.Name {
				merged[i] = u
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, u)
		}
	}
	return merged
}
```

- [ ] **Step 5: Wire the merge into config load**

In `config/config.go`, after the existing cogito-option defaults block (after the `MaxRetries` default, around line 122), add:

```go
	// Merge user-provided agent types with the built-in defaults.
	cfg.Agents = MergeAgentTypes(cfg.Agents)
```

- [ ] **Step 6: Run tests**

Run: `go test ./config/ -run 'TestDefaultAgentTypes|TestYAMLOverrides'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add types/config.go config/agents.go config/config.go config/agents_test.go
git commit -m "feat(config): built-in agent types with YAML override"
```

---

## Task B3: Callbacks — AgentEvent + AgentID on tool requests

**Files:**
- Create: `chat/callbacks.go` (move `Callbacks`, `ToolCallRequest`, `ToolCallResponse`, `Plan`, `PlanResponse`, `Message` here from `session.go`; add `AgentEvent` + `OnAgentEvent`; add `AgentID` to `ToolCallRequest`)
- Modify: `chat/session.go` (delete the moved type declarations)
- Test: `chat/callbacks_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `chat/callbacks_test.go`:

```go
package chat

import "testing"

func TestToolCallRequestCarriesAgentID(t *testing.T) {
	r := ToolCallRequest{Name: "echo", AgentID: "a1"}
	if r.AgentID != "a1" {
		t.Fatalf("AgentID not set, got %q", r.AgentID)
	}
}

func TestAgentEventFields(t *testing.T) {
	e := AgentEvent{ID: "a1", Type: "explore", Task: "look", Status: AgentStatusRunning}
	if e.ID != "a1" || e.Status != AgentStatusRunning {
		t.Fatalf("AgentEvent fields wrong: %+v", e)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./chat/ -run 'TestToolCallRequestCarriesAgentID|TestAgentEventFields'`
Expected: FAIL — `ToolCallRequest.AgentID`, `AgentEvent`, `AgentStatusRunning` undefined.

- [ ] **Step 3: Create callbacks.go with the moved + new types**

Create `chat/callbacks.go`:

```go
package chat

// AgentStatus mirrors cogito's sub-agent lifecycle states for UI consumption,
// decoupling the UI from the cogito type.
type AgentStatus string

const (
	AgentStatusRunning   AgentStatus = "running"
	AgentStatusCompleted AgentStatus = "completed"
	AgentStatusFailed    AgentStatus = "failed"
)

// AgentEvent is emitted on sub-agent lifecycle changes (spawn/complete/fail).
type AgentEvent struct {
	ID     string
	Type   string // agent type name (e.g. "explore"); empty for generic
	Task   string
	Status AgentStatus
	Result string
	Err    error
}

// Message represents a chat message.
type Message struct {
	Role    string
	Content string
}

// ToolCallRequest contains information about a tool the agent wants to run.
type ToolCallRequest struct {
	Name      string
	Arguments string
	Reasoning string
	AgentID   string // non-empty when the requesting caller is a sub-agent
}

// ToolCallResponse represents the user's decision on a tool call.
type ToolCallResponse struct {
	Approved    bool
	Adjustment  string
	AlwaysAllow bool
}

// Plan represents a plan with description and subtasks.
type Plan struct {
	Description string
	Subtasks    []string
}

// PlanResponse represents the user's decision on a plan.
type PlanResponse struct {
	Approved bool
}

// Callbacks defines the interface for UI interactions.
type Callbacks struct {
	OnStatus    func(status string)
	OnReasoning func(reasoning string)
	OnToolCall  func(req ToolCallRequest) ToolCallResponse
	OnPlan      func(plan Plan) PlanResponse
	OnResponse  func(response string)
	OnError     func(err error)
	// OnAgentEvent is called on sub-agent lifecycle changes. Optional.
	OnAgentEvent func(ev AgentEvent)
}
```

- [ ] **Step 4: Remove the duplicated declarations from session.go**

In `chat/session.go`, delete the now-duplicated type declarations: `Message`, `ToolCallRequest`, `ToolCallResponse`, `Plan`, `PlanResponse`, and `Callbacks` (lines ~17–64 in the current file). Leave everything else.

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./chat/ -run 'TestToolCallRequestCarriesAgentID|TestAgentEventFields'`
Expected: BUILD OK, PASS. (Duplicate-declaration build errors mean a type was left in `session.go` — remove it.)

- [ ] **Step 6: Commit**

```bash
git add chat/callbacks.go chat/session.go chat/callbacks_test.go
git commit -m "refactor(chat): extract callbacks; add AgentEvent and AgentID on tool requests"
```

---

## Task B4: Session wiring — spawning, definitions, factory, agent-aware approval

**Files:**
- Modify: `chat/session.go`
- Test: `chat/session_agents_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `chat/session_agents_test.go`:

```go
package chat

import (
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/wiz/types"
)

func TestToCogitoDefinitionsMapsFields(t *testing.T) {
	in := []types.AgentTypeConfig{{
		Name: "explore", Description: "d", SystemPrompt: "sp",
		Tools: []string{"echo"}, Model: "m", Temperature: 0.4,
		Iterations: 9, MaxAttempts: 2, MaxRetries: 1,
	}}
	out := toCogitoDefinitions(in)
	if len(out) != 1 {
		t.Fatalf("want 1 def, got %d", len(out))
	}
	d := out[0]
	if d.Name != "explore" || d.SystemPrompt != "sp" || d.Model != "m" ||
		d.Temperature != 0.4 || d.Iterations != 9 || d.MaxAttempts != 2 || d.MaxRetries != 1 ||
		len(d.Tools) != 1 || d.Tools[0] != "echo" {
		t.Fatalf("mapping wrong: %+v", d)
	}
	var _ cogito.AgentDefinition = d
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./chat/ -run TestToCogitoDefinitions`
Expected: FAIL — `toCogitoDefinitions` undefined.

- [ ] **Step 3: Add the mapping helper**

In `chat/session.go`, add:

```go
// toCogitoDefinitions converts wiz agent-type config into cogito definitions.
func toCogitoDefinitions(types []types.AgentTypeConfig) []cogito.AgentDefinition {
	defs := make([]cogito.AgentDefinition, 0, len(types))
	for _, t := range types {
		defs = append(defs, cogito.AgentDefinition{
			Name:         t.Name,
			Description:  t.Description,
			SystemPrompt: t.SystemPrompt,
			Tools:        t.Tools,
			Model:        t.Model,
			Temperature:  t.Temperature,
			Iterations:   t.Iterations,
			MaxAttempts:  t.MaxAttempts,
			MaxRetries:   t.MaxRetries,
		})
	}
	return defs
}
```

> The parameter name `types` shadows the imported `types` package inside this function only — that's fine since the body doesn't reference the package. If the linter objects, rename the param to `cfgs`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./chat/ -run TestToCogitoDefinitions`
Expected: PASS.

- [ ] **Step 5: Add the AgentManager to the Session and store config**

In `chat/session.go`, add fields to the `Session` struct:

```go
	agentManager *cogito.AgentManager
	agentDefs    []cogito.AgentDefinition
	llmModel     string
	apiKey       string
	baseURL      string
```

In `NewSession`, after creating `llm`, store the connection params and build the manager + defs:

```go
	agentManager := cogito.NewAgentManager()
```

and in the returned `&Session{...}` literal add:

```go
		agentManager: agentManager,
		agentDefs:    toCogitoDefinitions(cfg.Agents),
		llmModel:     cfg.Model,
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
```

- [ ] **Step 6: Add an AgentManager accessor (for the TUI)**

In `chat/session.go`:

```go
// AgentManager exposes the sub-agent registry so the UI can list and detach agents.
func (s *Session) AgentManager() *cogito.AgentManager {
	return s.agentManager
}
```

- [ ] **Step 7: Assemble the agent options in SendMessage**

In `chat/session.go`, in `SendMessage`, extend the `cogitoOpts` slice (after the existing `WithToolCallBack(...)` block, before the `ForceReasoning` block). First, make the existing tool callback agent-aware by reading `state.AgentID`:

Change the `OnToolCall` forwarding inside `WithToolCallBack` so the request carries the agent id:

```go
			resp := s.callbacks.OnToolCall(ToolCallRequest{
				Name:      tool.Name,
				Arguments: string(args),
				Reasoning: tool.Reasoning,
				AgentID:   state.AgentID,
			})
```

Then append the agent options:

```go
	cogitoOpts = append(cogitoOpts,
		cogito.EnableAgentSpawning,
		cogito.WithAgentManager(s.agentManager),
		cogito.WithAgentDefinitions(s.agentDefs...),
		cogito.WithAgentLLMFactory(func(model string, temperature float32) cogito.LLM {
			return clients.NewOpenAILLMWithOptions(model, s.apiKey, s.baseURL, clients.OpenAIOptions{Temperature: temperature})
		}),
		cogito.WithAgentCompletionCallback(func(a *cogito.AgentState) {
			if s.callbacks.OnAgentEvent != nil {
				s.callbacks.OnAgentEvent(AgentEvent{
					ID:     a.ID,
					Task:   a.Task,
					Status: AgentStatus(a.Status),
					Result: a.Result,
					Err:    a.Error,
				})
			}
		}),
	)
```

> `clients` is already imported from Task B1. `cogito.AgentState.Status` is `cogito.AgentStatusType` (a string); `AgentStatus(a.Status)` converts it to the wiz UI type.

- [ ] **Step 8: Build + run**

Run: `go build ./... && go test ./chat/ ./config/`
Expected: BUILD OK, PASS.

- [ ] **Step 9: Commit**

```bash
git add chat/session.go chat/session_agents_test.go
git commit -m "feat(chat): always-on agent spawning, definitions, LLM factory, agent-aware approval"
```

---

## Task B5: TUI — agent events channel + jobs footer + Ctrl+B

**Files:**
- Modify: `tui/model.go`
- Create: `tui/agents.go`
- Test: `tui/agents_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `tui/agents_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/chat"
)

func TestRenderJobsFooterEmpty(t *testing.T) {
	if got := renderJobsFooter(nil, 80); got != "" {
		t.Fatalf("empty jobs should render nothing, got %q", got)
	}
}

func TestRenderJobsFooterCounts(t *testing.T) {
	jobs := []agentJob{
		{ID: "a1", Type: "explore", Task: "scan repo", Status: chat.AgentStatusRunning},
		{ID: "b2", Type: "plan", Task: "draft", Status: chat.AgentStatusCompleted},
	}
	out := renderJobsFooter(jobs, 80)
	if !strings.Contains(out, "1 running") {
		t.Fatalf("footer should report running count, got %q", out)
	}
}

func TestToolLabelWithAgent(t *testing.T) {
	got := toolApprovalLabel(chat.ToolCallRequest{Name: "echo", AgentID: "a1"})
	if !strings.Contains(got, "a1") || !strings.Contains(got, "echo") {
		t.Fatalf("expected agent-labeled approval, got %q", got)
	}
	root := toolApprovalLabel(chat.ToolCallRequest{Name: "echo"})
	if strings.Contains(root, "→") {
		t.Fatalf("root tool should not show an agent arrow, got %q", root)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tui/ -run 'TestRenderJobsFooter|TestToolLabel'`
Expected: FAIL — `renderJobsFooter`, `agentJob`, `toolApprovalLabel` undefined.

- [ ] **Step 3: Create tui/agents.go**

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/wiz/chat"
)

// agentJob is the UI view of a sub-agent for the jobs footer.
type agentJob struct {
	ID     string
	Type   string
	Task   string
	Status chat.AgentStatus
}

var jobsFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

// renderJobsFooter renders a compact one-line summary of active jobs.
// Returns "" when there are no jobs so the footer takes no vertical space.
func renderJobsFooter(jobs []agentJob, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var running, done, failed int
	for _, j := range jobs {
		switch j.Status {
		case chat.AgentStatusRunning:
			running++
		case chat.AgentStatusCompleted:
			done++
		case chat.AgentStatusFailed:
			failed++
		}
	}
	parts := []string{fmt.Sprintf("⚙ jobs: %d running", running)}
	if done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", done))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	parts = append(parts, "(ctrl+b background · ctrl+j detail)")
	line := strings.Join(parts, "  ·  ")
	return jobsFooterStyle.Width(width).Render(line)
}

// renderJobsDetail renders the expanded per-job list.
func renderJobsDetail(jobs []agentJob, width int) string {
	if len(jobs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, j := range jobs {
		task := j.Task
		if len(task) > 40 {
			task = task[:37] + "..."
		}
		typ := j.Type
		if typ == "" {
			typ = "agent"
		}
		b.WriteString(fmt.Sprintf("  %s  %-8s  %-10s  %s\n",
			shortID(j.ID), typ, j.Status, task))
	}
	return jobsFooterStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// toolApprovalLabel builds the tool-approval header, labeling sub-agent calls.
func toolApprovalLabel(req chat.ToolCallRequest) string {
	if req.AgentID != "" {
		return fmt.Sprintf("🤖 %s → run: %s", shortID(req.AgentID), req.Name)
	}
	return fmt.Sprintf("🔧 run: %s", req.Name)
}
```

- [ ] **Step 4: Run the footer/label tests**

Run: `go test ./tui/ -run 'TestRenderJobsFooter|TestToolLabel'`
Expected: PASS.

- [ ] **Step 5: Add agent-event plumbing to the model**

In `tui/model.go`, add to the `Model` struct:

```go
	// Sub-agent jobs state
	jobs            []agentJob
	showJobsDetail  bool
	agentEventChan  chan chat.AgentEvent
```

In `NewModel`, initialize the channel:

```go
		agentEventChan:   make(chan chat.AgentEvent, 16),
```

Add `agentEventMsg` near the other msg types:

```go
// agentEventMsg is sent for sub-agent lifecycle updates.
type agentEventMsg chat.AgentEvent
```

In `initSession`, add the callback to the `chat.Callbacks{...}`:

```go
			OnAgentEvent: func(ev chat.AgentEvent) {
				select {
				case m.agentEventChan <- ev:
				default:
				}
			},
```

Add a listener command (near `listenReasoning`):

```go
func (m Model) listenAgentEvents() tea.Cmd {
	return func() tea.Msg {
		return agentEventMsg(<-m.agentEventChan)
	}
}
```

Register the listener wherever the other listeners are batched (the same place `listenStatus()`/`listenReasoning()` are added, around line 268):

```go
		cmds = append(cmds, m.listenStatus(), m.listenReasoning(), m.listenToolRequest(), m.listenPlanRequest(), m.listenAgentEvents())
```

- [ ] **Step 6: Handle agentEventMsg in Update**

In `tui/model.go` `Update`, add a case (mirroring the `reasoningMsg` case):

```go
	case agentEventMsg:
		m.applyAgentEvent(chat.AgentEvent(msg))
		cmds = append(cmds, m.listenAgentEvents())
```

Add the apply helper to `tui/agents.go`:

```go
// applyAgentEvent upserts a job by ID and refreshes status.
func (m *Model) applyAgentEvent(ev chat.AgentEvent) {
	for i := range m.jobs {
		if m.jobs[i].ID == ev.ID {
			m.jobs[i].Status = ev.Status
			if ev.Type != "" {
				m.jobs[i].Type = ev.Type
			}
			return
		}
	}
	m.jobs = append(m.jobs, agentJob{ID: ev.ID, Type: ev.Type, Task: ev.Task, Status: ev.Status})
}
```

> `applyAgentEvent` has a pointer receiver; in the `Update` case call it on a pointer. Since bubbletea's `Update` uses a value receiver `m Model`, take the address: `(&m).applyAgentEvent(...)` or refactor the case to `tmp := m; tmp.applyAgentEvent(...); m = tmp`. Use `am := m; (&am).applyAgentEvent(chat.AgentEvent(msg)); m = am` to stay within the value-receiver pattern already used in this file.

- [ ] **Step 7: Wire Ctrl+B to detach the running foreground job**

In `tui/model.go` `Update`, in the `tea.KeyMsg` switch, add a case for Ctrl+B (alongside the existing `KeyCtrlC`/`KeyEsc` handling):

```go
		case tea.KeyCtrlB:
			if m.sessionReady && m.session != nil {
				if id := m.firstRunningJobID(); id != "" {
					_ = m.session.AgentManager().Detach(id)
				}
			}
			return m, nil
		case tea.KeyCtrlJ:
			m.showJobsDetail = !m.showJobsDetail
			return m, nil
```

Add the helper to `tui/agents.go`:

```go
// firstRunningJobID returns the id of the first running job, or "".
func (m Model) firstRunningJobID() string {
	for _, j := range m.jobs {
		if j.Status == chat.AgentStatusRunning {
			return j.ID
		}
	}
	return ""
}
```

> **Foreground detach nuance:** in v1 the foreground sub-agent is the one the loop is blocked on; it is registered in the manager (Plan A Task A9). `firstRunningJobID` selects it. If multiple agents run, this targets the first running one — acceptable for v1; a job-picker is future work.

- [ ] **Step 8: Render the footer in View**

In `tui/model.go` `View`, append the jobs footer/detail near the bottom of the rendered output (after the main content, before/around the textarea). Locate the final `return` of `View` and incorporate:

```go
	footer := renderJobsFooter(m.jobs, m.width)
	if m.showJobsDetail {
		if d := renderJobsDetail(m.jobs, m.width); d != "" {
			footer = d + "\n" + footer
		}
	}
	// Append `footer` to the composed view string when non-empty.
```

> Integrate by concatenating `footer` into the existing `View` return value (the file builds the view as a string). If `footer == ""`, add nothing so layout is unchanged when there are no jobs.

- [ ] **Step 9: Use the agent-aware label in the approval prompt**

In `tui/model.go` where the pending tool approval is rendered (the `m.pendingTool` block around line 786), replace the hardcoded tool header with `toolApprovalLabel(*m.pendingTool)` so sub-agent calls are labeled with their agent id.

- [ ] **Step 10: Build + test**

Run: `go build ./... && go test ./tui/`
Expected: BUILD OK, PASS.

- [ ] **Step 11: Commit**

```bash
git add tui/model.go tui/agents.go tui/agents_test.go
git commit -m "feat(tui): jobs footer, agent-labeled approvals, ctrl+b detach, ctrl+j detail"
```

---

## Task B6: CLI — agent lifecycle notifications

**Files:**
- Modify: `cmd/cli.go`
- Test: `cmd/cli_agents_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `cmd/cli_agents_test.go`:

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/chat"
)

func TestFormatAgentEventLine(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{
		ID: "abcd1234ef", Type: "explore", Task: "scan", Status: chat.AgentStatusCompleted, Result: "found 3 files",
	})
	if !strings.Contains(line, "abcd1234") || !strings.Contains(line, "completed") {
		t.Fatalf("unexpected line: %q", line)
	}
}

func TestFormatAgentEventLineFailure(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{ID: "x", Status: chat.AgentStatusFailed})
	if !strings.Contains(line, "failed") {
		t.Fatalf("expected failed marker, got %q", line)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestFormatAgentEventLine`
Expected: FAIL — `formatAgentEventLine` undefined.

- [ ] **Step 3: Add the formatter + callback**

In `cmd/cli.go`, add the formatter:

```go
// formatAgentEventLine renders a one-line CLI notification for a sub-agent event.
func formatAgentEventLine(ev chat.AgentEvent) string {
	id := ev.ID
	if len(id) > 8 {
		id = id[:8]
	}
	typ := ev.Type
	if typ == "" {
		typ = "agent"
	}
	switch ev.Status {
	case chat.AgentStatusCompleted:
		return fmt.Sprintf("%s🤖 %s (%s) completed: %s%s", colorGray, typ, id, ev.Result, colorReset)
	case chat.AgentStatusFailed:
		return fmt.Sprintf("%s🤖 %s (%s) failed: %v%s", colorRed, typ, id, ev.Err, colorReset)
	default:
		return fmt.Sprintf("%s🤖 %s (%s) %s%s", colorGray, typ, id, ev.Status, colorReset)
	}
}
```

In `RunCLI`, add to the `chat.Callbacks{...}`:

```go
		OnAgentEvent: func(ev chat.AgentEvent) {
			spin.stop()
			fmt.Println(formatAgentEventLine(ev))
			spin.start("Conjuring...")
		},
```

- [ ] **Step 4: Build + test**

Run: `go build ./... && go test ./cmd/ -run TestFormatAgentEventLine`
Expected: BUILD OK, PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cli.go cmd/cli_agents_test.go
git commit -m "feat(cli): print sub-agent lifecycle notifications"
```

---

## Task B7: Full verification + manual smoke

**Files:** none

- [ ] **Step 1: Vet + full test suite**

Run: `go vet ./... && go test ./...`
Expected: clean vet, all PASS.

- [ ] **Step 2: Manual smoke (CLI)**

Run wiz in CLI mode against a configured model and prompt something that invites delegation (e.g. "explore this repo and summarize the build setup, using a sub-agent"). Confirm:
- a `🤖 explore (…)` line appears,
- sub-agent tool calls still prompt for approval, labeled with the agent,
- the conversation auto-continues after the background agent completes.

- [ ] **Step 3: Manual smoke (TUI + Ctrl+B)**

Launch the TUI, trigger a sub-agent, confirm the jobs footer appears; while a foreground sub-agent runs, press **Ctrl+B** and confirm it detaches (prompt returns, footer shows it running), and **Ctrl+J** toggles the detail list.

- [ ] **Step 4: Update README features**

Add the new capabilities to `README.md` Features (sub-agents, background jobs, Ctrl+B). Commit:

```bash
git add README.md
git commit -m "docs: document sub-agents, background jobs, and ctrl+b"
```

- [ ] **Step 5: Push**

```bash
git push -u origin feat/cogito-subagents
```

---

## Self-Review (completed during planning)

- **Spec coverage:** W1→B1; W2→B2; W3→B4 (+B1 import migration); W4→B3; W5→B5; W6→B6. Plus B7 verification. All wiz components mapped.
- **Type consistency:** `AgentTypeConfig` fields identical in `types/config.go` (B2), `config/agents.go` (B2), and `toCogitoDefinitions` (B4), and map 1:1 to `cogito.AgentDefinition` (Plan A A3). `chat.AgentStatus` constants (`AgentStatusRunning/Completed/Failed`, B3) are used consistently in `tui/agents.go` (B5) and `cmd/cli.go` (B6). `AgentEvent` fields (B3) consumed unchanged in B5/B6. `agentJob` defined once (B5). `toolApprovalLabel`/`renderJobsFooter`/`firstRunningJobID`/`applyAgentEvent` all defined in B5.
- **Cross-plan contract:** B4's factory uses `clients.NewOpenAILLMWithOptions` + `clients.OpenAIOptions{Temperature}` from Plan A A5; B4 reads `state.AgentID` from Plan A A1; B5's Ctrl+B uses `AgentManager.Detach` from Plan A A9. All exist before Plan B starts (Plan A merged first).
- **Placeholder scan:** the only deliberately-prose steps are the `View`-integration (B5 Step 8) and manual smokes (B7) — these are integration points into an existing string-built view and runtime checks, with the concrete helper code fully specified; no `TODO`/`TBD` left in code.
- **Decisions honored:** always-on spawning (B4, no flag); hardcoded defaults + YAML override (B2); shared parent LLM with model+temp factory (B4); auto-continue (cogito built-in, unchanged); persistent footer (B5); force_reasoning default off, no reasoning-tool (B1 guard test + grep).
