package tui

import (
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestObservationOwnershipAndBarriers(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	now := time.Unix(100, 0)
	emit := q.observationCallback()
	emit(chat.Observation{OwnerKnown: true, Kind: "content", HasText: true, Received: now, Order: 1})
	emit(chat.Observation{OwnerKnown: false, Kind: "status", Received: now.Add(time.Second), Order: 2})
	emit(chat.Observation{OwnerKnown: true, Owner: "child", Kind: "content", HasText: true, Received: now.Add(2 * time.Second), Order: 3})
	root := q.rootObservation()
	if !root.Text.Equal(now) || !root.Latest.Received.Equal(now) {
		t.Fatal(root)
	}
	emit(chat.Observation{OwnerKnown: true, Kind: "parked", Order: 4})
	emit(chat.Observation{OwnerKnown: true, Kind: "resumed", Order: 5})
	if !q.rootObservation().Text.IsZero() {
		t.Fatal("resume did not reset text clock")
	}
	emit(chat.Observation{OwnerKnown: true, HasText: true, Received: now.Add(3 * time.Second), Order: 6})
	if !q.rootObservation().Text.Equal(now.Add(3 * time.Second)) {
		t.Fatal("same-run resumed text rejected")
	}
	q.end()
	q.begin()
	emit(chat.Observation{OwnerKnown: true, HasText: true, Received: now.Add(4 * time.Second), Order: 5})
	if !q.rootObservation().Text.IsZero() {
		t.Fatal("old run refreshed root")
	}
}

// Drive the same factory used by initSession, with receipt time supplied by a
// fake producer clock. Public park mailbox delivery must not reset it again.
func TestObservationReceiptEpochAndCoalescing(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	emit := q.observationCallback()
	now := time.Unix(100, 0)
	send := func(order uint64, kind string, text bool) {
		emit(chat.Observation{RunID: 42, Order: order, Kind: kind, OwnerKnown: true, HasText: text, Received: now})
	}
	send(1, "content", true)
	old := q.rootObservation()
	send(2, "parked", false)
	send(3, "resumed", false)
	now = now.Add(time.Second)
	send(4, "content", true)
	q.pushFor(q.generation(), toolEvent{park: &parkEvent{parked: true}})
	q.pushFor(q.generation(), toolEvent{park: &parkEvent{parked: false}})
	if got := q.rootObservation(); got.Latest.Scope.Epoch == old.Latest.Scope.Epoch || !got.Text.Equal(now) {
		t.Fatal(got)
	}
	emit(old.Latest)         // delayed metadata from the closed segment
	send(4, "content", true) // duplicate
	if len(q.ready) != 1 {
		t.Fatal("unbounded or missing notification")
	}
	<-q.ready
	if len(q.drain()) != 2 {
		t.Fatal("coalescing lost lifecycle")
	}
	now = now.Add(time.Second)
	send(5, "content", true)
	if len(q.ready) != 1 || !q.rootObservation().Text.Equal(now) {
		t.Fatal("wakeup or receipt lost after drain")
	}
	q.end()
	send(6, "resumed", false)
	send(7, "content", true)
	if q.rootObservation().Latest.Order != 5 {
		t.Fatal("terminal revived root")
	}
	q.begin()
	send(8, "resumed", false)
	send(9, "content", true)
	if !q.rootObservation().Text.IsZero() {
		t.Fatal("old run relabeled")
	}
}

func TestObservationChildrenRetentionAndTerminal(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q}
	m.applyAgentEvent(chat.AgentEvent{ObservationScope: chat.ObservationScope{Generation: q.gen, Epoch: q.epoch}, ID: "child", Status: chat.AgentStatusRunning})
	emit := q.observationCallback()
	now := time.Unix(100, 0)
	emit(chat.Observation{RunID: 1, Order: 1, OwnerKnown: true, HasText: true, Received: now})
	root := q.rootObservation()
	q.end()
	q.begin()
	emit(chat.Observation{RunID: 1, Order: 2, Owner: "child", OwnerKnown: true, HasText: true, Received: now.Add(time.Second)})
	if got := m.childObservation("child"); !got.Text.Equal(now.Add(time.Second)) || got.Latest.Scope.Generation != root.Latest.Scope.Generation {
		t.Fatal(got)
	}
	if !q.rootObservation().Text.IsZero() {
		t.Fatal("child refreshed root")
	}
	m.applyAgentEvent(chat.AgentEvent{ObservationScope: root.Latest.Scope, ID: "child", Status: chat.AgentStatusCompleted})
	emit(chat.Observation{RunID: 1, Order: 3, Owner: "child", OwnerKnown: true, HasText: true, Received: now.Add(2 * time.Second)})
	if m.childObservation("child").Latest.Order != 2 {
		t.Fatal("closed child revived")
	}
	for i := uint64(4); i < 1000; i++ {
		emit(chat.Observation{RunID: 1, Order: i, Owner: "unretained", OwnerKnown: true})
	}
	if len(q.childReceipts) != 1 {
		t.Fatal("unretained observations allocated metadata")
	}
	m.jobs = nil
	q.syncChildRetention(m.jobs)
	if len(q.childReceipts) != 0 {
		t.Fatal("orphan receipt retained")
	}
}

