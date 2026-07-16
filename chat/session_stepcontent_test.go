package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// TestStepContentReachesCallbackBeforeToolResult verifies the OnStepContent
// wiring: the assistant commentary that accompanies a tool call ("Let me
// confirm something first.") must reach OnStepContent at the step boundary,
// BEFORE that step's OnToolResult — so a UI can commit the commentary in
// chronological order (text → tool result → final answer) instead of losing
// it. Before cogito's WithStepContentCallback, this text was dropped outright.
func TestStepContentReachesCallbackBeforeToolResult(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	const stepText = "Let me confirm something first."

	var reqN int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := map[string]any{"role": "assistant"}
		finish := "stop"
		if atomic.AddInt64(&reqN, 1) == 1 {
			// Step 1: commentary alongside an ask_user tool call.
			msg["content"] = stepText
			msg["tool_calls"] = []any{map[string]any{
				"id": "call_ask", "type": "function", "index": 0,
				"function": map[string]any{"name": "ask_user", "arguments": `{"question":"proceed?"}`},
			}}
			finish = "tool_calls"
		} else {
			// Step 2: text reply ends the turn.
			msg["content"] = "done"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		})
	}))
	defer srv.Close()

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{
			Iterations:  10,
			MaxAttempts: 3,
			MaxRetries:  3,
		},
	}

	// events records the cross-callback order the UI would see.
	var mu chanlessMutexSlice
	session, err := NewSession(context.Background(), cfg, Callbacks{
		OnStepContent: func(content string) { mu.append("step:" + content) },
		OnToolResult:  func(res ToolResult) { mu.append("toolresult:" + res.Name) },
		OnAskUser:     func(req AskRequest) string { return "yes" },
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	type sendResult struct {
		response string
		err      error
	}
	doneCh := make(chan sendResult, 1)
	go func() {
		resp, err := session.SendMessage("start")
		doneCh <- sendResult{resp, err}
	}()

	select {
	case res := <-doneCh:
		if res.err != nil {
			t.Fatalf("SendMessage: %v", res.err)
		}
		if res.response != "done" {
			t.Fatalf("final response = %q, want %q", res.response, "done")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not finish")
	}

	want := []string{"step:" + stepText, "toolresult:ask_user"}
	got := mu.snapshot()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// chanlessMutexSlice is a tiny thread-safe append-only string slice: callbacks
// fire on the agent goroutine while the test asserts on its own.
type chanlessMutexSlice struct {
	mu sync.Mutex
	s  []string
}

func (c *chanlessMutexSlice) append(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s = append(c.s, v)
}

func (c *chanlessMutexSlice) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.s...)
}
