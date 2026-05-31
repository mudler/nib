# Cogito Sub-Agent Primitives Implementation Plan (Plan A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Claude-Code-style sub-agent primitives to cogito: parent tool-approval + MCP propagation to sub-agents, named agent definitions (with per-type system prompt, tools, model, temperature, and execution limits), per-agent model/temperature LLM resolution, a unified `send_agent_message` resume/inject tool, and detachable foreground spawns.

**Architecture:** All work happens in the `cogito` repo at `~/_git/cogito` on a feature branch. cogito gains the mechanisms; policy/UI stays in the embedder (wiz, Plan B). Sub-agents reuse cogito's existing message-injection/parking machinery for resume and background completion. Foreground spawns become `select`-able so an embedder can promote them to background.

**Tech Stack:** Go 1.24, `github.com/sashabaranov/go-openai`, `github.com/google/uuid`, Ginkgo/Gomega test suite (existing in repo), standard `testing` for plain unit tests.

**Baseline:** Branch from cogito's latest `master` (which includes the `v0.10.0` line: `WithAgentCompletionFormatter`, `formatAgentCompletion`). All file references are relative to `~/_git/cogito`.

---

## File Structure

- `agent.go` — extend: `AgentDefinition`, `SpawnAgentArgs` (new fields), `spawnAgentRunner` (definition resolution, LLM factory, detach), `AgentManager` (`Inject`, `Detach`, per-agent injection channel), `send_agent_message` tool.
- `options.go` — extend: `Options` struct fields (`agentDefinitions`, `agentLLMFactory`), new `With*` options.
- `tools.go` — extend: agent-tool injection block (~line 1115) to pass new fields + register `send_agent_message`; `SessionState` gets `AgentID` (defined in `tools.go`).
- `clients/openai_client.go` — extend: temperature support + `NewOpenAILLMWithOptions`.
- New tests live beside the code: `agent_definitions_test.go`, `agent_propagation_test.go`, `agent_resume_test.go`, `agent_detach_test.go`, `clients/openai_client_test.go`.

---

## Task A0: Prep — branch and baseline

**Files:** none (git only)

- [ ] **Step 1: Sync and branch cogito**

```bash
cd ~/_git/cogito
git stash --include-untracked   # set aside the local uncommitted go.mod/tools.go/acp scratch files
git fetch origin
git checkout master
git pull --ff-only origin master
git checkout -b feat/subagent-enhancements
```

- [ ] **Step 2: Verify the v0.10.0 baseline is present**

Run: `grep -n "func formatAgentCompletion" agent.go`
Expected: one match (confirms the completion-formatter baseline). If absent, the branch is older than v0.10.0 — stop and reconcile with the repo owner before continuing.

- [ ] **Step 3: Confirm the suite is green before changes**

