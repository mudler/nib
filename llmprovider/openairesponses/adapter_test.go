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

func TestTranslateRequestReasoningOptions(t *testing.T) {
	tests := []struct {
		name, model, configModel, configEffort, requestEffort, effort, summary string
	}{
		{name: "default", model: "gpt-5", summary: "auto"},
		{name: "config model", configModel: "gpt-5.2", summary: "auto"},
		{name: "request model wins", model: "gpt-4o", configModel: "gpt-5"},
		{name: "request effort", model: "gpt-5", requestEffort: "low", effort: "low", summary: "auto"},
		{name: "config override", model: "gpt-5", configEffort: "high", requestEffort: "low", effort: "high", summary: "auto"},
		{name: "request none", model: "gpt-5", requestEffort: "none", effort: "none"},
		{name: "config none", model: "gpt-5", configEffort: "none", requestEffort: "high", effort: "none"},
		{name: "override none", model: "gpt-5", configEffort: "high", requestEffort: "none", effort: "high", summary: "auto"},
		{name: "nonreasoning", model: "gpt-4o"},
		{name: "nonreasoning explicit", model: "gpt-4o", requestEffort: "high", effort: "high"},
		{name: "o3 mini explicit", model: "o3-mini", requestEffort: "low", effort: "low"},
		{name: "chat variant", model: "gpt-5-chat-latest"},
		{name: "unknown deployment", model: "my-deployment"},
		{name: "explicit deployment", model: "my-deployment", requestEffort: "high", effort: "high", summary: "auto"},
		{name: "o3", model: "o3-2025-04-16", summary: "auto"},
		{name: "o4 mini", model: "o4-mini", summary: "auto"},
		{name: "o3 mini lacks summaries", model: "o3-mini"},
		{name: "o1 lacks summaries", model: "o1"},
		{name: "codex mini", model: "codex-mini-latest", summary: "auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := openai.ChatCompletionRequest{Model: tt.model, ReasoningEffort: tt.requestEffort}
			l := New(Config{Model: tt.configModel, ReasoningEffort: tt.configEffort})
			check := func(body []byte, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				var got responsesRequest
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatal(err)
				}
				if tt.effort == "" && tt.summary == "" {
					if got.Reasoning != nil {
						t.Fatalf("reasoning = %+v, want omitted", got.Reasoning)
					}
				} else if got.Reasoning == nil || got.Reasoning.Effort != tt.effort || got.Reasoning.Summary != tt.summary {
					t.Fatalf("reasoning = %+v, want effort %q summary %q", got.Reasoning, tt.effort, tt.summary)
				}
			}
			check(l.translateRequest(req))
			if tt.configModel == "" && tt.configEffort == "" {
				check(TranslateRequest(req))
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

func TestTranslateRequestPreservesEmptyToolOutputs(t *testing.T) {
	l := New(Config{Model: "gpt-5"})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model: "gpt-5",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "run tools"},
			{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{
				{ID: "call_empty", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "empty", Arguments: `{}`}},
				{ID: "call_value", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "value", Arguments: `{}`}},
			}},
			{Role: openai.ChatMessageRoleTool, ToolCallID: "call_empty", Content: ""},
			{Role: openai.ChatMessageRoleTool, ToolCallID: "call_value", Content: "result"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var request struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Input) != 5 {
		t.Fatalf("input count = %d, want 5", len(request.Input))
	}
	for i, raw := range request.Input {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		_, hasOutput := item["output"]
		if i < 3 && hasOutput {
			t.Errorf("input[%d] unexpectedly contains output: %s", i, raw)
		}
		if i >= 3 && !hasOutput {
			t.Errorf("input[%d] is a tool result without output: %s", i, raw)
		}
	}
}
