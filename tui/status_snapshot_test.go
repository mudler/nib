package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// No reader runs until the final callback has returned. The full production
// mailbox must therefore drop BOTH lifecycle notifications, including FINAL.
func TestStatusReconcileDroppedFinalNotification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := newWakeupTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m.agentEventChan = make(chan chat.AgentEvent, 1)
	m.agentEventChan <- chat.AgentEvent{ID: "sentinel"}
	emit := m.agentCallbacks()
	var starts, finishes atomic.Int32
	var roots atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		child := false
		for _, msg := range req.Messages {
			child = child || msg.Role == "user" && msg.Content == "status child"
		}
		msg := map[string]any{"role": "assistant", "content": "done"}
		if !child && roots.Add(1) == 1 {
			msg["content"] = ""
			msg["tool_calls"] = []any{map[string]any{"id": "spawn", "type": "function", "function": map[string]any{"name": "spawn_agent", "arguments": `{"task":"status child","background":false}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "test", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
	}))
	defer backend.Close()
	s, err := chat.NewSession(ctx, types.Config{Model: "test", APIKey: "test", BaseURL: backend.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}, chat.Callbacks{AgentCallbacks: func() func(chat.AgentEvent) {
		return func(ev chat.AgentEvent) {
			emit(ev)
			if ev.Status == chat.AgentStatusRunning {
				starts.Add(1)
			}
			if ev.Status == chat.AgentStatusCompleted {
				finishes.Add(1)
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m.session = s
	if _, err := s.SendMessage("spawn child"); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || finishes.Load() != 1 {
		t.Fatalf("production callbacks: starts=%d FINAL=%d", starts.Load(), finishes.Load())
	}
	if ev := <-m.agentEventChan; ev.ID != "sentinel" {
		t.Fatalf("mailbox was not saturated: %+v", ev)
	}
	select {
	case ev := <-m.agentEventChan:
		t.Fatalf("notification was not dropped: %+v", ev)
	default:
	}
	t.Log("proved running and FINAL completion callbacks dropped from saturated production channel")
	// No activity event follows the drain. A mounted refresh alone must recover.
	next, _ := m.Update(statusRefreshMsg{})
	m = next.(Model)
	got := m.activitySummary(time.Now())
	if got.Primary != "Ready for input" || !got.CountsKnown || got.Agents != 0 || got.Shells != 0 {
		t.Fatalf("cadence did not converge: %+v (ledger %+v)", got, s.ActivitySnapshot())
	}
	if strings.Contains(got.Primary, "updating") {
		t.Fatal(got)
	}
}

func knownStatus() statusSnapshot {
	return statusSnapshot{Known: true, Coherent: true, Execution: chat.ActivitySnapshot{Generation: 7, Revision: 10, Known: true, Coherent: true, ReadyAllowed: true, Barrier: chat.CompletionSnapshot{Known: true}}, Schedule: scheduleSnapshot{Generation: 7, Revision: 4, Known: true}}
}

func TestStatusPhaseMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, phase string
		change      func(*statusSnapshot)
		counts      bool
	}{
		{"ready", "Ready for input", func(s *statusSnapshot) {}, true},
		{"generation", "Working", func(s *statusSnapshot) { s.Execution.RootActive = true }, true},
		{"tool", "Working", func(s *statusSnapshot) {
			s.Execution.RootActive = true
			s.Execution.Shells = []chat.ActivityJob{{ID: "fg", Running: true}}
		}, true},
		{"review execution", "Working", func(s *statusSnapshot) { s.Execution.RootActive = true; s.Execution.Barrier.SupervisorReviewing = true }, true},
		{"mixed parked", "Waiting for jobs", func(s *statusSnapshot) {
			s.Execution.RootParked = true
			s.Execution.Agents = []chat.ActivityJob{{ID: "a", Running: true}, {ID: "a", Running: true, Background: true}}
			s.Execution.Shells = []chat.ActivityJob{{ID: "s", Running: true, Background: true}}
		}, true},
		{"post turn work", "Working", func(s *statusSnapshot) { s.Execution.Agents = []chat.ActivityJob{{ID: "fg", Running: true}} }, true},
		{"unknown", "Working", func(s *statusSnapshot) { s.Known = false }, false},
		{"inconsistent", "Working", func(s *statusSnapshot) { s.Execution.Coherent = false }, false},
		{"startup", "Working", func(s *statusSnapshot) { s.Execution.ReadyAllowed = false }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := knownStatus()
			tc.change(&s)
			got := summarizeStatus(s, time.Now())
			if got.Primary != tc.phase || got.CountsKnown != tc.counts {
				t.Fatal(got)
			}
			if tc.name == "mixed parked" && (got.Agents != 1 || got.Shells != 1) {
				t.Fatal(got)
			}
			if tc.name == "tool" && got.Shells != 0 {
				t.Fatal(got)
			}
		})
	}
}
func TestStatusBarrierIntegration(t *testing.T) {
	for _, change := range []func(*chat.CompletionSnapshot){func(b *chat.CompletionSnapshot) { b.Publishers = 1 }, func(b *chat.CompletionSnapshot) { b.QueuedNotices = 1 }, func(b *chat.CompletionSnapshot) { b.ReservedNotices = 1 }, func(b *chat.CompletionSnapshot) { b.EventSequence = 1 }, func(b *chat.CompletionSnapshot) { b.SupervisorQueued = true }, func(b *chat.CompletionSnapshot) { b.SupervisorReviewing = true }} {
		s := knownStatus()
		change(&s.Execution.Barrier)
		s.Execution.ReadyAllowed = false
		if got := summarizeStatus(s, time.Now()); got.Primary != "Reviewing results" || got.Agents != 0 || got.Shells != 0 {
			t.Fatal(got)
		}
	}
}
func TestStatusNextRunFormatting(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		seconds int
		want    string
	}{{-1, "next run due now"}, {0, "next run due now"}, {59, "next run in less than 1m"}, {60, "next run in 1m"}, {61, "next run in 2m"}} {
		s := knownStatus()
		s.Schedule.Entries = []scheduledRun{{Generation: 7, Active: true, NextDue: now.Add(time.Duration(tc.seconds) * time.Second)}}
		got := summarizeStatus(s, now)
		if got.Schedule != tc.want || got.Primary != "Ready for input" {
			t.Fatal(got)
		}
	}
	s := knownStatus()
	s.Schedule.Entries = []scheduledRun{{Generation: 7, Active: true}}
	if got := summarizeStatus(s, now); got.Schedule != "" {
		t.Fatal(got)
	}
}
func TestStatusReconcileRejectsStale(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newWakeupTestModel()
	m.session = s
	m.reconcileStatus()
	request := m.statusOwner
	snap := m.statusSnapshot
	if !snap.Known {
		t.Fatal("startup not reconciled")
	}
	old := snap
	old.Execution.Revision = 0
	m.statusExecutionRevision = 1
	if m.acceptStatus(request, old) {
		t.Fatal("accepted older execution revision")
	}
	m.statusExecutionRevision = snap.Execution.Revision
	m.statusScheduleRevision = 1
	old = snap
	old.Schedule.Revision = 0
	if m.acceptStatus(request, old) {
		t.Fatal("accepted older schedule revision")
	}
	m.statusScheduleRevision = snap.Schedule.Revision
	old = snap
	old.Execution.Generation++
	if m.acceptStatus(request, old) {
		t.Fatal("accepted wrong chat generation")
	}
	m.resetSchedules()
	if got := m.activitySummary(time.Now()); got.Primary == "Ready for input" {
		t.Fatal("reset retained cached readiness")
	}
	if m.acceptStatus(request, snap) {
		t.Fatal("accepted in-flight result after reset")
	}
}
func TestStatusPendingFirstCollection(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := newWakeupTestModel()
	m.session = s
	m.invalidateStatus()
	m.reconcileStatus()
	if got := m.activitySummary(time.Now()); got.Primary == "Ready for input" {
		t.Fatal("first collection cleared unresolved input")
	}
}
func TestStatusRefreshChain(t *testing.T) {
	m := newWakeupTestModel()
	next, cmd := m.Update(statusRefreshMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("idle refresh stopped")
	}
	next, cmd = m.Update(statusRefreshMsg{})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("duplicate tick forked chain")
	}
	m.quitting = true
	_, cmd = m.Update(statusRefreshMsg{m.statusTickSequence})
	if cmd != nil {
		t.Fatal("quit rearmed refresh")
	}
}

func TestStatusPendingUnrelatedRevision(t *testing.T) {
	m := newWakeupTestModel()
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m.session = s
	m.reconcileStatus()
	m.loading = true
	m.invalidateStatus()
	fresh := m.statusSnapshot
	fresh.Known = true
	fresh.Coherent = true
	fresh.Execution.Revision++
	if m.acceptStatus(m.statusOwner, fresh) {
		t.Fatal("unrelated revision restored ready before queued execution began")
	}
}

// The HTTP provider blocks only on channels: the same production turn is
// observed once in flight and once after settlement, or entirely between polls.
func TestStatusPendingProductionExecution(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(fmt.Sprint("fast=", fast), func(t *testing.T) {
			entered, release := make(chan struct{}, 1), make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.NotFound(w, r)
					return
				}
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-ctx.Done():
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			s, err := chat.NewSession(ctx, types.Config{Model: "test", APIKey: "test", BaseURL: server.URL + "/v1", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 2, MaxAttempts: 1, MaxRetries: 1}}, chat.Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			m := newWakeupTestModel()
			m.session = s
			m.reconcileStatus()
			before := s.ActivitySnapshot().RootExecutionSequence
			m.loading = true
			m.syncActivityPhase(time.Now())
			m.reconcileStatus()
			if got := m.activitySummary(time.Now()); !got.Updating || got.CountsKnown {
				t.Fatal("pending input", got)
			}
			done := make(chan error, 1)
			go func() { _, err := s.SendMessage("hello"); done <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if !fast {
				m.reconcileStatus()
				if got := m.activitySummary(time.Now()); got.Primary != "Working" || got.Updating {
					t.Fatal("active", got)
				}
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
			m.reconcileStatus()
			got := m.activitySummary(time.Now())
			if got.Primary != "Ready for input" || !got.CountsKnown || m.statusPending {
				t.Fatal("settled", got)
			}
			if s.ActivitySnapshot().RootExecutionSequence <= before {
				t.Fatal("production execution not acknowledged")
			}
		})
	}
}

func TestStatusStartupDoesNotAwaitExecution(t *testing.T) {
	m := newWakeupTestModel()
	m.sessionReady = false
	m.syncActivityPhase(time.Now())
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m.session = s
	m.sessionReady = true
	m.reconcileStatus()
	if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" {
		t.Fatal("startup waits for a nonexistent turn", got)
	}
}
