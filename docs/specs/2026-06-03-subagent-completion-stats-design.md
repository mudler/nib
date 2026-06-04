# Sub-agent completion stats line

**Date:** 2026-06-03
**Status:** Approved design, ready for implementation planning

## Goal

When a spawned sub-agent finishes, render a one-line summary of its run —
Claude-Code-style — in both the TUI and the plain CLI:

```
sub-agent explore finished · 3 tools · 12.4k tokens · 1m 03s
```

The three numbers are: tools executed, cumulative tokens consumed, and wall-clock
elapsed time.

### Scope

- **In scope:** the completion line for *spawned sub-agents only*.
- **Out of scope:** a live status counter during execution; a summary for the
  root agent's own turns. (Both were considered and explicitly deferred.)
- **Token semantics:** *cumulative total* — the sum of total tokens across every
  LLM call the sub-agent made over its whole run, not the final call's context
  size.

## Background

nib drives its agent loop through `github.com/mudler/cogito`. Sub-agents are a
first-class cogito concept: the root agent's `spawn_agent` tool runs a sub-agent
via its own `ExecuteTools` call, and cogito fires `WithAgentSpawnCallback` /
`WithAgentCompletionCallback` with an `*AgentState`. nib maps those into
`chat.AgentEvent` in `emitAgentEvent` (`chat/session.go:244`) and renders them in
the TUI (`tui/agents.go`, `tui/model.go`) and CLI (`cmd/cli.go`).

What is already available at completion, with **no** cogito change:

- **Tool count** — `a.Fragment.Status.ToolsCalled` is the complete, cumulative
  list of executed tools for the run. Verified: `ExecuteTools` is one function
  (`tools.go:1146`) that appends each executed tool at `tools.go:1807` across all
  iterations; the save/restore sites at `tools.go:1325` and `tools.go:1834`
  preserve the accumulated slice around the final `Ask`. `len()` of this is the
  count. It counts only *executed* tools (appended after execution), which is the
  desired semantics.
- **Elapsed time** — nib can timestamp the spawn event vs. the completion event.

What is **not** available without a cogito change:

- **Cumulative tokens** — cogito only keeps the *last* LLM call's usage on the
  fragment (`Status.LastUsage`, set at `fragment.go:221`, `fragment.go:284`,
  `tools.go:965`). There is no per-call usage callback in the non-streaming path
  nib uses, and the completion callback fires once at the end. A nib-side LLM
  decorator was rejected: the agent LLM factory receives only `(model,
  temperature)` — the agent ID does not exist yet when it is called
  (`agent.go:352` builds the LLM, `agent.go:354` generates the ID) — so a wrapped
  LLM cannot be attributed to a specific finishing `AgentState`, and attribution
  is racy under concurrent background agents.

The clean fix lives in cogito (owned by the same author): accumulate usage per
`ExecuteTools` run and expose it on the returned fragment, which cogito already
assigns to `AgentState.Fragment` (`agent.go:471`).

## Design

### Part 1 — cogito (`/home/mudler/_git/cogito`, at commit `24b4c56`)

Expose per-run cumulative token usage on the fragment.

1. **New field.** Add `CumulativeUsage LLMUsage` to `Status` (`fragment.go:34`),
   initialized to zero where `Status` is constructed (`fragment.go:99`,
   `fragment.go:112`).

2. **Counting decorator.** A small unexported type wrapping `cogito.LLM` that, on
   each call, sums the call's `LLMUsage` (prompt / completion / total) into an
   internal accumulator guarded by an atomic or mutex (sub-agents run
   concurrently). It exposes a snapshot accessor.

3. **Wrap once in `ExecuteTools`.** After `agentLLM := llm` (`tools.go:1159`, the
   unwrapped LLM used as the sub-agent fallback so sub-agent calls are not
   double-counted in the parent), wrap the incoming `llm` with the decorator and
   use it for all of this run's calls. Stamp the accumulator's snapshot onto the
   returned fragment's `Status.CumulativeUsage` via a `defer` on a named return
   value, so every exit path (there are many) is covered.

