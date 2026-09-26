package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// proxiedLLM stands in for a backend behind a proxy that cuts a response off
// when no byte has arrived in time (Cloudflare's 524): a blocking request that
// has to process a whole conversation fails, a streamed one gets through.
type proxiedLLM struct {
	blocking, streamed int
	events             []cogito.StreamEvent
}

func (p *proxiedLLM) CreateChatCompletion(context.Context, openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	p.blocking++
	return cogito.LLMReply{}, cogito.LLMUsage{}, errors.New("status code: 524, A timeout occurred")
}

func (p *proxiedLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f, errors.New("not used")
}

func (p *proxiedLLM) CreateChatCompletionStream(_ context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	p.streamed++
	ch := make(chan cogito.StreamEvent, len(p.events))
	for _, ev := range p.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// A session whose turns stream sends the summary streamed too. Summarizing a
// whole conversation is the slowest request nib makes, and the one a proxy
// with a first-byte timeout kills when it is sent blocking: every automatic
// compaction then failed, and the conversation grew past the trigger.
func TestSummaryStreamsWhenTurnsStream(t *testing.T) {
	llm := &proxiedLLM{events: []cogito.StreamEvent{
		{Type: cogito.StreamEventReasoning, Content: "thinking about it"},
		{Type: cogito.StreamEventContent, Content: "SUMMARY "},
		{Type: cogito.StreamEventContent, Content: "TEXT"},
		{Type: cogito.StreamEventDone, Usage: cogito.LLMUsage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105}},
	}}
	s := &Session{llm: llm, callbacks: Callbacks{OnStream: func(StreamEvent) {}}}

	got, _, err := s.summarize(context.Background(), []summaryPiece{{text: "user: hello"}}, 0)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got != "SUMMARY TEXT" {
		t.Fatalf("summary = %q, want the streamed content without the reasoning", got)
	}
	if llm.blocking != 0 || llm.streamed != 1 {
		t.Fatalf("blocking=%d streamed=%d, want one streamed request", llm.blocking, llm.streamed)
	}
	if u := s.Usage(); u.TotalTokens != 105 {
		t.Fatalf("usage total = %d, want the streamed request's 105 counted", u.TotalTokens)
	}
}

// A stream that ends in an error fails the summary, the same as a blocking
// request that returns one.
func TestStreamedSummaryError(t *testing.T) {
	llm := &proxiedLLM{events: []cogito.StreamEvent{
		{Type: cogito.StreamEventContent, Content: "partial"},
		{Type: cogito.StreamEventError, Error: errors.New("connection reset")},
	}}
	s := &Session{llm: llm, callbacks: Callbacks{OnStream: func(StreamEvent) {}}}

	if _, _, err := s.summarize(context.Background(), []summaryPiece{{text: "user: hello"}}, 0); err == nil {
		t.Fatal("summarize succeeded on a stream that failed")
	}
}
