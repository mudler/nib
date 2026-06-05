package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// reasoningEffortCapturingOpenAI records the "reasoning_effort" of every request
// and replies with a single stop message (streaming + non-streaming paths).
func reasoningEffortCapturingOpenAI(record func(string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream          bool   `json:"stream"`
			ReasoningEffort string `json:"reasoning_effort"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		record(req.ReasoningEffort)

		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "fake", "object": "chat.completion", "model": "fake",
				"choices": []any{map[string]any{
					"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				}},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(delta map[string]any, finish string) {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			b, _ := json.Marshal(map[string]any{
				"id": "fake", "object": "chat.completion.chunk", "model": "fake",
				"choices": []any{choice},
			})
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		emit(map[string]any{"content": "ok"}, "")
		emit(map[string]any{}, "stop")
		w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}
}

// TestSessionSendsConfiguredReasoningEffort proves Config.ReasoningEffort reaches
// the wire as the OpenAI "reasoning_effort" field — the lever that disables a
// reasoning model's thinking when its template has no enable_thinking toggle.
func TestSessionSendsConfiguredReasoningEffort(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last string
	srv := httptest.NewServer(reasoningEffortCapturingOpenAI(func(s string) {
		mu.Lock()
		last = s
		mu.Unlock()
	}))
	defer srv.Close()

	cfg := types.Config{
		Model:           "fake-model",
		APIKey:          "fake-key",
		BaseURL:         srv.URL + "/v1",
		LogLevel:        "error",
		ApprovalMode:    "auto",
		AgentOptions:    types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		ReasoningEffort: "none",
	}

	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	got := last
	mu.Unlock()
	if got != "none" {
		t.Fatalf("request reasoning_effort = %q, want none", got)
	}
}
