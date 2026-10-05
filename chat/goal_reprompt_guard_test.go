package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

func TestGoalRepromptRollingBoundary(t *testing.T) {
	now := time.Unix(1000, 0)
	g := goalRepromptGuard{max: 2, window: time.Minute}
	if !g.allow(now) || !g.allow(now.Add(time.Second)) {
		t.Fatal("budget refused early")
	}
	if g.allow(now.Add(time.Minute)) {
		t.Fatal("inclusive boundary expired")
	}
	if !g.allow(now.Add(time.Minute + time.Nanosecond)) {
		t.Fatal("expired oldest not pruned")
	}
	if g.allow(now.Add(time.Minute + time.Second)) {
		t.Fatal("second inclusive boundary expired")
	}
	if !g.allow(now.Add(time.Minute+time.Second+time.Nanosecond)) || len(g.timestamps) != 2 {
		t.Fatal("rolling expiry/cap")
	}
	one := goalRepromptGuard{max: 1, window: time.Minute}
	if !one.allow(now) || one.allow(now) {
		t.Fatal("threshold one")
	}
	unlimited := goalRepromptGuard{max: -1, window: time.Minute}
	for i := 0; i < 100; i++ {
		if !unlimited.allow(now) {
			t.Fatal("unlimited refused")
		}
	}
	if len(unlimited.timestamps) != 0 {
		t.Fatal("unlimited accumulates")
	}
}

func TestGoalRepromptStopGate(t *testing.T) {
	for _, max := range []int{0, 1} {
		t.Run(fmt.Sprint(max), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if isModelProbe(r) {
					serveEmptyModels(w)
					return
				}
				request := requests.Add(1)
				if request > 12 {
					http.Error(w, "guard failed", 400)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "still working"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}})
			}))
			defer srv.Close()
			notices, responses := 0, 0
			var s *Session
			var err error
			s, err = NewSession(context.Background(), types.Config{AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3}, Model: "fake", BaseURL: srv.URL + "/v1", APIKey: "fake", Goal: types.GoalConfig{MaxReprompts: max}}, Callbacks{
				OnGoalPaused: func(n GoalPausedNotice) {
					notices++
					wantMax := max
					if wantMax == 0 {
						wantMax = 10
					}
					if !s.GoalPaused() || !n.Paused || n.Window != 2*time.Minute || n.MaxReprompts != wantMax {
						t.Error("bad notice")
					}
					s.AcceptHumanMessage("accepted after pause")
				},
				OnResponse: func(text string) {
					responses++
					if text != "still working" {
						t.Error(text)
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.SetGoal("keep working")
			response, err := s.SendMessage("start")
			n := max
			if n == 0 {
				n = 10
			}
			if err != nil || response != "still working" || int(requests.Load()) != n+1 || notices != 1 || responses != 1 || !s.GoalPaused() || s.Goal() != "keep working" {
				t.Fatalf("response=%q err=%v requests=%d notices=%d responses=%d", response, err, requests.Load(), notices, responses)
			}
			if u := s.Usage(); u.Turns != 1 || u.PromptTokens != 10*(n+1) || u.CompletionTokens != 2*(n+1) {
				t.Fatalf("usage not preserved: %+v", u)
			}
			for _, messages := range [][]Message{s.GetMessages()} {
				count := 0
				for _, m := range messages {
					if m.Content == goalReminder("keep working") {
						count++
					}
				}
				if count != n {
					t.Fatalf("history reminders=%d want %d", count, n)
				}
			}
			count := 0
			for _, m := range s.ExportContext() {
				if m.Content == goalReminder("keep working") {
					count++
				}
			}
			if count != n {
				t.Fatalf("context reminders=%d", count)
			}
		})
	}
}

func TestGoalRepromptLifecycleAndSupervisor(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{Goal: types.GoalConfig{MaxReprompts: 1}}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1000, 0)
	s.SetGoal("work")
	if !s.appendGoalReminder(now) {
		t.Fatal("first reminder")
	}
	s.AcceptHumanMessage(" \n")
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second})
	s.goalSupervisor = g
	makeSupervisorEligible(g, state)
	timer := latestGoalTimer(t, factory)
	if s.appendGoalReminder(now) || !s.GoalPaused() {
		t.Fatal("did not pause")
	}
	timer.fire()
	if _, ok := g.takeReview(); ok {
		t.Fatal("pause left supervision armed")
	}
	s.AcceptHumanMessage("hello")
	if !s.GoalPaused() || s.appendGoalReminder(now.Add(time.Hour)) {
		t.Fatal("implicit resume")
	}
	if !s.ResumeGoal() || !s.appendGoalReminder(now) {
		t.Fatal("resume did not reset")
	}
	s.SetGoal("work")
	if !s.appendGoalReminder(now) {
		t.Fatal("same goal replacement did not reset")
	}
	s.AcceptHumanMessage("human")
	if !s.appendGoalReminder(now) {
		t.Fatal("human did not reset")
	}
	s.ClearGoal()
	if len(s.goalReprompts.timestamps) != 0 || s.appendGoalReminder(now) {
		t.Fatal("clear")
	}
}

