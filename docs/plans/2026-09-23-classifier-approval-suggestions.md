# Classifier Auto-Approval and Reply Suggestions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `classify` approval mode and TUI reply autosuggestions, both
driven by a small classifier reached through the SystemOne API.

**Architecture:** A new `classify` package defines a backend-neutral
`Classifier` interface; `classify/systemone` implements it over HTTP. The
chat session owns two policies on top of it: an approver used by
`decideToolCall`, and a suggester the TUI calls when the session waits on the
user. The TUI adds mode switching (`Shift+Tab`, `/approve`), a header badge,
and fish-style ghost text.

**Tech Stack:** Go, Bubble Tea TUI, `net/http`, `gopkg.in/yaml.v3`.

**Spec:** `docs/specs/2026-09-23-classifier-approver-and-suggestions-design.md`

## Global Constraints

- The classifier never denies a call. It only turns a prompt into an approval.
- The classifier never runs for externally influenced calls, and never
  overrides hooks. Read-only calls never reach it.
- A classifier error or timeout never approves.
- Categories: `inspect`, `build_test`, `local_edit`, `destructive`, `network`, `system`.
- Defaults: `auto_approve.allow: [inspect, build_test]`, `auto_approve.threshold: 0.85`,
  `classifier.timeout: 2s`, `classifier.api: systemone`,
  `suggestions.threshold: 0.5`, `suggestions.delay: 500ms`, suggestion request timeout 1 s,
  stock replies `continue`, `yes, go ahead`, `run the tests`, `fix it`, `commit it`.
- Recent user messages as candidates: at most 5, each at most 80 characters.
- Suggestions are TUI-only and never sent automatically.
- README.md changes must be synced with `make sync-readme`.

## Review Focus

- A SystemOne server that returns HTTP 200 with an answer of the wrong type,
  or no answer: the approver must fall through to the prompt (Task 1 test).
- `approval_mode: classify` set by `/settings` or `/approve` with no classifier
  configured: rejected, mode unchanged (Task 2 test).
- `/yolo on` while in `classify` mode, then `/yolo off`: returns to a mode the
  user can see in the header, not a stale badge (Task 3 test).
- A slow classifier response that lands after the user started typing, or
  after a new turn: dropped (Task 5 test).
- Multi-line composer text and case differences in prefix matching (Task 5 test).

---

### Task 1: Config types, `classify` interface, SystemOne client

**Files:**
- Create: `classify/classify.go`, `classify/classify_test.go`
- Create: `classify/systemone/systemone.go`, `classify/systemone/systemone_test.go`
- Modify: `types/config.go` (new `ClassifierConfig`, `AutoApproveConfig`, `SuggestionsConfig` fields on `Config`)
- Modify: `config/settings.go:97` (add `classify` to approval modes; descriptions for new keys)

**Interfaces:**
- Produces:
  - `types.ClassifierConfig{Endpoint, Model, API string; Timeout time.Duration}` (yaml `classifier`)
  - `types.AutoApproveConfig{Allow []string; Threshold float64}` (yaml `auto_approve`)
  - `types.SuggestionsConfig{Disabled bool; Threshold float64; Delay time.Duration; Replies []string}` (yaml `suggestions`; `enabled: false` is expressed as `disabled: true`, negative-sense like the rest of the config)
  - `classify.Question`, `classify.Entity`, `classify.Answer`, `classify.Classifier` as in the spec
  - `classify.Categories []string`, `classify.ValidCategory(string) bool`
  - `classify.New(cfg types.Config) (classify.Classifier, error)` → nil, nil when `cfg.Classifier.Endpoint == "" && cfg.Classifier.Model == ""`
  - `systemone.New(baseURL, apiKey, model string, timeout time.Duration) *systemone.Client`

- [ ] **Step 1: Write failing tests** for `systemone.Client.Classify` using `httptest`:
  request path `/v1/systemone` when base is `<srv>/v1`; body has `state`, `model`,
  `questions` with `choice` criteria as an object and `score` criteria as an array;
  `Authorization: Bearer k` only when a key is set; answer decoding for noul/choice;
  missing answer → error; wrong-type answer (`choice` question answered with no `choice`) → error;
  HTTP 500 → error with the status; a handler that sleeps past the timeout → error.
  Tests for `classify.New`: unknown endpoint name → error; `api: other` → error;
  endpoint resolves base_url and key from the named entry; no endpoint → top-level base_url.
