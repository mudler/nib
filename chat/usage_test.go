package chat

import (
	"context"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// Usage starts empty so the TUI badge can hide itself before the first turn.
func TestUsageStartsZero(t *testing.T) {
	s := &Session{}
	if got := s.Usage(); got != (SessionUsage{}) {
		t.Fatalf("fresh session usage = %+v, want zero", got)
	}
}

// Each ExecuteTools run contributes its whole cumulative total, so a turn that
// looped through several tool calls counts all of them.
func TestAddUsageAccumulatesAcrossRuns(t *testing.T) {
	s := &Session{}
	s.addUsage(cogito.LLMUsage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110})
	s.addUsage(cogito.LLMUsage{PromptTokens: 250, CompletionTokens: 20, TotalTokens: 270})

	got := s.Usage()
	want := SessionUsage{PromptTokens: 350, CompletionTokens: 30, TotalTokens: 380}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
}

// A turn is a user-visible exchange, not an LLM call: sub-agent and compaction
// calls add tokens without inflating the turn count.
func TestTurnsCountUserExchangesOnly(t *testing.T) {
	s := &Session{}
	s.countTurn()
	s.addUsage(cogito.LLMUsage{TotalTokens: 5})
	s.addUsage(cogito.LLMUsage{TotalTokens: 5})
	s.countTurn()

	if got := s.Usage().Turns; got != 2 {
		t.Fatalf("Turns = %d, want 2", got)
	}
}

// Spent tokens stay spent. Compaction shrinks the context, not the bill, and a
// counter that dropped after compaction would make long sessions look cheap.
func TestUsageNeverDecreases(t *testing.T) {
	s := &Session{}
	s.addUsage(cogito.LLMUsage{PromptTokens: 900, CompletionTokens: 50, TotalTokens: 950})
	before := s.Usage()

	// Whatever compaction does to the fragment, the counter only ever grows.
	s.addUsage(cogito.LLMUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11})
	after := s.Usage()

	if after.TotalTokens < before.TotalTokens {
		t.Fatalf("usage decreased: %d -> %d", before.TotalTokens, after.TotalTokens)
	}
}

// A zero usage (a backend that reported nothing) must not corrupt the totals.
func TestAddUsageIgnoresEmptyReports(t *testing.T) {
	s := &Session{}
	s.addUsage(cogito.LLMUsage{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8})
	s.addUsage(cogito.LLMUsage{})

	if got := s.Usage().TotalTokens; got != 8 {
		t.Fatalf("TotalTokens = %d, want 8", got)
	}
}

// usageLLM answers with a plain tool-free reply like captureLLM, but reports
// non-zero token usage — captureLLM returns an empty LLMUsage, which would make
// this test pass vacuously.
type usageLLM struct {
	mu sync.Mutex
	n  int
}

func (u *usageLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	u.mu.Lock()
	u.n++
	u.mu.Unlock()
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "ok"},
				FinishReason: openai.FinishReasonStop,
			}},
		},
	}, cogito.LLMUsage{PromptTokens: 120, CompletionTokens: 8, TotalTokens: 128}, nil
}

func (u *usageLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage("assistant", "ok"), nil
}

// newUsageTestSession mirrors newWarmTestSession in chat/warm_test.go: a
// Session built directly, so no MCP transports and no network.
func newUsageTestSession(t *testing.T) *Session {
	t.Helper()
	return &Session{
		ctx:           context.Background(),
		llm:           &usageLLM{},
		systemPrompt:  "You are the usage-test assistant.",
		fragment:      cogito.NewEmptyFragment(),
		cogitoOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		agentManager:  cogito.NewAgentManager(),
		agentLogs:     newAgentLogStore(),
		inject:        make(chan openai.ChatCompletionMessage, 8),
	}
}

// The wiring: a real SendMessage must move the counter. Unit-testing addUsage
// proves the arithmetic, not that anything calls it.
func TestSendMessageRecordsUsage(t *testing.T) {
	s := newUsageTestSession(t)

	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	got := s.Usage()
	if got.Turns != 1 {
		t.Fatalf("Turns = %d, want 1", got.Turns)
	}
	if got.TotalTokens == 0 {
		t.Fatal("a completed turn recorded no tokens; the counter is not wired to the run loop")
	}
}

