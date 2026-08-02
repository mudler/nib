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
