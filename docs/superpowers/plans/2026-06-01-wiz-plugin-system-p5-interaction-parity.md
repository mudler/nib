# Wiz — P5 (Claude-Code Interaction Parity) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two Claude-Code interaction-parity features, independent of plugins. (1) **Ctrl+C interrupts running work** instead of quitting: while a turn/sub-agent is in flight, Ctrl+C cancels just that work (session stays alive); when idle, Ctrl+C quits; Esc quits anytime. (2) An **`ask_user(question, options?)` tool** the agent calls to ask the user a question — free-text or multiple-choice — surfaced through the TUI and returned to the agent as the tool result.

**Architecture:** (1) `chat.Session` gets a per-turn cancellable context: `SendMessage` runs under a `turnCtx` derived from the session context; `Session.Interrupt()` cancels just that turn (which propagates to sub-agents spawned within it — cogito exposes no per-agent cancel). A cancelled turn surfaces as `context.Canceled`, which the TUI renders as "Interrupted" and keeps the session alive. (2) `ask_user` is a cogito in-session tool (`cogito.WithTools`) whose `Run` calls a new `chat.Callbacks.OnAskUser` callback; the TUI implements it via the same channel round-trip pattern as tool approval, showing a question prompt (numbered options when supplied) and returning the answer.

**Tech Stack:** Go 1.24, `context`, `github.com/mudler/cogito` (`WithContext`, `WithTools`, `NewToolDefinition`), `bubbletea`, standard `testing`. Builds on the existing approval-prompt channel plumbing in `tui/model.go`.

**Branch:** `feat/plugin-system`. All paths relative to `~/_git/wiz`.

