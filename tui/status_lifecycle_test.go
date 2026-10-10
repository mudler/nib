package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func TestStatusManualCompactProduction(t *testing.T) {
	for _, outcome := range []string{"success", "noop", "error"} {
		t.Run(outcome, func(t *testing.T) {
			var fail atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					http.NotFound(w, r)
					return
				}
				if fail.Load() {
					http.Error(w, "summary failed", 400)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			s, err := chat.NewSession(context.Background(), types.Config{Model: "test", APIKey: "test", BaseURL: server.URL + "/v1", WorkingDir: t.TempDir(), Compaction: types.CompactionConfig{KeepRecent: 2}, AgentOptions: types.AgentOptions{Iterations: 2, MaxAttempts: 1, MaxRetries: 1}}, chat.Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if outcome != "noop" {
				for i := 0; i < 4; i++ {
					if _, err := s.SendMessage(strings.Repeat("history ", 100)); err != nil {
						t.Fatal(err)
					}
				}
			}
			fail.Store(outcome == "error")
			m := newWakeupTestModel()
			m.session = s
			m.reconcileStatus()
			cmd := m.dispatchResolved("/compact")
			m.reconcileStatus()
			if got := m.activitySummary(time.Now()); got.Primary == "Ready for input" {
				t.Fatal("ready during compaction")
			}
			result := cmd().(compactResultMsg)
			if outcome == "success" && (result.err != nil || result.before == result.after) {
				t.Fatalf("not compacted: %+v", result)
			}
			if outcome == "error" && result.err == nil {
				t.Fatal("missing compaction failure")
			}
			if outcome == "noop" && (result.err != nil || result.before != result.after) {
				t.Fatalf("not noop: %+v", result)
			}
			next, _ := m.Update(result)
			m = next.(Model)
			next, _ = m.Update(statusRefreshMsg{m.statusTickSequence})
			m = next.(Model)
			if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" || m.statusPending {
				t.Fatalf("%s left status pending: %+v", outcome, got)
			}
		})
	}
}

func TestStatusResumedTurnSettledBeforeNotificationDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release := make(chan struct{})
	parked := make(chan struct{}, 1)
	var roots atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct{ Role, Content string }
		}
		json.NewDecoder(r.Body).Decode(&req)
		child := false
		for _, msg := range req.Messages {
			child = child || msg.Role == "user" && msg.Content == "subtask"
		}
		msg := map[string]any{"role": "assistant", "content": "done"}
		if child {
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
		} else if roots.Add(1) == 1 {
			msg["content"] = ""
			msg["tool_calls"] = []any{map[string]any{"id": "spawn", "type": "function", "function": map[string]any{"name": "spawn_agent", "arguments": `{"task":"subtask","background":true}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	m := newWakeupTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	callbacks := m.scheduleCallbacks()
	onPark := callbacks.OnParked
	callbacks.OnParked = func(reply string) {
		onPark(reply)
		select {
		case parked <- struct{}{}:
		default:
		}
	}
	s, err := chat.NewSession(ctx, types.Config{Model: "test", APIKey: "test", BaseURL: server.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 8, MaxAttempts: 1, MaxRetries: 1}}, callbacks)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m.session = s
	done := make(chan error, 1)
	go func() { _, err := s.SendMessage("start"); done <- err }()
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !s.ActivitySnapshot().ReadyAllowed {
		t.Fatal("production turn not settled")
	}
	// Both lifecycle messages were published by chat before settlement; neither
	// has reached Update. Drain the production adapter only now.
	events := m.toolEvents.drain()
	if len(events) != 2 {
		t.Fatalf("expected park and resume, got %d", len(events))
	}
	m.applyToolEvents(events)
	next, _ := m.Update(responseMsg{content: "done"})
	m = next.(Model)
	next, _ = m.Update(statusRefreshMsg{m.statusTickSequence})
	m = next.(Model)
	if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" || m.statusPending {
		t.Fatalf("retrospective resume left status pending: %+v", got)
	}
}

func TestStatusCompactResultOwnership(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newWakeupTestModel()
	m.session = s
	old := m.dispatchResolved("/compact")().(compactResultMsg)
	m.resetSchedules()
	current := m.dispatchResolved("/compact")().(compactResultMsg)
	before := len(m.messages)
	next, _ := m.Update(old)
	m = next.(Model)
	if !m.compactPending || !m.loading || len(m.messages) != before {
		t.Fatal("obsolete result settled current compaction")
	}
	next, _ = m.Update(current)
	m = next.(Model)
	if m.compactPending || m.loading || m.activitySummary(time.Now()).Primary != "Ready for input" {
		t.Fatal("current result did not settle")
	}
	before = len(m.messages)
	next, _ = m.Update(current)
	m = next.(Model)
	if len(m.messages) != before {
		t.Fatal("duplicate result applied")
	}
}

func TestStatusObservationDoesNotAcknowledgePendingDispatch(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newWakeupTestModel()
	m.session = s
	m.toolEvents = newToolEventQueue()
	m.reconcileStatus()
	if m.injectStatus("not live", chat.InputAutomatic) {
		t.Fatal("injected without live run")
	}
	if m.statusPending || m.activitySummary(time.Now()).Primary != "Ready for input" {
		t.Fatal("rejected injection invalidated readiness")
	}
	_ = m.sendMessageDelivery("not started", chat.InputAutomatic)
	next, _ := m.Update(parkMsg{parked: false})
	m = next.(Model)
	next, _ = m.Update(statusRefreshMsg{m.statusTickSequence})
	m = next.(Model)
	if !m.statusPending || !m.activitySummary(time.Now()).Updating {
		t.Fatal("notification acknowledged an unpublished dispatch")
	}
}
