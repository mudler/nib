package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// capturedMessage is the subset of an OpenAI request message the history tests
// assert on (role + content is enough to prove prior turns reached the model).
type capturedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messageCapturingOpenAI is a minimal non-streaming OpenAI-compatible endpoint
// that records the "messages" array of every request and always replies with a
// plain stop message, so each turn ends in a single call. record is invoked with
// the messages of each request in order.
func messageCapturingOpenAI(record func([]capturedMessage)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []capturedMessage `json:"messages"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)
		record(req.Messages)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}
}

// TestExportHistoryReturnsCopy proves ExportHistory returns the full
// conversation and that the returned slice is a copy: mutating it (or appending
// to it) does not disturb the live session's history.
func TestExportHistoryReturnsCopy(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(messageCapturingOpenAI(func([]capturedMessage) {}))
	defer srv.Close()

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
	}

	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	exported := session.ExportHistory()
	// The turn recorded a user message and the assistant reply.
	if len(exported) != 2 {
		t.Fatalf("ExportHistory len = %d, want 2 (%+v)", len(exported), exported)
	}
	if exported[0].Role != "user" || exported[0].Content != "hello" {
		t.Fatalf("exported[0] = %+v, want user/hello", exported[0])
	}
	if exported[1].Role != "assistant" || exported[1].Content != "ok" {
		t.Fatalf("exported[1] = %+v, want assistant/ok", exported[1])
	}
	// No system prompt is ever exported (none was configured, and s.messages
	// never records the system message regardless).
	for _, m := range exported {
		if m.Role == "system" {
			t.Fatalf("ExportHistory must not include a system message, got %+v", exported)
		}
	}

	// Mutating the returned slice must not affect the session.
	exported[0].Content = "TAMPERED"
	exported = append(exported, openai.ChatCompletionMessage{Role: "user", Content: "extra"})

	again := session.ExportHistory()
	if len(again) != 2 {
		t.Fatalf("second ExportHistory len = %d, want 2 (copy leaked?)", len(again))
	}
	if again[0].Content != "hello" {
		t.Fatalf("session history mutated through the exported copy: %+v", again)
	}
}

// TestSeededHistoryReachesModel proves that a session constructed with
// InitialHistory continues the prior conversation: the seeded turns are present
// in the model context of the very first resumed request.
func TestSeededHistoryReachesModel(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last []capturedMessage
	srv := httptest.NewServer(messageCapturingOpenAI(func(m []capturedMessage) {
		mu.Lock()
		last = m
		mu.Unlock()
	}))
	defer srv.Close()

	prior := []openai.ChatCompletionMessage{
		{Role: "user", Content: "my favorite color is amber"},
		{Role: "assistant", Content: "noted, amber it is"},
	}

	cfg := types.Config{
		Model:          "fake-model",
		APIKey:         "fake-key",
		BaseURL:        srv.URL + "/v1",
		LogLevel:       "error",
		ApprovalMode:   "auto",
		AgentOptions:   types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		InitialHistory: prior,
	}

	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("what is my favorite color?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	got := last
	mu.Unlock()

	// The first resumed request must carry the prior turns AND the new one, in
	// order, so the model has full memory of the conversation.
	want := []capturedMessage{
		{Role: "user", Content: "my favorite color is amber"},
		{Role: "assistant", Content: "noted, amber it is"},
		{Role: "user", Content: "what is my favorite color?"},
	}
	if len(got) != len(want) {
		t.Fatalf("request messages = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("request message[%d] = %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}

	// ExportHistory of the resumed session includes the seed + the new turn +
	// the assistant reply — a full round-trip.
	exported := session.ExportHistory()
	if len(exported) != 4 {
		t.Fatalf("ExportHistory after resume = %d msgs, want 4 (%+v)", len(exported), exported)
	}
}

// TestSeededHistorySystemPromptExactlyOnce is the correctness guard for the
// resumed-turn system prompt: with a system prompt configured and history
// seeded, the first resumed request must carry the system prompt EXACTLY ONCE
// (no duplication from baking it into the seed, no omission), at the front, with
// the prior turns following it. This exercises the "seed the fragment WITHOUT
// the system message and let SendMessage add it" decision documented in
// NewSession.
func TestSeededHistorySystemPromptExactlyOnce(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last []capturedMessage
	srv := httptest.NewServer(messageCapturingOpenAI(func(m []capturedMessage) {
		mu.Lock()
		last = m
		mu.Unlock()
	}))
	defer srv.Close()

	const systemPrompt = "You are Dante, a precise local assistant."
	prior := []openai.ChatCompletionMessage{
		{Role: "user", Content: "hi there"},
		{Role: "assistant", Content: "hello"},
	}

	cfg := types.Config{
		Model:          "fake-model",
		APIKey:         "fake-key",
		BaseURL:        srv.URL + "/v1",
		LogLevel:       "error",
		ApprovalMode:   "auto",
		Prompt:         systemPrompt,
		AgentOptions:   types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		InitialHistory: prior,
	}

	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("still there?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	got := last
	mu.Unlock()

	// Exactly one system message, and it must carry the prompt exactly once.
	// (cogito consolidates system messages to the front; a system message baked
	// into the seed would have doubled the content here.)
	var systemCount int
	var systemContent string
	for _, m := range got {
		if m.Role == "system" {
			systemCount++
			systemContent = m.Content
		}
	}
	if systemCount != 1 {
		t.Fatalf("system message count = %d, want exactly 1 (msgs: %+v)", systemCount, got)
	}
	// The base prompt must be present and NOT duplicated (baking a system message
	// into the seed would have joined it twice). The prompt builder may append
	// extra guidance, so assert the base prompt is a prefix and appears once.
	if !strings.HasPrefix(systemContent, systemPrompt) {
		t.Fatalf("system content = %q, want prefix %q (omitted?)", systemContent, systemPrompt)
	}
	if n := strings.Count(systemContent, systemPrompt); n != 1 {
		t.Fatalf("base prompt appears %d times in system message, want exactly 1 (duplicated?): %q", n, systemContent)
	}
	if got[0].Role != "system" {
		t.Fatalf("system message must be first, got %+v", got)
	}

	// The prior turns and the new user message follow the system prompt in order.
	wantTail := []capturedMessage{
		{Role: "user", Content: "hi there"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "still there?"},
	}
	tail := got[1:]
	if len(tail) != len(wantTail) {
		t.Fatalf("non-system messages = %+v, want %+v", tail, wantTail)
	}
	for i := range wantTail {
		if tail[i].Role != wantTail[i].Role || tail[i].Content != wantTail[i].Content {
			t.Fatalf("message[%d] after system = %+v, want %+v (full: %+v)", i, tail[i], wantTail[i], got)
		}
	}
}
