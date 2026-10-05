package tui

import (
	"context"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/types"
	"strings"
	"testing"
	"time"
)

func TestActivitySummaryCopyMatrixAndPriority(t *testing.T) {
	tests := []struct {
		name             string
		model            Model
		primary, compact string
	}{
		{"ready", Model{}, "Ready", "Ready"},
		{"working", Model{loading: true}, "Working", "Working"},
		{"named tool", Model{loading: true, running: []runningTool{{name: "  bash   -lc  "}}}, "Running bash -lc", "Running"},
		{"unnamed tool", Model{loading: true, running: []runningTool{{}}}, "Running tool", "Running"},
		{"tools", Model{loading: true, running: []runningTool{{name: "bash"}, {name: "read"}}}, "2 tools active", "Tools active"},
		{"parked", Model{parked: true}, "Parked", "Parked"},
		{"working over parked", Model{loading: true, parked: true}, "Working", "Working"},
		{"answer over tools", Model{awaitingAsk: true, running: []runningTool{{name: "bash"}}}, "Waiting for your answer", "Answer needed"},
		{"approval over answer", Model{awaitingApproval: true, awaitingAsk: true}, "Approval needed", "Approval"},
		{"interrupt over approval", Model{interruptArmed: true, awaitingApproval: true}, "Interrupting", "Interrupting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.model.activitySummary(time.Unix(100, 0))
			if got.Primary != tt.primary || got.Compact != tt.compact {
				t.Fatalf("summary=%+v want %q/%q", got, tt.primary, tt.compact)
			}
		})
	}
}

func TestPhaseIdentityResetsOnlyForDisplayedLifecycleChange(t *testing.T) {
	now := time.Unix(100, 0)
	m := Model{loading: true}
	m.syncActivityPhase(now)
	start := m.phaseStartedAt
	m.syncActivityPhase(now.Add(time.Second))
	_ = m.activitySummary(now.Add(2 * time.Second))
	if !m.phaseStartedAt.Equal(start) {
		t.Fatal("tick/projection reset phase")
	}
	m.running = []runningTool{{name: "bash"}}
	m.syncActivityPhase(now.Add(3 * time.Second))
	if !m.phaseStartedAt.Equal(now.Add(3 * time.Second)) {
		t.Fatal("working to running did not reset")
	}
	m.running[0].name = "read"
	m.syncActivityPhase(now.Add(4 * time.Second))
	if !m.phaseStartedAt.Equal(now.Add(4 * time.Second)) {
		t.Fatal("tool identity did not reset")
	}
	m.running = append(m.running, runningTool{name: "bash"})
	m.syncActivityPhase(now.Add(5 * time.Second))
	if !m.phaseStartedAt.Equal(now.Add(5 * time.Second)) {
		t.Fatal("tool count did not reset")
	}
	m.running = nil
	m.syncActivityPhase(now.Add(6 * time.Second))
	if got := m.activitySummary(now.Add(8 * time.Second)); got.Primary != "Working" || got.Secondary != "2s" {
		t.Fatalf("completion=%+v", got)
	}
}

func TestActivitySummaryOmitsUnknownAndClampsFuturePhaseStart(t *testing.T) {
	now := time.Unix(100, 0)
	m := Model{loading: true}
	if got := m.activitySummary(now).Secondary; got != "" {
		t.Fatalf("unknown start=%q", got)
	}
	m.syncActivityPhase(now.Add(time.Hour))
	if got := m.activitySummary(now).Secondary; got != "0s" {
		t.Fatalf("future start=%q", got)
	}
}

