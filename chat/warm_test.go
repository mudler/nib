package chat

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// captureLLM is a cogito.LLM that records the last request it was handed and
// answers with a plain, tool-free reply, so a SendMessage run terminates after
// one decision.
type captureLLM struct {
	mu   sync.Mutex
	last openai.ChatCompletionRequest
	n    int
}

func (c *captureLLM) record(req openai.ChatCompletionRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = req
	c.n++
}

func (c *captureLLM) lastRequest() openai.ChatCompletionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *captureLLM) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = openai.ChatCompletionRequest{}
}

func (c *captureLLM) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *captureLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	c.record(req)
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "ok"},
				FinishReason: openai.FinishReasonStop,
			}},
		},
	}, cogito.LLMUsage{}, nil
}

func (c *captureLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage("assistant", "ok"), nil
}

// newWarmTestSession builds a Session directly (no NewSession, so no MCP
// transports and no network) with a recognizable system prompt, a capture LLM,
// and several built-in tools allowed — without those, the tool-parity test
// would compare two empty lists and pass vacuously.
func newWarmTestSession(t *testing.T) *Session {
	t.Helper()
	return &Session{
		ctx:          context.Background(),
		llm:          &captureLLM{},
		systemPrompt: "You are the warm-test assistant.",
		fragment:     cogito.NewEmptyFragment(), // as NewSession does
		toolAllow: map[string]bool{
			"ask_user":    true,
			"agent_logs":  true,
			"spawn_agent": true,
		},
		// The real defaults config.Load applies; a zero MaxAttempts makes
		// cogito's retry loop run zero times and fail the turn.
		cogitoOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		agentManager:  cogito.NewAgentManager(),
		agentLogs:     newAgentLogStore(),
		inject:        make(chan openai.ChatCompletionMessage, 8),
	}
}

// toolNames extracts the advertised function names, in order.
func toolNames(tools []openai.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		if tl.Function != nil {
			names = append(names, tl.Function.Name)
		}
	}
	return names
}

// TestWarmLeavesTranscriptUntouched is the property that makes Warm safe to call
// on a live session: it must prime the server's cache without the user ever
// seeing that it happened.
func TestWarmLeavesTranscriptUntouched(t *testing.T) {
	s := newWarmTestSession(t)
	before := len(s.ExportHistory())
	if err := s.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if got := len(s.ExportHistory()); got != before {
		t.Fatalf("Warm changed history: %d → %d", before, got)
	}
	if got := len(s.GetMessages()); got != 0 {
		t.Fatalf("Warm leaked %d messages into GetMessages", got)
	}
}

// TestWarmSendsSystemPromptAndOneToken locks the two properties the whole
// feature depends on: the real system prompt is in the primed prefix, and the
// prime generates nothing.
func TestWarmSendsSystemPromptAndOneToken(t *testing.T) {
	s := newWarmTestSession(t)
	llm := s.llm.(*captureLLM)
	if err := s.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	req := llm.lastRequest()
	if req.MaxTokens != 1 {
		t.Fatalf("want MaxTokens=1, got %d", req.MaxTokens)
	}
	var sawSystem bool
	for _, m := range req.Messages {
		if m.Role == "system" && m.Content == s.systemPrompt {
			sawSystem = true
		}
	}
	if !sawSystem {
		t.Fatalf("Warm did not send the session's system prompt; messages=%+v", req.Messages)
	}
}

// TestWarmAdvertisesTheSameToolsAsARealTurn is THE test. The whole feature
// rests on the primed prefix matching the real one, and the tool schemas are
// the bulk of that prefix. A mismatch here is invisible at runtime: the prime
// succeeds, costs a full prefill, and the user's first message is slow anyway.
func TestWarmAdvertisesTheSameToolsAsARealTurn(t *testing.T) {
	s := newWarmTestSession(t)
	llm := s.llm.(*captureLLM)

	if err := s.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	warmTools := toolNames(llm.lastRequest().Tools)

	// Guard against a vacuous pass: an empty-vs-empty comparison would report
	// perfect parity while proving nothing.
	if len(warmTools) < 3 {
		t.Fatalf("test session advertises too few tools (%v) to prove parity", warmTools)
	}

	llm.reset()
	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	realTools := toolNames(llm.lastRequest().Tools)

	if !slices.Equal(warmTools, realTools) {
		t.Fatalf("prefix mismatch — Warm would not prime the real turn\n warm: %v\n real: %v", warmTools, realTools)
	}
}

// TestWarmAdvertisesTheGoalToolWhenAGoalIsActive pins the goal-dependent half
// of the tool set: goal_done is registered only while a goal is armed, and Warm
// must track that or the primed prefix is short one schema exactly when a goal
// is running.
func TestWarmAdvertisesTheGoalToolWhenAGoalIsActive(t *testing.T) {
	s := newWarmTestSession(t)
	llm := s.llm.(*captureLLM)
	s.SetGoal("finish the task")

	if err := s.Warm(context.Background()); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if got := toolNames(llm.lastRequest().Tools); !slices.Contains(got, "goal_done") {
		t.Fatalf("Warm omitted goal_done while a goal was active: %v", got)
	}
}

// TestWarmRefusesWhenForceReasoningIsOn: with force reasoning configured, the
// real turn's first request is a reasoning call, not the tool-selection call a
// prime reproduces. Priming anyway would cache a prefix nothing asks for, so
// Warm must surface cogito's refusal rather than report a useless success.
func TestWarmRefusesWhenForceReasoningIsOn(t *testing.T) {
	s := newWarmTestSession(t)
	s.cogitoOptions.ForceReasoning = true
	llm := s.llm.(*captureLLM)

	if err := s.Warm(context.Background()); err == nil {
		t.Fatal("Warm must refuse to prime a prefix it cannot reproduce")
	}
	if llm.calls() != 0 {
		t.Fatalf("Warm spent %d LLM call(s) on a prefix it cannot reproduce", llm.calls())
	}
}

// TestWarmRespectsCancellation: a user who sends a message mid-prime must not
// queue behind it.
func TestWarmRespectsCancellation(t *testing.T) {
	s := newWarmTestSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Warm(ctx); err == nil {
		t.Fatal("Warm on a cancelled context must return an error")
	}
}
