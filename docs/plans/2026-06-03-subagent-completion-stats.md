# Sub-agent Completion Stats Line — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a spawned sub-agent finishes, render a one-line run summary — `… finished · 3 tools · 12.4k tokens · 1m 03s` — in both the nib TUI and the plain CLI.

**Architecture:** cogito gains a per-`ExecuteTools`-run token accumulator (a counting `LLM` decorator) whose total is stamped onto the returned fragment's `Status.CumulativeUsage`; since each sub-agent runs its own `ExecuteTools` and cogito assigns that fragment to `AgentState.Fragment`, the cumulative usage is available per sub-agent at the completion callback. nib reads tokens + `len(ToolsCalled)` from the fragment, times spawn→completion itself, carries the three numbers on `chat.AgentEvent`, and appends a formatted suffix in the TUI and CLI renderers.

**Tech Stack:** Go 1.24, `github.com/mudler/cogito` (sibling checkout at `/home/mudler/_git/cogito`), Ginkgo/Gomega (cogito tests), plain `testing` (nib tests).

**Two repos.** Tasks 1–2 are committed in `/home/mudler/_git/cogito`. Tasks 3–9 are committed in `/home/mudler/_git/wiz` on branch `feat/subagent-completion-stats`. A local `replace` directive (Task 3) lets nib build against the un-released cogito; Task 9 documents the release/un-replace handoff.

---

## File Structure

**cogito (`/home/mudler/_git/cogito`):**
- Create `usage_counter.go` — the `usageCounter` accumulator and the `countingLLM` / `countingStreamingLLM` decorators + `newCountingLLM` constructor.
- Create `usage_counter_internal_test.go` (`package cogito`) — unit tests for the decorator.
- Create `tools_cumulative_test.go` (`package cogito_test`) — `ExecuteTools` integration test asserting `CumulativeUsage`.
- Modify `fragment.go:34` — add `CumulativeUsage` field to `Status`.
- Modify `tools.go` — wrap `llm` in `ExecuteTools`, stamp result via `defer`.

**nib (`/home/mudler/_git/wiz`):**
- Modify `go.mod` — local `replace` for cogito.
- Modify `chat/callbacks.go:14` — three new `AgentEvent` fields.
- Create `chat/agentstats.go` — `humanTokens`, `humanDuration`, `AgentEvent.StatsSuffix`.
- Create `chat/agentstats_test.go` — formatter unit tests.
- Modify `chat/session.go` — per-agent start-time map; populate stats in `emitAgentEvent`; `agentUsage` helper.
- Create `chat/agentusage_test.go` — `agentUsage` unit test.
- Modify `tui/agents.go:24` — append stats suffix in `agentTranscriptLine` completed case.
- Modify `tui/model.go:634` — always emit the completion marker, plus the result block.
- Create `tui/agents_stats_test.go` — transcript-line test.
- Modify `cmd/cli.go` — append stats suffix in `formatAgentEventLine` completed case.
- Modify `cmd/cli_agents_test.go` — assert stats in the CLI line.

---

## Task 1: cogito — token-counting LLM decorator

**Files:**
- Create: `/home/mudler/_git/cogito/usage_counter.go`
- Create: `/home/mudler/_git/cogito/usage_counter_internal_test.go`
- Modify: `/home/mudler/_git/cogito/fragment.go:34`

- [ ] **Step 1: Add the `CumulativeUsage` field to `Status`**

In `/home/mudler/_git/cogito/fragment.go`, change the `Status` struct (currently starting at line 34):

```go
type Status struct {
	LastUsage        LLMUsage // Track token usage from the last LLM call
	CumulativeUsage  LLMUsage // Sum of token usage across every LLM call in the run
	Iterations       int
	ToolsCalled      Tools
	ToolResults      []ToolStatus
	Plans            []PlanStatus
	PastActions      []ToolStatus         // Track past actions for loop detections
	ReasoningLog     []string             // Track reasoning for each iteration
	TODOs            *structures.TODOList // TODO tracking for iterative execution
	TODOIteration    int                  // Current TODO iteration
	TODOPhase        string               // Current phase: "work" or "review"
	InjectedMessages []InjectedMessage    // Track successfully injected messages with timing
}
```

