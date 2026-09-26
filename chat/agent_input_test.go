package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// agentInputBackend scripts a turn that spawns a background sub-agent. The
// sub-agent's first request waits for the test (subWaiting / subGo), then
// calls a harmless tool so its loop takes another step, which is where it reads
// injected messages. Its later requests are recorded and answered plainly.
type agentInputBackend struct {
	subWaiting chan struct{}
	subGo      chan struct{}

	mainReq, subReq int64
	mu              sync.Mutex
	subLater        [][]string // contents of each later sub-agent request
}

func (b *agentInputBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	_ = json.Unmarshal(readBody(r), &req)

	firstUser := ""
	var contents []string
	for _, m := range req.Messages {
		if m.Role == "user" && firstUser == "" {
			firstUser = m.Content
		}
		contents = append(contents, m.Content)
	}

	tool, text := "", ""
	if firstUser == "subtask" {
		if atomic.AddInt64(&b.subReq, 1) == 1 {
			close(b.subWaiting)
			select {
			case <-b.subGo:
			case <-time.After(10 * time.Second):
			}
			tool = `{"name":"agent_logs","arguments":"{\"agent_id\":\"none\"}"}`
		} else {
			b.mu.Lock()
			b.subLater = append(b.subLater, contents)
			b.mu.Unlock()
			text = "sub done"
		}
	} else {
		switch atomic.AddInt64(&b.mainReq, 1) {
		case 1:
			tool = `{"name":"spawn_agent","arguments":"{\"task\":\"subtask\",\"background\":true}"}`
		case 2:
			text = "waiting for the agent"
		default:
			text = "all done"
		}
	}

	msg := map[string]any{"role": "assistant", "content": text}
	finish := "stop"
	if tool != "" {
		var fn map[string]any
		_ = json.Unmarshal([]byte(tool), &fn)
		msg["content"] = nil
		msg["tool_calls"] = []any{map[string]any{"id": fmt.Sprintf("call_%s", fn["name"]), "type": "function", "index": 0, "function": fn}}
		finish = "tool_calls"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "fake", "object": "chat.completion", "model": "fake",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
	})
}

// A message the user sends to a running sub-agent reaches its conversation at
// its next step, and shows in the agent's log.
func TestSendToAgentReachesTheRunningAgent(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	backend := &agentInputBackend{subWaiting: make(chan struct{}), subGo: make(chan struct{})}
	srv := httptest.NewServer(backend)
	defer srv.Close()

	var agentID atomic.Value
	s, err := NewSession(context.Background(), types.Config{
		Model: "fake-model", APIKey: "fake-key", BaseURL: srv.URL + "/v1", ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
	}, Callbacks{
		OnAgentEvent: func(ev AgentEvent) {
			if ev.Status == AgentStatusRunning {
				agentID.Store(ev.ID)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	done := make(chan error, 1)
	go func() {
		_, err := s.SendMessage("start")
		done <- err
	}()

	select {
	case <-backend.subWaiting:
	case <-time.After(10 * time.Second):
		t.Fatal("the sub-agent never started")
	}
	id, _ := agentID.Load().(string)
	if id == "" {
		t.Fatal("no running agent event")
	}
	if err := s.SendToAgent(id, "also check the LoRA scales"); err != nil {
		t.Fatalf("SendToAgent: %v", err)
	}
	if log := s.AgentLog(id); !strings.Contains(log, "also check the LoRA scales") {
		t.Fatalf("agent log = %q, want the user's message", log)
	}
	close(backend.subGo)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the turn did not finish")
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.subLater) == 0 {
		t.Fatal("the sub-agent made no second request")
	}
	if got := strings.Join(backend.subLater[0], "\n"); !strings.Contains(got, "also check the LoRA scales") {
		t.Fatalf("the sub-agent's next request lacks the user's message:\n%s", got)
	}

	// Finished now: sending again says so instead of blocking.
	if err := s.SendToAgent(id, "anything else?"); !errors.Is(err, ErrAgentFinished) {
		t.Fatalf("SendToAgent to a finished agent = %v, want ErrAgentFinished", err)
	}
}

func TestSendToAgentValidates(t *testing.T) {
	s := &Session{}
	if err := s.SendToAgent("a1", "hi"); err == nil {
		t.Fatal("no agent manager: want an error")
	}
	s = &Session{agentManager: nil}
	if err := s.SendToAgent("a1", "   "); err == nil {
		t.Fatal("empty message: want an error")
	}
}
