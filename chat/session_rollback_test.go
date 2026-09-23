package chat

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// rollbackBlockingLLM succeeds on the first CreateChatCompletion call and
// blocks on every subsequent call until the context is cancelled, so a test can
// interrupt the second turn mid-flight. Ask always succeeds (for compaction
// paths).
type rollbackBlockingLLM struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func newRollbackBlockingLLM() *rollbackBlockingLLM {
	return &rollbackBlockingLLM{release: make(chan struct{})}
}

func (b *rollbackBlockingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()

	if n > 1 {
		select {
		case <-b.release:
		case <-ctx.Done():
			return cogito.LLMReply{}, cogito.LLMUsage{}, ctx.Err()
		}
	}

	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "ok"},
				FinishReason: openai.FinishReasonStop,
			}},
		},
	}, cogito.LLMUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}, nil
}

func (b *rollbackBlockingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage("assistant", "compacted"), nil
}

func newRollbackTestSession(t *testing.T, llm cogito.LLM) *Session {
	t.Helper()
	s := &Session{
		ctx:           context.Background(),
		llm:           llm,
		llmModel:      "test-model",
		systemPrompt:  "you are a test assistant",
		cogitoOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
		agentManager:  cogito.NewAgentManager(),
		agentLogs:     newAgentLogStore(),
		inject:        make(chan openai.ChatCompletionMessage, 8),
	}
	s.fragment = cogito.NewEmptyFragment()
	s.messages = []openai.ChatCompletionMessage{}
	return s
}

// TestInterruptKeepsUserMessage proves that interrupting a turn (Ctrl+C) keeps
// the user's message in the history, followed by a note that the turn was
// interrupted. The transcript still shows the message, so the model must see it
// too: a follow-up like "go" means nothing without it.
func TestInterruptKeepsUserMessage(t *testing.T) {
	llm := newRollbackBlockingLLM()
	s := newRollbackTestSession(t, llm)
	defer s.Close()

	// Turn 1: completes normally, populating Status.
	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}

	// Snapshot Status fields after turn 1.
	if s.fragment.Status == nil {
		t.Fatal("Status should be populated after a successful turn")
	}
	beforeIterations := s.fragment.Status.Iterations
	beforePastActions := len(s.fragment.Status.PastActions)
	beforeToolsCalled := len(s.fragment.Status.ToolsCalled)
	beforeCumulative := s.fragment.Status.CumulativeUsage

	// Turn 2: interrupt mid-flight.
	go func() {
		llm.mu.Lock()
		blocked := llm.calls >= 2
		llm.mu.Unlock()
		for !blocked {
			llm.mu.Lock()
			blocked = llm.calls >= 2
			llm.mu.Unlock()
		}
		s.Interrupt()
	}()

	_, err := s.SendMessage("world")
	if err == nil {
		t.Fatal("second SendMessage should have been interrupted")
	}

	if n := len(s.messages); n != 3 || s.messages[2].Role != "user" || s.messages[2].Content != "world" {
		t.Fatalf("messages after interrupt = %+v, want the interrupted user message last", s.messages)
	}
	msgs := s.fragment.Messages
	if len(msgs) < 2 {
		t.Fatalf("fragment too short after interrupt: %+v", msgs)
	}
	if got := msgs[len(msgs)-2]; got.Role != "user" || got.Content != "world" {
		t.Errorf("fragment lost the interrupted user message: %+v", msgs)
	}
	if got := msgs[len(msgs)-1]; got.Role != "user" || got.Content != interruptedTurnNote {
		t.Errorf("fragment does not end with the interrupt note: %+v", msgs)
	}

	// s.fragment.Status must not have been mutated by the interrupted
	// ExecuteTools run (deep-copy).
	after := s.fragment.Status
	if after == nil {
		t.Fatal("Status should still be non-nil after interrupt")
	}
	if after.Iterations != beforeIterations {
		t.Errorf("Iterations polluted by interrupted turn: before=%d after=%d", beforeIterations, after.Iterations)
	}
	if len(after.PastActions) != beforePastActions {
		t.Errorf("PastActions polluted by interrupted turn: before=%d after=%d", beforePastActions, len(after.PastActions))
	}
	if len(after.ToolsCalled) != beforeToolsCalled {
		t.Errorf("ToolsCalled polluted by interrupted turn: before=%d after=%d", beforeToolsCalled, len(after.ToolsCalled))
	}
	if after.CumulativeUsage != beforeCumulative {
		t.Errorf("CumulativeUsage polluted by interrupted turn: before=%v after=%v", beforeCumulative, after.CumulativeUsage)
	}
}

// errorLLM fails every CreateChatCompletion call with a non-overflow,
// non-context error, so the error path runs without triggering overflow
// recovery.
type errorLLM struct {
	mu    sync.Mutex
	calls int
}

func (e *errorLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	return cogito.LLMReply{}, cogito.LLMUsage{PromptTokens: 5, TotalTokens: 5}, errFakeLLM
}

func (e *errorLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage("assistant", "compacted"), nil
}

var errFakeLLM = errFake("localai stream: status 401: the LLM is unavailable")

type errFake string

func (e errFake) Error() string { return string(e) }

// TestErrorKeepsUserMessage proves that a failed turn (e.g. the LLM is down)
// keeps the user's message, followed by a note naming the error. Nothing
// re-sends the message for the user, so a follow-up like "try again" must still
// find it in the history.
func TestErrorKeepsUserMessage(t *testing.T) {
	llm := &errorLLM{}
	s := newRollbackTestSession(t, llm)
	defer s.Close()

	// Seed a completed turn so there is prior history to preserve.
	s.fragment = s.fragment.
		AddMessage("user", "prior question").
		AddMessage("assistant", "prior answer")
	s.messages = []openai.ChatCompletionMessage{
		{Role: "user", Content: "prior question"},
		{Role: "assistant", Content: "prior answer"},
	}

	// Turn 2: LLM error (not interrupt, not overflow).
	_, err := s.SendMessage("this will fail")
	if err == nil {
		t.Fatal("SendMessage should have failed")
	}

	if n := len(s.messages); n != 3 || s.messages[2].Content != "this will fail" {
		t.Fatalf("messages after error = %+v, want prior history plus the failed user message", s.messages)
	}
	msgs := s.fragment.Messages
	if got := msgs[len(msgs)-2]; got.Role != "user" || got.Content != "this will fail" {
		t.Errorf("fragment lost the failed user message: %+v", msgs)
	}
	if got := msgs[len(msgs)-1]; got.Role != "user" || !strings.Contains(got.Content, errFakeLLM.Error()) {
		t.Errorf("fragment does not end with a note naming the error: %+v", msgs)
	}
}
