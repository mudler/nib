package chat

import (
	"sort"
	"sync/atomic"
)

var activityGeneration atomic.Uint64

// ActivitySnapshot is a caller-owned view of one session's execution state.
type ActivitySnapshot struct {
	Generation, Revision   uint64
	Known, Coherent        bool
	RootActive, RootParked bool
	Agents, Shells         []ActivityJob
	Barrier                CompletionSnapshot
	ReadyAllowed           bool
}
type ActivityJob struct {
	ID                  string
	Running, Background bool
}
type CompletionSnapshot struct {
	Known                                      bool
	Publishers, QueuedNotices, ReservedNotices int
	EventSequence, RootObservedSequence        uint64
	SupervisorQueued, SupervisorReviewing      bool
}

// ActivitySnapshot reads only the session ledger. No provider, job pointer,
// output buffer or notification delivery is involved. The ledger is the sole
// synchronization domain; returned slices never alias it.
func (s *Session) ActivitySnapshot() ActivitySnapshot {
	if s == nil {
		return ActivitySnapshot{}
	}
	s.shellEventsMu.Lock()
	b := s.background
	s.shellEventsMu.Unlock()
	if b == nil {
		return ActivitySnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a := ActivitySnapshot{Generation: b.generation, Revision: b.revision}
	if b.closed || b.internalErr != nil {
		return a
	}
	a.Known, a.Coherent = true, true
	a.RootActive, a.RootParked = b.rootActive && !b.rootParked, b.rootParked
	c := CompletionSnapshot{Known: true, Publishers: len(b.publishers), EventSequence: b.eventSequence, RootObservedSequence: b.rootObservedSequence, SupervisorQueued: b.reviewQueued, SupervisorReviewing: b.reviewing}
	for _, n := range b.notices {
		if n.state == noticeQueued {
			c.QueuedNotices++
		}
		if n.state == noticeReserved {
			c.ReservedNotices++
		}
	}
	a.Barrier = c
	jobs := make(map[string]ActivityJob, len(b.children)+len(b.agents))
	for id, j := range b.children {
		jobs[id] = j
	}
	for id, state := range b.agents {
		jobs[id] = ActivityJob{ID: id, Running: state == backgroundRunning, Background: true}
	}
	running := false
	for _, j := range jobs {
		a.Agents = append(a.Agents, j)
		running = running || j.Running
	}
	for id, state := range b.shells {
		j := ActivityJob{ID: id, Running: state == backgroundRunning, Background: true}
		a.Shells = append(a.Shells, j)
		running = running || j.Running
	}
	sort.Slice(a.Agents, func(i, j int) bool { return a.Agents[i].ID < a.Agents[j].ID })
	sort.Slice(a.Shells, func(i, j int) bool { return a.Shells[i].ID < a.Shells[j].ID })
	// Interruption is not success, but a settled interrupted turn is available
	// once the unchanged delivery/observation obligations have cleared.
	a.ReadyAllowed = !b.rootActive && !b.rootParked && !running && c.Publishers == 0 && c.QueuedNotices == 0 && c.ReservedNotices == 0 && c.EventSequence == c.RootObservedSequence && !c.SupervisorQueued && !c.SupervisorReviewing
	return a
}

// publishUnlock is used only at writer boundaries, including supervisor and
// notice transitions which do not advance the completion event sequence.
func (b *backgroundState) publishUnlock() {
	if b.revision == ^uint64(0) {
		b.internalErr = errBackgroundSequenceOverflow
	} else {
		b.revision++
	}
	b.mu.Unlock()
}

func (b *backgroundState) childActivity(id string, running, background bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.publishUnlock()
	if b.closed {
		return
	}
	if b.children == nil {
		b.children = make(map[string]ActivityJob)
	}
	b.children[id] = ActivityJob{ID: id, Running: running, Background: background}
}