func TestGoalRepromptRestoredAndRace(t *testing.T) {
	for _, max := range []int{1, -1} {
		for _, paused := range []bool{true, false} {
			s, err := NewSession(context.Background(), types.Config{InitialGoal: "restored", InitialGoalPaused: paused, Goal: types.GoalConfig{MaxReprompts: max}}, Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			if len(s.goalReprompts.timestamps) != 0 || s.appendGoalReminder(time.Now()) == paused {
				t.Fatal("restored state")
			}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(2)
				go func() { defer wg.Done(); s.AcceptHumanMessage("human") }()
				go func() { defer wg.Done(); s.appendGoalReminder(time.Now()) }()
			}
			wg.Wait()
			if paused && !s.GoalPaused() {
				t.Fatal("human resumed restored goal")
			}
			s.Close()
		}
	}
}

// History readers/turn setup may take runMu while holding historyMu. A
// reminder waiting for history must not hold runMu and invert that order.
func TestGoalRepromptHistoryLockOrder(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetGoal("work")
	s.historyMu.Lock()
	appended := make(chan struct{})
	go func() { s.appendGoalReminder(time.Now()); close(appended) }()
	// Wait for the reminder goroutine to reach the contested lock.
	time.Sleep(20 * time.Millisecond)
	accepted := make(chan struct{})
	go func() { s.AcceptHumanMessage("human"); close(accepted) }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Error("reminder holds runMu while waiting for historyMu")
	}
	s.historyMu.Unlock()
	<-appended
	<-accepted
}

func TestGoalRepromptBackendRetryCompletionAndFailure(t *testing.T) {
	for _, scenario := range []string{"retry", "complete", "unlimited", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			stubRetrySleep(t)
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if isModelProbe(r) {
					serveEmptyModels(w)
					return
				}
				n := requests.Add(1)
				if n == 2 && (scenario == "retry" || scenario == "failure") {
					code := http.StatusServiceUnavailable
					if scenario == "failure" {
						code = http.StatusUnauthorized
					}
					http.Error(w, `{"error":{"message":"backend unavailable"}}`, code)
					return
				}
				message := map[string]any{"role": "assistant", "content": "working"}
				finish := "stop"
				completeAt := int32(2)
				if scenario == "unlimited" {
					completeAt = 13
				}
				if n == completeAt && (scenario == "complete" || scenario == "unlimited") {
					message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "done", "type": "function", "function": map[string]any{"name": "goal_done", "arguments": `{"justification":"verified"}`}}}}
					finish = "tool_calls"
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
			}))
			defer srv.Close()
			max := 1
			if scenario == "unlimited" {
				max = -1
			}
			notices := 0
			s, err := NewSession(context.Background(), types.Config{Model: "fake", APIKey: "fake", BaseURL: srv.URL + "/v1", ApprovalMode: "auto", AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 1, MaxRetries: 1}, Goal: types.GoalConfig{MaxReprompts: max}}, Callbacks{OnGoalPaused: func(GoalPausedNotice) { notices++ }})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.SetGoal("work")
			_, err = s.SendMessage("start")
			switch scenario {
			case "retry":
				if err != nil || requests.Load() != 3 || notices != 1 || len(s.goalReprompts.timestamps) != 1 {
					t.Fatalf("retry counted twice: requests=%d notices=%d timestamps=%d err=%v", requests.Load(), notices, len(s.goalReprompts.timestamps), err)
				}
			case "failure":
				if err == nil || notices != 0 || len(s.goalReprompts.timestamps) != 1 {
					t.Fatalf("failed request lost reminder: %v", err)
				}
			default:
				if err != nil || s.Goal() != "" || notices != 0 || len(s.goalReprompts.timestamps) != 0 {
					t.Fatalf("completion/unlimited: goal=%q notices=%d count=%d err=%v", s.Goal(), notices, len(s.goalReprompts.timestamps), err)
				}
			}
		})
	}
}