4. **Both call paths.** The tool-decision calls go through `CreateChatCompletion`,
   which returns `LLMUsage` directly — the dominant path, captured by the
   decorator's `CreateChatCompletion`. The answer / auxiliary path can go through
   `llm.Ask`, which currently discards usage. To keep the number honest
   regardless of config, `Ask` must surface its call's usage so the decorator can
   account for it (e.g. the concrete client sets `LastUsage` on the fragment it
   returns, and the decorator reads it after delegating). Exact mechanism is an
   implementation-plan detail; the requirement is that **both** paths are counted.

5. **Attribution is automatic.** Each sub-agent runs its own `ExecuteTools` with
   its own factory-built LLM, so its returned fragment carries exactly that
   sub-agent's cumulative usage. No threading through fragment merge sites.

6. **Tests.** A fake `LLM` returning known per-call usage: assert a multi-call run
   yields `CumulativeUsage` equal to the sum; assert a sub-agent's returned
   fragment reflects only its own calls (not the parent's).

7. **Release.** Tag / publish cogito and note the new version for nib to bump to.

### Part 2 — nib wiring (`/home/mudler/_git/wiz`)

1. **Dependency.** Add a `replace github.com/mudler/cogito => /home/mudler/_git/cogito`
   directive in `go.mod` for development. Remove it and bump to the released
   cogito version once cogito is tagged.

2. **Per-agent start times.** Add a small mutex-guarded `map[string]time.Time` to
   `Session`. On a spawn event (`Status == running`) record `time.Now()`; on
   completion read it to compute `time.Since(start)`, then delete the entry.
   Missing entry → omit the elapsed segment.

3. **Populate the event.** Extend `chat.AgentEvent` (`chat/callbacks.go:14`) with:
   - `ToolCount int`
   - `TotalTokens int`
   - `Elapsed time.Duration`

   In `emitAgentEvent` (`chat/session.go:244`), on completion populate these from
   `a.Fragment.Status` (`len(ToolsCalled)`, `CumulativeUsage.TotalTokens`) and the
   start-time map. Guard against a nil `Fragment` / nil `Status` — fall back to
   zero values and let the renderer omit empty segments.

### Part 3 — rendering

1. **Formatters (pure, unit-tested).** Two helpers:
   - `humanTokens(n int) string`: `847` → `"847 tokens"`; `12400` → `"12.4k
     tokens"` (one decimal, trailing `.0` trimmed); `0` → `""` (segment omitted).
   - `humanDuration(d time.Duration) string`: `< 1m` → `"12s"`; `< 1h` → `"1m
     03s"`; `>= 1h` → `"1h 04m"`.

2. **Stats suffix.** A shared helper builds `· N tools · X tokens · Mm SSs` from an
   `AgentEvent`, using singular `"1 tool"`, and omitting any segment whose value
   is zero/unknown.

3. **TUI** (`tui/agents.go` `agentTranscriptLine`, completed case): append the
   stats suffix. Restructure `tui/model.go:634` so the completion stats marker is
   shown on completion **even when** there is a result block (today the result
   block path at `model.go:639` suppresses the transcript marker). The marker line
   carries the stats; the result block (if any) still renders below it.

4. **CLI** (`cmd/cli.go` `formatAgentEventLine`, completed case): insert the stats
   suffix before the result text.

## Testing

- **cogito:** unit tests for cumulative usage accumulation and per-sub-agent
  attribution (Part 1.6).
- **nib unit:** table tests for `humanTokens` and `humanDuration` across
  boundaries (sub-1k, k-range, sub-minute, minute, hour); a test for the stats
  suffix builder including zero-value segment omission and singular/plural.
- **nib e2e:** drive the compiled binary against the fake-LLM harness with a
  scripted sub-agent that executes a known number of tools and returns known
  per-call usage; assert the completion line shows the expected tool count and
  token figure. Elapsed is wall-clock, so assert on format/presence rather than an
  exact value.

## Failure modes

- `Fragment` or `Status` nil, or `CumulativeUsage` zero → omit the token segment
  (render no `· X tokens`).
- Unknown start time → omit the elapsed segment.
- Never block or fail completion rendering on missing stats; the result and the
  "finished" marker always render.

## Display format

Default wording: `· N tools · X tokens · Mm SSs` (singular `1 tool`). Easily
adjustable to Claude's exact `(N tool uses · X tokens · 4m 55s)` phrasing if
preferred — isolated in the stats-suffix helper.