- [ ] **Step 2: Run** `go test ./classify/...` — expect compile failure.
- [ ] **Step 3: Implement.** Wire shape (from mudler/LocalAI#12140):
  request `{"state": "<string>", "questions": {id: {"type", "instructions", "criteria"}}, "model"}`;
  response `{"model", "answers": {id: {"type", "noul", "entities": [{"text","start","end","confidence"}], "choice", "confidence", "probabilities", "score", "legend"}}}`.
  `classify.New` resolves the endpoint through `endpoint.New(cfg, nil).Config("@"+name)`
  (no credential store needed for named entries), else `cfg.ResolvedMainModel()`.
- [ ] **Step 4: Run** `go test ./classify/... ./types/... ./config/...` — PASS.
- [ ] **Step 5: Commit** `feat(classify): add a classifier interface and a SystemOne client`.

### Task 2: Approver and `classify` mode in the session

**Files:**
- Create: `chat/approver.go`, `chat/approver_test.go`
- Modify: `chat/session.go` (fields, constructor, `decideToolCall`)
- Modify: `chat/settings.go` (`SetApprovalMode` returns error)
- Modify: `chat/callbacks.go` (`ToolCallRequest.Verdict string`; `Callbacks.OnAutoApproved func(req ToolCallRequest, v Verdict)`)
- Modify: `tui/settings.go:352`, any other `SetApprovalMode` caller (handle the error)

**Interfaces:**
- Consumes: `classify.Classifier`, `types.AutoApproveConfig`
- Produces:
  - `chat.Verdict{Category string; Confidence float64; Approved bool; Err error}` with `func (v Verdict) String() string` → `"build_test (0.93)"` or `"unavailable"`
  - `chat.NewApprover(c classify.Classifier, cfg types.AutoApproveConfig, workDir string) *Approver`
  - `(*Approver).Judge(ctx, req ToolCallRequest) Verdict`
  - `(*Session).SetApprovalMode(mode string) error` — rejects `classify` when no classifier
  - `(*Session).ApprovalMode() string`, `(*Session).HasClassifier() bool`
  - `(*Session).Classifier() classify.Classifier`

- [ ] **Step 1: Failing tests** with a fake `Classifier`: allowed category above threshold → approved;
  at exactly threshold → approved; below → not; disallowed category → not; error → not, `String()=="unavailable"`;
  rendered state contains tool name, bash command verbatim, reasoning, and is capped (8 KiB).
  `decideToolCall` in `classify` mode: approved verdict skips `OnToolCall` and fires `OnAutoApproved`;
  rejected verdict calls `OnToolCall` with `req.Verdict` set; a read-only call never reaches the
  fake; an externally influenced call never reaches it; a hook decision wins; `prompt` mode never calls it.
  `SetApprovalMode("classify")` without a classifier → error, mode unchanged.
- [ ] **Step 2: Run** `go test ./chat/ -run 'Approver|Classify'` — FAIL.
- [ ] **Step 3: Implement.** In `decideToolCall`, extend the read-only check to `mode == "classify"`,
  then before `OnToolCall`: `if mode == "classify" && s.approver != nil { v := s.approver.Judge(s.ctx, req); if v.Approved { fire OnAutoApproved; return approved }; req.Verdict = v.String() }`.
  `NewSession` builds the classifier with `classify.New(cfg)`; an error is appended to `configErrs`
  (not fatal), and `approval_mode: classify` then falls back to `prompt` with that error shown.
  Log each verdict with `xlog.Debug`.
- [ ] **Step 4: Run** `go test ./chat/ ./tui/ ./cmd/...` — PASS.
- [ ] **Step 5: Commit** `feat(chat): approve tool calls with a classifier in classify mode`.

### Task 3: Mode switching and visibility in the TUI and CLI

**Files:**
- Modify: `slash/slash.go` (`cmdApprove`, `KindApprove`, `Action.Mode string`), `slash/slash_test.go`
- Modify: `tui/model.go` (KeyShiftTab, `KindApprove` case, act-at-once like `/yolo`), `tui/render/viewstate.go` + `tui/render/base.go:304` (badge), `theme/*.go` (badge + notices)
- Modify: `tui/` approval prompt rendering (show `req.Verdict` line), `OnAutoApproved` → dim transcript line `auto-approved · build_test 0.93`
- Modify: `cmd/cli.go` (`KindApprove` case; print verdict in prompt; print auto-approval line)
- Test: `slash/slash_test.go`, `tui/approvalmode_test.go`

**Interfaces:**
- Consumes: `Session.SetApprovalMode`, `Session.ApprovalMode`, `Session.HasClassifier`, `Session.AutoApprove`
- Produces: `nextApprovalMode(current, base string, hasClassifier bool) string` in `tui/approvalmode.go`

- [ ] **Step 1: Failing tests:** `/approve` parses bare/each mode/unknown (error);
  `nextApprovalMode` cycle `prompt→classify→auto→prompt`, `strict→classify→auto→strict`,
  base `classify` or `auto` → `prompt`, no classifier skips `classify`;
  header shows `classify` badge in classify mode and the yolo badge in auto;
  `/yolo on` then `/yolo off` in classify mode returns to classify (SetAutoApprove leaves approvalMode alone).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** The effective mode for the badge is `auto` when `AutoApprove()` is true,
  else `ApprovalMode()`. `Shift+Tab` acts only when no picker/popup is open.
  `/approve` acts at once like `/yolo`, and if an approval prompt is showing and the new mode is `auto`, resolves it approved.
- [ ] **Step 4: Run** `go test ./slash/ ./tui/... ./cmd/...` — PASS.
- [ ] **Step 5: Commit** `feat(tui): switch approval modes with Shift+Tab and /approve`.

### Task 4: Suggester

**Files:**
- Create: `chat/suggest.go`, `chat/suggest_test.go`

**Interfaces:**
- Consumes: `classify.Classifier`, `types.SuggestionsConfig`
- Produces:
  - `chat.Suggestion{Text string; Confidence float64}`, `chat.SuggestInput{LastAssistant string; RecentUser []string}`
  - `chat.Suggester` interface; `chat.NewClassifierSuggester(c classify.Classifier, cfg types.SuggestionsConfig) Suggester`
  - `(*Session).Suggest(ctx) ([]Suggestion, error)` — builds `SuggestInput` from `s.messages`; returns nil, nil when there is no suggester or no assistant message
  - `(*Session).SuggestionsEnabled() bool`, `(*Session).SuggestionDelay() time.Duration`

- [ ] **Step 1: Failing tests** with a fake Classifier: candidates = stock + recent user (≤5, ≤80 chars, newest first) + extracted entity spans above 0.5;
  de-duplicated case-insensitively; ranking uses the choice probabilities; options below threshold dropped;
  sorted best first; empty assistant message → nil; classifier error → error.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** Two `Classify` calls: `offers` (noul) then `reply` (choice over candidates, criteria = candidate → "").
  Assistant text capped to its last 4 KiB.
- [ ] **Step 4: Run** `go test ./chat/ -run Suggest` — PASS.
- [ ] **Step 5: Commit** `feat(chat): suggest the user's next reply with the classifier`.

### Task 5: TUI autosuggestion

**Files:**
- Create: `tui/suggest.go`, `tui/suggest_test.go`
- Modify: `tui/model.go` (responseMsg end, key handling, Tab, composer render)

**Interfaces:**
- Consumes: `Session.Suggest`, `Session.SuggestionsEnabled`, `Session.SuggestionDelay`
- Produces: `suggestState{seq int; armed bool; items []chat.Suggestion}`; `ghostFor(items, input string) string`

- [ ] **Step 1: Failing tests:** `ghostFor` empty input → top; prefix (case-insensitive) → remainder of best match; no match → ""; multi-line input → "".
  Model tests: after a successful `responseMsg`, a `suggestTickMsg` with the current seq fires `Suggest`; a key press before it disarms;
  an interrupted/failed turn does not arm; a `suggestResultMsg` with a stale seq is dropped;
  `Tab` with ghost text sets the composer to the full suggestion and sends nothing; completion popup open → no ghost and Tab completes the popup.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** On successful `responseMsg` with no turn dispatched and empty composer: bump seq, arm, `tea.Tick(delay, suggestTickMsg{seq})`.
  Any `tea.KeyMsg` disarms (items stay for prefix matching). On tick with matching seq and armed: run `Suggest` in a cmd with a 1 s timeout → `suggestResultMsg{seq, items, err}`.
  Starting a turn clears items and bumps seq. Render ghost with the existing completion ghost style after the composer text.
- [ ] **Step 4: Run** `go test ./tui/...` — PASS.
- [ ] **Step 5: Commit** `feat(tui): show reply suggestions as ghost text, Tab to accept`.

### Task 6: Setup wizard offer

**Files:**
- Modify: `setup/probe.go`, `setup/wizard.go`, tests

- [ ] **Step 1: Failing test:** when the probed model list contains a name with `gliner` (case-insensitive), the saved config gets `classifier.model` set to it after the user accepts; declining leaves `classifier` empty; `approval_mode` is never changed.
- [ ] **Step 2–4:** implement and pass `go test ./setup/`.
- [ ] **Step 5: Commit** `feat(setup): offer to configure a GLiNER classifier`.

### Task 7: Documentation

- [ ] README: config example (`classifier`, `auto_approve`, `suggestions`, `approval_mode: classify`), Tool Approval (classify mode, Shift+Tab, `/approve`), a short "Reply suggestions" section, the difference from `prompt_injection_protection.classifier`.
- [ ] `make sync-readme`, `go test ./...`, commit `docs: document classifier approval and reply suggestions`.
