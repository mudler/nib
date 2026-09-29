package codex

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	openai "github.com/sashabaranov/go-openai"
)

// --- Reasoning controls ---

func TestReasoningEffortSetInBody(t *testing.T) {
	l := New(Config{
		Model:           "gpt-5",
		Token:           "test-token",
		ReasoningEffort: "high",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		Reasoning *struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Reasoning == nil {
		t.Fatal("reasoning should be set when ReasoningEffort is configured")
	}
	if cr.Reasoning.Effort != "high" {
		t.Fatalf("effort = %q, want high", cr.Reasoning.Effort)
	}
	if cr.Reasoning.Summary != "auto" {
		t.Fatalf("summary = %q, want auto", cr.Reasoning.Summary)
	}
}

func TestReasoningSummaryRequestedWithoutEffort(t *testing.T) {
	l := New(Config{
		Model: "gpt-5",
		Token: "test-token",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		Reasoning *struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	// No effort configured still asks for the summary, or the backend sends
	// no readable reasoning; the effort is left to the model's default.
	if cr.Reasoning == nil || cr.Reasoning.Summary != "auto" || cr.Reasoning.Effort != "" {
		t.Fatalf("reasoning = %+v, want summary auto and no effort", cr.Reasoning)
	}
}

func TestReasoningLiteForcesAllTurnsContext(t *testing.T) {
	l := New(Config{
		Model:         "gpt-5",
		Token:         "test-token",
		ResponsesLite: true,
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		Reasoning *struct {
			Context string `json:"context"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Reasoning == nil {
		t.Fatal("reasoning should be set when Lite is enabled")
	}
	if cr.Reasoning.Context != "all_turns" {
		t.Fatalf("context = %q, want all_turns", cr.Reasoning.Context)
	}
}

// --- Service tier ---

func TestServiceTierSetWhenNonAuto(t *testing.T) {
	l := New(Config{
		Model:       "gpt-5",
		Token:       "test-token",
		ServiceTier: "priority",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		ServiceTier string `json:"service_tier"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.ServiceTier != "priority" {
		t.Fatalf("service_tier = %q, want priority", cr.ServiceTier)
	}
}

func TestServiceTierOmittedWhenAuto(t *testing.T) {
	l := New(Config{
		Model:       "gpt-5",
		Token:       "test-token",
		ServiceTier: "auto",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		ServiceTier *string `json:"service_tier"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.ServiceTier != nil {
		t.Fatalf("service_tier should be omitted for auto, got %q", *cr.ServiceTier)
	}
}

func TestRoutingHintHeaderWithTier(t *testing.T) {
	l := New(Config{
		Model:       "gpt-5",
		Token:       "test-token",
		ServiceTier: "priority",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "https://example.com", nil)
	l.setHeaders(req, meta)

	hint := req.Header.Get(headerRoutingHint)
	if !strings.Contains(hint, "model=gpt-5") {
		t.Fatalf("routing hint missing model: %q", hint)
	}
	if !strings.Contains(hint, "tier=priority") {
		t.Fatalf("routing hint missing tier: %q", hint)
	}
}

func TestRoutingHintHeaderWithoutTier(t *testing.T) {
	l := New(Config{
		Model: "gpt-5",
		Token: "test-token",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "https://example.com", nil)
	l.setHeaders(req, meta)

	hint := req.Header.Get(headerRoutingHint)
	if !strings.Contains(hint, "model=gpt-5") {
		t.Fatalf("routing hint missing model: %q", hint)
	}
	if strings.Contains(hint, "tier=") {
		t.Fatalf("routing hint should not contain tier when not set: %q", hint)
	}
}

// --- Prompt cache key ---

func TestPromptCacheKeySetFromSessionID(t *testing.T) {
	l := New(Config{
		Model: "gpt-5",
		Token: "test-token",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.PromptCacheKey != meta.SessionID {
		t.Fatalf("prompt_cache_key = %q, want %q", cr.PromptCacheKey, meta.SessionID)
	}
}

// --- Responses Lite body shaping ---

func TestApplyResponsesLiteShape(t *testing.T) {
	tools := []codexTool{
		{Type: "function", Name: "run", Parameters: map[string]any{"type": "object"}},
	}
	input := []codexInput{
		{Role: "user", Content: "hello"},
	}
	cr := codexRequest{
		Model:        "gpt-5",
		Input:        input,
		Instructions: "be helpful",
		Tools:        tools,
		Stream:       true,
	}

	applyResponsesLiteShape(&cr)

	if cr.Instructions != "" {
		t.Fatal("instructions should be cleared")
	}
	if cr.Tools != nil {
		t.Fatal("tools should be cleared")
	}
	if cr.ParallelToolCalls == nil || *cr.ParallelToolCalls != false {
		t.Fatal("parallel_tool_calls should be false")
	}
	if len(cr.Input) != 3 {
		t.Fatalf("expected 3 input items (additional_tools + developer message + user), got %d", len(cr.Input))
	}
	if cr.Input[0].Type != "additional_tools" || cr.Input[0].Role != "developer" {
		t.Fatalf("first input should be additional_tools/developer, got %s/%s", cr.Input[0].Type, cr.Input[0].Role)
	}
	if len(cr.Input[0].Tools) != 1 {
		t.Fatalf("additional_tools should have 1 tool, got %d", len(cr.Input[0].Tools))
	}
	if cr.Input[1].Type != "message" || cr.Input[1].Role != "developer" || cr.Input[1].Content != "be helpful" {
		t.Fatalf("second input should be developer message, got %+v", cr.Input[1])
	}
	if cr.Input[2].Role != "user" {
		t.Fatalf("third input should be user message, got role %s", cr.Input[2].Role)
	}
}

func TestApplyResponsesLiteShapeNoTools(t *testing.T) {
	input := []codexInput{
		{Role: "user", Content: "hello"},
	}
	cr := codexRequest{
		Model:        "gpt-5",
		Input:        input,
		Instructions: "be helpful",
		Stream:       true,
	}

	applyResponsesLiteShape(&cr)

	if len(cr.Input) != 2 {
		t.Fatalf("expected 2 input items (developer message + user), got %d", len(cr.Input))
	}
	if cr.Input[0].Type != "message" || cr.Input[0].Role != "developer" {
		t.Fatalf("first input should be developer message, got %s/%s", cr.Input[0].Type, cr.Input[0].Role)
	}
}

func TestApplyResponsesLiteShapeNoInstructions(t *testing.T) {
	tools := []codexTool{
		{Type: "function", Name: "run", Parameters: map[string]any{"type": "object"}},
	}
	input := []codexInput{
		{Role: "user", Content: "hello"},
	}
	cr := codexRequest{
		Model:  "gpt-5",
		Input:  input,
		Tools:  tools,
		Stream: true,
	}

	applyResponsesLiteShape(&cr)

	if len(cr.Input) != 2 {
		t.Fatalf("expected 2 input items (additional_tools + user), got %d", len(cr.Input))
	}
	if cr.Input[0].Type != "additional_tools" {
		t.Fatalf("first input should be additional_tools, got %s", cr.Input[0].Type)
	}
	if cr.Input[1].Role != "user" {
		t.Fatalf("second input should be user, got %s", cr.Input[1].Role)
	}
}

func TestResponsesLiteHeaderSet(t *testing.T) {
	l := New(Config{
		Model:         "gpt-5",
		Token:         "test-token",
		ResponsesLite: true,
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "https://example.com", nil)
	l.setHeaders(req, meta)

	if req.Header.Get(headerResponsesLite) != "true" {
		t.Fatalf("responses-lite header should be 'true', got %q", req.Header.Get(headerResponsesLite))
	}
}

func TestResponsesLiteHeaderOmittedWhenDisabled(t *testing.T) {
	l := New(Config{
		Model: "gpt-5",
		Token: "test-token",
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "https://example.com", nil)
	l.setHeaders(req, meta)

	if req.Header.Get(headerResponsesLite) != "" {
		t.Fatalf("responses-lite header should be omitted, got %q", req.Header.Get(headerResponsesLite))
	}
}

// --- zstd compression ---

func TestCompressZstdRoundTrip(t *testing.T) {
	original := []byte(`{"model":"gpt-5","input":[{"role":"user","content":"hello world"}],"stream":true}`)
	compressed := compressZstd(original)
	if compressed == nil {
		t.Fatal("compressZstd returned nil")
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	decompressed, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("decompression failed: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("round-trip mismatch:\n  want: %s\n  got:  %s", original, decompressed)
	}
}

func TestCompressZstdProducesValidData(t *testing.T) {
	original := []byte("test data for zstd compression 12345")
	compressed := compressZstd(original)
	if compressed == nil {
		t.Fatal("compressZstd returned nil")
	}
	if bytes.Equal(compressed, original) {
		t.Fatal("compressed data should differ from original")
	}

	// zstd magic number: 0x28 0xB5 0x2F 0xFD
	if len(compressed) < 4 || compressed[0] != 0x28 || compressed[1] != 0xB5 {
		t.Fatalf("compressed data doesn't start with zstd magic number: % x", compressed[:4])
	}
}

func TestIsOfficialCodexURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://chatgpt.com/backend-api", true},
		{"https://chatgpt.com/backend-api/codex/responses", true},
		{"https://api.openai.com/v1/responses", false},
		{"https://example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isOfficialCodexURL(tt.url); got != tt.want {
			t.Errorf("isOfficialCodexURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// --- Integration: full request body shape ---

func TestFullRequestBodyWithAllFeatures(t *testing.T) {
	l := New(Config{
		Model:           "gpt-5",
		Token:           "test-token",
		ReasoningEffort: "medium",
		ServiceTier:     "priority",
		ResponsesLite:   true,
	})
	meta := l.session.prepareTurn([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})

	tools := []openai.Tool{{
		Function: &openai.FunctionDefinition{
			Name:       "run",
			Parameters: map[string]any{"type": "object"},
		},
	}}
	body, err := l.translateRequest(openai.ChatCompletionRequest{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
		Tools:    tools,
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	var cr struct {
		Reasoning *struct {
			Effort  string `json:"effort"`
			Context string `json:"context"`
		} `json:"reasoning"`
		ServiceTier       string `json:"service_tier"`
		PromptCacheKey    string `json:"prompt_cache_key"`
		Instructions      string `json:"instructions"`
		Tools             []any  `json:"tools"`
		ParallelToolCalls *bool  `json:"parallel_tool_calls"`
		Input             []struct {
			Type string `json:"type"`
			Role string `json:"role"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		t.Fatal(err)
	}

	if cr.Reasoning == nil || cr.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning.effort wrong: %+v", cr.Reasoning)
	}
	if cr.Reasoning.Context != "all_turns" {
		t.Fatalf("reasoning.context = %q, want all_turns", cr.Reasoning.Context)
	}
	if cr.ServiceTier != "priority" {
		t.Fatalf("service_tier = %q, want priority", cr.ServiceTier)
	}
	if cr.PromptCacheKey == "" {
		t.Fatal("prompt_cache_key should be set")
	}
	if cr.Instructions != "" {
		t.Fatal("instructions should be cleared in Lite mode")
	}
	if cr.Tools != nil {
		t.Fatal("tools should be cleared in Lite mode")
	}
	if cr.ParallelToolCalls == nil || *cr.ParallelToolCalls != false {
		t.Fatal("parallel_tool_calls should be false in Lite mode")
	}
	if len(cr.Input) < 2 {
		t.Fatalf("input should have at least 2 items in Lite mode, got %d", len(cr.Input))
	}
	if cr.Input[0].Type != "additional_tools" {
		t.Fatalf("first input should be additional_tools, got %s", cr.Input[0].Type)
	}
}