func TestObservationConcurrentSaturatedAndCancel(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q}
	m.applyAgentEvent(chat.AgentEvent{ObservationScope: chat.ObservationScope{Generation: q.gen, Epoch: q.epoch}, ID: "child", Status: chat.AgentStatusRunning})
	emit := q.observationCallback()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := uint64(1); i <= 10000; i++ {
			owner := ""
			if i%2 == 0 {
				owner = "child"
			}
			emit(chat.Observation{RunID: 1, Order: i, OwnerKnown: true, Owner: owner, Received: time.Unix(int64(i), 0), HasText: true})
		}
	}()
	for i := 0; i < 1000; i++ {
		q.rootObservation()
		m.childObservation("child")
		q.drain()
	}
	q.end()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receipt blocked on saturated notification or cancellation")
	}
	if len(q.ready) != 1 {
		t.Fatal("notifications not coalesced")
	}
	if m.childObservation("child").Latest.Order != 10000 {
		t.Fatal("root closure discarded surviving child")
	}
}

func TestObservationUILifecycle(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	q := m.toolEvents
	q.begin()
	callbacks := chat.Callbacks{ObservationCallbacks: q.observationCallback}
	emit := callbacks.ObservationCallbacks()
	now := time.Unix(100, 0)
	emit(chat.Observation{RunID: 77, Order: 1, OwnerKnown: true, HasText: true, Received: now})
	emit(chat.Observation{RunID: 77, Order: 2, OwnerKnown: true, Kind: "parked", Received: now})
	q.push(toolEvent{park: &parkEvent{parked: true}})
	m = update(m, toolEventsReadyMsg{})
	if !m.parked {
		t.Fatal("UI did not park")
	}
	emit(chat.Observation{RunID: 77, Order: 3, OwnerKnown: true, Kind: "resumed", Received: now})
	emit(chat.Observation{RunID: 77, Order: 4, OwnerKnown: true, HasText: true, Received: now.Add(time.Second)})
	q.push(toolEvent{park: &parkEvent{parked: false}})
	m = update(m, toolEventsReadyMsg{})
	if m.parked || !q.rootObservation().Text.Equal(now.Add(time.Second)) {
		t.Fatal("UI resume lost receipt")
	}
	m = update(m, responseMsg{content: "done"})
	emit(chat.Observation{RunID: 77, Order: 5, OwnerKnown: true, HasText: true, Received: now.Add(2 * time.Second)})
	if q.rootObservation().Latest.Order != 4 {
		t.Fatal("UI terminal acknowledgement admitted late receipt")
	}
}

// Agent lifecycle and root completion use separate UI channels. A queued spawn
// must not acquire the generation current when the UI finally reads it.
func TestObservationQueuedChildAcrossRootTurnover(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q, agentEventChan: make(chan chat.AgentEvent, 16)}
	callbacks := chat.Callbacks{ObservationCallbacks: q.observationCallback, AgentCallbacks: m.agentCallbacks}
	emit := callbacks.ObservationCallbacks()
	agent := callbacks.AgentCallbacks()
	now := time.Unix(100, 0)
	emit(chat.Observation{RunID: 1, Order: 1, OwnerKnown: true, HasText: true, Received: now})
	origin := q.rootObservation().Latest.Scope.Generation
	agent(chat.AgentEvent{ID: "child", Status: chat.AgentStatusRunning})
	q.end()
	q.begin()
	m.applyAgentEvent(<-m.agentEventChan)
	emit(chat.Observation{RunID: 1, Order: 2, Owner: "child", OwnerKnown: true, HasText: true, Received: now.Add(time.Second)})
	if got := m.childObservation("child"); !got.Text.Equal(now.Add(time.Second)) || got.Latest.Scope.Generation != origin {
		t.Fatalf("queued child lost origin receipt: %+v", got)
	}
	emit(chat.Observation{RunID: 1, Order: 3, OwnerKnown: true, HasText: true, Received: now.Add(2 * time.Second)})
	if !q.rootObservation().Text.IsZero() {
		t.Fatal("old root refreshed new run")
	}
}

func TestObservationChildOriginCapturedBeforeCallback(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	m := Model{toolEvents: q, agentEventChan: make(chan chat.AgentEvent, 16)}
	agent := m.agentCallbacks()
	emit := q.observationCallback()
	origin := q.generation()
	// Even the producer callback itself may outlive the foreground run.
	q.end()
	q.begin()
	agent(chat.AgentEvent{ID: "survivor", Status: chat.AgentStatusRunning})
	m.applyAgentEvent(<-m.agentEventChan)
	emit(chat.Observation{RunID: 1, Order: 1, Owner: "survivor", OwnerKnown: true, HasText: true, Received: time.Unix(101, 0)})
	if got := m.childObservation("survivor"); got.Latest.Scope.Generation != origin || got.Text.IsZero() {
		t.Fatal(got)
	}
	newer := q.observationCallback()
	newer(chat.Observation{RunID: 2, Order: 1, Owner: "survivor", OwnerKnown: true, HasText: true, Received: time.Unix(102, 0)})
	if m.childObservation("survivor").Text != time.Unix(101, 0) {
		t.Fatal("another run claimed child")
	}
	// Unknown lifecycle attribution does not authorize arbitrary child receipts.
	m.applyAgentEvent(chat.AgentEvent{ID: "unknown", Status: chat.AgentStatusRunning})
	newer(chat.Observation{RunID: 2, Order: 2, Owner: "unknown", OwnerKnown: true, HasText: true})
	if len(q.childReceipts) != 1 {
		t.Fatal("unattributed owner allocated receipt")
	}
	agent(chat.AgentEvent{ID: "survivor", Status: chat.AgentStatusCompleted})
	m.applyAgentEvent(<-m.agentEventChan)
	emit(chat.Observation{RunID: 1, Order: 2, Owner: "survivor", OwnerKnown: true, HasText: true, Received: time.Unix(103, 0)})
	if m.childObservation("survivor").Latest.Order != 1 {
		t.Fatal("terminal child revived")
	}
}
