package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// promiseServer answers each chat request with the next scripted reply (the
// last one repeats) and keeps the request bodies.
type promiseServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newPromiseServer(t *testing.T, replies ...string) *promiseServer {
	t.Helper()
	ps := &promiseServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isModelProbe(r) {
			serveEmptyModels(w)
			return
		}
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		ps.bodies = append(ps.bodies, string(body))
		i := len(ps.bodies) - 1
		ps.mu.Unlock()
		if i >= len(replies) {
			i = len(replies) - 1
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": replies[i]}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
		})
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *promiseServer) requests() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return len(ps.bodies)
}

func (ps *promiseServer) noticesSent() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	n := 0
	for _, b := range ps.bodies {
		if strings.Contains(b, "[Host notice]") {
			n++
		}
	}
	return n
}

func promiseSession(t *testing.T, ps *promiseServer, cb Callbacks, goal types.GoalConfig) *Session {
	t.Helper()
	s, err := NewSession(context.Background(), types.Config{
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		Model:        "fake", BaseURL: ps.URL + "/v1", APIKey: "fake", Goal: goal,
	}, cb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const promiseText = "The build is running. I'll check on it shortly."

func TestPromiseGuardFiresOnce(t *testing.T) {
	ps := newPromiseServer(t, promiseText, "I cannot watch it. Ask me again when it is done.")
	var steps []string
	s := promiseSession(t, ps, Callbacks{
		OnArmedCount:  func() int { return 0 },
		OnStepContent: func(c string) { steps = append(steps, c) },
	}, types.GoalConfig{})
	response, err := s.SendMessage("start the build")
	if err != nil {
		t.Fatal(err)
	}
	if response != "I cannot watch it. Ask me again when it is done." {
		t.Fatalf("response = %q", response)
	}
	if ps.requests() != 2 || ps.noticesSent() != 1 {
		t.Fatalf("requests=%d notices=%d, want 2 and 1", ps.requests(), ps.noticesSent())
	}
	if len(steps) != 1 || steps[0] != promiseText {
		t.Fatalf("the first answer should be shown as a step: %q", steps)
	}
	for _, m := range s.GetMessages() {
		if strings.Contains(m.Content, "[Host notice]") {
			t.Fatalf("the host notice must not show as a message: %q", m.Content)
		}
	}
}

func TestPromiseGuardNeverLoops(t *testing.T) {
	// The retry promises again: it is left alone.
	ps := newPromiseServer(t, promiseText, "Sorry. I'll check on it later.", promiseText, "Understood, I cannot watch it.")
	s := promiseSession(t, ps, Callbacks{OnArmedCount: func() int { return 0 }}, types.GoalConfig{})
	response, err := s.SendMessage("start the build")
	if err != nil {
		t.Fatal(err)
	}
	if response != "Sorry. I'll check on it later." || ps.requests() != 2 || ps.noticesSent() != 1 {
		t.Fatalf("response=%q requests=%d notices=%d", response, ps.requests(), ps.noticesSent())
	}
	// The next message is a new turn: the guard may speak again.
	if _, err := s.SendMessage("and now?"); err != nil {
		t.Fatal(err)
	}
	if ps.requests() != 4 {
		t.Fatalf("requests=%d, want 4", ps.requests())
	}
}

func TestPromiseGuardQuietCases(t *testing.T) {
	cases := map[string]struct {
		cb    Callbacks
		reply string
	}{
		"job armed":        {Callbacks{OnArmedCount: func() int { return 1 }}, promiseText},
		"piped, no arming": {Callbacks{}, promiseText},
		"no promise":       {Callbacks{OnArmedCount: func() int { return 0 }}, "All done. The build passed."},
		"offer":            {Callbacks{OnArmedCount: func() int { return 0 }}, "Do you want me to check later?"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ps := newPromiseServer(t, c.reply)
			s := promiseSession(t, ps, c.cb, types.GoalConfig{})
			if _, err := s.SendMessage("go"); err != nil {
				t.Fatal(err)
			}
			if ps.requests() != 1 || ps.noticesSent() != 0 {
				t.Fatalf("requests=%d notices=%d, want 1 and 0", ps.requests(), ps.noticesSent())
			}
		})
	}
}

func TestPromiseGuardSkipsGoalReminderTurn(t *testing.T) {
	// First run: no promise. The goal reminder follows. The run after it
	// promises, and the guard stays quiet; the goal budget (one reminder) then
	// ends the turn.
	ps := newPromiseServer(t, "Working on it.", promiseText)
	s := promiseSession(t, ps, Callbacks{OnArmedCount: func() int { return 0 }}, types.GoalConfig{MaxReprompts: 1})
	s.SetGoal("keep working")
	if _, err := s.SendMessage("start"); err != nil {
		t.Fatal(err)
	}
	if ps.requests() != 2 || ps.noticesSent() != 0 {
		t.Fatalf("requests=%d notices=%d, want 2 and 0", ps.requests(), ps.noticesSent())
	}
}

func TestHasArmedWork(t *testing.T) {
	ps := newPromiseServer(t, "ok")
	armed := 0
	s := promiseSession(t, ps, Callbacks{OnArmedCount: func() int { return armed }}, types.GoalConfig{})
	if s.hasArmedWork() {
		t.Fatal("nothing is armed")
	}
	armed = 2
	if !s.hasArmedWork() {
		t.Fatal("a held job counts")
	}
	armed = 0

	s.noteWakeup(60)
	if !s.hasArmedWork() {
		t.Fatal("a pending wake-up counts")
	}
	s.wakeups = wakeupTracker{}
	s.noteWakeup(0)
	if s.hasArmedWork() {
		t.Fatal("an elapsed wake-up does not count")
	}

	s.inject <- queuedNotice()
	if !s.hasArmedWork() {
		t.Fatal("queued input counts")
	}
	<-s.inject

	for name, source := range map[string]backgroundSource{"sub-agent": backgroundAgent, "shell job": backgroundShell} {
		if !s.background.startBackground(source, name) {
			t.Fatalf("%s did not start", name)
		}
		if !s.hasArmedWork() {
			t.Fatalf("a running %s counts", name)
		}
		s.background.completeBackground(source, name, "done", true)
	}
}

func TestWakeupTrackerPrunes(t *testing.T) {
	var w wakeupTracker
	now := time.Unix(1000, 0)
	w.add(time.Minute, now)
	if !w.pending(now.Add(59 * time.Second)) {
		t.Fatal("pending before the deadline")
	}
	if w.pending(now.Add(time.Minute)) {
		t.Fatal("not pending at the deadline")
	}
	if len(w.deadlines) != 0 {
		t.Fatal("expired entries are dropped")
	}
}

func queuedNotice() openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: "user", Content: "queued"}
}