**Scope boundary (do NOT build here):** cancelling **detached** (Ctrl+B'd) background sub-agents — cogito has no per-agent cancel, so Ctrl+C cancels the turn context (foreground + in-turn agents) only; detached agents continue (documented follow-up). No new plugin contribution types.

---

## File Structure

- `chat/session.go` — **modify**: per-turn context (`beginTurn`/`endTurn`/`Interrupt`); thread `turnCtx` through `SendMessage`; register the `ask_user` tool; the `OnAskUser` plumbing.
- `chat/callbacks.go` — **modify**: add `AskRequest` + `Callbacks.OnAskUser`.
- `chat/asktool.go` — **new**: the `askUserTool` cogito tool + arg schema.
- `chat/session_interrupt_test.go`, `chat/asktool_test.go` — **new** tests.
- `tui/model.go` — **modify**: split Ctrl+C from Esc (interrupt vs quit); handle `context.Canceled` as "Interrupted"; `ask_user` channels + prompt state + rendering + answer submission.
- `tui/ask.go` — **new**: ask-prompt rendering + answer parsing (pure helpers).
- `tui/ask_test.go` — **new**.

---

## Task 1: Per-turn cancellation in the session

**Files:**
- Modify: `chat/session.go`
- Test: `chat/session_interrupt_test.go` (create)

**Context:** A turn needs its own cancellable context so Ctrl+C can cancel just that turn without killing the session. `Session.Interrupt()` is called from the TUI goroutine while the turn runs on another goroutine, so the cancel func is guarded by a mutex.

- [ ] **Step 1: Write the failing test** — create `chat/session_interrupt_test.go`:

```go
package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/wiz/types"
)

func TestInterruptCancelsTurn(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Interrupt with no active turn is a safe no-op.
	s.Interrupt()

	turnCtx := s.beginTurn()
	if turnCtx.Err() != nil {
		t.Fatal("fresh turn context should not be cancelled")
	}
	s.Interrupt()
	if !errors.Is(turnCtx.Err(), context.Canceled) {
		t.Fatalf("Interrupt should cancel the turn context, got %v", turnCtx.Err())
	}

	// After the turn ends, Interrupt is a no-op again (does not affect a new turn).
	s.endTurn()
	next := s.beginTurn()
	s.endTurn()
	s.Interrupt()
	if next.Err() == nil {
		// next was already ended (cancelled by endTurn) — that's fine; the point is
		// Interrupt after endTurn doesn't panic and doesn't touch the session ctx.
	}
	if s.ctx.Err() != nil {
		t.Fatal("session context must remain alive after interrupts")
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./chat/ -run TestInterruptCancelsTurn -v` (beginTurn/endTurn/Interrupt undefined).

- [ ] **Step 3: Implement** in `chat/session.go`.

(a) Add `"sync"` to the imports.

(b) Add fields to the `Session` struct (near `ctx`):

```go
	turnMu     sync.Mutex
	turnCancel context.CancelFunc
```

(c) Add the methods (after `SetPlanMode`/`GetPlanMode`):

```go
// beginTurn starts a per-turn cancellable context derived from the session
// context and stores its cancel func so Interrupt can cancel just this turn.
func (s *Session) beginTurn() context.Context {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	ctx, cancel := context.WithCancel(s.ctx)
	s.turnCancel = cancel
	return ctx
}

// endTurn releases the current turn context.
func (s *Session) endTurn() {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
		s.turnCancel = nil
	}
}

// Interrupt cancels the in-flight turn (and any sub-agents spawned within it),
// leaving the session alive. Safe to call when no turn is running.
func (s *Session) Interrupt() {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
	}
}
```

(d) Thread the turn context through `SendMessage`. At the very top of `SendMessage` (after the existing UserPromptSubmit hook fire, before `if s.systemPrompt != ""`), add:

```go
	turnCtx := s.beginTurn()
	defer s.endTurn()
```

Then change every cogito context use in `SendMessage` from the session context to `turnCtx`:
- `cogito.WithContext(s.ctx)` → `cogito.WithContext(turnCtx)`
- the final `s.fragment, err = s.llm.Ask(context.Background(), s.fragment)` → `s.fragment, err = s.llm.Ask(turnCtx, s.fragment)`

(Leave the hook `s.ctx` uses as they are — hooks are not part of the cancellable LLM work.)

- [ ] **Step 4: Run, expect PASS** — `go test ./chat/ -run TestInterruptCancelsTurn -v`. Then `go test ./chat/ -v` and `go vet ./chat/`.

- [ ] **Step 5: Commit**

```bash
git add chat/session.go chat/session_interrupt_test.go
git commit -m "feat(chat): per-turn context + Session.Interrupt() for Ctrl+C"
```

---

## Task 2: TUI Ctrl+C — interrupt when running, quit when idle

**Files:**
- Modify: `tui/model.go`

**Context:** Split `Ctrl+C` from `Esc`. While work is running (`m.loading`, or the agent manager has running sub-agents), Ctrl+C interrupts (calls `Session.Interrupt()`) and keeps the session alive. When idle, Ctrl+C quits. Esc always quits (and still rejects a pending plan). A turn cancelled by Ctrl+C returns `context.Canceled`, which the response handler renders as "Interrupted".

- [ ] **Step 1: Add the import.** Add `"context"` and `"errors"` to `tui/model.go` imports if not already present (`context` already is; add `errors`).

- [ ] **Step 2: Split Ctrl+C from Esc.** Find the existing combined case:

```go
		case tea.KeyCtrlC, tea.KeyEsc:
			// If awaiting plan approval, reject the plan
			if m.awaitingPlanApproval {
				return m.handlePlanApproval(false)
			}
			m.quitting = true
			if m.session != nil {
				m.session.Close()
			}
			m.cancel()
			return m, tea.Quit
```

Replace it with two cases:

```go
		case tea.KeyCtrlC:
			// Interrupt running work; quit only when idle.
			if m.awaitingPlanApproval {
				return m.handlePlanApproval(false)
			}
			if m.isWorking() {
				if m.session != nil {
					m.session.Interrupt()
				}
				m.status = "Interrupting…"
				return m, nil
			}
			return m.quit()

		case tea.KeyEsc:
			if m.awaitingPlanApproval {
				return m.handlePlanApproval(false)
			}
			return m.quit()
```

- [ ] **Step 3: Add the `isWorking` and `quit` helpers** (near the other Model methods, e.g. after `Output()`):

```go
// isWorking reports whether a turn or sub-agent is currently running.
func (m Model) isWorking() bool {
	if m.loading {
		return true
	}
	if m.session != nil && m.session.AgentManager() != nil {
		return m.session.AgentManager().HasRunning()
	}
	return false
}

// quit tears down the session and exits.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if m.session != nil {
		m.session.Close()
	}
	m.cancel()
	return m, tea.Quit
}
```

- [ ] **Step 4: Render an interrupted turn as "Interrupted".** In `Update`, find the `case responseMsg:` block. After `m.loading = false` and before appending the error/assistant message, special-case a cancelled turn. Change the error branch:

```go
		case responseMsg:
			m.loading = false
			m.status = ""
			m.reasoning = ""
			if msg.err != nil {
				if errors.Is(msg.err, context.Canceled) {
					m.messages = append(m.messages, ChatMessage{Role: "agent", Content: "⛔ Interrupted."})
				} else {
					m.err = msg.err
					m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error()})
				}
			} else {
				m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.content})
			}
			m.updateViewport()
```

- [ ] **Step 5: Update the help text** in `View` to reflect the new Ctrl+C semantics. Find the `helpText := "Enter: send • Esc: exit"` line and change it to:

```go
	helpText := "Enter: send • Ctrl+C: interrupt/exit • Esc: exit"
```

- [ ] **Step 6: Build + vet.**

Run: `go build ./...`, `go vet ./...`, `go test ./tui/ -v`
Expected: clean build, vet clean, existing tui tests pass.

- [ ] **Step 7: Commit**

```bash
git add tui/model.go
git commit -m "feat(tui): Ctrl+C interrupts running work; quits only when idle"
```

---

## Task 3: ask_user tool + OnAskUser callback

**Files:**
- Modify: `chat/callbacks.go`, `chat/session.go`
- Create: `chat/asktool.go`
- Test: `chat/asktool_test.go` (create)

**Context:** `ask_user` is a cogito in-session tool. Its `Run` parses `question` + optional `options`, calls the session's `OnAskUser` callback (which the TUI implements via a blocking channel round-trip), and returns the user's answer as the tool result.

- [ ] **Step 1: Write the failing test** — create `chat/asktool_test.go`:

```go
package chat

import "testing"

func TestAskUserToolRun(t *testing.T) {
	var got AskRequest
	tool := &askUserTool{ask: func(req AskRequest) string {
		got = req
		return "the answer"
	}}

	out, _, err := tool.Run(map[string]any{
		"question": "Which one?",
		"options":  []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "the answer" {
		t.Fatalf("answer = %q", out)
	}
	if got.Question != "Which one?" || len(got.Options) != 2 || got.Options[0] != "a" {
		t.Fatalf("request parsed wrong: %+v", got)
	}

	// nil ask func → empty answer, no panic.
	safe := &askUserTool{}
	if out, _, err := safe.Run(map[string]any{"question": "x"}); err != nil || out != "" {
		t.Fatalf("nil-ask should be safe: out=%q err=%v", out, err)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./chat/ -run TestAskUserToolRun -v`.

- [ ] **Step 3: Implement.**

In `chat/callbacks.go`, add the request type and the callback field. Add near the other request types:

```go
// AskRequest is a question the agent wants to ask the user.
type AskRequest struct {
	Question string
	Options  []string // optional multiple-choice options
}
```

And add to the `Callbacks` struct (after `OnAgentEvent`):

```go
	// OnAskUser is called when the agent asks the user a question (ask_user tool).
	// It blocks until the user answers and returns the answer.
	OnAskUser func(req AskRequest) string
```

Create `chat/asktool.go`:

```go
package chat

import "github.com/mudler/cogito"

// askUserArgs is the JSON-schema shape of the ask_user tool's parameters.
type askUserArgs struct {
	Question string   `json:"question" jsonschema:"the question to ask the user"`
	Options  []string `json:"options,omitempty" jsonschema:"optional list of choices the user can pick from"`
}

// askUserTool is a cogito tool that asks the user a question and returns the
// answer. It satisfies cogito.Tool[map[string]any].
type askUserTool struct {
	ask func(AskRequest) string
}

// Run parses the tool arguments, asks the user, and returns the answer string.
func (a *askUserTool) Run(args map[string]any) (string, any, error) {
	q, _ := args["question"].(string)
	var opts []string
	switch v := args["options"].(type) {
	case []any:
		for _, o := range v {
			if s, ok := o.(string); ok {
				opts = append(opts, s)
			}
		}
	case []string:
		opts = v
	}
	if a.ask == nil {
		return "", nil, nil
	}
	return a.ask(AskRequest{Question: q, Options: opts}), nil, nil
}

// askUserToolDefinition builds the cogito tool definition for ask_user.
func askUserToolDefinition(ask func(AskRequest) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&askUserTool{ask: ask},
		askUserArgs{},
		"ask_user",
		"Ask the user a clarifying question and wait for their answer. Provide `options` for a multiple-choice question; omit them for free-text. Use this when you need information only the user can provide.",
	)
}
```

In `chat/session.go` `SendMessage`, register the tool in `cogitoOpts` (add to the second `cogitoOpts = append(...)` block, alongside the agent options):

```go
		cogito.WithTools(askUserToolDefinition(func(req AskRequest) string {
			if s.callbacks.OnAskUser != nil {
				return s.callbacks.OnAskUser(req)
			}
			return ""
		})),
```

- [ ] **Step 4: Run, expect PASS** — `go test ./chat/ -run TestAskUserToolRun -v`. Then `go test ./chat/ -v`, `go vet ./chat/`, `go build ./...`.

- [ ] **Step 5: Commit**

```bash
git add chat/callbacks.go chat/asktool.go chat/session.go chat/asktool_test.go
git commit -m "feat(chat): ask_user tool + OnAskUser callback"
```

---

## Task 4: ask_user TUI surface

**Files:**
- Create: `tui/ask.go`, `tui/ask_test.go`
- Modify: `tui/model.go`

**Context:** The TUI implements `OnAskUser` with the same blocking channel round-trip as tool approval: the callback (on the session goroutine) sends an `AskRequest` to a channel and waits on a response channel; the model enters an "awaiting answer" state, renders the question (numbered options when present), and on Enter parses the typed answer and sends it back. Pure rendering/parsing helpers live in `tui/ask.go`.

- [ ] **Step 1: Write the failing test** — create `tui/ask_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/chat"
)

func TestRenderAskAndParse(t *testing.T) {
	req := chat.AskRequest{Question: "Pick one", Options: []string{"alpha", "beta"}}
	out := renderAsk(req, 80)
	if !strings.Contains(out, "Pick one") || !strings.Contains(out, "1.") || !strings.Contains(out, "alpha") {
		t.Fatalf("ask render missing question/options:\n%s", out)
	}

	// A numeric answer picks the option text.
	if got := parseAskAnswer("2", req); got != "beta" {
		t.Fatalf("numeric pick: %q", got)
	}
	// Out-of-range or non-numeric → returned verbatim (free text).
	if got := parseAskAnswer("something else", req); got != "something else" {
		t.Fatalf("free text: %q", got)
	}
	if got := parseAskAnswer("9", req); got != "9" {
		t.Fatalf("out-of-range should be verbatim: %q", got)
	}
	// No options → verbatim.
	if got := parseAskAnswer("hi", chat.AskRequest{Question: "q"}); got != "hi" {
		t.Fatalf("no-options verbatim: %q", got)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./tui/ -run TestRenderAskAndParse -v`.

- [ ] **Step 3: Implement** — create `tui/ask.go`:

```go
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/wiz/chat"

	"github.com/charmbracelet/lipgloss"
)

// renderAsk renders the agent's question (and numbered options, if any).
func renderAsk(req chat.AskRequest, width int) string {
	var b strings.Builder
	b.WriteString(askHeaderStyle.Render("❓ " + req.Question))
	b.WriteString("\n")
	for i, o := range req.Options {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, o)
	}
	if len(req.Options) > 0 {
		b.WriteString(dimmedStyle.Render("Type a number to pick, or type your own answer."))
	} else {
		b.WriteString(dimmedStyle.Render("Type your answer."))
	}
	return askBoxStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// parseAskAnswer maps a typed answer to an option when it is a valid 1-based
// index into req.Options; otherwise it returns the raw text (free-form answer).
func parseAskAnswer(input string, req chat.AskRequest) string {
	trimmed := strings.TrimSpace(input)
	if len(req.Options) > 0 {
		if n, err := strconv.Atoi(trimmed); err == nil && n >= 1 && n <= len(req.Options) {
			return req.Options[n-1]
		}
	}
	return input
}

var (
	askBoxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	askHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
)
```

> NOTE: `dimmedStyle` already exists in `tui/styles.go` — reuse it. If `askBoxStyle`/`askHeaderStyle` collide with existing names, rename the new ones.

- [ ] **Step 4: Wire the channels + state into `tui/model.go`.**

(a) Add fields to the `Model` struct:

```go
	// ask_user state
	pendingAsk      *chat.AskRequest
	awaitingAsk     bool
	askRequestChan  chan chat.AskRequest
	askResponseChan chan string
```

(b) In `NewModel`, initialize the channels (with the other `make(chan …)` inits):

```go
		askRequestChan:   make(chan chat.AskRequest),
		askResponseChan:  make(chan string),
```

(c) In `initSession`'s `chat.Callbacks{...}`, add the `OnAskUser` callback (mirrors the OnToolCall blocking pattern):

```go
			OnAskUser: func(req chat.AskRequest) string {
				m.askRequestChan <- req
				return <-m.askResponseChan
			},
```

(d) Add an `askMsg` type + listener (mirroring `toolCallMsg`/`listenToolRequest`):

```go
// askMsg is sent when the agent asks the user a question.
type askMsg chat.AskRequest
```

```go
// listenAskRequest listens for ask_user requests from the session.
func (m Model) listenAskRequest() tea.Cmd {
	return func() tea.Msg {
		select {
		case req := <-m.askRequestChan:
			return askMsg(req)
		case <-m.ctx.Done():
			return nil
		}
	}
}
```

(e) Start the listener: in the `case sessionReadyMsg:` block, add `m.listenAskRequest()` to the `cmds = append(cmds, …)` list of listeners.

(f) Handle `askMsg` in `Update` (mirroring `toolCallMsg`):

```go
		case askMsg:
			req := chat.AskRequest(msg)
			m.pendingAsk = &req
			m.awaitingAsk = true
			m.loading = false
			m.textarea.Focus()
			m.updateViewport()
			cmds = append(cmds, m.listenAskRequest())
```

(g) Handle the answer on Enter. In the `case tea.KeyEnter:` block, AFTER the completion-accept check and the `loading/!sessionReady` guard, BEFORE the plan/tool approval checks, add:

```go
			if m.awaitingAsk && m.pendingAsk != nil {
				answer := parseAskAnswer(m.textarea.Value(), *m.pendingAsk)
				m.messages = append(m.messages, ChatMessage{Role: "user", Content: answer})
				m.textarea.Reset()
				m.awaitingAsk = false
				m.pendingAsk = nil
				m.loading = true
				m.status = "Thinking…"
				m.updateViewport()
				m.askResponseChan <- answer
				return m, nil
			}
```

> The `m.askResponseChan <- answer` send unblocks the `OnAskUser` callback on the session goroutine. It is an unbuffered send to a goroutine that is guaranteed to be waiting (it sent the request and is blocked on the response), so it will not deadlock the UI.

(h) Render the ask prompt in `View`. Near the tool-approval box rendering (`if m.awaitingApproval && m.pendingTool != nil { … }`), add an analogous block:

```go
	if m.awaitingAsk && m.pendingAsk != nil {
		sb.WriteString(renderAsk(*m.pendingAsk, m.width))
		sb.WriteString("\n")
	}
```

(Place it in the same region of `View` where the other prompt boxes are appended to `sb`.)

- [ ] **Step 5: Build + test.**

Run: `go build ./...`, `go vet ./...`, `go test ./tui/ -v`
Expected: clean; `TestRenderAskAndParse` passes; existing tui tests pass.

- [ ] **Step 6: Commit**

```bash
git add tui/ask.go tui/ask_test.go tui/model.go
git commit -m "feat(tui): ask_user prompt surface (question + numbered options)"
```

---

## Task 5: e2e/binary validation (controller step)

Not a code task — the controller runs these after Task 4:

- [ ] **Ctrl+C interrupt** (pty + fake LLM that delays its response): start a turn, send Ctrl+C while it is in flight, assert wiz prints "Interrupted", stays alive (a second prompt works), and a second Ctrl+C when idle quits.
- [ ] **ask_user** (pty + a stateful fake LLM): first response is an `ask_user` tool call with options; assert the TUI renders the question + numbered options; send a numeric pick; assert the chosen option text flows back to the LLM as the tool result on the next request.
- [ ] **Full suite** `go test ./...` + `go vet ./...` green.

---

## Self-Review (completed during planning)

**Spec coverage (P5 scope):**
- Per-turn cancellable context + `Session.Interrupt()` → Task 1 ✓
- Ctrl+C interrupts running work / quits when idle; Esc quits; "Interrupted" rendering → Task 2 ✓
- `ask_user` tool (cogito tool + `OnAskUser` callback) → Task 3 ✓
- `ask_user` TUI surface (question + numbered options, numeric or free-text answer) → Task 4 ✓
- Binary validation → Task 5 ✓

**Out of P5 (documented):** cancelling detached (Ctrl+B'd) background sub-agents (cogito has no per-agent cancel; Ctrl+C cancels the turn context only). Rich multi-question forms (one question at a time).

**Type consistency:** `chat.AskRequest{Question,Options}`, `Callbacks.OnAskUser(AskRequest) string`, `askUserTool.Run(map[string]any) (string, any, error)`, `askUserToolDefinition(func(AskRequest) string) cogito.ToolDefinitionInterface`, `Session.Interrupt()/beginTurn()/endTurn()`, `renderAsk`/`parseAskAnswer`, `Model.isWorking()/quit()` consistent across files. The ask channel round-trip mirrors the existing `toolRequestChan`/`toolResponseChan` pattern exactly.

**Risk notes:** (1) Task 1 changes the turn context — verify existing chat tests stay green (the cancellable context is derived from `s.ctx`, so non-interrupted behavior is unchanged). (2) Task 2 changes a security-relevant key path (quit) — the refactor preserves Esc=quit and idle-Ctrl+C=quit; only running-Ctrl+C changes. (3) The ask channel send on Enter targets a guaranteed-waiting goroutine (it sent the request); no deadlock.
