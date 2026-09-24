package codex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mudler/nib/llmprovider/openairesponses"
	openai "github.com/sashabaranov/go-openai"
)

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexRetainsStreamedOutput(t *testing.T) {
	for _, tool := range []bool{false, true} {
		item := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Start a fresh session."}]}`
		if tool {
			item = `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"load_skill","arguments":"{\"name\":\"about-nib\"}"}`
		}
		for _, terminalOutput := range []string{`[]`, `[` + item + `]`} {
			stream := `data: {"type":"response.output_item.done","output_index":0,"item":` + item + "}\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":` + terminalOutput + `,"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`
			l := New(Config{Model: "test-model", Token: "test-token"})
			l.client = &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
			})}
			reply, usage, err := l.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "test-model", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "How do I clear a session?"}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(reply.ChatCompletionResponse.Choices) != 1 || usage.TotalTokens != 10 {
				t.Fatalf("bad translated response: %+v %+v", reply, usage)
			}
			msg := reply.ChatCompletionResponse.Choices[0].Message
			if tool {
				if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Function.Name != "load_skill" {
					t.Fatalf("lost or duplicated tool: %+v", msg.ToolCalls)
				}
			} else if msg.Content != "Start a fresh session." {
				t.Fatalf("lost or duplicated text: %q", msg.Content)
			}
		}
	}
}

func TestCodexPreservesTerminalFailures(t *testing.T) {
	for _, suffix := range []string{"", "\n\n"} {
		for _, test := range []struct{ event, want string }{
			{`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"try later"}}}`, "try later"},
			{`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, "max_output_tokens"},
			{`{"type":"error","code":"bad_request","message":"bad request"}`, "bad request"},
		} {
			raw, err := extractCompletedResponse([]byte("data: " + test.event + suffix))
			if err == nil {
				_, _, err = openairesponses.TranslateResponse(raw, "test-model")
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %s", err, test.want)
			}
		}
	}
}

func TestCodexRequiresTerminalEvent(t *testing.T) {
	_, err := extractCompletedResponse([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}}` + "\n\n"))
	if err == nil {
		t.Fatal("truncated stream accepted as complete")
	}
}

func TestCodexEmptyResponseStillFails(t *testing.T) {
	raw, err := extractCompletedResponse([]byte(`data: {"type":"response.completed","response":{"id":"empty","status":"completed","output":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = openairesponses.TranslateResponse(raw, "test-model")
	if !errors.Is(err, openairesponses.ErrNoResponse) {
		t.Fatalf("got %v", err)
	}
}
