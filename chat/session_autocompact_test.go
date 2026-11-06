package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// autoCompactOpenAI is a minimal OpenAI-compatible endpoint that always replies
// with a plain stop message and reports the request's prompt tokens as ratio
// times its byte/4 size (messages and tool schemas): a backend whose tokenizer
// counts more than the estimate, as real ones do. It stores the byte size of
// the largest tool list it saw in toolBytes when that is not nil.
func autoCompactOpenAI(ratio float64, toolBytes *atomic.Int64) http.HandlerFunc {
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
		if toolBytes != nil && int64(n) > toolBytes.Load() {
			toolBytes.Store(int64(n))
		}
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

const (
	// The window, reserve and minimum reply of the fixture below. The minimum
	// reply is chat's minOutputTokens: a request that leaves the model less
	// room than that is held back.
	autoCompactWindow  = 46000
	autoCompactReserve = 23000
	autoCompactMinOut  = 1024
)

func autoCompactConfig(baseURL string) types.Config {
	return types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      baseURL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		Compaction: types.CompactionConfig{
			MaxContextTokens: autoCompactWindow,
			ReserveTokens:    autoCompactReserve,
			KeepRecent:       0,
		},
	}
}

// autoCompactHistory returns the size, in byte/4 tokens, of the history the
// test sends first. It derives the size from the byte/4 size of the session's
// real tool schemas, so adding a tool does not move the test out of range.
//
// With tools at s tokens and a backend counting 1.5 times the estimate, the
// history h must satisfy two bounds:
//
//   - The first request is sent as estimated, before any calibration, so it
//     must leave the reply room: h + s <= window - minimum reply. Above this
//     bound the request is held back and the test fails for a reason that has
//     nothing to do with compaction.
//   - The residual of the backend's count over the estimate is 0.5h + 1.5s.
//     The old residual calibration took it for the tool-schema floor and
//     blocked compaction when it passed the budget: 0.5h + 1.5s > budget.
//     Below this bound the old bug passes the test, so the test no longer
//     guards it.
//
// The size is the middle of the two bounds.
func autoCompactHistory(t *testing.T, s int) int {
	t.Helper()
	budget := autoCompactWindow - autoCompactReserve
	low := 2*budget - 3*s
	high := autoCompactWindow - autoCompactMinOut - s
	if high-low < 2000 {
		t.Fatalf("the tool schemas (%d tokens) leave no room for a history that tells the old floor bug from a good run: need %d < h <= %d", s, low, high)
	}
	return (low + high) / 2
}

// TestSessionAutoCompacts drives a real chat.Session against a fake LLM that
// counts 1.5x the byte/4 estimate, with a large history and a large reserve,
// and proves the OnCompactDone callback fires. The tokenizer skew on the
// history must not be taken for a tool-schema floor that blocks compaction.
// The history is sized from the session's tool schemas (see
// autoCompactHistory), so the test fails if that old behaviour comes back and
// keeps working as tools are added.
func TestSessionAutoCompacts(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	// Measure the tool schemas with a session that counts tokens exactly as
	// estimated, so its calibration does not matter.
	var toolBytes atomic.Int64
	probe := httptest.NewServer(autoCompactOpenAI(1, &toolBytes))
	defer probe.Close()
	ps, err := chat.NewSession(context.Background(), autoCompactConfig(probe.URL), chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ps.SendMessage("hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	ps.Close()
	if toolBytes.Load() == 0 {
		t.Fatal("the probe request carried no tools")
	}
	history := autoCompactHistory(t, int(toolBytes.Load())/4)
	t.Logf("tool schemas %d tokens, history %d tokens", toolBytes.Load()/4, history)

	srv := httptest.NewServer(autoCompactOpenAI(1.5, nil))
	defer srv.Close()

	var mu sync.Mutex
	var called bool
	var gotBefore, gotAfter int

	cfg := autoCompactConfig(srv.URL)

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

	if _, err := session.SendMessage(strings.Repeat("earlier detail ", history*4/15)); err != nil {
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
