package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// The output reservation is sized from the prompt as the backend counts it:
// the byte/4 estimate times the calibrated tokenizer ratio. Sized from the
// raw estimate, a backend that counts 1.5x the estimate got 16384 output
// tokens asked of a window with 168 left.
func TestClampOutputUsesTheCalibratedRatio(t *testing.T) {
	got := clampOutputTokensSized(promptOf(64000), 16384, 100000, 0, 1.5)
	if want := 100000 - 96000 - outputSafetyMargin; got.MaxTokens != want {
		t.Fatalf("MaxTokens = %d, want %d", got.MaxTokens, want)
	}
}

// countingLLM records whether a request reached the backend.
type countingLLM struct {
	calls int
}

func (c *countingLLM) CreateChatCompletion(context.Context, openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	c.calls++
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "ok"}}},
	}}, cogito.LLMUsage{}, nil
}

func (c *countingLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f, nil
}

func (c *countingLLM) CreateChatCompletionStream(context.Context, openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	c.calls++
	ch := make(chan cogito.StreamEvent)
	close(ch)
	return ch, nil
}

// turnRequest is a request that carries a tool, as a turn's requests do, with
// an estimated prompt of about tokens.
func turnRequest(tokens int) openai.ChatCompletionRequest {
	req := promptOf(tokens)
	req.Tools = []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "read"}}}
	return req
}

// A request that would leave the model less than minOutputTokens of the
// window is not sent: the backend may accept it and cut the reply off
// mid-tool-call, which reads as invalid arguments. It fails as a context
// overflow instead, which starts the overflow recovery.
func TestStarvedRequestFailsAsAnOverflow(t *testing.T) {
	for _, stream := range []bool{false, true} {
		inner := &countingLLM{}
		live := &liveUsage{}
		live.setRatio(1.5)
		llm := trackUsage(inner, live, func() (int, int) { return 16384, 100000 })

		var err error
		if stream {
			_, err = llm.(cogito.StreamingLLM).CreateChatCompletionStream(context.Background(), turnRequest(66000))
		} else {
			_, _, err = llm.CreateChatCompletion(context.Background(), turnRequest(66000))
		}
		if !isWindowOverflow(err) {
			t.Fatalf("stream=%v: err = %v, want a context overflow", stream, err)
		}
		var full *contextFullError
		if !errors.As(err, &full) || full.Window != 100000 || full.Prompt < 99000 {
			t.Fatalf("stream=%v: err = %#v, want the figures", stream, err)
		}
		if inner.calls != 0 {
			t.Fatalf("stream=%v: the starved request reached the backend", stream)
		}
		if !strings.Contains(err.Error(), "100000") {
			t.Fatalf("stream=%v: err %q does not name the window", stream, err)
		}
	}
}

// The same request goes out when the backend counts what the estimate says.
func TestRequestWithRoomIsSent(t *testing.T) {
	inner := &countingLLM{}
	llm := trackUsage(inner, &liveUsage{}, func() (int, int) { return 16384, 100000 })
	if _, _, err := llm.CreateChatCompletion(context.Background(), turnRequest(66000)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if inner.calls != 1 {
		t.Fatal("the request was not sent")
	}
}