- [ ] **Step 2: Write the failing decorator unit test**

Create `/home/mudler/_git/cogito/usage_counter_internal_test.go`:

```go
package cogito

import (
	"context"
	"testing"

	"github.com/sashabaranov/go-openai"
)

// fakeLLM is a minimal LLM that returns a fixed usage per CreateChatCompletion
// call and records a fixed usage on the fragment it returns from Ask.
type fakeLLM struct {
	ccUsage  LLMUsage
	askUsage LLMUsage
}

func (f *fakeLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (LLMReply, LLMUsage, error) {
	return LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant"}}},
	}}, f.ccUsage, nil
}

func (f *fakeLLM) Ask(ctx context.Context, frag Fragment) (Fragment, error) {
	out := Fragment{Status: &Status{}}
	out.Status.LastUsage = f.askUsage
	return out, nil
}

func TestCountingLLMAccumulatesBothPaths(t *testing.T) {
	inner := &fakeLLM{
		ccUsage:  LLMUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		askUsage: LLMUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}
	counter := &usageCounter{}
	llm := newCountingLLM(inner, counter)

	if _, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
	if _, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
	if _, err := llm.Ask(context.Background(), NewEmptyFragment()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	got := counter.snapshot()
	if got.TotalTokens != 40 { // 15 + 15 + 10
		t.Errorf("TotalTokens = %d, want 40", got.TotalTokens)
	}
	if got.PromptTokens != 27 { // 10 + 10 + 7
		t.Errorf("PromptTokens = %d, want 27", got.PromptTokens)
	}
	if got.CompletionTokens != 13 { // 5 + 5 + 3
		t.Errorf("CompletionTokens = %d, want 13", got.CompletionTokens)
	}
}

// streamingFake additionally implements StreamingLLM.
type streamingFake struct{ fakeLLM }

func (s *streamingFake) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamEventDone, Usage: LLMUsage{TotalTokens: 99}}
	close(ch)
	return ch, nil
}

func TestNewCountingLLMPreservesStreaming(t *testing.T) {
	plain := newCountingLLM(&fakeLLM{}, &usageCounter{})
	if _, ok := plain.(StreamingLLM); ok {
		t.Error("wrapping a non-streaming LLM must not yield a StreamingLLM")
	}

	streaming := newCountingLLM(&streamingFake{}, &usageCounter{})
	if _, ok := streaming.(StreamingLLM); !ok {
		t.Error("wrapping a StreamingLLM must yield a StreamingLLM")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd /home/mudler/_git/cogito && go test ./ -run 'TestCountingLLM|TestNewCountingLLM' -v`
Expected: FAIL — `undefined: usageCounter`, `undefined: newCountingLLM`.

- [ ] **Step 4: Implement the decorator**

Create `/home/mudler/_git/cogito/usage_counter.go`:

