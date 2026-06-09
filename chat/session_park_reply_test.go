package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// TestParkedReplySurfacedToOnParked reproduces the "queued follow-up looks
// ignored" bug: a background sub-agent keeps the run parked; the model's
// text reply at the park gate (e.g. the answer to an injected follow-up)
// must reach the OnParked callback so the UI can show it. Before the fix,
// OnParked always received "" (the reply only lived inside the cogito
// fragment), so every park-boundary answer was silently dropped.
func TestParkedReplySurfacedToOnParked(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	const parkedReply = "Agent started in the background. Meanwhile: 2+2 = 4."

	releaseSub := make(chan struct{})
	var mainReq int64 // atomic; counts main-conversation requests

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(readBody(r), &req)

		// The sub-agent's conversation starts with user "subtask"; the main
		// conversation's first user message is "start".
		firstUser := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				firstUser = m.Content
				break
			}
		}

		type responseSpec struct {
			isTool bool
			name   string
			args   string
			text   string
		}
		var spec responseSpec
		if firstUser == "subtask" {
			// Sub-agent turn: hold it running until the test saw the park,
			// then finish with a plain reply.
			select {
			case <-releaseSub:
			case <-time.After(10 * time.Second):
			}
			spec = responseSpec{text: "sub reply"}
		} else {
			switch atomic.AddInt64(&mainReq, 1) {
			case 1:
				// user "start" → spawn a background sub-agent.
				spec = responseSpec{
					isTool: true,
					name:   "spawn_agent",
					args:   `{"task":"subtask","background":true}`,
				}
			case 2:
				// spawn tool result processed → reply with text while the
				// sub-agent still runs: the run parks with THIS reply.
				spec = responseSpec{text: parkedReply}
			default:
				// completion notice injected → final answer, run returns.
				spec = responseSpec{text: "all done"}
			}
		}

		if !req.Stream {
			msg := map[string]any{"role": "assistant"}
			finish := "stop"
			if spec.isTool {
				msg["content"] = nil
				msg["tool_calls"] = []any{map[string]any{
					"id": "call_spawn", "type": "function", "index": 0,
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
				"index": 0, "id": "call_spawn", "type": "function",
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

	parked := make(chan string, 8)
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
	session, err := NewSession(context.Background(), cfg, Callbacks{
		OnParked: func(reply string) { parked <- reply },
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

	// The run must park while the sub-agent is still running, surfacing the
	// model's pre-park reply to OnParked.
	select {
	case reply := <-parked:
		if reply != parkedReply {
			t.Fatalf("OnParked reply = %q, want %q", reply, parkedReply)
		}
	case res := <-doneCh:
		t.Fatalf("run returned before parking (response %q, err %v)", res.response, res.err)
	case <-time.After(10 * time.Second):
		t.Fatal("run never parked")
	}

	// Let the sub-agent finish: its completion injects into the parked run,
	// which resumes and returns the final answer.
	close(releaseSub)
	select {
	case res := <-doneCh:
		if res.err != nil {
			t.Fatalf("SendMessage: %v", res.err)
		}
		if res.response != "all done" {
			t.Fatalf("final response = %q, want %q", res.response, "all done")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after sub-agent completion")
	}
}
