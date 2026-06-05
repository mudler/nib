package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// autoCompactOpenAI is a minimal OpenAI-compatible endpoint that always replies
// with a plain stop message and reports a fixed, high prompt-token usage so the
// auto-compaction threshold is deterministically crossed after a single turn.
func autoCompactOpenAI(promptTokens int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)

		usage := map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": 1,
			"total_tokens":      promptTokens + 1,
		}

		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "fake", "object": "chat.completion", "model": "fake",
				"choices": []any{map[string]any{
					"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				}},
				"usage": usage,
			})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(payload map[string]any) {
			b, _ := json.Marshal(payload)
			w.Write([]byte("data: "))
			w.Write(b)
			w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		emit(map[string]any{
			"id": "fake", "object": "chat.completion.chunk", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "ok"}}},
		})
		emit(map[string]any{
			"id": "fake", "object": "chat.completion.chunk", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   usage,
		})
		w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}
}

// TestSessionAutoCompacts drives a real chat.Session against a fake LLM that
// reports a prompt-token usage above the configured threshold, and proves the
// OnCompactDone callback fires after the turn.
func TestSessionAutoCompacts(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(autoCompactOpenAI(1000))
	defer srv.Close()

	var mu sync.Mutex
	var called bool
	var gotBefore, gotAfter int

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		Compaction: types.CompactionConfig{
			MaxContextTokens: 100, // limit = 80 with default 0.8 threshold; 1000 >> 80
			KeepRecent:       0,
		},
	}

	cb := chat.Callbacks{
		OnCompactDone: func(before, after int) {
			mu.Lock()
			called = true
			gotBefore = before
			gotAfter = after
			mu.Unlock()
		},
	}

	session, err := chat.NewSession(context.Background(), cfg, cb)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("OnCompactDone was not called; auto-compaction did not fire")
	}
	if gotBefore == gotAfter {
		t.Fatalf("expected before != after, got before=%d after=%d", gotBefore, gotAfter)
	}
}
