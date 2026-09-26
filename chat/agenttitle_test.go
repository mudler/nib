package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

func TestCleanAgentTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Scan LoRA loader options.":                       "Scan LoRA loader options",
		"Title: **Fix the flaky build**":                  "Fix the flaky build",
		"<think>what is it about</think>\n\"Audit auth\"": "Audit auth",
		"\n\n`Map the render queue`\nextra line":          "Map the render queue",
		"   ":                                             "",
		strings.Repeat("word ", 30):                       strings.TrimSpace(strings.Repeat("word ", 9)) + "…",
	} {
		if got := cleanAgentTitle(in); got != want {
			t.Errorf("cleanAgentTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// titleLLM answers a title request with a fixed reply, streamed, and records
// the requests it saw.
type titleLLM struct {
	mu    sync.Mutex
	reply string
	reqs  []openai.ChatCompletionRequest
}

func (l *titleLLM) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.mu.Lock()
	l.reqs = append(l.reqs, req)
	l.mu.Unlock()
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: l.reply}}},
	}}, cogito.LLMUsage{PromptTokens: 40, CompletionTokens: 6, TotalTokens: 46}, nil
}

func (l *titleLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) { return f, nil }

func (l *titleLLM) CreateChatCompletionStream(_ context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	l.mu.Lock()
	l.reqs = append(l.reqs, req)
	l.mu.Unlock()
	ch := make(chan cogito.StreamEvent, 3)
	ch <- cogito.StreamEvent{Type: cogito.StreamEventReasoning, Content: "thinking"}
	ch <- cogito.StreamEvent{Type: cogito.StreamEventContent, Content: l.reply}
	ch <- cogito.StreamEvent{Type: cogito.StreamEventDone, Usage: cogito.LLMUsage{PromptTokens: 40, CompletionTokens: 6, TotalTokens: 46}}
	close(ch)
	return ch, nil
}

// A new sub-agent gets a short title from the model, in the background. The
// request is small, its spend is counted, and it reports no compaction
// status, which the summary path it shares used to do.
func TestAgentTitleFromTheModel(t *testing.T) {
	llm := &titleLLM{reply: "Scan the LoRA loader."}
	titles := make(chan [2]string, 1)
	var statuses []string
	var mu sync.Mutex
	s := &Session{ctx: context.Background(), llm: llm, callbacks: Callbacks{
		OnStream:     func(StreamEvent) {},
		OnStatus:     func(st string) { mu.Lock(); statuses = append(statuses, st); mu.Unlock() },
		OnAgentTitle: func(id, title string) { titles <- [2]string{id, title} },
	}}

	s.titleAgent("a1", "Look through backend/go/sd and find every option that controls LoRA loading. "+strings.Repeat("Context. ", 2000))
	select {
	case got := <-titles:
		if got != [2]string{"a1", "Scan the LoRA loader"} {
			t.Fatalf("title = %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no title")
	}
	llm.mu.Lock()
	req := llm.reqs[0]
	llm.mu.Unlock()
	if req.MaxTokens != agentTitleMaxTokens || len(req.Messages[0].Content) > agentTitleTaskBytes+len(agentTitlePrompt)+16 {
		t.Fatalf("request max_tokens=%d, prompt %d bytes: want a small request", req.MaxTokens, len(req.Messages[0].Content))
	}
	if u := s.Usage(); u.TotalTokens != 46 {
		t.Fatalf("usage = %d, want the title request's 46 counted", u.TotalTokens)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(statuses) != 0 {
		t.Fatalf("title request reported statuses %v", statuses)
	}
}

// agent_options.no_titles keeps the model out of it.
func TestAgentTitleDisabled(t *testing.T) {
	llm := &titleLLM{reply: "x"}
	called := make(chan struct{}, 1)
	s := &Session{ctx: context.Background(), llm: llm, cogitoOptions: types.AgentOptions{NoTitles: true},
		callbacks: Callbacks{OnAgentTitle: func(string, string) { called <- struct{}{} }}}
	s.titleAgent("a1", "a task")
	select {
	case <-called:
		t.Fatal("titled with no_titles set")
	case <-time.After(100 * time.Millisecond):
	}
	if len(llm.reqs) != 0 {
		t.Fatal("a title request was sent")
	}
}

// A resumed agent reports running again; it is titled once.
func TestAgentTitledOnce(t *testing.T) {
	llm := &titleLLM{reply: "Scan the loader"}
	titles := make(chan string, 4)
	s := &Session{ctx: context.Background(), llm: llm, callbacks: Callbacks{
		OnAgentTitle: func(_, title string) { titles <- title },
	}}
	s.titleAgent("a1", "task")
	<-titles
	s.titleAgent("a1", "task")
	select {
	case <-titles:
		t.Fatal("titled twice")
	case <-time.After(100 * time.Millisecond):
	}
}
