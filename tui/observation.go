package tui

import (
	"time"

	"github.com/mudler/nib/chat"
)

// receiptState is constant-size metadata, not a lifecycle registry. Missing
// text is unknown: boundary callbacks lack identity, background streams lose
// their kind, and finished-child resume does not provide text callbacks.
type receiptState struct {
	Unknown   bool
	Latest    chat.Observation
	Text      time.Time
	textOrder uint64
}

func (s *receiptState) accept(o chat.Observation) {
	if o.HasText && o.Order > s.textOrder {
		s.Text, s.textOrder = o.Received, o.Order
	}
	if o.Order > s.Latest.Order {
		s.Latest = o
	}
}

// observationCallback captures generation once per SendMessage. Chat serializes
// delivery (including park/resume) before public lifecycle callbacks. Only this
// receipt boundary assigns epochs; UI mailbox drain time never stamps metadata.
// No callback invokes chat, waits for UI work, or sends on a blocking channel.
func (q *toolEventQueue) observationCallback() func(chat.Observation) {
	q.mu.Lock()
	generation := q.gen
	q.mu.Unlock()
	var bound bool
	var runID, order uint64 // protected by q.mu, including concurrent test sinks
	return func(o chat.Observation) {
		q.mu.Lock()
		defer q.mu.Unlock()
		if !bound {
			runID, bound = o.RunID, true
		}
		if o.RunID != runID || o.Order <= order {
			return
		}
		order = o.Order
		if o.OwnerKnown && o.Owner != "" {
			slot, ok := q.childReceipts[o.Owner]
			if !ok || slot.generation != generation || !slot.active {
				return
			}
			o.Scope = chat.ObservationScope{Generation: generation, Epoch: slot.epoch}
			slot.receipt.accept(o)
			q.childReceipts[o.Owner] = slot
		} else {
			if !q.active || q.gen != generation {
				return
			}
			if o.Scope != (chat.ObservationScope{}) && o.Scope != (chat.ObservationScope{Generation: generation, Epoch: q.epoch}) {
				return
			}
			if o.OwnerKnown && o.Kind == "resumed" && q.parked {
				q.epoch++
				q.parked = false
				q.receipt = receiptState{}
			} else if q.parked {
				return
			}
			o.Scope = chat.ObservationScope{Generation: generation, Epoch: q.epoch}
			if !o.OwnerKnown {
				// Preserve uncertainty independently, never erase a verified root clock.
				q.receipt.Unknown = true
			} else {
				q.receipt.accept(o)
				if o.Kind == "parked" {
					q.parked = true
				}
			}
		}
		select {
		case q.ready <- struct{}{}:
		default:
		}
	}
}

type childReceipt struct {
	generation uint64
	epoch      uint64
	active     bool
	receipt    receiptState
}

func (q *toolEventQueue) rootObservation() receiptState {
	if q == nil {
		return receiptState{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.receipt
}

func (q *toolEventQueue) retainChild(ev chat.AgentEvent) {
	if q == nil || ev.ID == "" || ev.ObservationScope == (chat.ObservationScope{}) {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.childReceipts == nil {
		q.childReceipts = make(map[string]childReceipt)
	}
	slot, ok := q.childReceipts[ev.ID]
	if ok && slot.generation != ev.ObservationScope.Generation {
		return // another run cannot close or claim this retained owner
	}
	if !ok || (!slot.active && ev.Status == chat.AgentStatusRunning) {
		slot = childReceipt{generation: ev.ObservationScope.Generation, epoch: ev.ObservationScope.Epoch}
	}
	slot.active = ev.Status == chat.AgentStatusRunning
	q.childReceipts[ev.ID] = slot
}

func (m Model) childObservation(id string) receiptState {
	if m.toolEvents == nil {
		return receiptState{}
	}
	m.toolEvents.mu.Lock()
	defer m.toolEvents.mu.Unlock()
	return m.toolEvents.childReceipts[id].receipt
}

// syncChildRetention shares the UI job owner retention policy. Observations
// cannot allocate owners, and removed jobs cannot leave orphan metadata.
func (q *toolEventQueue) syncChildRetention(jobs []agentJob) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for id := range q.childReceipts {
		found := false
		for _, j := range jobs {
			if j.ID == id {
				found = true
				break
			}
		}
		if !found {
			delete(q.childReceipts, id)
		}
	}
}

// agentCallbacks captures immutable origin before chat starts the run. Keep it
// on the existing agent channel: registration and eviction still follow jobs,
// without a second lifecycle queue or an unbounded pending-owner map.
func (m Model) agentCallbacks() func(chat.AgentEvent) {
	m.toolEvents.mu.Lock()
	scope := chat.ObservationScope{Generation: m.toolEvents.gen, Epoch: m.toolEvents.epoch}
	m.toolEvents.mu.Unlock()
	return func(ev chat.AgentEvent) {
		ev.ObservationScope = scope
		select {
		case m.agentEventChan <- ev:
		default:
		}
	}
}
