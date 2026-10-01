package chat

import (
	"sync"
	"sync/atomic"
	"time"
)

// ObservationScope identifies the originating run and foreground segment.
// This is consumer-owned metadata: chat leaves it zero. Bind Generation to the
// captured run, and select Epoch at the serialized receipt boundary using the
// ordered parked/resumed observations, not later when draining UI messages.
type ObservationScope struct{ Generation, Epoch uint64 }

// Observation contains receipt metadata only, never model or tool payloads.
// A known empty Owner is root. Unknown ownership must not refresh root ages.
type Observation struct {
	// RunID is process-local, nonzero and immutable for one SendMessage,
	// including retries, park/resume and callbacks retained by children.
	// It is not a persisted session ID or a foreground epoch.
	RunID       uint64
	Scope       ObservationScope
	Owner       string
	OwnerKnown  bool
	Kind        string
	Received    time.Time
	Order       uint64
	OperationID string
	HasText     bool
}

var observationRunID atomic.Uint64

type observationEmitter struct {
	runID uint64
	mu    sync.Mutex
	order uint64
	now   func() time.Time
	emit  func(Observation)
}

func newObservationEmitter(emit func(Observation), now func() time.Time) *observationEmitter {
	return &observationEmitter{runID: observationRunID.Add(1), emit: emit, now: now}
}

// record serializes timestamp, order and sink delivery. The sink must be short,
// nonblocking and non-reentrant. Order is per RunID, including child receipts;
// it is not a lifecycle watermark or evidence that a tool executed.
func (e *observationEmitter) record(owner string, known bool, kind, id string, text bool) {
	if e.emit == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.order++
	e.emit(Observation{RunID: e.runID, Owner: owner, OwnerKnown: known, Kind: kind, OperationID: id, HasText: text, Received: e.now(), Order: e.order})
}

func (e *observationEmitter) stream(ev StreamEvent) {
	// Background streams collapse their type to sub_agent upstream. Their
	// payload may be a tool result, so it cannot establish model-text receipt.
	known := ev.AgentID != "" || ev.Kind == "content" || ev.Kind == "reasoning" || ev.Kind == "tool_call"
	kind := "stream event received"
	text := (ev.Kind == "content" || ev.Kind == "reasoning") && ev.Content != ""
	if text {
		kind = "model text received"
	}
	if ev.Kind == "tool_call" {
		kind = "tool-call fragment received"
	}
	e.record(ev.AgentID, known, kind, "", text)
}
