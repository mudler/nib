package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// A background agent that finished and was delivered once can be resumed with
// send_agent_message. Cogito does not call the spawn callback for a resume, so
// the second completion has to be accepted by the background ledger as a new
// run of the same agent. If it is not, the legacy wake-up is the only signal,
// it carries no text, and the parent never sees the result.
func TestResumedAgentCompletionReachesTheParent(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const childTask = "synthetic child task"
	const childResult = "synthetic second result"

	var mu sync.Mutex
	rootCalls := 0
	var rootSawResult bool
	parentParked := make(chan struct{})
	var parkedOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		isChild := false
		sawResult := false
		for _, m := range req.Messages {
			if m.Role == "user" && m.Content == childTask {
				isChild = true
			}
			if strings.Contains(m.Content, "Agent finished-child completed") && strings.Contains(m.Content, childResult) {
				sawResult = true
			}
		}
		msg := map[string]any{"role": "assistant", "content": "ok"}
		if isChild {
			// Finish only once the parent is parked waiting for it.
			select {
			case <-parentParked:
			case <-ctx.Done():
				return
			}
			msg["content"] = childResult
		} else {
			mu.Lock()
			rootCalls++
			n := rootCalls
			if sawResult {
				rootSawResult = true
			}
			mu.Unlock()
			switch n {
			case 1:
				msg["content"] = ""
				msg["tool_calls"] = []any{map[string]any{"id": "synthetic-call", "type": "function", "function": map[string]any{"name": "send_agent_message", "arguments": `{"agent_id":"finished-child","message":"continue"}`}}}
			case 2:
				msg["content"] = "waiting for the child"
				parkedOnce.Do(func() { close(parentParked) })
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
	}))
	defer srv.Close()

	session, err := NewSession(ctx, types.Config{Model: "synthetic", APIKey: "synthetic", BaseURL: srv.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 8, MaxAttempts: 1, MaxRetries: 1}}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// The agent already finished once and the parent already read that notice.
	frag := cogito.NewEmptyFragment().AddMessage(cogito.UserMessageRole, childTask).AddMessage(cogito.AssistantMessageRole, "first result")
	session.agentManager.Register(&cogito.AgentState{ID: "finished-child", Status: cogito.AgentStatusCompleted, Background: true, Fragment: &frag, Result: "first result"})
	session.background.startBackground(backgroundAgent, "finished-child")
	session.background.completeBackground(backgroundAgent, "finished-child", "Agent finished-child completed:\nfirst result", true)
	reservation, _, observed := session.background.reserveNoticesForRoot(0)
	session.background.consumeNotices(reservation)
	session.background.markRootObserved(observed)

	done := make(chan error, 1)
	go func() {
		_, err := session.SendMessage("resume the finished child")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the turn did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if !rootSawResult {
		t.Fatalf("the parent never received the resumed agent's completion (root requests: %d)", rootCalls)
	}
}
