# OpenAI model limits and reasoning summaries

**Date:** 2026-09-28
**Status:** Approved design, ready for implementation

## Goal

Use authoritative model context metadata for ChatGPT Codex sessions and surface
readable reasoning summaries from both the ChatGPT Codex and OpenAI Responses
APIs.

The change must preserve explicit user settings, reuse nib's existing model-limit
discovery architecture, and never present encrypted reasoning state as readable
reasoning.

## Existing behavior

nib resolves context windows through `catalog.Limits` and endpoint-specific
limit sources. The current sources inspect LocalAI capabilities, metadata in an
OpenAI-compatible `/models` response, and LiteLLM model information. A static
model map and the 128,000-token default cover unresolved models.

The native ChatGPT Codex adapter already calls `/codex/models`, but its
`ListModels` method discards every field except the model ID and visibility.
Therefore, the session cannot use the endpoint's `context_window` or
`max_context_window` values and falls back to 128,000 tokens for new models.

The session and both user interfaces already support `LLMReply.ReasoningContent`.
However, the shared OpenAI Responses translator handles only messages and
function calls. It ignores `reasoning` output items. The adapters also do not
consistently request readable reasoning summaries.

## Context-window design

### Existing abstraction

Keep `catalog.Limits` as the common representation for discovered context and
output limits. Add a small native-adapter capability in `llmprovider`:

```go
type ModelLimitsProvider interface {
    ModelLimits(ctx context.Context, model string) (catalog.Limits, error)
}
```

This capability extends the existing discovery mechanism to protocols whose
model metadata cannot be queried through an OpenAI-compatible models endpoint.
It does not replace the endpoint discovery sources.

### Resolution order

At the start of the first turn after each model selection,
`Session.ensureModelLimits` resolves limits in this order. The session captures
the native capability before tracing or another decorator wraps the LLM and
hides its optional interfaces:

1. Preserve explicit user configuration. Discovery never overwrites an explicit
   `max_context_tokens` value.
2. If the active LLM implements `ModelLimitsProvider`, ask it for native model
   metadata. This change consumes only its context-window fields; native output
   caps remain available for a future extension.
3. Use the existing generic `catalog` endpoint discovery when the native source
   does not return a positive context window.
4. Retain existing static and default fallback behavior when no source returns a
   context window.

The generic output-cap resolution path remains independent and unchanged. A
native lookup failure is best-effort. It must not prevent a turn. Existing
fallback behavior remains available. Before applying a result, the session must
confirm that the selected model and captured provider capability are still
active. It discards stale results from a lookup overtaken by a concurrent model
switch.

### ChatGPT Codex metadata

Refactor the Codex models request so one parser supplies both `ListModels` and
`ModelLimits`. Parse these fields for each visible model:

- `slug` and `id`;
- `visibility`;
- `context_window`;
- `max_context_window`;
- any output limit exposed in a compatible field, if available.

Output-limit parsing from the native Codex catalog is deferred because the
current endpoint does not expose a documented output-cap field. Existing output
cap resolution remains unchanged.

Model matching uses the same effective ID used by `ListModels`: `slug` when it
is present, otherwise `id`. Matching is case-insensitive. An `id` is not an
alias when the same entry has a non-empty `slug`. Entries whose visibility is
`hide` or `none`, compared case-insensitively, remain excluded; missing or
unknown visibility values remain visible for forward compatibility.

Resolve the context window as follows:

1. use a positive `context_window`;
2. otherwise use a positive `max_context_window`;
3. otherwise report the limit as unknown.

This matches Codex's metadata precedence. nib keeps its existing configurable
compaction threshold instead of adopting Codex's fixed 90% auto-compaction and
95% usable-window policies. The discovered value is the model window; nib's
budget policy remains a separate concern.

Do not change the global 128,000-token fallback. Authoritative Codex metadata
removes that fallback for known ChatGPT models without changing unrelated
providers.

### Request lifetime

The native lookup uses the existing per-turn model-limit timeout. It is called
at the first turn after each model selection by the session's `limitsFor` guard.
Model selection itself performs no network request. Switching away and back is a
new selection and may query again. This design does not add a persistent
model-catalog cache. A model picker request and a limit request may remain
separate HTTP calls; caching can be added later if measurements justify it.

## Reasoning-summary design

### Request behavior

Both OpenAI Responses paths request readable summaries when nib has a configured
reasoning effort other than the exact normalized value `none`. Here, "both"
means ChatGPT Codex and the OpenAI Responses adapter. Configuration validation
supplies lowercase, trimmed effort values before adapter creation:

```json
"reasoning": {
  "effort": "<configured effort>",
  "summary": "auto"
}
```

