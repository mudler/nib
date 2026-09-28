package codex

import (
	"testing"

	"github.com/mudler/nib/llmprovider/openairesponses"
)

// The Codex backend streams output items in response.output_item.done events
// and sends response.completed with an empty output array.
const codexSSE = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[]},"output_index":0}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"},"output_index":2}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi!"}]},"output_index":1}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}}

`

func TestExtractCompletedResponseFillsOutputFromItems(t *testing.T) {
	raw, err := extractCompletedResponse([]byte(codexSSE))
	if err != nil {
		t.Fatal(err)
	}
	reply, usage, err := openairesponses.TranslateResponse(raw, "gpt-5.5")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	msg := reply.ChatCompletionResponse.Choices[0].Message
	if msg.Content != "Hi!" {
		t.Errorf("content = %q, want %q", msg.Content, "Hi!")
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "bash" {
		t.Errorf("tool calls = %+v, want one bash call", msg.ToolCalls)
	}
	if usage.TotalTokens != 13 {
		t.Errorf("total tokens = %d, want 13", usage.TotalTokens)
	}
}

func TestExtractCompletedResponseKeepsNonEmptyOutput(t *testing.T) {
	sse := `data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"stale"}]},"output_index":0}

data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final"}]}]}}

`
	raw, err := extractCompletedResponse([]byte(sse))
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := openairesponses.TranslateResponse(raw, "gpt-5.5")
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.ChatCompletionResponse.Choices[0].Message.Content; got != "final" {
		t.Errorf("content = %q, want %q", got, "final")
	}
}
