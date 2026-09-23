# Classifier auto-approval and reply suggestions

## Goal

Use a small, fast classification model to do two things:

1. **Auto-approve** safe tool calls in a new `classify` approval mode, so the
   user does not have to approve every build or test command by hand, and
   does not have to trust every call with `auto`.
2. **Suggest the user's next reply** after each turn, so the user can keep
   the session going with one key. The suggestion shows as grey text in the
   empty composer. `Tab` fills it in. Nothing is sent automatically.

The first backend is the SystemOne API (kev-compatible), which LocalAI serves
since mudler/LocalAI#12140 through the vllm-cpp backend and GLiNER2.5
zero-shot NER. vllm.cpp's own server and kev serve the same API. nib talks to
it through an interface, so other backends can be added later without
changing the callers.

## Non-goals

- The classifier never denies a call. It can only turn a prompt into an
  approval. A call it does not approve goes to the normal human prompt.
- No free-form generated suggestions in v1. The `Suggester` interface leaves
  room for a generative implementation later.
- No LLM-backed `Classifier` implementation in v1.
- Suggestions are TUI-only. `--cli` does not show them.

## Configuration

```yaml
endpoints:
  home-localai:
    base_url: http://nas:8080/v1

# The small classification model. Both features use it.
classifier:
  endpoint: home-localai   # a named entry under endpoints:; omit to use the
                           # top-level base_url/api_key
  model: gliner2.5         # sent as the SystemOne "model" field; optional
  api: systemone           # the only value in v1; default systemone
  timeout: 2s              # per request; default 2s

approval_mode: classify    # new value: prompt | strict | allowlist | classify | auto

auto_approve:
  allow: [inspect, build_test]   # categories that may auto-approve (default)
  threshold: 0.85                # min confidence of the top category (default)

suggestions:
  enabled: true            # default true when classifier is configured
  threshold: 0.5           # min confidence to show a suggestion
  replies:                 # stock candidate user replies (default below)
    - continue
    - yes, go ahead
    - run the tests
    - fix it
    - commit it
```

Validation:

- `classifier.endpoint` must name an existing entry in `endpoints:`. The
  classifier takes that entry's `base_url`, `api_key` and `api_key_env`. It
  never takes that entry's `model`.
- `auto_approve.allow` entries must be known category names. An unknown name
  is a config error.
- `approval_mode: classify` without a usable `classifier` block is a config
  error at startup. When the mode is set at runtime, nib rejects the change
  with a message and keeps the current mode.
- `prompt_injection_protection.classifier` is a separate, LLM-based role and
  does not change. The README must explain the difference.

## Units

### `classify/` — the interface

```go
type Question struct {
    Type         string            // "noul" | "choice" | "score"
    Instructions string
    Choices      map[string]string // choice: option name → description
    Levels       []string          // score: level descriptions
}

type Entity struct {
    Text       string
    Start, End int
    Confidence float64
}

type Answer struct {
    Noul          float64            // noul
    Entities      []Entity           // noul
    Choice        string             // choice
    Confidence    float64            // choice, score
    Probabilities map[string]float64 // choice, score
    Score         float64            // score
}

type Classifier interface {
    Classify(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, error)
}

func New(cfg types.Config) (Classifier, error) // nil, nil when not configured
```

### `classify/systemone/` — the HTTP client

- `POST {base_url}/systemone` with `{state, questions, model}`.
- Maps `Question` to the wire shape: `choice` criteria is an object of
  name → description, and `score` criteria is an array.
- Maps the wire `answers` back to `Answer`. It reports an error when an answer
  is missing or has the wrong type.
- Applies `classifier.timeout` through the context. It sends
  `Authorization: Bearer` when an API key is set.
- Contains no nib policy.

### `chat/approver.go` — the approval policy

- Renders a call as `state`: the tool name, the arguments (a bash command is
  shown verbatim), the working directory, and the model's stated reason. The
  text is capped at a fixed size.
- Asks one `choice` question, `category`, with these options and descriptions:

  | category | meaning |
  |---|---|
  | `inspect` | only reads or lists state |
  | `build_test` | runs builds, tests, formatters, linters |
  | `local_edit` | modifies files in the workspace, reversible with git |
  | `destructive` | deletes, overwrites, force-pushes, resets |
  | `network` | sends data out, installs packages, pushes, publishes |
  | `system` | uses sudo, touches files outside the workspace, services, credentials |

- Returns `Verdict{Category string; Confidence float64; Approved bool; Err error}`.
  `Approved` is true only if `Category` is in `auto_approve.allow` and
  `Confidence >= auto_approve.threshold`.

### `chat/suggest.go` — the reply suggester

The suggester predicts **what the user would type next** in reply to the
last assistant message: the reply that keeps the session going. It does not
predict an action for the assistant.

```go
type Suggester interface {
    // Suggest returns candidate replies, best first. Empty means no suggestion.
    Suggest(ctx context.Context, in SuggestInput) ([]Suggestion, error)
}

type Suggestion struct {
    Text       string
    Confidence float64
}

type SuggestInput struct {
    LastAssistant string   // the message the user is replying to
    RecentUser    []string // the user's recent messages in this session
}
```

The v1 implementation, `classifierSuggester`, uses the `Classifier`. It
builds a set of candidate user replies and asks which one the user would
send:

