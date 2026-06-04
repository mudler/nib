package trace

import (
	"context"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// fakeStreamingLLM implements cogito.StreamingLLM by embedding fakeLLM and
// emitting a fixed event sequence.
type fakeStreamingLLM struct {
	fakeLLM
	events []cogito.StreamEvent
}

func (f *fakeStreamingLLM) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	ch := make(chan cogito.StreamEvent)
	go func() {
		defer close(ch)
		for _, ev := range f.events {
			ch <- ev
		}
	}()
	return ch, nil
}

func TestNewRecordingLLMStreamingIsTransparent(t *testing.T) {
	llm := NewRecordingLLM(&fakeStreamingLLM{}, nil, "m", "")
	if _, ok := llm.(cogito.StreamingLLM); !ok {
		t.Fatal("wrapping a StreamingLLM must yield a StreamingLLM")
	}
}

func TestRecordingStreamingLLMForwardsAndRecords(t *testing.T) {
	dir := t.TempDir()
	rec, _ := NewRecorder(dir)
	defer rec.Close()

	inner := &fakeStreamingLLM{events: []cogito.StreamEvent{
		{Type: cogito.StreamEventContent, Content: "hel"},
		{Type: cogito.StreamEventContent, Content: "lo"},
		{Type: cogito.StreamEventToolCall, ToolCallIndex: 0, ToolCallID: "c1", ToolName: "bash", ToolArgs: `{"script":"ls"}`},
		{Type: cogito.StreamEventDone, FinishReason: "tool_calls", Usage: cogito.LLMUsage{TotalTokens: 9}},
	}}

	llm := NewRecordingLLM(inner, rec, "cfg-model", "").(cogito.StreamingLLM)
	ch, err := llm.CreateChatCompletionStream(context.Background(), openai.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("CreateChatCompletionStream: %v", err)
	}

	var forwarded int
	for range ch {
		forwarded++
	}
	if forwarded != 4 {
		t.Fatalf("forwarded %d events, want 4", forwarded)
	}

	recs := readRecords(t, dir)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.Method != "stream" {
		t.Fatalf("method = %q", r.Method)
	}
	if r.Response == nil {
		t.Fatal("no response recorded")
	}
	msg := r.Response.Choices[0].Message
	if msg.Content != "hello" {
		t.Fatalf("reassembled content = %q, want hello", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("reassembled tool calls wrong: %+v", msg.ToolCalls)
	}
	if r.Response.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %+v", r.Response.Usage)
	}
}
