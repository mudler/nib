package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

func TestGoalAccessors(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if s.Goal() != "" {
		t.Fatalf("new session goal = %q, want empty", s.Goal())
	}
	s.SetGoal("make tests pass")
	if s.Goal() != "make tests pass" {
		t.Fatalf("Goal() = %q after SetGoal", s.Goal())
	}
	s.SetGoal("replace it")
	if s.Goal() != "replace it" {
		t.Fatalf("Goal() = %q, want replaced", s.Goal())
	}
	s.ClearGoal()
	if s.Goal() != "" {
		t.Fatalf("Goal() = %q after ClearGoal, want empty", s.Goal())
	}
}

// readBody reads an HTTP request body fully.
func readBody(r *http.Request) []byte {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

// TestStopGateReRunsUntilGoalDone exercises the /goal stop-gate end-to-end:
// the first stop re-triggers the turn; the second stop carries a goal_done
// tool call which clears the goal and lets the turn finish.
func TestStopGateReRunsUntilGoalDone(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var reqCount int64 // atomic

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&reqCount, 1)

		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(readBody(r), &req)

		// Determine what to emit for this request number.
		// n=1: plain text + stop (goal not met → reminder re-runs turn)
		// n=2: tool_call goal_done (goal met)
		// n>=3: plain text + stop (final answer after tool execution)
		type responseSpec struct {
			isTool bool
			name   string
			args   string
			text   string
		}
		var spec responseSpec
		switch n {
		case 1:
			spec = responseSpec{text: "working on it"}
		case 2:
			spec = responseSpec{
				isTool: true,
				name:   "goal_done",
				args:   `{"justification":"done"}`,
			}
		default:
			spec = responseSpec{text: "all done"}
		}

		if !req.Stream {
			// Non-streaming JSON response.
			msg := map[string]any{"role": "assistant"}
			finish := "stop"
			if spec.isTool {
				msg["content"] = nil
				msg["tool_calls"] = []any{map[string]any{
					"id": fmt.Sprintf("call_%d", n), "type": "function", "index": 0,
					"function": map[string]any{"name": spec.name, "arguments": spec.args},
				}}
				finish = "tool_calls"
			} else {
				msg["content"] = spec.text
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "fake", "object": "chat.completion", "model": "fake",
				"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
			})
			return
		}

		// Streaming SSE response.
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
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
		if spec.isTool {
			emit(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": fmt.Sprintf("call_%d", n), "type": "function",
				"function": map[string]any{"name": spec.name, "arguments": spec.args},
			}}}, "")
			emit(map[string]any{}, "tool_calls")
		} else {
			emit(map[string]any{"content": spec.text}, "")
			emit(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
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
	session, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	session.SetGoal("finish the thing")
	if _, err := session.SendMessage("start"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	got := atomic.LoadInt64(&reqCount)
	if got < 2 {
		t.Fatalf("request count = %d, want >= 2 (turn must re-run after first stop)", got)
	}
	if g := session.Goal(); g != "" {
		t.Fatalf("Goal() = %q after goal_done, want empty", g)
	}
}