Run: `go test ./... 2>&1 | tail -20`
Expected: PASS (or the repo's known-good baseline). Record any pre-existing failures so they are not attributed to this work.

---

## Task A1: Add AgentID to SessionState (C1, part 1)

**Files:**
- Modify: `tools.go` (the `SessionState` struct)
- Test: `agent_propagation_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `agent_propagation_test.go`:

```go
package cogito

import "testing"

func TestSessionStateHasAgentID(t *testing.T) {
	s := SessionState{AgentID: "abc-123"}
	if s.AgentID != "abc-123" {
		t.Fatalf("expected AgentID to round-trip, got %q", s.AgentID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSessionStateHasAgentID ./...`
Expected: FAIL — `unknown field 'AgentID' in struct literal of type SessionState`.

- [ ] **Step 3: Add the field**

In `tools.go`, change the `SessionState` struct from:

```go
type SessionState struct {
	ToolChoice *ToolChoice `json:"tool_choice"`
	Fragment   Fragment    `json:"fragment"`
}
```

to:

```go
type SessionState struct {
	ToolChoice *ToolChoice `json:"tool_choice"`
	Fragment   Fragment    `json:"fragment"`
	// AgentID identifies the sub-agent whose tool call is being evaluated.
	// Empty for the root agent. Set when the tool-call callback is invoked
	// from within a spawned sub-agent (see WithToolCallBack propagation).
	AgentID string `json:"agent_id,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestSessionStateHasAgentID ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools.go agent_propagation_test.go
git commit -m "feat(agent): add AgentID to SessionState for sub-agent tool routing"
```

---

## Task A2: Propagate parent tool-callback + MCPs to sub-agents (C1, part 2)

**Files:**
- Modify: `tools.go` (agent-spawning setup block, ~line 1115–1145 where `subAgentOpts` is built)
- Modify: `agent.go` (`spawnAgentRunner` — set `AgentID` on the propagated callback)
- Test: `agent_propagation_test.go`

**Context:** Today `tools.go` builds `subAgentOpts` with only iterations/attempts/retries. The parent `toolCallCallback` and `mcpSessions` are dropped, so sub-agent tool calls bypass approval. We add both, wrapping the callback so it stamps the sub-agent's ID into `SessionState.AgentID`.

- [ ] **Step 1: Write the failing test**

Append to `agent_propagation_test.go`:

```go
import (
	"context"
	"sync"
	"testing"
)

// fakeLLM selects a single named tool once, then replies (sink).
// Implemented inline in the test via the repo's existing test LLM helpers.
// See agent_test.go for the canonical mock; reuse newScriptedLLM there.

func TestSubAgentToolCallReachesParentCallback(t *testing.T) {
	var mu sync.Mutex
	var seenAgentIDs []string

	parentCB := func(tc *ToolChoice, st *SessionState) ToolCallDecision {
		mu.Lock()
		seenAgentIDs = append(seenAgentIDs, st.AgentID)
		mu.Unlock()
		return ToolCallDecision{Approved: true}
	}

	// A sub-agent that calls one echo tool then stops.
	echo := newEchoTool() // helper defined in Step 3 test-support
	llm := newScriptedLLM( // existing helper in agent_test.go / tools_test.go
		scriptCallTool("echo", map[string]any{"text": "hi"}),
		scriptReply("done"),
	)

	runner := &spawnAgentRunner{
		llm:            llm,
		parentTools:    Tools{echo},
		parentOpts:     []Option{WithToolCallBack(parentCB)},
		manager:        NewAgentManager(),
		ctx:            context.Background(),
	}

	_, _, err := runner.Run(SpawnAgentArgs{Task: "say hi", Background: false})
	if err != nil {
		t.Fatalf("foreground spawn errored: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenAgentIDs) == 0 {
		t.Fatal("parent tool callback was never invoked from the sub-agent")
	}
	for _, id := range seenAgentIDs {
		if id == "" {
			t.Fatal("expected a non-empty AgentID in sub-agent tool callback")
		}
	}
}
```

> **Note for the implementer:** `newScriptedLLM`, `scriptCallTool`, `scriptReply`, and a trivial `newEchoTool` may already exist under different names in `agent_test.go`/`tools_test.go`. Before writing new helpers, grep: `grep -n "func newScripted\|func newEcho\|mockLLM\|fakeLLM" *_test.go`. Reuse the existing mock; only add `newEchoTool` if no echo-style tool exists. If you add it, put it in `agent_propagation_test.go`:
>
> ```go
> type echoRunner struct{}
> func (echoRunner) Run(a map[string]any) (string, any, error) { return "echo", nil, nil }
> func newEchoTool() ToolDefinitionInterface {
> 	return NewToolDefinition(&rawRunner{echoRunner{}}, map[string]any{}, "echo", "echo text")
> }
> ```
> If `NewToolDefinition` requires a typed runner, mirror the signature used by `newCheckAgentTool` in `agent.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSubAgentToolCallReachesParentCallback ./...`
Expected: FAIL — the callback is never invoked because `spawnAgentRunner` does not propagate it (it only forwards `parentOpts`, which in production is just iterations/attempts/retries) and never sets `AgentID`.

- [ ] **Step 3: Make the sub-agent ID available to the runner**

In `agent.go`, add an `agentID` field on `spawnAgentRunner` is not needed for the foreground path (the foreground agent has no registry ID). Instead, generate a transient ID for foreground spawns so the callback can be stamped. Modify `spawnAgentRunner.Run` foreground branch:

Change:

```go
	if !args.Background {
		// Foreground: execute synchronously
		if r.streamCB != nil {
			subOpts = append(subOpts, WithStreamCallback(r.streamCB))
		}
		result, err := ExecuteTools(r.llm, subFragment, subOpts...)
```

to:

```go
	if !args.Background {
		// Foreground: execute synchronously.
		fgID := uuid.New().String()
		subOpts = append(subOpts, withAgentIDStamp(fgID))
		if r.streamCB != nil {
			subOpts = append(subOpts, WithStreamCallback(r.streamCB))
		}
		result, err := ExecuteTools(r.llm, subFragment, subOpts...)
```

- [ ] **Step 4: Add the AgentID-stamping option and propagate parent callback + MCPs**

In `agent.go`, add a helper that wraps any already-set tool callback so it stamps the agent ID:

```go
// withAgentIDStamp wraps the option set so that, when ExecuteTools invokes the
// tool-call callback, SessionState.AgentID carries the given sub-agent id. It
// composes with the propagated parent callback rather than replacing it.
func withAgentIDStamp(id string) Option {
	return func(o *Options) {
		inner := o.toolCallCallback
		if inner == nil {
			return
		}
		o.toolCallCallback = func(tc *ToolChoice, st *SessionState) ToolCallDecision {
			if st != nil {
				st.AgentID = id
			}
			return inner(tc, st)
		}
	}
}
```

In `tools.go`, in the agent-spawning setup block where `subAgentOpts` is assembled (after the `maxRetries` append), propagate the parent callback and MCP sessions:

```go
		if o.toolCallCallback != nil {
			subAgentOpts = append(subAgentOpts, WithToolCallBack(o.toolCallCallback))
		}
		if len(o.mcpSessions) > 0 {
			subAgentOpts = append(subAgentOpts, WithMCPs(o.mcpSessions...))
		}
```

> Order matters: `withAgentIDStamp` (added in the runner via `subOpts`) runs *after* `subAgentOpts` are applied, so `o.toolCallCallback` is already the parent callback when the stamp wraps it.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -run TestSubAgentToolCallReachesParentCallback ./...`
Expected: PASS.

- [ ] **Step 6: Add a rejection-honored test**

Append to `agent_propagation_test.go`:

```go
func TestSubAgentToolRejectionIsHonored(t *testing.T) {
	rejectCB := func(tc *ToolChoice, st *SessionState) ToolCallDecision {
		return ToolCallDecision{Approved: false}
	}
	echo := newEchoTool()
	llm := newScriptedLLM(scriptCallTool("echo", map[string]any{"text": "hi"}), scriptReply("done"))
	runner := &spawnAgentRunner{
		llm:         llm,
		parentTools: Tools{echo},
		parentOpts:  []Option{WithToolCallBack(rejectCB)},
		manager:     NewAgentManager(),
		ctx:         context.Background(),
	}
	out, _, err := runner.Run(SpawnAgentArgs{Task: "say hi", Background: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = out // assertion is that no panic and the echo tool did not run; tighten if echoRunner records calls
}
```

> Strengthen this test by making `echoRunner` record invocation count (a package-level `int` guarded by a mutex, reset per test) and asserting it stays 0 when rejected. Use that same counter to assert it is 1 in the approve test.

- [ ] **Step 7: Run full agent tests**

Run: `go test -run 'TestSubAgent|TestSessionState' ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add agent.go tools.go agent_propagation_test.go
git commit -m "feat(agent): route sub-agent tool calls through parent callback + MCPs with AgentID"
```

---

## Task A3: Agent definitions — type, option, struct fields (C2, part 1)

**Files:**
- Modify: `agent.go` (`AgentDefinition` type, `SpawnAgentArgs.AgentType`)
- Modify: `options.go` (`agentDefinitions` field, `WithAgentDefinitions`)
- Test: `agent_definitions_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `agent_definitions_test.go`:

```go
package cogito

import "testing"

func TestWithAgentDefinitionsStoresDefs(t *testing.T) {
	defs := []AgentDefinition{
		{Name: "explore", Description: "read-only exploration",
			SystemPrompt: "You explore.", Tools: []string{"echo"},
			Model: "small-model", Temperature: 0.2,
			Iterations: 20, MaxAttempts: 2, MaxRetries: 2},
	}
	o := defaultOptions()
	o.Apply(WithAgentDefinitions(defs...))
	if len(o.agentDefinitions) != 1 || o.agentDefinitions[0].Name != "explore" {
		t.Fatalf("agent definitions not stored: %+v", o.agentDefinitions)
	}
}

func TestFindAgentDefinition(t *testing.T) {
	defs := []AgentDefinition{{Name: "plan"}, {Name: "explore"}}
	if d := findAgentDefinition(defs, "explore"); d == nil || d.Name != "explore" {
		t.Fatalf("expected to find explore, got %+v", d)
	}
	if d := findAgentDefinition(defs, "missing"); d != nil {
		t.Fatalf("expected nil for missing type, got %+v", d)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run 'TestWithAgentDefinitions|TestFindAgentDefinition' ./...`
Expected: FAIL — `AgentDefinition`, `WithAgentDefinitions`, `findAgentDefinition`, and `o.agentDefinitions` are undefined.

- [ ] **Step 3: Add the AgentDefinition type and lookup**

In `agent.go`, add near the top (after `SpawnAgentArgs`):

```go
// AgentDefinition is a named sub-agent "type" (persona). The embedder registers
// definitions via WithAgentDefinitions; spawn_agent selects one by Name.
type AgentDefinition struct {
	Name         string   // unique identifier referenced by spawn_agent.agent_type
	Description  string   // shown to the LLM in the spawn tool description
	SystemPrompt string   // seeded as the sub-agent's first system message
	Tools        []string // tool-name allow-list for this type (empty = all parent tools)
	Model        string   // optional model override resolved via the agent LLM factory
	Temperature  float32  // optional sampling temperature for this type
	Iterations   int      // optional per-type iteration cap (0 = inherit parent)
	MaxAttempts  int      // optional per-type attempt cap (0 = inherit parent)
	MaxRetries   int      // optional per-type retry cap (0 = inherit parent)
}

// findAgentDefinition returns the definition with the given name, or nil.
func findAgentDefinition(defs []AgentDefinition, name string) *AgentDefinition {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}
```

Add the `AgentType` field to `SpawnAgentArgs`:

```go
type SpawnAgentArgs struct {
	AgentType   string   `json:"agent_type" description:"Optional named agent type to use (persona/system prompt/tools/model). If empty, a generic sub-agent is used."`
	Task        string   `json:"task" description:"The task or prompt for the sub-agent to execute"`
	Background  bool     `json:"background" description:"If true, the agent runs in the background and returns an ID immediately. If false, blocks until the agent completes."`
	Tools       []string `json:"tools" description:"Optional subset of tool names available to the sub-agent. If empty, the agent type's tools (or all parent tools) are used."`
	Model       string   `json:"model" description:"Optional model override for this sub-agent."`
}
```

- [ ] **Step 4: Add the option + struct field**

In `options.go`, add to the `Options` struct (in the `Sub-agent spawning options` block):

```go
	agentDefinitions        []AgentDefinition
	agentLLMFactory         func(model string, temperature float32) LLM
```

Add the option (near `WithAgentLLM`):

```go
// WithAgentDefinitions registers named sub-agent types (personas). spawn_agent
// can select one via its agent_type argument; the chosen definition supplies the
// system prompt, tool allow-list, model, temperature, and per-type execution limits.
func WithAgentDefinitions(defs ...AgentDefinition) Option {
	return func(o *Options) {
		o.agentDefinitions = defs
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -run 'TestWithAgentDefinitions|TestFindAgentDefinition' ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add agent.go options.go agent_definitions_test.go
git commit -m "feat(agent): add AgentDefinition type and WithAgentDefinitions option"
```

---

## Task A4: Apply agent definition on spawn (C2, part 2)

**Files:**
- Modify: `agent.go` (`spawnAgentRunner` struct + `Run`: resolve definition → system prompt, tools, limits)
- Modify: `tools.go` (pass `agentDefinitions` into `newSpawnAgentTool`; enumerate types in tool description)
- Test: `agent_definitions_test.go`

- [ ] **Step 1: Write the failing test**

Append to `agent_definitions_test.go`:

```go
import "context"

func TestSpawnAppliesDefinitionSystemPromptAndTools(t *testing.T) {
	echo := newEchoTool()
	secret := newNamedTool("secret") // helper: a tool the explore type must NOT receive
	defs := []AgentDefinition{{
		Name: "explore", SystemPrompt: "You are EXPLORE.",
		Tools: []string{"echo"}, Iterations: 7,
	}}

	var gotSystem string
	var gotToolNames []string
	llm := newInspectingLLM(func(f Fragment, tools Tools) {
		gotSystem = firstSystemContent(f)
		for _, tl := range tools {
			gotToolNames = append(gotToolNames, tl.Tool().Function.Name)
		}
	})

	runner := &spawnAgentRunner{
		llm:              llm,
		parentTools:      Tools{echo, secret},
		manager:          NewAgentManager(),
		ctx:              context.Background(),
		agentDefinitions: defs,
	}
	_, _, _ = runner.Run(SpawnAgentArgs{AgentType: "explore", Task: "look around", Background: false})

	if gotSystem != "You are EXPLORE." {
		t.Fatalf("definition system prompt not seeded, got %q", gotSystem)
	}
	if contains(gotToolNames, "secret") {
		t.Fatalf("explore must not receive 'secret' tool, got %v", gotToolNames)
	}
}

func TestSpawnUnknownAgentTypeErrorsCleanly(t *testing.T) {
	runner := &spawnAgentRunner{
		llm: newInspectingLLM(func(Fragment, Tools) {}),
		manager: NewAgentManager(), ctx: context.Background(),
		agentDefinitions: []AgentDefinition{{Name: "explore"}},
	}
	out, _, err := runner.Run(SpawnAgentArgs{AgentType: "nope", Task: "x", Background: false})
	if err != nil {
		t.Fatalf("unknown type should not hard-error, got %v", err)
	}
	if !strings.Contains(out, "unknown agent type") {
		t.Fatalf("expected a clear message, got %q", out)
	}
}
```

> **Helpers:** `newNamedTool(name)` (like `newEchoTool` but parameterized), `newInspectingLLM(fn)` (a `LLM` whose `Ask` records the fragment+tools then returns a sink reply — model it on the repo's existing mock LLM; the inspector fires inside the tool-selection path), `firstSystemContent(Fragment)`, `contains([]string,string)`. Put them in `agent_definitions_test.go`. Reuse existing mocks where grep finds them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run 'TestSpawnApplies|TestSpawnUnknown' ./...`
Expected: FAIL — `spawnAgentRunner` has no `agentDefinitions` field and ignores `AgentType`.

- [ ] **Step 3: Add the field and resolution logic**

In `agent.go`, add to `spawnAgentRunner`:

```go
	agentDefinitions []AgentDefinition
	llmFactory       func(model string, temperature float32) LLM
```

At the start of `spawnAgentRunner.Run`, before building `subTools`, resolve the definition:

```go
func (r *spawnAgentRunner) Run(args SpawnAgentArgs) (string, any, error) {
	var def *AgentDefinition
	if args.AgentType != "" {
		def = findAgentDefinition(r.agentDefinitions, args.AgentType)
		if def == nil {
			return fmt.Sprintf("Cannot spawn: unknown agent type %q", args.AgentType), nil, nil
		}
	}

	// Resolve the tool allow-list: explicit spawn arg > definition tools > all parent tools.
	requestedTools := args.Tools
	if len(requestedTools) == 0 && def != nil {
		requestedTools = def.Tools
	}
	subTools := FilterToolsForSubAgent(r.parentTools, requestedTools)

	subOpts := append([]Option{},
		WithTools(subTools...),
		WithContext(r.ctx),
	)
	subOpts = append(subOpts, r.parentOpts...)

	// Per-type execution limits override the propagated parent limits.
	if def != nil {
		if def.Iterations > 0 {
			subOpts = append(subOpts, WithIterations(def.Iterations))
		}
		if def.MaxAttempts > 0 {
			subOpts = append(subOpts, WithMaxAttempts(def.MaxAttempts))
		}
		if def.MaxRetries > 0 {
			subOpts = append(subOpts, WithMaxRetries(def.MaxRetries))
		}
	}

	// Seed the system prompt from the definition.
	var subFragment Fragment
	if def != nil && def.SystemPrompt != "" {
		subFragment = NewFragment(
			openai.ChatCompletionMessage{Role: "system", Content: def.SystemPrompt},
			openai.ChatCompletionMessage{Role: "user", Content: args.Task},
		)
	} else {
		subFragment = NewFragment(
			openai.ChatCompletionMessage{Role: "user", Content: args.Task},
		)
	}

	// Resolve the LLM (model/temperature) for this sub-agent.
	subLLM := r.resolveLLM(args, def)
	// ... continue with the existing foreground/background branches, using subLLM
	//     instead of r.llm everywhere below.
```

> **Implementer:** replace the two `ExecuteTools(r.llm, ...)` calls (foreground and the background goroutine) with `ExecuteTools(subLLM, ...)`. Keep the rest of the foreground/background logic from the current `Run`, including the `withAgentIDStamp(fgID)` line added in Task A2.

- [ ] **Step 4: Add resolveLLM (temporary parent-only version)**

In `agent.go`, add:

```go
// resolveLLM picks the LLM for a sub-agent. Order: spawn-arg model > definition
// model/temperature via the factory > parent LLM. Fully wired in Task A6.
func (r *spawnAgentRunner) resolveLLM(args SpawnAgentArgs, def *AgentDefinition) LLM {
	model := args.Model
	var temp float32
	if def != nil {
		if model == "" {
			model = def.Model
		}
		temp = def.Temperature
	}
	if model != "" && r.llmFactory != nil {
		return r.llmFactory(model, temp)
	}
	return r.llm
}
```

- [ ] **Step 5: Pass definitions through tool construction**

In `tools.go`, where `newSpawnAgentTool(...)` is called, pass the new fields. Update the call to include `o.agentDefinitions` and `o.agentLLMFactory` (the signature change lands in Step 6). In `agent.go`, update `newSpawnAgentTool` signature and body:

```go
func newSpawnAgentTool(
	llm LLM,
	parentTools Tools,
	manager *AgentManager,
	ctx context.Context,
	parentOpts []Option,
	streamCB StreamCallback,
	injectionChan chan openai.ChatCompletionMessage,
	completionCB func(*AgentState),
	completionFormatter func(*AgentState) string,
	defs []AgentDefinition,
	llmFactory func(model string, temperature float32) LLM,
) ToolDefinitionInterface {
	return NewToolDefinition(
		&spawnAgentRunner{
			llm:                     llm,
			parentTools:             parentTools,
			parentOpts:              parentOpts,
			manager:                 manager,
			ctx:                     ctx,
			streamCB:                streamCB,
			messageInjectionChan:    injectionChan,
			agentCompletionCallback: completionCB,
			completionFormatter:     completionFormatter,
			agentDefinitions:        defs,
			llmFactory:              llmFactory,
		},
		SpawnAgentArgs{},
		"spawn_agent",
		spawnToolDescription(defs),
	)
}

// spawnToolDescription enumerates available agent types so the LLM can choose one.
func spawnToolDescription(defs []AgentDefinition) string {
	base := "Spawn a sub-agent to handle a task. Use background=true for non-blocking execution, or background=false to wait for the result."
	if len(defs) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(" Available agent_type values: ")
	for i, d := range defs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Name)
		if d.Description != "" {
			b.WriteString(" (" + d.Description + ")")
		}
	}
	return b.String()
}
```

> Add `"strings"` to `agent.go` imports. Update the `newSpawnAgentTool(...)` call site in `tools.go` to pass `o.agentCompletionFormatter, o.agentDefinitions, o.agentLLMFactory` (the formatter arg already exists on the v0.10.0 baseline).

- [ ] **Step 6: Run tests**

Run: `go test -run 'TestSpawnApplies|TestSpawnUnknown' ./...`
Expected: PASS.

- [ ] **Step 7: Run the full suite**

Run: `go test ./... 2>&1 | tail -20`
Expected: PASS (no regressions in existing agent tests).

- [ ] **Step 8: Commit**

```bash
git add agent.go tools.go agent_definitions_test.go
git commit -m "feat(agent): apply agent definition (prompt/tools/limits) on spawn"
```

---

## Task A5: Temperature support in the OpenAI client (C3, part 1)

**Files:**
- Modify: `clients/openai_client.go`
- Test: `clients/openai_client_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `clients/openai_client_test.go`:

```go
package clients

import "testing"

func TestNewOpenAILLMWithOptionsSetsTemperature(t *testing.T) {
	llm := NewOpenAILLMWithOptions("m", "k", "http://localhost", OpenAIOptions{Temperature: 0.7})
	if llm.temperature != 0.7 {
		t.Fatalf("expected temperature 0.7, got %v", llm.temperature)
	}
}

func TestNewOpenAILLMDefaultsTemperatureZeroMeansUnset(t *testing.T) {
	llm := NewOpenAILLM("m", "k", "http://localhost")
	if llm.temperature != 0 {
		t.Fatalf("expected default temperature 0 (unset), got %v", llm.temperature)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./clients/ -run TestNewOpenAILLM`
Expected: FAIL — `NewOpenAILLMWithOptions`, `OpenAIOptions`, and the `temperature` field are undefined.

- [ ] **Step 3: Add temperature to the client**

In `clients/openai_client.go`, change the struct and constructors:

```go
type OpenAIClient struct {
	model       string
	client      *openai.Client
	temperature float32
}

// OpenAIOptions carries optional per-client settings.
type OpenAIOptions struct {
	Temperature float32
}

func NewOpenAILLM(model, apiKey, baseURL string) *OpenAIClient {
	return NewOpenAILLMWithOptions(model, apiKey, baseURL, OpenAIOptions{})
}

func NewOpenAILLMWithOptions(model, apiKey, baseURL string, opts OpenAIOptions) *OpenAIClient {
	client := openaiClient(apiKey, baseURL)
	return &OpenAIClient{
		model:       model,
		client:      client,
		temperature: opts.Temperature,
	}
}
```

In the `Ask` method (and any streaming method that builds a `ChatCompletionRequest`), set the temperature when non-zero:

```go
	req := openai.ChatCompletionRequest{
		Model:    llm.model,
		Messages: messages,
	}
	if llm.temperature != 0 {
		req.Temperature = llm.temperature
	}
	resp, err := llm.client.CreateChatCompletion(ctx, req)
```

> Apply the same `if llm.temperature != 0 { req.Temperature = ... }` to the streaming request builder if `OpenAIClient` implements `StreamingLLM` (it does — `CreateChatCompletionStream`). Grep: `grep -n "ChatCompletionRequest{" clients/openai_client.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./clients/ -run TestNewOpenAILLM`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add clients/openai_client.go clients/openai_client_test.go
git commit -m "feat(clients): add temperature support to OpenAI client"
```

---

## Task A6: Wire the agent LLM factory (C3, part 2)

**Files:**
- Modify: `options.go` (`WithAgentLLMFactory`)
- Modify: `tools.go` (already passes `o.agentLLMFactory` from Task A4 Step 5 — verify)
- Test: `agent_definitions_test.go`

- [ ] **Step 1: Write the failing test**

Append to `agent_definitions_test.go`:

```go
func TestFactoryResolvesModelAndTemperature(t *testing.T) {
	var gotModel string
	var gotTemp float32
	factory := func(model string, temp float32) LLM {
		gotModel, gotTemp = model, temp
		return newInspectingLLM(func(Fragment, Tools) {})
	}
	defs := []AgentDefinition{{Name: "cheap", Model: "small", Temperature: 0.3}}
	runner := &spawnAgentRunner{
		llm:              newInspectingLLM(func(Fragment, Tools) {}),
		manager:          NewAgentManager(),
		ctx:              context.Background(),
		agentDefinitions: defs,
		llmFactory:       factory,
	}
	_, _, _ = runner.Run(SpawnAgentArgs{AgentType: "cheap", Task: "x", Background: false})
	if gotModel != "small" || gotTemp != 0.3 {
		t.Fatalf("factory got (%q,%v), want (small,0.3)", gotModel, gotTemp)
	}
}

func TestSpawnArgModelBeatsDefinition(t *testing.T) {
	var gotModel string
	factory := func(model string, temp float32) LLM {
		gotModel = model
		return newInspectingLLM(func(Fragment, Tools) {})
	}
	defs := []AgentDefinition{{Name: "cheap", Model: "small"}}
	runner := &spawnAgentRunner{
		llm: newInspectingLLM(func(Fragment, Tools) {}), manager: NewAgentManager(),
		ctx: context.Background(), agentDefinitions: defs, llmFactory: factory,
	}
	_, _, _ = runner.Run(SpawnAgentArgs{AgentType: "cheap", Model: "big", Task: "x", Background: false})
	if gotModel != "big" {
		t.Fatalf("spawn-arg model should win, got %q", gotModel)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run 'TestFactoryResolves|TestSpawnArgModel' ./...`
Expected: FAIL only if `WithAgentLLMFactory` is referenced — these tests set `llmFactory` directly so they should pass already from Task A4's `resolveLLM`. If they pass, that confirms resolution; proceed to add the public option in Step 3 (its own test below).

- [ ] **Step 3: Add the public option**

In `options.go`, add:

```go
// WithAgentLLMFactory sets a factory that builds an LLM for a sub-agent from a
// model name and temperature. Used to resolve per-agent-type or per-spawn model
// overrides while reusing the parent's endpoint/credentials.
func WithAgentLLMFactory(fn func(model string, temperature float32) LLM) Option {
	return func(o *Options) {
		o.agentLLMFactory = fn
	}
}
```

Add a test that the option stores the factory:

```go
func TestWithAgentLLMFactoryStores(t *testing.T) {
	o := defaultOptions()
	o.Apply(WithAgentLLMFactory(func(string, float32) LLM { return nil }))
	if o.agentLLMFactory == nil {
		t.Fatal("factory not stored")
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -run 'TestFactoryResolves|TestSpawnArgModel|TestWithAgentLLMFactory' ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add options.go agent_definitions_test.go
git commit -m "feat(agent): per-agent model+temperature via WithAgentLLMFactory"
```

---

## Task A7: Per-agent injection channel + AgentManager.Inject (C4, part 1)

**Files:**
- Modify: `agent.go` (`AgentState.inject` channel, `AgentManager.Inject`)
- Modify: `agent.go` (background goroutine wires a per-agent injection channel into sub-agent opts)
- Test: `agent_resume_test.go` (create)

**Context:** Background sub-agents already run `ExecuteTools`, which parks on `messageInjectionChan` when idle (we saw this in `tools.go`). Giving each background agent its own injection channel + an `Inject` method lets the embedder push a follow-up message into a *running* agent.

- [ ] **Step 1: Write the failing test**

Create `agent_resume_test.go`:

```go
package cogito

import (
	"context"
	"testing"
	"time"
)

func TestInjectDeliversToRunningAgent(t *testing.T) {
	m := NewAgentManager()
	delivered := make(chan string, 1)
	agent := &AgentState{
		ID: "a1", Status: AgentStatusRunning,
		done:   make(chan struct{}),
		inject: make(chan openai.ChatCompletionMessage, 1),
	}
	m.Register(agent)

	go func() {
		msg := <-agent.inject
		delivered <- msg.Content
	}()

	if err := m.Inject("a1", "keep going"); err != nil {
		t.Fatalf("inject errored: %v", err)
	}
	select {
	case got := <-delivered:
		if got != "keep going" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("inject not delivered")
	}
	_ = context.Background()
}

func TestInjectUnknownAgentErrors(t *testing.T) {
	m := NewAgentManager()
	if err := m.Inject("missing", "x"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestInject ./...`
Expected: FAIL — `AgentState.inject` and `AgentManager.Inject` are undefined.

- [ ] **Step 3: Add the channel and method**

In `agent.go`, add `inject` to `AgentState`:

```go
type AgentState struct {
	ID       string
	Task     string
	Status   AgentStatusType
	Result   string
	Fragment *Fragment
	Error    error
	Cancel   context.CancelFunc
	done     chan struct{}
	inject   chan openai.ChatCompletionMessage
}
```

Add the method:

```go
// Inject pushes a user-role follow-up message into a running agent's loop.
// Returns an error if the agent is unknown or has no injection channel.
func (m *AgentManager) Inject(id, message string) error {
	m.mu.RLock()
	a, ok := m.agents[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}
	if a.inject == nil {
		return fmt.Errorf("agent %s does not accept injections", id)
	}
	a.inject <- openai.ChatCompletionMessage{Role: "user", Content: message}
	return nil
}
```

In the background branch of `spawnAgentRunner.Run`, create the per-agent channel and pass it to the sub-agent via `WithMessageInjectionChan`:

```go
	agent := &AgentState{
		ID:     agentID,
		Task:   args.Task,
		Status: AgentStatusRunning,
		done:   make(chan struct{}),
		inject: make(chan openai.ChatCompletionMessage, 8),
	}
	r.manager.Register(agent)
	// ...
	subOpts = append(subOpts, WithMessageInjectionChan(agent.inject))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestInject ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent.go agent_resume_test.go
git commit -m "feat(agent): per-agent injection channel and AgentManager.Inject"
```

---

## Task A8: send_agent_message tool — inject or resume (C4, part 2)

**Files:**
- Modify: `agent.go` (`SendAgentMessageArgs`, `sendAgentMessageRunner`, `newSendAgentMessageTool`, `agentToolNames`)
- Modify: `tools.go` (register `send_agent_message` in the agent-tool slice)
- Test: `agent_resume_test.go`

- [ ] **Step 1: Write the failing test**

Append to `agent_resume_test.go`:

```go
func TestSendAgentMessageResumesCompletedAgent(t *testing.T) {
	m := NewAgentManager()
	frag := NewFragment(openai.ChatCompletionMessage{Role: "user", Content: "first task"})
	agent := &AgentState{
		ID: "done1", Status: AgentStatusCompleted,
		Result: "first result", Fragment: &frag,
		done: closedChan(),
	}
	m.Register(agent)

	llm := newScriptedLLM(scriptReply("second result"))
	runner := &sendAgentMessageRunner{manager: m, ctx: context.Background(), llm: llm}
	out, _, err := runner.Run(SendAgentMessageArgs{AgentID: "done1", Message: "now do more"})
	if err != nil {
		t.Fatalf("resume errored: %v", err)
	}
	if !strings.Contains(out, "second result") {
		t.Fatalf("expected re-run result, got %q", out)
	}
}

func TestSendAgentMessageInjectsRunningAgent(t *testing.T) {
	m := NewAgentManager()
	agent := &AgentState{ID: "run1", Status: AgentStatusRunning,
		done: make(chan struct{}), inject: make(chan openai.ChatCompletionMessage, 1)}
	m.Register(agent)
	runner := &sendAgentMessageRunner{manager: m, ctx: context.Background()}
	out, _, err := runner.Run(SendAgentMessageArgs{AgentID: "run1", Message: "hint"})
	if err != nil {
		t.Fatalf("inject errored: %v", err)
	}
	if got := <-agent.inject; got.Content != "hint" {
		t.Fatalf("injected %q", got.Content)
	}
	if !strings.Contains(out, "run1") {
		t.Fatalf("expected ack mentioning agent id, got %q", out)
	}
}

func closedChan() chan struct{} { c := make(chan struct{}); close(c); return c }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestSendAgentMessage ./...`
Expected: FAIL — `SendAgentMessageArgs` and `sendAgentMessageRunner` undefined.

- [ ] **Step 3: Implement the unified tool**

In `agent.go`, add `"send_agent_message"` to `agentToolNames`:

```go
var agentToolNames = []string{"spawn_agent", "check_agent", "get_agent_result", "send_agent_message"}
```

Add the args, runner, and constructor:

```go
// SendAgentMessageArgs is the argument for the unified resume/inject tool.
type SendAgentMessageArgs struct {
	AgentID string `json:"agent_id" description:"The ID of the agent to message"`
	Message string `json:"message" description:"The follow-up message. Injected live if the agent is running, or re-runs the agent with prior context if it has finished."`
}

type sendAgentMessageRunner struct {
	manager *AgentManager
	ctx     context.Context
	llm     LLM
	subOpts []Option
}

func (r *sendAgentMessageRunner) Run(args SendAgentMessageArgs) (string, any, error) {
	agent, ok := r.manager.Get(args.AgentID)
	if !ok {
		return fmt.Sprintf("Agent %s not found", args.AgentID), nil, nil
	}

	if agent.Status == AgentStatusRunning {
		if err := r.manager.Inject(args.AgentID, args.Message); err != nil {
			return fmt.Sprintf("Could not message agent %s: %v", args.AgentID, err), nil, nil
		}
		return fmt.Sprintf("Message delivered to running agent %s.", args.AgentID), nil, nil
	}

	// Completed/failed: resume by appending the message to the stored fragment and re-running.
	if agent.Fragment == nil {
		return fmt.Sprintf("Agent %s has no stored context to resume", args.AgentID), nil, nil
	}
	resumed := agent.Fragment.AddMessage("user", args.Message)
	opts := append([]Option{WithContext(r.ctx)}, r.subOpts...)
	result, err := ExecuteTools(r.llm, resumed, opts...)
	if err != nil {
		return fmt.Sprintf("Resume of agent %s failed: %v", args.AgentID, err), nil, nil
	}
	r.manager.mu.Lock()
	agent.Status = AgentStatusCompleted
	agent.Result = result.LastMessage().Content
	agent.Fragment = &result
	r.manager.mu.Unlock()
	return agent.Result, result, nil
}

func newSendAgentMessageTool(manager *AgentManager, ctx context.Context, llm LLM, subOpts []Option) ToolDefinitionInterface {
	return NewToolDefinition(
		&sendAgentMessageRunner{manager: manager, ctx: ctx, llm: llm, subOpts: subOpts},
		SendAgentMessageArgs{},
		"send_agent_message",
		"Send a follow-up message to a sub-agent. If it is still running the message is injected live; if it has finished, the agent resumes from its prior context.",
	)
}
```

- [ ] **Step 4: Register the tool**

In `tools.go`, in the `agentTools := []ToolDefinitionInterface{...}` slice, add:

```go
			newSendAgentMessageTool(o.agentManager, o.context, agentLLM, subAgentOpts),
```

- [ ] **Step 5: Run tests**

Run: `go test -run TestSendAgentMessage ./...`
Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add agent.go tools.go agent_resume_test.go
git commit -m "feat(agent): add unified send_agent_message resume/inject tool"
```

---

## Task A9: Detachable foreground spawn + AgentManager.Detach (C5)

**Files:**
- Modify: `agent.go` (foreground branch becomes registered + select-able; `AgentManager.Detach`)
- Test: `agent_detach_test.go` (create)

**Context:** Today a foreground spawn blocks in `ExecuteTools` with no registry entry, so an embedder cannot background it. We register every foreground agent, run it in a goroutine, and block on `done` OR a per-agent `detach` signal. On detach we return the ID immediately; the goroutine keeps running and the agent becomes an ordinary background agent (it already has an injection channel and completion wiring).

- [ ] **Step 1: Write the failing test**

Create `agent_detach_test.go`:

```go
package cogito

import (
	"context"
	"testing"
	"time"
)

func TestDetachReturnsBeforeCompletion(t *testing.T) {
	m := NewAgentManager()
	release := make(chan struct{})
	// An LLM that blocks until released, simulating a long-running foreground agent.
	llm := newBlockingLLM(release)

	runner := &spawnAgentRunner{
		llm: llm, manager: m, ctx: context.Background(),
	}

	type res struct {
		out string
		id  any
	}
	resCh := make(chan res, 1)
	go func() {
		out, id, _ := runner.Run(SpawnAgentArgs{Task: "long job", Background: false})
		resCh <- res{out, id}
	}()

	// Wait for the foreground agent to register, then detach it.
	var id string
	deadline := time.After(2 * time.Second)
	for {
		agents := m.List()
		if len(agents) == 1 {
			id = agents[0].ID
			break
		}
		select {
		case <-deadline:
			t.Fatal("foreground agent never registered")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if err := m.Detach(id); err != nil {
		t.Fatalf("detach errored: %v", err)
	}

	select {
	case r := <-resCh:
		if r.id == nil {
			t.Fatal("expected detach to return the agent id")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after detach")
	}

	// The goroutine is still running; release it so the test can clean up.
	close(release)
}
```

> **Helper:** `newBlockingLLM(release chan struct{})` returns an `LLM` whose `Ask` blocks on `<-release` before returning a sink reply. Put it in `agent_detach_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDetach ./...`
Expected: FAIL — `AgentManager.Detach` undefined and foreground spawns are not registered.

- [ ] **Step 3: Add the detach channel and method**

In `agent.go`, add `detach` to `AgentState`:

```go
	detach chan struct{}
```

Add the method:

```go
// Detach promotes a running foreground agent to background. The blocked
// spawn_agent call returns immediately with the agent ID; the agent's goroutine
// keeps running. Returns an error if the agent is unknown or not detachable.
func (m *AgentManager) Detach(id string) error {
	m.mu.RLock()
	a, ok := m.agents[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}
	if a.detach == nil {
		return fmt.Errorf("agent %s is not detachable", id)
	}
	select {
	case a.detach <- struct{}{}:
	default:
	}
	return nil
}
```

- [ ] **Step 4: Rewrite the foreground branch to register + select**

In `spawnAgentRunner.Run`, replace the foreground branch with a registered, goroutine-backed, detachable version. The key shape:

```go
	if !args.Background {
		agentID := uuid.New().String()
		subCtx, cancel := context.WithCancel(r.ctx)
		agent := &AgentState{
			ID:     agentID,
			Task:   args.Task,
			Status: AgentStatusRunning,
			Cancel: cancel,
			done:   make(chan struct{}),
			inject: make(chan openai.ChatCompletionMessage, 8),
			detach: make(chan struct{}, 1),
		}
		r.manager.Register(agent)

		fgOpts := append([]Option{}, subOpts...)
		fgOpts = append(fgOpts, withAgentIDStamp(agentID))
		fgOpts = append(fgOpts, WithContext(subCtx))
		fgOpts = append(fgOpts, WithMessageInjectionChan(agent.inject))
		if r.streamCB != nil {
			fgOpts = append(fgOpts, WithStreamCallback(r.streamCB))
		}

		go r.runAgent(agent, subLLM, subFragment, fgOpts, args.Task, cancel)

		select {
		case <-agent.done:
			// Completed before any detach: behave like the old synchronous path.
			r.manager.mu.RLock()
			defer r.manager.mu.RUnlock()
			if agent.Status == AgentStatusFailed {
				return fmt.Sprintf("Sub-agent failed: %v", agent.Error), agent.Fragment, nil
			}
			return agent.Result, derefFragment(agent.Fragment), nil
		case <-agent.detach:
			// Promoted to background: return the ID, leave the goroutine running.
			return fmt.Sprintf("Agent detached to background with ID: %s", agentID), agentID, nil
		case <-r.ctx.Done():
			return "Sub-agent cancelled", nil, r.ctx.Err()
		}
	}
```

Extract the goroutine body shared by foreground and background into a method (DRY):

```go
// runAgent executes a sub-agent to completion and records its terminal state,
// firing the completion callback and injecting a completion notification.
func (r *spawnAgentRunner) runAgent(agent *AgentState, llm LLM, frag Fragment, opts []Option, task string, cancel context.CancelFunc) {
	defer close(agent.done)
	defer cancel()

	result, err := ExecuteTools(llm, frag, opts...)

	r.manager.mu.Lock()
	if err != nil {
		agent.Status = AgentStatusFailed
		agent.Error = err
		agent.Result = fmt.Sprintf("Failed: %v", err)
	} else {
		agent.Status = AgentStatusCompleted
		agent.Result = result.LastMessage().Content
		agent.Fragment = &result
	}
	r.manager.mu.Unlock()

	if r.agentCompletionCallback != nil {
		r.agentCompletionCallback(agent)
	}
	if r.messageInjectionChan != nil {
		content := formatAgentCompletion(agent, r.completionFormatter)
		select {
		case r.messageInjectionChan <- openai.ChatCompletionMessage{Role: "user", Content: content}:
		default:
		}
	}
}

func derefFragment(f *Fragment) any {
	if f == nil {
		return nil
	}
	return *f
}
```

Refactor the existing **background** branch to also call `r.runAgent(...)` instead of duplicating the goroutine body. Background still creates `agent` (now also setting `detach: make(chan struct{}, 1)` is optional — background agents are already detached; leave `detach` nil so `Detach` reports "not detachable" for already-background agents, which is correct).

> **Important (regression guard):** when no detach ever fires, the foreground `select` returns on `agent.done` and yields `agent.Result` — byte-for-byte the same content the old synchronous path returned via `result.LastMessage().Content`. The risk note in the spec (foreground behavior unchanged when Ctrl+B is never pressed) is covered by Step 6.

- [ ] **Step 5: Run the detach test**

Run: `go test -run TestDetach ./...`
Expected: PASS.

- [ ] **Step 6: Add a regression test — foreground unchanged without detach**

Append to `agent_detach_test.go`:

```go
func TestForegroundSpawnUnchangedWithoutDetach(t *testing.T) {
	m := NewAgentManager()
	llm := newScriptedLLM(scriptReply("final answer"))
	runner := &spawnAgentRunner{llm: llm, manager: m, ctx: context.Background()}
	out, _, err := runner.Run(SpawnAgentArgs{Task: "quick", Background: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("foreground result changed, got %q", out)
	}
}
```

- [ ] **Step 7: Run the full suite**

Run: `go test ./... 2>&1 | tail -30`
Expected: PASS. Pay attention to existing `agent_test.go` / `tools_e2e_test.go` — the foreground path change must not regress them.

- [ ] **Step 8: Commit**

```bash
git add agent.go agent_detach_test.go
git commit -m "feat(agent): detachable foreground spawns + AgentManager.Detach"
```

---

## Task A10: Push the branch

**Files:** none (git only)

- [ ] **Step 1: Run vet + full suite once more**

Run: `go vet ./... && go test ./... 2>&1 | tail -20`
Expected: clean vet, PASS.

- [ ] **Step 2: Push and open a PR**

```bash
git push -u origin feat/subagent-enhancements
gh pr create --title "feat: sub-agent enhancements (definitions, model/temp, resume, detach, approval propagation)" \
  --body "Adds AgentDefinition registry, per-agent model+temperature, unified send_agent_message resume/inject, detachable foreground spawns, and parent tool-approval + MCP propagation to sub-agents (SessionState.AgentID)."
```

- [ ] **Step 3: Record the branch tip commit for Plan B**

Run: `git rev-parse --short HEAD`
Expected: a short SHA — note it; Plan B Task B1 pins wiz's `go.mod` to this commit.

---

## Self-Review (completed during planning)

- **Spec coverage:** C1 → A1+A2; C2 → A3+A4; C3 → A5+A6; C4 → A7+A8; C5 → A9. All five cogito components map to tasks.
- **Type consistency:** `AgentDefinition` fields (`Name/Description/SystemPrompt/Tools/Model/Temperature/Iterations/MaxAttempts/MaxRetries`) are identical across A3, A4, A6. Factory signature `func(model string, temperature float32) LLM` is identical in A4, A6 (`resolveLLM`, `WithAgentLLMFactory`) and consumed by `newSpawnAgentTool`. `withAgentIDStamp` defined in A2, reused in A9. `runAgent`/`derefFragment` defined in A9.
- **Known follow-up handled by tests-as-helpers:** the repo's existing mock-LLM helper names (`newScriptedLLM`, etc.) must be confirmed by grep at A2 Step 1; the plan instructs reuse and only adds `newEchoTool`/`newInspectingLLM`/`newBlockingLLM` if absent.
- **Out of scope (documented in spec):** async/detachable raw-command tool execution — not in this plan.