The ChatGPT Codex adapter continues to request
`reasoning.encrypted_content` where required for protocol compatibility.
Encrypted content is opaque continuation state and is not user-visible text.

Do not request summaries when the reasoning effort is empty or `none`. The
standard OpenAI Responses path omits the reasoning object in that case. Codex
Responses Lite may still send its required context-only reasoning object, but it
must not add `effort` or `summary`. This preserves ordinary non-reasoning
requests and avoids sending unsupported parameters to models that were not
configured for reasoning.

### Shared response translation

Extend the shared OpenAI Responses wire types to recognize a reasoning item:

```json
{
  "type": "reasoning",
  "summary": [
    {"type": "summary_text", "text": "..."}
  ],
  "content": [
    {"type": "reasoning_text", "text": "..."}
  ],
  "encrypted_content": "..."
}
```

Collect non-empty `summary_text` entries in response order and join separate
parts with newlines. Set the result on
`cogito.LLMReply.ReasoningContent`. Existing session, CLI, and TUI callbacks then surface it without additional UI changes.

Raw `reasoning_text` is always ignored by this change; no opt-in is introduced.
`encrypted_content` is never rendered, logged as readable reasoning, or copied
into `LLMReply.ReasoningContent`. Supporting raw reasoning or opaque
continuation across turns is outside this change.

### Codex SSE

The Codex adapter reconstructs the completed Responses object from
`response.output_item.done` events before invoking the shared translator. A
reasoning item received in an output-item event must therefore survive
reconstruction unchanged and be parsed by the same summary logic as a normal
non-streaming Responses payload.

No incremental UI streaming is added. Summaries are emitted after the completed
response is translated, consistent with nib's current reply flow.

## Error and fallback behavior

- A failed Codex model-catalog request leaves the existing context value in
  place and does not fail the user turn.
- A missing model entry or absent context fields is treated as unknown and uses
  existing fallback behavior.
- Unknown output-item and summary-part types, absent optional fields, and empty
  summary text are ignored. Wrong JSON shapes for declared fields and invalid
  response JSON remain response-decoding errors; this change does not add a lax
  per-item parser.
- A response containing only reasoning and no assistant message or tool call
  retains the existing `ErrNoResponse` behavior.
- Explicit context configuration always wins over discovered metadata.
- Empty reasoning summaries do not trigger `OnReasoning`.

## Testing

Use focused tests for each behavior.

### Codex model limits

- `context_window` is returned for a visible matching model.
- `max_context_window` is used when `context_window` is absent or non-positive.
- `context_window` takes precedence when both fields exist.
- Slug/ID matching and hidden-entry filtering match `ListModels`.
- Unknown models return zero limits without an error.
- HTTP and decode failures return errors from the adapter, while session-level
  tests prove fallback behavior continues.
- An explicit `max_context_tokens` setting is never replaced.

### Requests and responses

- OpenAI Responses requests include effort and `summary: "auto"` when reasoning
  is configured.
- Requests omit the reasoning object when reasoning is not configured on the
  standard OpenAI Responses path; Codex Lite may retain its required
  context-only object.
- Codex requests include the same readable-summary setting.
- A non-streaming reasoning item populates `LLMReply.ReasoningContent`.
- Multiple summary parts retain order and have stable joining.
- Raw `reasoning_text` does not appear in the user-visible reasoning value.
- `encrypted_content` does not appear in the user-visible reasoning value.
- Responses without reasoning retain existing message and tool-call behavior.
- Codex SSE `response.output_item.done` reasoning items reach the shared
  translator.

Run focused `llmprovider/openairesponses`, `llmprovider/codex`, and `chat` tests,
then run `go test ./...`. Finally, use the local nib CLI against the configured
ChatGPT Codex account to verify the selected model receives its endpoint context
window and a reasoning-enabled prompt surfaces a readable summary when the
provider supplies one. Credentials and bearer tokens must not appear in logs,
test output, or committed files.

## Documentation

This fixes provider metadata use and restores an already-present reasoning UI
path. If configuration or visible behavior needs explanation, update `README.md`
and run `make sync-readme` so the embedded documentation remains current.

## Out of scope

- Replacing nib's configurable compaction policy with Codex's 90%/95% policy
- Changing the global fallback context window for unrelated providers
- Persistently caching the Codex model catalog
- Displaying raw `reasoning_text` by default
- Decrypting or displaying `encrypted_content`
- Replaying plaintext reasoning in later requests
- Incremental reasoning-summary streaming
- Extending summary-request configuration to Azure OpenAI Responses
- Changing turn-scoped reasoning-overflow retry behavior