1. **Candidates.** The candidates come from three sources, de-duplicated:
   - `suggestions.replies`, the configured stock replies.
   - The user's own recent short messages in this session (at most 5, each at
     most 80 characters). The suggestions then match how this user writes.
   - Answer options the assistant put to the user. A `noul` question, "an
     option the user is asked to choose between", runs over the tail of
     the last assistant message (capped at a fixed size). Each entity span
     above the extraction threshold is a candidate, verbatim. For example,
     *"Use Postgres or SQLite?"* gives `Postgres` and `SQLite`.
2. **Rank.** A `choice` question, "the reply the user sends next to keep
   the work going", over the last assistant message. The options are the
   candidates.
3. Returns the options whose probability is at least
   `suggestions.threshold`, sorted best first.

## Approval flow

`decideToolCall` (`chat/session.go`) keeps its order. The classifier step goes
after the read-only auto-approval and before `OnToolCall`:

1. External-data boundary → human prompt (unchanged; the classifier never runs)
2. PreToolUse hooks (unchanged)
3. `auto` / `/yolo`, turn-wide grant, allowed tools, bash prefix grants (unchanged)
4. Read-only auto-approval. This now applies in `prompt` **and** `classify`
   mode.
5. **New:** in `classify` mode, run the approver. `Approved` → approve.
6. Human prompt. When step 5 ran, the prompt shows the verdict, for example
   `classifier: destructive (0.82)` or `classifier: unavailable`.

Consequences:

- A classifier error or timeout never approves. The call goes to the prompt.
- Sub-agents share the session gate, so they use the same mode.
- A piped `--cli` run behaves as it does today: a call that falls through to
  the prompt with stdin closed exits with code 3.

Visibility: a call approved by the classifier shows the tag
`auto · build_test 0.93` on its tool block. Each verdict is logged at debug
level with the tool name, category and confidence.

## Switching modes in the TUI

- `Shift+Tab` in the composer cycles `base → classify → auto → base`, where
  `base` is the configured mode (`prompt`, `strict` or `allowlist`). When the
  configured mode is `classify` or `auto`, `base` is `prompt`. When no
  classifier is configured, the cycle skips `classify`.
- `/approve prompt|strict|allowlist|classify|auto` sets the mode directly.
  Bare `/approve` prints the current mode. `/yolo` does not change.
- The status bar shows the current mode when it is not `prompt`.
- Both paths call `Session.SetApprovalMode`. The change is session-only and is
  not written to the config file (`/settings` still persists).

## Suggestion flow (TUI)

- When a turn ends normally (not interrupted, not an error), the TUI starts
  `Suggest` in a command with a 1 s timeout and a sequence number.
- The result is dropped when the sequence number is stale: the user typed, a
  new turn started, or the session changed.
- It works like shell autosuggestion (fish, zsh-autosuggestions). The ranked
  list is computed once per turn. It is not recomputed on each key press, so
  typing makes no model calls.
- With an empty composer, the grey text is the top suggestion.
- While the user types, the grey text is the rest of the best-ranked
  suggestion that starts with the composer text (case-insensitive prefix
  match). When no suggestion matches, nothing shows. Deleting back to a
  matching prefix shows it again.
- The completion popup (slash commands, `@` files) wins: while it is open,
  no suggestion shows, and `Tab` keeps its current meaning.
- `Tab` with visible grey text completes the composer to the full suggestion
  exactly as if the user had typed it: plain editable input, cursor at the
  end. It is not sent. The user can edit it or press Enter.
- The suggestions are cleared when a new turn starts or the session changes.
- Errors and an empty result show nothing. Errors are logged at debug level.

## Setup offer

When the setup wizard's probe lists the endpoint's models and one of them has
a name that contains `gliner`, the wizard offers to configure the
`classifier` block with that endpoint and model. The wizard does not change
`approval_mode`. This phase is last and optional.

## Testing

- `classify/systemone`: `httptest` server with the JSON shapes from
  mudler/LocalAI#12140. Covers the request encoding for each question type,
  answer decoding, missing or mistyped answers, HTTP errors, timeout, and the
  auth header.
- `chat/approver`: a fake `Classifier`. Table tests for the allow set, the
  threshold boundary, and errors.
- `decideToolCall`: a fake `Classifier`. Tests prove that hooks and the
  external-data boundary win over the classifier, that read-only calls never
  reach it, that errors prompt, and that the prompt carries the verdict.
- `chat/suggest`: a fake `Classifier`. Covers the three candidate sources,
  de-duplication, the length caps, the threshold, the ordering, and an empty
  message.
- TUI: `Shift+Tab` cycle with and without a classifier, the `/approve`
  parsing, `Tab` accepting a suggestion (the text fills in and is not sent),
  prefix matching while typing (match, no match, backspace to a match,
  case-insensitivity), the completion popup taking precedence,
  stale suggestion results dropped, and the status bar mode.
- Config: validation errors for an unknown endpoint, an unknown category, and
  `classify` without a classifier.

## Documentation

Update README.md (the configuration example, Tool Approval, and a short
Suggestions section), then run `make sync-readme` and commit both files.

## Phases

1. `classify` interface + SystemOne client + config.
2. Approver + `classify` mode + `Shift+Tab` + `/approve` + status bar.
3. Suggester + TUI ghost text.
4. Setup wizard offer.

Each phase ships working and tested on its own.
