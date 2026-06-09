package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// TestUndeliveredInjectHandedBack reproduces the end-of-run drain race: a
// follow-up injected while the run's FINAL LLM call is already in flight is
// never consumed (the run returns right after), and the old drain silently
// discarded it. InjectUser-delivered texts must instead be handed back via
// TakeUndelivered so the caller can re-dispatch them; plain Inject notices
// must still be dropped (re-running a stale notice would re-trigger work).
func TestUndeliveredInjectHandedBack(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	llmCalled := make(chan struct{})
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal the test that the final LLM call is in flight, then hold the
		// response until the test has injected mid-call.
		select {
		case llmCalled <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-time.After(10 * time.Second):
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "final answer"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 2, MaxRetries: 2},
	}
	session, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	done := make(chan error, 1)
	go func() {
		_, err := session.SendMessage("start")
		done <- err
	}()

	select {
	case <-llmCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("LLM was never called")
	}

	// Inject while the (final) LLM call is in flight: the loop has already
	// passed its injection select, so neither message will be consumed.
	if !session.InjectUser("whats 2+2?") {
		t.Fatal("InjectUser should succeed against a live run")
	}
	if !session.Inject("shell job 1234 completed") {
		t.Fatal("Inject should succeed against a live run")
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SendMessage did not return")
	}

	und := session.TakeUndelivered()
	if len(und) != 1 || und[0] != "whats 2+2?" {
		t.Fatalf("TakeUndelivered = %v, want exactly the user follow-up", und)
	}
	// Cleared after taking; system notices are never handed back.
	if again := session.TakeUndelivered(); len(again) != 0 {
		t.Fatalf("TakeUndelivered should clear, got %v", again)
	}
}
