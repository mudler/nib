package tui

import (
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"strings"
	"testing"
	"time"
)

func TestObservationSummaryPriorityAndAge(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q, loading: true}
	now := time.Unix(100, 0)
	emit := q.observationCallback()
	emit(chat.Observation{OwnerKnown: true, Kind: "content", HasText: true, Received: now, Order: 1})
	if got := m.activitySummary(now.Add(12500 * time.Millisecond)); got.Primary != "Working" || got.Secondary != "last model text received 12s ago" {
		t.Fatal(got)
	}
	m.awaitingApproval = true
	m.loading = false
	if got := m.activitySummary(now); got.Primary != "Approval needed" {
		t.Fatal(got)
	}
	m.interruptArmed = true
	if got := m.activitySummary(now); got.Primary != "Interrupting foreground" {
		t.Fatal(got)
	}
	emit(chat.Observation{OwnerKnown: true, Kind: "tool completed", Received: now.Add(time.Second), Order: 2})
	if !q.rootObservation().Text.Equal(now) {
		t.Fatal("tool result changed text clock")
	}
	if got := m.activitySummary(now.Add(2 * time.Second)); got.Secondary != "tool completed 1s ago" {
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
	for _, want := range []string{"latest event: tool completed 1s ago", "last model text received 2s ago"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	m.interruptArmed = true
	summary := m.activitySummary(now.Add(3 * time.Second))
	if summary.Primary != "Interrupting foreground" || summary.Secondary != "last model text received 3s ago" || summary.Counts != "background: agents 1, shell 0" {
		t.Fatal(summary)
	}
	got = m.jobActivityTailAt(jobRef{Kind: "agent", ID: "a2"}, now)
	if !strings.Contains(got, "latest event age: unknown") || !strings.Contains(got, "last model text age: unknown") {
		t.Fatal(got)
	}
	m.toolEvents.end()
	emit(chat.Observation{Order: 4, OwnerKnown: true, HasText: true, Received: now.Add(time.Hour)})
	if got := m.activitySummary(now.Add(4 * time.Second)).Secondary; got != "last model text received 4s ago" {
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
	emit(chat.Observation{Order: 1, OwnerKnown: true, HasText: true, Received: now})
	emit(chat.Observation{Order: 2, Kind: "Compacting conversation", Received: now.Add(time.Hour)})
	if got := m.activitySummary(now.Add(time.Second)); got.Primary != "Working" || got.Secondary != "last model text received 1s ago" {
		t.Fatal(got)
	}
	q.end()
	q.begin()
	emit(chat.Observation{Order: 3, OwnerKnown: true, HasText: true, Received: now.Add(time.Hour)})
	if got := m.activitySummary(now); got.Secondary != "last model text age: unknown" {
		t.Fatal(got)
	}
	// The callback contract lacks complete text attribution even in a fresh epoch.
	// A restored model and a new run must not invent either zero or "not yet".
	if got := (Model{}).activitySummary(now); got.Primary != "Ready" || got.Secondary != "last model text age: unknown" {
		t.Fatal(got)
	}
}