func TestPhaseSynchronizationAtLoopAndQueuedTurnBoundaries(t *testing.T) {
	now := time.Now()
	loopModel := newLoopTestModel()
	if cmd := loopModel.dispatchLoop("do it"); cmd == nil {
		t.Fatal("dispatchLoop did not start")
	}
	if loopModel.activityPhase.state != phaseWorking || loopModel.phaseStartedAt.Before(now) {
		t.Fatal("dispatchLoop did not synchronize Working")
	}

	startModel := newLoopTestModel()
	if cmd := startModel.startLoop(slash.Action{Kind: slash.KindLoopStart, Payload: "do it"}); cmd == nil {
		t.Fatal("startLoop did not start")
	}
	if startModel.activityPhase.state != phaseWorking || startModel.phaseStartedAt.Before(now) {
		t.Fatal("startLoop did not synchronize Working")
	}

	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	queued := newQueueTestModel()
	queued.session = s
	queued.queue = queuedTexts("follow up")
	if cmd := queued.flushQueueAsTurn(); cmd == nil {
		t.Fatal("flush did not start")
	}
	if queued.activityPhase.state != phaseWorking || queued.phaseStartedAt.Before(now) {
		t.Fatal("queued turn did not synchronize Working")
	}
}

func TestObservationSummaryPriorityAndAge(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q, loading: true}
	now := time.Unix(100, 0)
	m.syncActivityPhase(now)
	emit := q.observationCallback()
	emit(chat.Observation{OwnerKnown: true, Kind: "content", HasText: true, Received: now, Order: 1})
	if got := m.activitySummary(now.Add(12500 * time.Millisecond)); got.Primary != "Working" || got.Secondary != "13s" {
		t.Fatal(got)
	}
	m.awaitingApproval = true
	m.loading = false
	m.syncActivityPhase(now)
	if got := m.activitySummary(now); got.Primary != "Approval needed" {
		t.Fatal(got)
	}
	m.interruptArmed = true
	m.syncActivityPhase(now)
	if got := m.activitySummary(now); got.Primary != "Interrupting" {
		t.Fatal(got)
	}
	emit(chat.Observation{OwnerKnown: true, Kind: "tool completed", Received: now.Add(time.Second), Order: 2})
	if !q.rootObservation().Text.Equal(now) {
		t.Fatal("tool result changed text clock")
	}
	if got := m.activitySummary(now.Add(2 * time.Second)); got.Secondary != "2s" {
		t.Fatal(got)
	}
}

func TestObservationSummaryLifecycle(t *testing.T) {
	now := time.Unix(100, 0)
	m := Model{loading: true, status: "Compacting conversation", stepThought: 1, reasoningSince: now, messages: []ChatMessage{{Role: "reasoning", Content: "retained reasoning"}}}
	m.startTool(chat.ToolStart{ID: "t", Name: "bash"})
	if got := m.activitySummary(now).Primary; got != "Running bash" {
		t.Fatal(got)
	}
	m.finishTool(chat.ToolResult{ID: "t", Name: "bash"})
	for _, delta := range []time.Duration{0, time.Hour} {
		if got := m.activitySummary(now.Add(delta)).Primary; got != "Working" {
			t.Fatal(got)
		}
	}
	m.startTool(chat.ToolStart{ID: "t", Name: "bash"}) // terminal-before-start remains closed
	if got := m.activitySummary(now).Primary; got != "Working" {
		t.Fatal(got)
	}
	m.loading, m.parked = false, true
	if got := m.activitySummary(now).Primary; got != "Parked" {
		t.Fatal(got)
	}
	m.awaitingAsk = true
	if got := m.activitySummary(now).Primary; got != "Waiting for your answer" {
		t.Fatal(got)
	}
	m.awaitingAsk, m.parked = false, false
	if got := m.activitySummary(now).Primary; got != "Ready" {
		t.Fatal(got)
	}
}

func TestObservationSummaryMissingAndNamedAges(t *testing.T) {
	now := time.Now()
	for _, state := range []receiptState{{}, {Unknown: true}, {Latest: chat.Observation{Kind: "tool completed"}}} {
		if got := observationAge(state, now); got != "last model text age: unknown" {
			t.Fatal(got)
		}
	}
	for _, kind := range []string{"parked", "resumed", "tool requested", "tool-call fragment received", "stream event received"} {
		s := receiptState{Latest: chat.Observation{Kind: kind, Received: now}, Text: now.Add(-time.Minute)}
		if got := observationAge(s, now.Add(1900*time.Millisecond)); got != kind+" 1s ago" {
			t.Fatal(got)
		}
		if got := textReceiptAge(s, now); got != "last model text received 60s ago" {
			t.Fatal(got)
		}
	}
}

