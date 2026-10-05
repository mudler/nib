package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

// These continuations use user-role messages, but are neither human acceptance
// nor ordinary goal reminders. Start with a full budget to catch either mistake.
func TestGoalRepromptTerminalExclusions(t *testing.T) {
	var s *Session
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isModelProbe(r) {
			serveEmptyModels(w)
			return
		}
		n := requests.Add(1)
		if n > 3 {
			http.Error(w, "unexpected extra continuation", 400)
			return
		}
		if got := repromptCount(s); got != 1 {
			t.Errorf("request %d changed budget: %d", n, got)
		}
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if n == 1 {
			s.background.startBackground(backgroundShell, "worker")
		}
		if n == 2 {
			found := false
			for _, m := range request.Messages {
				found = found || strings.Contains(m.Content, "automatic completion")
			}
			if !found {
				t.Error("missing terminal-barrier continuation")
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "working"}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	var err error
	s, err = NewSession(context.Background(), types.Config{AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 1, MaxRetries: 1}, Model: "fake", APIKey: "fake", BaseURL: srv.URL + "/v1", Goal: types.GoalConfig{MaxReprompts: 1}}, Callbacks{OnParked: func(string) {
		if !s.background.completeBackground(backgroundShell, "worker", "automatic completion", true) {
			t.Error("parked worker completion refused")
		}
		if !s.Inject("automatic completion") {
			t.Error("parked injection refused")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetGoal("work")
	seedReprompt(s)
	if _, err := s.SendMessageWithDelivery("already accepted", InputAccepted); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || !s.GoalPaused() || repromptCount(s) != 1 {
		t.Fatalf("requests=%d paused=%v count=%d", requests.Load(), s.GoalPaused(), repromptCount(s))
	}
}

func TestGoalRepromptSupervisionExclusions(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{Goal: types.GoalConfig{MaxReprompts: 1}}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetGoal("work")
	now := time.Unix(1000, 0)
	if !s.appendGoalReminder(now) {
		t.Fatal("first reminder refused")
	}
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second})
	s.goalSupervisor = g
	makeSupervisorEligible(g, state)
	for i := 0; i < 3; i++ {
		latestGoalTimer(t, factory).fire()
		if _, ok := g.takeReview(); !ok {
			t.Fatal("missing check-in")
		}
		g.reviewFinishedAndParked()
		if repromptCount(s) != 1 || !s.goalReprompts.timestamps[0].Equal(now) {
			t.Fatal("check-in changed reminder budget")
		}
	}
	if s.appendGoalReminder(now.Add(time.Minute)) || !s.GoalPaused() {
		t.Fatal("check-ins reset full budget")
	}
}

func TestGoalRepromptDirectEmbeddingDefaultsAndErrors(t *testing.T) {
	for _, window := range []string{"", "0", "0s"} {
		s, err := NewSession(context.Background(), types.Config{Goal: types.GoalConfig{RepromptWindow: window}}, Callbacks{})
		if err != nil {
			t.Fatal(err)
		}
		if s.goalReprompts.max != 10 || s.goalReprompts.window != 2*time.Minute {
			t.Errorf("window %q: %+v", window, s.goalReprompts)
		}
		s.Close()
	}
	for _, goal := range []types.GoalConfig{{MaxReprompts: -2}, {MaxReprompts: -1, RepromptWindow: "-1s"}, {MaxReprompts: -1, RepromptWindow: "999999999999999h"}} {
		s, err := NewSession(context.Background(), types.Config{Goal: goal}, Callbacks{})
		if s != nil {
			s.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "goal.") {
			t.Fatalf("%+v: %v", goal, err)
		}
	}
}

func TestGoalRepromptPausePreservesIndependentBackground(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{Goal: types.GoalConfig{MaxReprompts: 1}}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetGoal("work")
	s.background.startBackground(backgroundShell, "independent")
	now := time.Unix(1000, 0)
	if !s.appendGoalReminder(now) || s.appendGoalReminder(now) {
		t.Fatal("guard did not trip")
	}
	snapshot := s.background.terminalSnapshot()
	if snapshot.closed || snapshot.interrupted || snapshot.runningShells != 1 {
		t.Fatalf("pause canceled independent work: %+v", snapshot)
	}
	if !s.background.completeBackground(backgroundShell, "independent", "finished after pause", true) {
		t.Fatal("completion rejected after pause")
	}
	if !s.GoalPaused() || s.background.terminalSnapshot().queuedNotices != 1 {
		t.Fatal("completion resumed goal or lost notice")
	}
}