```go
package cogito

import (
	"context"
	"sync/atomic"

	"github.com/sashabaranov/go-openai"
)

// usageCounter accumulates token usage across every LLM call routed through a
// countingLLM. Safe for concurrent use (sub-agents run in their own goroutines,
// each with its own counter, but streaming delivery may add from a goroutine).
type usageCounter struct {
	prompt     atomic.Int64
	completion atomic.Int64
	total      atomic.Int64
}

func (c *usageCounter) add(u LLMUsage) {
	c.prompt.Add(int64(u.PromptTokens))
	c.completion.Add(int64(u.CompletionTokens))
	c.total.Add(int64(u.TotalTokens))
}

func (c *usageCounter) snapshot() LLMUsage {
	return LLMUsage{
		PromptTokens:     int(c.prompt.Load()),
		CompletionTokens: int(c.completion.Load()),
		TotalTokens:      int(c.total.Load()),
	}
}

// countingLLM wraps an LLM, accumulating token usage from every call into
// counter. CreateChatCompletion returns usage directly; Ask discards it from
// its signature but records it on the returned fragment's Status.LastUsage,
// which is where we read it.
type countingLLM struct {
	LLM
	counter *usageCounter
}

func (c *countingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (LLMReply, LLMUsage, error) {
	reply, usage, err := c.LLM.CreateChatCompletion(ctx, req)
	if err == nil {
		c.counter.add(usage)
	}
	return reply, usage, err
}

func (c *countingLLM) Ask(ctx context.Context, f Fragment) (Fragment, error) {
	res, err := c.LLM.Ask(ctx, f)
	if err == nil && res.Status != nil {
		c.counter.add(res.Status.LastUsage)
	}
	return res, err
}

// countingStreamingLLM preserves StreamingLLM so wrapping does not disable the
// streaming code path for callers that use it. Usage is accumulated from the
// StreamEventDone event.
type countingStreamingLLM struct {
	countingLLM
	streaming StreamingLLM
}

func (c *countingStreamingLLM) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan StreamEvent, error) {
	in, err := c.streaming.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Type == StreamEventDone {
				c.counter.add(ev.Usage)
			}
			out <- ev
		}
	}()
	return out, nil
}

// newCountingLLM wraps llm so token usage accumulates into counter. When llm is
// streaming-capable, the returned wrapper is too, so the streaming path is
// preserved.
func newCountingLLM(llm LLM, counter *usageCounter) LLM {
	base := countingLLM{LLM: llm, counter: counter}
	if s, ok := llm.(StreamingLLM); ok {
		return &countingStreamingLLM{countingLLM: base, streaming: s}
	}
	return &base
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd /home/mudler/_git/cogito && go test ./ -run 'TestCountingLLM|TestNewCountingLLM' -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
cd /home/mudler/_git/cogito
git add usage_counter.go usage_counter_internal_test.go fragment.go
git commit -m "feat: add per-run token usage accumulator (CumulativeUsage)

Adds a counting LLM decorator and a CumulativeUsage field on Status so a
full ExecuteTools run's token usage can be summed and exposed. Preserves
StreamingLLM so wrapping does not disable streaming."
```

---

## Task 2: cogito — stamp cumulative usage onto the `ExecuteTools` result

**Files:**
- Modify: `/home/mudler/_git/cogito/tools.go:1146` (signature), `tools.go:1159` (agentLLM), insert after `tools.go:1207`
- Create: `/home/mudler/_git/cogito/tools_cumulative_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `/home/mudler/_git/cogito/tools_cumulative_test.go`:

```go
package cogito_test

