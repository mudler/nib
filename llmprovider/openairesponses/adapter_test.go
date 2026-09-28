package openairesponses

import (
	"encoding/json"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestTranslateRequestIncludesReasoningSummary(t *testing.T) {
	l := New(Config{Model: "gpt-5", ReasoningEffort: "high"})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got responsesRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "high" || got.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning = %+v, want effort high and summary auto", got.Reasoning)
	}
}

func TestTranslateRequestOmitsDisabledReasoning(t *testing.T) {
	for _, effort := range []string{"", "none"} {
		t.Run(effort, func(t *testing.T) {
			l := New(Config{Model: "gpt-5", ReasoningEffort: effort})
			body, err := l.translateRequest(openai.ChatCompletionRequest{
				Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var got responsesRequest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Reasoning != nil {
				t.Fatalf("reasoning = %+v, want omitted", got.Reasoning)
			}
		})
	}
}

func TestTranslateResponseSurfacesReasoningSummariesOnly(t *testing.T) {
	body := []byte(`{
		"id":"resp_1","model":"gpt-5","status":"completed",
		"output":[
			{"type":"reasoning","summary":[
				{"type":"summary_text","text":"First part."},
				{"type":"other","text":"ignore me"},
				{"type":"summary_text","text":"Second part."}
			],"content":[{"type":"reasoning_text","text":"private raw trace"}],"encrypted_content":"opaque-secret"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}
		],
		"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
	}`)
	reply, _, err := TranslateResponse(body, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ReasoningContent != "First part.\nSecond part." {
		t.Fatalf("reasoning = %q", reply.ReasoningContent)
	}
	if strings.Contains(reply.ReasoningContent, "private") || strings.Contains(reply.ReasoningContent, "opaque") {
		t.Fatalf("reasoning exposed hidden content: %q", reply.ReasoningContent)
	}
	if got := reply.ChatCompletionResponse.Choices[0].Message.Content; got != "Done" {
		t.Fatalf("content = %q, want Done", got)
	}
}

func TestTranslateResponseWithoutReasoning(t *testing.T) {
	body := []byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}]}`)
	reply, _, err := TranslateResponse(body, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ReasoningContent != "" {
		t.Fatalf("reasoning = %q, want empty", reply.ReasoningContent)
	}
}