func TestObservationSummaryChildDetails(t *testing.T) {
	now := time.Unix(100, 0)
	m := newLogsModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m.jobs[0].Background = true
	m.applyAgentEvent(chat.AgentEvent{ID: "a1", Status: chat.AgentStatusRunning, Background: true, ObservationScope: chat.ObservationScope{Generation: m.toolEvents.gen, Epoch: m.toolEvents.epoch}})
	emit := m.toolEvents.observationCallback()
	emit(chat.Observation{Order: 1, OwnerKnown: true, HasText: true, Received: now})
	emit(chat.Observation{Order: 2, OwnerKnown: true, Owner: "a1", HasText: true, Received: now.Add(time.Second)})
	emit(chat.Observation{Order: 3, OwnerKnown: true, Owner: "a1", Kind: "tool completed", Received: now.Add(2 * time.Second)})
	got := m.jobActivityTailAt(jobRef{Kind: "agent", ID: "a1"}, now.Add(3*time.Second))
	for _, want := range []string{"event receipt: 1s ago", "last model text: 2s ago", "current phase start: unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	m.interruptArmed = true
	m.syncActivityPhase(now.Add(3 * time.Second))
	summary := m.activitySummary(now.Add(3 * time.Second))
	if summary.Primary != "Interrupting" || summary.Secondary != "0s" || summary.Counts != "background: agents 1, shell 0" {
		t.Fatal(summary)
	}
	got = m.jobActivityTailAt(jobRef{Kind: "agent", ID: "a2"}, now)
	if !strings.Contains(got, "event receipt: unavailable") || !strings.Contains(got, "last model text: unavailable") {
		t.Fatal(got)
	}
	m.toolEvents.end()
	emit(chat.Observation{Order: 4, OwnerKnown: true, HasText: true, Received: now.Add(time.Hour)})
	if got := m.activitySummary(now.Add(4 * time.Second)).Secondary; got != "1s" {
		t.Fatal(got)
	}
}

func TestObservationSummaryBackgroundCounts(t *testing.T) {
	agents := []agentJob{{Status: chat.AgentStatusRunning, Background: true}, {Status: chat.AgentStatusRunning}, {Status: chat.AgentStatusCompleted, Background: true}}
	shells := []wizmcp.ShellJobInfo{{Running: true, Backgrounded: true}, {Running: true}, {Backgrounded: true}}
	if got := backgroundCounts(agents, shells); got != "background: agents 1, shell 1" {
		t.Fatal(got)
	}
	if got := backgroundCounts(nil, nil); got != "" {
		t.Fatal(got)
	}
}

func TestObservationSummaryStaleAndUnknownMetadata(t *testing.T) {
	now := time.Unix(100, 0)
	q := newToolEventQueue()
	q.begin()
	emit := q.observationCallback()
	m := Model{toolEvents: q, loading: true}
	m.syncActivityPhase(now)
	emit(chat.Observation{Order: 1, OwnerKnown: true, HasText: true, Received: now})
	emit(chat.Observation{Order: 2, Kind: "Compacting conversation", Received: now.Add(time.Hour)})
	if got := m.activitySummary(now.Add(time.Second)); got.Primary != "Working" || got.Secondary != "1s" {
		t.Fatal(got)
	}
	q.end()
	q.begin()
	emit(chat.Observation{Order: 3, OwnerKnown: true, HasText: true, Received: now.Add(time.Hour)})
	if got := m.activitySummary(now); got.Secondary != "0s" {
		t.Fatal(got)
	}
	// Restored state without lifecycle provenance must not invent a duration.
	if got := (Model{}).activitySummary(now); got.Primary != "Ready" || got.Secondary != "" {
		t.Fatal(got)
	}
}