// Two turns accumulate rather than replace — the bug that would hide if the
// session merely mirrored cogito's per-run CumulativeUsage.
func TestSendMessageAccumulatesAcrossTurns(t *testing.T) {
	s := newUsageTestSession(t)

	if _, err := s.SendMessage("first"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	afterOne := s.Usage()

	if _, err := s.SendMessage("second"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	afterTwo := s.Usage()

	if afterTwo.Turns != 2 {
		t.Fatalf("Turns = %d, want 2", afterTwo.Turns)
	}
	if afterTwo.TotalTokens <= afterOne.TotalTokens {
		t.Fatalf("second turn did not add tokens: %d then %d",
			afterOne.TotalTokens, afterTwo.TotalTokens)
	}
}

// interruptedLLM models the shape of a real interrupt: a call that the backend
// served and billed, followed by a turn that never finishes.
//
// It reports usage and then cancels the turn, and returns a reply with no
// choices so cogito treats the attempt as unusable and goes to retry. cogito's
// retry backoff aborts immediately on a cancelled context, so ExecuteTools
// returns an error — with the tokens from the served call already stamped onto
// the fragment by cogito's defer. Scripting it through a plain error return
// instead would prove nothing: cogito's counting wrapper only records usage
// when the call succeeded, so a first call that errors has no spend to lose.
type interruptedLLM struct {
	mu      sync.Mutex
	n       int
	session *Session
}

func (u *interruptedLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	u.mu.Lock()
	u.n++
	u.mu.Unlock()

	// The user hits Ctrl+C after the backend already answered and billed.
	u.session.Interrupt()

	return cogito.LLMReply{}, cogito.LLMUsage{PromptTokens: 900, CompletionTokens: 40, TotalTokens: 940}, nil
}

func (u *interruptedLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f, ctx.Err()
}

// Tokens spent before an interrupt stay counted. The turn does not: the user
// never got the exchange. Without the accumulation on SendMessage's error
// return, a Ctrl+C would silently erase everything the run had already paid for.
func TestInterruptedTurnKeepsTokensButNotTurn(t *testing.T) {
	s := newUsageTestSession(t)
	llm := &interruptedLLM{session: s}
	s.llm = llm

	if _, err := s.SendMessage("start something long"); err == nil {
		t.Fatal("SendMessage succeeded; the interrupt did not reach the run loop")
	}

	got := s.Usage()
	if got.TotalTokens == 0 {
		t.Fatal("interrupted turn recorded no tokens; spend before the interrupt was discarded")
	}
	if got.Turns != 0 {
		t.Fatalf("Turns = %d, want 0 — an interrupted turn is not a completed exchange", got.Turns)
	}
}

// Sub-agent tokens are spend on the same bill, so they belong in the session
// total even though they were burned by a different fragment.
func TestAgentUsageFullReturnsBothDirections(t *testing.T) {
	st := &cogito.Status{
		CumulativeUsage: cogito.LLMUsage{PromptTokens: 80, CompletionTokens: 9, TotalTokens: 89},
	}
	frag := cogito.NewEmptyFragment()
	frag.Status = st

	tools, u := agentUsageFull(&cogito.AgentState{Fragment: &frag})
	if u.PromptTokens != 80 || u.CompletionTokens != 9 || u.TotalTokens != 89 {
		t.Fatalf("usage = %+v, want 80/9/89", u)
	}
	_ = tools
}

// A failed agent never gets a fragment. Reading through it must not panic.
func TestAgentUsageFullIsNilSafe(t *testing.T) {
	if _, u := agentUsageFull(nil); u != (cogito.LLMUsage{}) {
		t.Fatalf("nil agent produced usage %+v", u)
	}
	if _, u := agentUsageFull(&cogito.AgentState{}); u != (cogito.LLMUsage{}) {
		t.Fatalf("agent without a fragment produced usage %+v", u)
	}
}

// The old two-return helper still exists, because emitAgentEvent's AgentEvent
// and its tests use exactly that shape.
func TestAgentUsageStillReportsTotalTokens(t *testing.T) {
	st := &cogito.Status{CumulativeUsage: cogito.LLMUsage{TotalTokens: 42}}
	frag := cogito.NewEmptyFragment()
	frag.Status = st

	if _, tokens := agentUsage(&cogito.AgentState{Fragment: &frag}); tokens != 42 {
		t.Fatalf("agentUsage tokens = %d, want 42", tokens)
	}
}

// The wiring: emitAgentEvent is the one place every finished sub-agent passes
// through, so proving the helper is not enough — the event path must feed the
// counter, and must not mistake a sub-agent run for a user turn.
func TestAgentEventFoldsUsageIntoTheSession(t *testing.T) {
	s := &Session{}
	st := &cogito.Status{
		CumulativeUsage: cogito.LLMUsage{PromptTokens: 80, CompletionTokens: 9, TotalTokens: 89},
	}
	frag := cogito.NewEmptyFragment()
	frag.Status = st

	s.emitAgentEvent(&cogito.AgentState{
		ID:       "a1",
		Status:   cogito.AgentStatusCompleted,
		Fragment: &frag,
	})

	got := s.Usage()
	if got.TotalTokens != 89 {
		t.Fatalf("TotalTokens = %d, want 89: the sub-agent event did not reach the counter", got.TotalTokens)
	}
	if got.Turns != 0 {
		t.Fatalf("Turns = %d, want 0: a sub-agent run is not a user turn", got.Turns)
	}
}

// A failed agent still burned tokens before it failed, and the user was billed
// for them. Skipping failures would make the flakiest runs look the cheapest.
func TestFailedAgentStillContributesItsSpend(t *testing.T) {
	s := &Session{}
	st := &cogito.Status{CumulativeUsage: cogito.LLMUsage{TotalTokens: 31}}
	frag := cogito.NewEmptyFragment()
	frag.Status = st

	s.emitAgentEvent(&cogito.AgentState{
		ID:       "a2",
		Status:   cogito.AgentStatusFailed,
		Fragment: &frag,
	})

	if got := s.Usage().TotalTokens; got != 31 {
		t.Fatalf("TotalTokens = %d, want 31: a failed agent's spend was dropped", got)
	}
}

// The spawn callback fires with Status=running, when nothing has been spent and
// the fragment is still nil. Counting there would add zero today, but would
// double-count the moment cogito starts populating a running agent's fragment.
func TestRunningAgentEventAddsNothing(t *testing.T) {
	s := &Session{}
	st := &cogito.Status{CumulativeUsage: cogito.LLMUsage{TotalTokens: 500}}
	frag := cogito.NewEmptyFragment()
	frag.Status = st

	s.emitAgentEvent(&cogito.AgentState{
		ID:       "a3",
		Status:   cogito.AgentStatusRunning,
		Fragment: &frag,
	})

	if got := s.Usage(); got != (SessionUsage{}) {
		t.Fatalf("usage = %+v, want zero: a still-running agent has no final figure to fold in", got)
	}
}

// summarizingLLM answers Ask with a summary and reports what that call cost,
// which is the figure compaction has been spending invisibly.
type summarizingLLM struct{}

func (summarizingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	return cogito.LLMReply{}, cogito.LLMUsage{}, nil
}

func (summarizingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	out := f.AddMessage("assistant", "a summary of earlier turns")
	if out.Status != nil {
		out.Status.LastUsage = cogito.LLMUsage{PromptTokens: 60, CompletionTokens: 6, TotalTokens: 66}
	}
	return out, nil
}

// Compaction spends tokens on a summary the user never asked for, at the moment
// the session is already large. Invisible spend is the thing being fixed.
func TestCompactionUsageCountsTowardTheSession(t *testing.T) {
	s := &Session{
		ctx: context.Background(),
		llm: summarizingLLM{},
		fragment: cogito.NewFragment(
			openai.ChatCompletionMessage{Role: "user", Content: "u1"},
			openai.ChatCompletionMessage{Role: "assistant", Content: "a1"},
			openai.ChatCompletionMessage{Role: "user", Content: "u2"},
			openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
		),
		compaction: types.CompactionConfig{KeepRecent: 2},
	}

	before, after, err := s.compactHistory(context.Background())
	if err != nil {
		t.Fatalf("compactHistory: %v", err)
	}
	if before == after {
		t.Fatal("nothing was compacted, so the summary call never happened")
	}

	got := s.Usage()
	if got.TotalTokens != 66 {
		t.Fatalf("TotalTokens = %d, want 66: the compaction summary call was not counted", got.TotalTokens)
	}
	if got.Turns != 0 {
		t.Fatalf("Turns = %d, want 0: compaction is not a user turn", got.Turns)
	}
}
