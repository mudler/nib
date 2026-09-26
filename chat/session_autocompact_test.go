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
)

// autoCompactOpenAI is a minimal OpenAI-compatible endpoint that always replies
// with a plain stop message and reports the request's prompt tokens as ratio
// times its byte/4 size (messages and tool schemas): a backend whose tokenizer
// counts more than the estimate, as real ones do.
func autoCompactOpenAI(ratio float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)
		n := len(req.Tools)
		for _, m := range req.Messages {
			n += len(m.Content)
		}
		promptTokens := int(float64(n/4) * ratio)

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
// counts 1.5x the byte/4 estimate, with a large history and a large reserve,
// and proves the OnCompactDone callback fires. The tokenizer skew on the
// history must not be taken for a tool-schema floor that blocks compaction.
func TestSessionAutoCompacts(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(autoCompactOpenAI(1.5))
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
			// Budget 20000 (window less half of it), trigger 16000. The
			// history below is 40000 byte/4 tokens, 60000 as counted: the
			// skew alone on it is 20000, the whole budget.
			MaxContextTokens: 40000,
			ReserveTokens:    20000,
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

	if _, err := session.SendMessage(strings.Repeat("earlier detail ", 40000*4/15)); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
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
