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

// metadataCapturingOpenAI is a minimal OpenAI-compatible endpoint that records
// the "metadata" object of every request it receives and always replies with a
// plain stop message so each turn ends in a single call. It handles both the
// streaming and non-streaming paths.
func metadataCapturingOpenAI(record func(map[string]string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool              `json:"stream"`
			Metadata map[string]string `json:"metadata"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)
		record(req.Metadata)

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

// TestSessionSendsConfiguredMetadata drives a real chat.Session against a fake
// LLM and proves the configured per-request metadata reaches the wire as the
// OpenAI "metadata" object on the main session's requests.
func TestSessionSendsConfiguredMetadata(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last map[string]string
	srv := httptest.NewServer(metadataCapturingOpenAI(func(m map[string]string) {
		mu.Lock()
		last = m
		mu.Unlock()
	}))
	defer srv.Close()

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		Metadata:     map[string]string{"enable_thinking": "false"},
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
	if got["enable_thinking"] != "false" {
		t.Fatalf("request metadata = %v, want enable_thinking=false", got)
	}
}

// TestSessionSendsNoMetadataByDefault proves that with no metadata configured,
// no "metadata" object is attached (the field stays omitted).
func TestSessionSendsNoMetadataByDefault(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var seen bool
	var last map[string]string
	srv := httptest.NewServer(metadataCapturingOpenAI(func(m map[string]string) {
		mu.Lock()
		seen = true
		last = m
		mu.Unlock()
	}))
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

	if _, err := session.SendMessage("hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !seen {
		t.Fatal("server received no request")
	}
	if len(last) != 0 {
		t.Fatalf("expected no metadata, got %v", last)
	}
}