import (
	. "github.com/mudler/cogito"
	"github.com/mudler/cogito/tests/mock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

var _ = Describe("ExecuteTools cumulative usage", func() {
	It("sums token usage across every LLM call in the run", func() {
		mockLLM := mock.NewMockOpenAIClient()

		// One tool round then a final text answer => >= 2 CreateChatCompletion
		// calls plus one Ask. Each configured call reports 100 total tokens.
		mockLLM.AddCreateChatCompletionFunction("search", `{"query": "test"}`)
		mockTool := mock.NewMockTool("search", "Search for information")
		mock.SetRunResult(mockTool, "Result")
		mockLLM.SetAskResponse("Final answer")
		mockLLM.SetCreateChatCompletionResponse(openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "No more tools needed."}},
			},
		})
		mockLLM.SetUsage(40, 60, 100)
		mockLLM.SetUsage(40, 60, 100)
		mockLLM.SetUsage(40, 60, 100)

		fragment := NewEmptyFragment().AddMessage(UserMessageRole, "Task")
		result, err := ExecuteTools(mockLLM, fragment, WithTools(mockTool))
		Expect(err).ToNot(HaveOccurred())

		// Expected = the total tokens of every usage entry the mock dispensed.
		expected := 0
		for i := 0; i < mockLLM.CreateChatCompletionUsageIndex; i++ {
			expected += mockLLM.CreateChatCompletionUsage[i].TotalTokens
		}
		for i := 0; i < mockLLM.AskUsageIndex; i++ {
			expected += mockLLM.AskUsage[i].TotalTokens
		}

		Expect(expected).To(BeNumerically(">", 100), "test must drive at least two billed calls")
		Expect(result.Status.CumulativeUsage.TotalTokens).To(Equal(expected))
		Expect(result.Status.CumulativeUsage.TotalTokens).To(
			BeNumerically(">", result.Status.LastUsage.TotalTokens),
			"cumulative must exceed the last single call",
		)
	})
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /home/mudler/_git/cogito && go test ./ -run 'TestCogito|^Test$' -v 2>&1 | grep -A3 'cumulative usage'`
(Ginkgo specs run under `go test ./`; the suite entry point is `func Test`.)
Expected: FAIL — `CumulativeUsage.TotalTokens` is 0 (never stamped), so `Equal(expected)` fails.

- [ ] **Step 3: Change `ExecuteTools` to named returns and stamp via defer**

In `/home/mudler/_git/cogito/tools.go`, change the signature at line 1146 from:

```go
func ExecuteTools(llm LLM, f Fragment, opts ...Option) (Fragment, error) {
```

to:

```go
func ExecuteTools(llm LLM, f Fragment, opts ...Option) (result Fragment, retErr error) {
```

(Existing `return f, x` / `return Fragment{}, x` statements populate the named results positionally — no other return statements change. The one top-scope `tools, _, _, err := ...` at the autoPlan/usableTools sites still compiles because `tools` is a new variable on the left.)

- [ ] **Step 4: Keep the sub-agent fallback LLM unwrapped**

Still in `tools.go`, inside the `if o.enableAgentSpawning {` block, change line 1159 from:

```go
		agentLLM := llm
```

to:

```go
		agentLLM := llm // unwrapped: a sub-agent's own usage is counted in its own run, not folded into this one
```

(No code change beyond the comment — `agentLLM` is captured before the wrap in Step 5, which is inserted *after* this block.)

- [ ] **Step 5: Insert the wrap + defer after the spawning/pending-work setup**

In `tools.go`, find this exact block (ends at line 1207, just before the `// should I plan?` comment):

```go
	if o.pendingWork != nil && o.messageInjectionChan == nil {
		o.messageInjectionChan = make(chan openai.ChatCompletionMessage, 16)
	}

	// should I plan?
```

Replace it with:

```go
	if o.pendingWork != nil && o.messageInjectionChan == nil {
		o.messageInjectionChan = make(chan openai.ChatCompletionMessage, 16)
	}

	// Accumulate token usage across every LLM call in this run and stamp the
	// total onto the returned fragment, so callers (and sub-agent completion
	// callbacks) can report cumulative usage. The sub-agent fallback LLM
	// (agentLLM, captured above) stays unwrapped so its usage is not folded in.
	runUsage := &usageCounter{}
	llm = newCountingLLM(llm, runUsage)
	defer func() {
		if result.Status != nil {
			result.Status.CumulativeUsage = runUsage.snapshot()
		}
	}()

	// should I plan?
```

- [ ] **Step 6: Run the cumulative test to verify it passes**

Run: `cd /home/mudler/_git/cogito && go test ./ -run '^Test$' -v 2>&1 | grep -A3 'cumulative usage'`
Expected: PASS.

- [ ] **Step 7: Run the full cogito suite to verify no regressions (esp. streaming)**

Run: `cd /home/mudler/_git/cogito && go build ./... && go test ./...`
Expected: PASS, including `streaming_toolcall_test.go` (proves the wrap preserved `StreamingLLM`).

- [ ] **Step 8: Commit**

```bash
cd /home/mudler/_git/cogito
git add tools.go tools_cumulative_test.go
git commit -m "feat: expose cumulative token usage on the ExecuteTools result

Wraps the run LLM in a counting decorator and stamps the summed usage onto
the returned fragment's Status.CumulativeUsage via a deferred named return,
covering all exit paths. Each sub-agent run reports its own total."
```

---

## Task 3: nib — point at local cogito via `replace`

**Files:**
- Modify: `/home/mudler/_git/wiz/go.mod`

- [ ] **Step 1: Add the replace directive**

Append to `/home/mudler/_git/wiz/go.mod`:

```
replace github.com/mudler/cogito => /home/mudler/_git/cogito
```

- [ ] **Step 2: Verify nib builds against local cogito**

Run: `cd /home/mudler/_git/wiz && go build ./...`
Expected: builds cleanly (the new `CumulativeUsage` field resolves from the local checkout).

- [ ] **Step 3: Commit**

```bash
cd /home/mudler/_git/wiz
git add go.mod
git commit -m "build: replace cogito with local checkout during dev

Temporary: lets nib build against the un-released CumulativeUsage change.
Removed once cogito is tagged (see plan Task 9)."
```

---

## Task 4: nib — `AgentEvent` fields and the formatters

**Files:**
- Modify: `/home/mudler/_git/wiz/chat/callbacks.go:14`
- Create: `/home/mudler/_git/wiz/chat/agentstats.go`
- Create: `/home/mudler/_git/wiz/chat/agentstats_test.go`

- [ ] **Step 1: Add the three fields to `AgentEvent`**

In `/home/mudler/_git/wiz/chat/callbacks.go`, add `import "time"` to the import block (the file currently has no imports — add one), and extend the struct:

```go
package chat

import "time"

// ... (unchanged doc comments above) ...

// AgentEvent is emitted on sub-agent lifecycle changes (spawn/complete/fail).
type AgentEvent struct {
	ID     string
	Type   string // agent type name (e.g. "explore"); empty for generic
	Task   string
	Status AgentStatus
	Result string
	Err    error
	// Populated on completion/failure events (zero otherwise):
	ToolCount   int           // tools the sub-agent executed
	TotalTokens int           // cumulative tokens consumed across the run
	Elapsed     time.Duration // wall-clock from spawn to completion
}
```

- [ ] **Step 2: Write the failing formatter tests**

Create `/home/mudler/_git/wiz/chat/agentstats_test.go`:

```go
package chat

import (
	"testing"
	"time"
)

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, ""},
		{-5, ""},
		{1, "1 tokens"},
		{847, "847 tokens"},
		{999, "999 tokens"},
		{1000, "1k tokens"},
		{12000, "12k tokens"},
		{12400, "12.4k tokens"},
		{58800, "58.8k tokens"},
	}
	for _, c := range cases {
		if got := humanTokens(c.in); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{12 * time.Second, "12s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m 00s"},
		{63 * time.Second, "1m 03s"},
		{295 * time.Second, "4m 55s"},
		{3661 * time.Second, "1h 01m"},
	}
	for _, c := range cases {
		if got := humanDuration(c.in); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatsSuffix(t *testing.T) {
	cases := []struct {
		name string
		ev   AgentEvent
		want string
	}{
		{"all", AgentEvent{ToolCount: 3, TotalTokens: 12400, Elapsed: 63 * time.Second}, " · 3 tools · 12.4k tokens · 1m 03s"},
		{"singular tool", AgentEvent{ToolCount: 1, TotalTokens: 500, Elapsed: 5 * time.Second}, " · 1 tool · 500 tokens · 5s"},
		{"no tools", AgentEvent{ToolCount: 0, TotalTokens: 500, Elapsed: 5 * time.Second}, " · 500 tokens · 5s"},
		{"tokens only", AgentEvent{TotalTokens: 500}, " · 500 tokens"},
		{"empty", AgentEvent{}, ""},
	}
	for _, c := range cases {
		if got := c.ev.StatsSuffix(); got != c.want {
			t.Errorf("%s: StatsSuffix() = %q, want %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /home/mudler/_git/wiz && go test ./chat/ -run 'TestHumanTokens|TestHumanDuration|TestStatsSuffix' -v`
Expected: FAIL — `undefined: humanTokens`, `undefined: humanDuration`, `ev.StatsSuffix undefined`.

- [ ] **Step 4: Implement the formatters**

Create `/home/mudler/_git/wiz/chat/agentstats.go`:

```go
package chat

import (
	"fmt"
	"strings"
	"time"
)

// humanTokens renders a token count like "847 tokens" or "12.4k tokens".
// Returns "" for zero/negative so the segment can be omitted.
func humanTokens(n int) string {
	if n <= 0 {
		return ""
	}
	if n < 1000 {
		return fmt.Sprintf("%d tokens", n)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000.0)
	s = strings.TrimSuffix(s, ".0")
	return s + "k tokens"
}

// humanDuration renders a duration like "12s", "1m 03s", or "1h 01m".
// Returns "" for zero/negative so the segment can be omitted.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
}

// StatsSuffix renders the trailing run-stats summary for a completed sub-agent,
// e.g. " · 3 tools · 12.4k tokens · 1m 03s". Segments whose value is zero or
// unknown are omitted; returns "" when nothing is known.
func (ev AgentEvent) StatsSuffix() string {
	var parts []string
	switch {
	case ev.ToolCount == 1:
		parts = append(parts, "1 tool")
	case ev.ToolCount > 1:
		parts = append(parts, fmt.Sprintf("%d tools", ev.ToolCount))
	}
	if t := humanTokens(ev.TotalTokens); t != "" {
		parts = append(parts, t)
	}
	if d := humanDuration(ev.Elapsed); d != "" {
		parts = append(parts, d)
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd /home/mudler/_git/wiz && go test ./chat/ -run 'TestHumanTokens|TestHumanDuration|TestStatsSuffix' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/mudler/_git/wiz
git add chat/callbacks.go chat/agentstats.go chat/agentstats_test.go
git commit -m "feat(chat): AgentEvent run-stats fields and formatters"
```

---

## Task 5: nib — time sub-agents and populate the event stats

**Files:**
- Modify: `/home/mudler/_git/wiz/chat/session.go` (struct, `NewSession`, `emitAgentEvent`, new `agentUsage`)
- Create: `/home/mudler/_git/wiz/chat/agentusage_test.go`

- [ ] **Step 1: Write the failing `agentUsage` test**

Create `/home/mudler/_git/wiz/chat/agentusage_test.go`:

```go
package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestAgentUsage(t *testing.T) {
	t.Run("nil fragment", func(t *testing.T) {
		tc, tok := agentUsage(&cogito.AgentState{})
		if tc != 0 || tok != 0 {
			t.Fatalf("got (%d, %d), want (0, 0)", tc, tok)
		}
	})

	t.Run("populated", func(t *testing.T) {
		a := &cogito.AgentState{
			Fragment: &cogito.Fragment{
				Status: &cogito.Status{
					ToolsCalled:     make(cogito.Tools, 3),
					CumulativeUsage: cogito.LLMUsage{TotalTokens: 12400},
				},
			},
		}
		tc, tok := agentUsage(a)
		if tc != 3 || tok != 12400 {
			t.Fatalf("got (%d, %d), want (3, 12400)", tc, tok)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /home/mudler/_git/wiz && go test ./chat/ -run TestAgentUsage -v`
Expected: FAIL — `undefined: agentUsage`.

- [ ] **Step 3: Add the start-time map to `Session` and init it**

In `/home/mudler/_git/wiz/chat/session.go`, add two fields to the `Session` struct (after `hooks *hooks.Dispatcher` around line 38):

```go
	agentMu    sync.Mutex
	agentStart map[string]time.Time // sub-agent ID -> spawn time, for elapsed
```

Add `"time"` to the import block. In `NewSession`, add to the `s := &Session{...}` literal (alongside the other fields):

```go
		agentStart: make(map[string]time.Time),
```

- [ ] **Step 4: Implement `agentUsage` and populate stats in `emitAgentEvent`**

Replace the existing `emitAgentEvent` (currently at `session.go:244`) with:

```go
func (s *Session) emitAgentEvent(a *cogito.AgentState) {
	ev := AgentEvent{
		ID:     a.ID,
		Type:   a.Type,
		Task:   a.Task,
		Status: AgentStatus(a.Status),
		Result: a.Result,
		Err:    a.Error,
	}
	switch ev.Status {
	case AgentStatusRunning:
		s.agentMu.Lock()
		s.agentStart[a.ID] = time.Now()
		s.agentMu.Unlock()
	case AgentStatusCompleted, AgentStatusFailed:
		ev.ToolCount, ev.TotalTokens = agentUsage(a)
		s.agentMu.Lock()
		if start, ok := s.agentStart[a.ID]; ok {
			ev.Elapsed = time.Since(start)
			delete(s.agentStart, a.ID)
		}
		s.agentMu.Unlock()
	}
	if s.callbacks.OnAgentEvent != nil {
		s.callbacks.OnAgentEvent(ev)
	}
	if s.hooks != nil {
		s.hooks.Fire(s.ctx, hooks.EventAgentEvent, string(a.Status), map[string]any{
			"event":  "AgentEvent",
			"id":     a.ID,
			"type":   a.Type,
			"status": string(a.Status),
		})
	}
}

// agentUsage extracts a finished sub-agent's executed-tool count and cumulative
// token usage from its fragment. Safe against a nil fragment/status (e.g. a
// failed agent, whose Fragment is never set).
func agentUsage(a *cogito.AgentState) (toolCount, tokens int) {
	if a == nil || a.Fragment == nil || a.Fragment.Status == nil {
		return 0, 0
	}
	return len(a.Fragment.Status.ToolsCalled), a.Fragment.Status.CumulativeUsage.TotalTokens
}
```

- [ ] **Step 5: Run the test + package build to verify pass**

Run: `cd /home/mudler/_git/wiz && go test ./chat/ -run TestAgentUsage -v && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 6: Commit**

```bash
cd /home/mudler/_git/wiz
git add chat/session.go chat/agentusage_test.go
git commit -m "feat(chat): time sub-agents and populate completion run-stats"
```

---

## Task 6: nib — render the stats line in the TUI

**Files:**
- Modify: `/home/mudler/_git/wiz/tui/agents.go:24`
- Modify: `/home/mudler/_git/wiz/tui/model.go:634`
- Create: `/home/mudler/_git/wiz/tui/agents_stats_test.go`

- [ ] **Step 1: Write the failing transcript-line test**

Create `/home/mudler/_git/wiz/tui/agents_stats_test.go`:

```go
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestAgentTranscriptLineCompletedHasStats(t *testing.T) {
	line := agentTranscriptLine(chat.AgentEvent{
		Type:        "explore",
		Status:      chat.AgentStatusCompleted,
		ToolCount:   3,
		TotalTokens: 12400,
		Elapsed:     63 * time.Second,
	})
	for _, want := range []string{"finished", "3 tools", "12.4k tokens", "1m 03s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /home/mudler/_git/wiz && go test ./tui/ -run TestAgentTranscriptLineCompletedHasStats -v`
Expected: FAIL — line contains "finished" but not the stats segments.

- [ ] **Step 3: Append the stats suffix in `agentTranscriptLine`**

In `/home/mudler/_git/wiz/tui/agents.go`, change the completed case (line 24-25) from:

```go
	case chat.AgentStatusCompleted:
		return fmt.Sprintf("sub-agent %s finished", typ)
```

to:

```go
	case chat.AgentStatusCompleted:
		return fmt.Sprintf("sub-agent %s finished%s", typ, ev.StatsSuffix())
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /home/mudler/_git/wiz && go test ./tui/ -run TestAgentTranscriptLineCompletedHasStats -v`
Expected: PASS.

- [ ] **Step 5: Always emit the completion marker, plus the result block**

In `/home/mudler/_git/wiz/tui/model.go`, replace the `agentEventMsg` rendering block (currently the `if ev.Status == ... else if ...` at lines 634-647) with:

```go
		// On completion, always show the stats marker line (e.g.
		// "sub-agent explore finished · 3 tools · …"); when the agent produced a
		// final result, also surface it inline as one labeled block. Per-tool
		// activity stays in the Ctrl+O log viewer.
		if line := agentTranscriptLine(ev); line != "" {
			m.messages = append(m.messages, ChatMessage{Role: "agent", Content: line})
		}
		if ev.Status == chat.AgentStatusCompleted && strings.TrimSpace(ev.Result) != "" {
			typ := ev.Type
			if typ == "" {
				typ = "agent"
			}
			m.messages = append(m.messages, ChatMessage{
				Role:    "tool",
				Name:    typ,
				AgentID: ev.ID,
				Content: chat.PreviewResult(ev.Result, toolResultPreviewLines),
			})
		}
```

- [ ] **Step 6: Run the TUI tests + build**

Run: `cd /home/mudler/_git/wiz && go test ./tui/ && go build ./...`
Expected: PASS. (If a pre-existing TUI snapshot/golden test asserted that a completed-with-result event produced *no* "finished" line, update it to expect the new marker — the marker is the intended change.)

- [ ] **Step 7: Commit**

```bash
cd /home/mudler/_git/wiz
git add tui/agents.go tui/model.go tui/agents_stats_test.go
git commit -m "feat(tui): show sub-agent run-stats on the completion marker"
```

---

## Task 7: nib — render the stats line in the CLI

**Files:**
- Modify: `/home/mudler/_git/wiz/cmd/cli.go` (`formatAgentEventLine`, completed case)
- Modify: `/home/mudler/_git/wiz/cmd/cli_agents_test.go`

- [ ] **Step 1: Write the failing CLI test**

Append to `/home/mudler/_git/wiz/cmd/cli_agents_test.go`:

```go
func TestFormatAgentEventLineHasStats(t *testing.T) {
	line := formatAgentEventLine(chat.AgentEvent{
		ID: "abcd1234ef", Type: "explore", Status: chat.AgentStatusCompleted,
		Result: "ok", ToolCount: 3, TotalTokens: 12400, Elapsed: 63 * time.Second,
	})
	for _, want := range []string{"completed", "3 tools", "12.4k tokens", "1m 03s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}
```

Add `"time"` to that file's import block (it currently imports only `strings`, `testing`, and `chat`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /home/mudler/_git/wiz && go test ./cmd/ -run TestFormatAgentEventLineHasStats -v`
Expected: FAIL — stats segments absent.

- [ ] **Step 3: Append the stats suffix in `formatAgentEventLine`**

In `/home/mudler/_git/wiz/cmd/cli.go`, change the completed case from:

```go
	case chat.AgentStatusCompleted:
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) completed: %s", theme.SubAgent, typ, id, ev.Result))
```

to:

```go
	case chat.AgentStatusCompleted:
		return theme.Subtle.Render(fmt.Sprintf("%s %s (%s) completed%s: %s", theme.SubAgent, typ, id, ev.StatsSuffix(), ev.Result))
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /home/mudler/_git/wiz && go test ./cmd/ -run 'TestFormatAgentEventLine' -v`
Expected: PASS (both the existing line test and the new stats test).

- [ ] **Step 5: Commit**

```bash
cd /home/mudler/_git/wiz
git add cmd/cli.go cmd/cli_agents_test.go
git commit -m "feat(cli): show sub-agent run-stats on the completion line"
```

---

## Task 8: nib — full verification

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `cd /home/mudler/_git/wiz && go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full nib test suite**

Run: `cd /home/mudler/_git/wiz && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 3: Vet**

Run: `cd /home/mudler/_git/wiz && go vet ./...`
Expected: no findings.

---

## Task 9: Release handoff (cogito tag + un-replace) — manual checkpoint

**Files:** `/home/mudler/_git/wiz/go.mod`

This task is a checkpoint for the maintainer; do not perform the tag automatically.

- [ ] **Step 1: Push & tag cogito** — From `/home/mudler/_git/cogito`, push the two commits (Tasks 1–2) and cut a release (e.g. `git tag v0.10.x && git push --tags`), or let CI produce a pseudo-version from the merged commit.

- [ ] **Step 2: Bump nib to the released cogito and drop the replace**

```bash
cd /home/mudler/_git/wiz
# remove the `replace github.com/mudler/cogito => /home/mudler/_git/cogito` line from go.mod
go get github.com/mudler/cogito@<new-version>
go mod tidy
go build ./... && go test ./...
```

- [ ] **Step 3: Commit the bump**

```bash
git add go.mod go.sum
git commit -m "build: bump cogito to <new-version> for CumulativeUsage; drop local replace"
```

---

## Self-Review Notes

- **Spec coverage:** cumulative tokens (Tasks 1–2, 5), tool count via `ToolsCalled` (Task 5), elapsed via spawn-time map (Task 5), `AgentEvent` carrying the three (Task 4), TUI render incl. marker-on-completion restructure (Task 6), CLI render (Task 7), formatters with zero-segment omission and singular/plural (Task 4), failure modes via nil-guards in `agentUsage` and zero-omitting `StatsSuffix` (Tasks 4–5), streaming preserved (Task 1 test + Task 2 Step 7), tests at every layer, replace/release handoff (Tasks 3, 9).
- **Type consistency:** `usageCounter`, `newCountingLLM`, `Status.CumulativeUsage`, `AgentEvent.{ToolCount,TotalTokens,Elapsed}`, `AgentEvent.StatsSuffix`, `agentUsage`, `humanTokens`, `humanDuration` are used identically across tasks.
- **Deferred from spec:** a compiled-binary e2e was considered but dropped in favor of layer-targeted tests (the logic is pure formatting + a thin extraction helper); the cogito integration test already exercises the cumulative path end-to-end through `ExecuteTools`.
