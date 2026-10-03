package chat

import (
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

var errBackgroundSequenceOverflow = errors.New("background event sequence overflow")

type backgroundSource uint8

const (
	backgroundAgent backgroundSource = iota + 1
	backgroundShell
)

type backgroundLifecycle uint8

const (
	backgroundRunning backgroundLifecycle = iota + 1
	backgroundSucceeded
	backgroundFailed
)

type noticeState uint8

const (
	noticeQueued noticeState = iota + 1
	noticeReserved
	noticeConsumed
)

type sequencedNotice struct {
	sequence uint64
	source   backgroundSource
	identity string
	content  string
	state    noticeState
	reserve  uint64
}

type goalLifecycle uint8

const (
	goalInactive goalLifecycle = iota
	goalActive
	goalPaused
	goalDone
)

// terminalSnapshot is computed while holding the ledger's sole mutex. eligible
// is deliberately false on interruption, closure, or an internal invariant
// failure; those conditions are abnormal exits rather than successful terminal
// states.
type terminalSnapshot struct {
	eligible             bool
	eventSequence        uint64
	rootObservedSequence uint64
	runningAgents        int
	runningShells        int
	activePublishers     int
	queuedNotices        int
	reservedNotices      int
	supervisorQueued     bool
	supervisorReviewing  bool
	interrupted          bool
	closed               bool
	internalError        error
}

type backgroundState struct {
	mu sync.Mutex

	eventSequence        uint64
	rootObservedSequence uint64
	agents               map[string]backgroundLifecycle
	shells               map[string]backgroundLifecycle
	publishers           map[string]struct{}
	notices              []*sequencedNotice
	nextReservation      uint64

	rootActive     bool
	rootRequesting bool
	rootParked     bool

	goalIdentity  uint64
	goalLifecycle goalLifecycle

	supervisorGeneration uint64
	scheduleIndex        int
	timer                goalTimer
	reviewQueued         bool
	reviewing            bool

	interrupted bool
	closed      bool
	internalErr error
}

func newBackgroundState() *backgroundState {
	return &backgroundState{
		agents:     make(map[string]backgroundLifecycle),
		shells:     make(map[string]backgroundLifecycle),
		publishers: make(map[string]struct{}),
	}
}

func (s *backgroundState) advanceLocked() (uint64, bool) {
	if s.internalErr != nil || s.closed {
		return s.eventSequence, false
	}
	if s.eventSequence == math.MaxUint64 {
		s.internalErr = errBackgroundSequenceOverflow
		s.stopTimerLocked()
		return s.eventSequence, false
	}
	s.eventSequence++
	return s.eventSequence, true
}

func (s *backgroundState) lifecycleMap(source backgroundSource) map[string]backgroundLifecycle {
	if source == backgroundShell {
		return s.shells
	}
	return s.agents
}

// startBackground records an identity once. Duplicate and stale starts are no-ops.
func (s *backgroundState) startBackground(source backgroundSource, identity string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := s.lifecycleMap(source)
	if _, exists := states[identity]; exists {
		return false
	}
	if _, ok := s.advanceLocked(); !ok {
		return false
	}
	states[identity] = backgroundRunning
	s.backgroundChangedLocked()
	return true
}

// completeBackground atomically advances the event sequence, leaves the running
// set, and durably queues a completion notice. Only a matching running identity
// can transition, making repeated terminal callbacks harmless.
func (s *backgroundState) completeBackground(source backgroundSource, identity, content string, success bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := s.lifecycleMap(source)
	if states[identity] != backgroundRunning {
		return false
	}
	sequence, ok := s.advanceLocked()
	if !ok {
		return false
	}
	if success {
		states[identity] = backgroundSucceeded
	} else {
		states[identity] = backgroundFailed
	}
	s.notices = append(s.notices, &sequencedNotice{
		sequence: sequence, source: source, identity: identity, content: content, state: noticeQueued,
	})
	s.backgroundChangedLocked()
	return true
}

func (s *backgroundState) beginPublisher(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.internalErr != nil {
		return false
	}
	if _, exists := s.publishers[key]; exists {
		return false
	}
	s.publishers[key] = struct{}{}
	return true
}

func (s *backgroundState) endPublisher(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.publishers[key]; !exists {
		return false
	}
	delete(s.publishers, key)
	return true
}

// reserveNotices selects every queued notice when limit is non-positive, or at
// most limit notices otherwise. The returned values are copies and may safely be
// used after the lock is released.
func (s *backgroundState) reserveNotices(limit int) (uint64, []sequencedNotice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, notice := range s.notices {
		if notice.state == noticeQueued && (limit <= 0 || count < limit) {
			count++
		}
	}
	if count == 0 || s.internalErr != nil || s.closed {
		return 0, nil
	}
	if s.nextReservation == math.MaxUint64 {
		s.internalErr = errBackgroundSequenceOverflow
		s.stopTimerLocked()
		return 0, nil
	}
	s.nextReservation++
	reservation := s.nextReservation
	result := make([]sequencedNotice, 0, count)
	for _, notice := range s.notices {
		if notice.state != noticeQueued || (limit > 0 && len(result) >= limit) {
			continue
		}
		notice.state = noticeReserved
		notice.reserve = reservation
		result = append(result, *notice)
	}
	return reservation, result
}

// rollbackNotices restores a whole reservation and globally re-establishes
// sequence order, including notices published while the request was assembled.
func (s *backgroundState) rollbackNotices(reservation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, notice := range s.notices {
		if notice.state == noticeReserved && notice.reserve == reservation {
			notice.state, notice.reserve, changed = noticeQueued, 0, true
		}
	}
	sort.SliceStable(s.notices, func(i, j int) bool { return s.notices[i].sequence < s.notices[j].sequence })
	return changed
}

// consumeNotices marks notices delivered only at the model-request handoff.
func (s *backgroundState) consumeNotices(reservation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, notice := range s.notices {
		if notice.state == noticeReserved && notice.reserve == reservation {
			notice.state, notice.reserve, changed = noticeConsumed, 0, true
		}
	}
	return changed
}

func (s *backgroundState) markRootObserved(sequence uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence > s.eventSequence {
		s.internalErr = errors.New("root observed future background sequence")
		s.stopTimerLocked()
		return false
	}
	if sequence > s.rootObservedSequence {
		s.rootObservedSequence = sequence
	}
	return true
}

func (s *backgroundState) setRoot(active, requesting, parked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasParked := s.rootParked
	s.rootActive, s.rootRequesting, s.rootParked = active, requesting, parked
	if wasParked && !parked {
		s.invalidateTimerLocked()
	}
}

func (s *backgroundState) setGoal(identity uint64, lifecycle goalLifecycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goalIdentity == identity && s.goalLifecycle == lifecycle {
		return
	}
	s.goalIdentity, s.goalLifecycle = identity, lifecycle
	s.invalidateSupervisorLocked()
}

func (s *backgroundState) setReviewing(reviewing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewing = reviewing
	if reviewing {
		s.reviewQueued = false
	}
}

func (s *backgroundState) interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupted = true
	s.invalidateSupervisorLocked()
}

func (s *backgroundState) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.invalidateSupervisorLocked()
}

func (s *backgroundState) terminalSnapshot() terminalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := terminalSnapshot{
		eventSequence: s.eventSequence, rootObservedSequence: s.rootObservedSequence,
		activePublishers: len(s.publishers), supervisorQueued: s.reviewQueued,
		supervisorReviewing: s.reviewing, interrupted: s.interrupted, closed: s.closed,
		internalError: s.internalErr,
	}
	for _, state := range s.agents {
		if state == backgroundRunning {
			snapshot.runningAgents++
		}
	}
	for _, state := range s.shells {
		if state == backgroundRunning {
			snapshot.runningShells++
		}
	}
	for _, notice := range s.notices {
		switch notice.state {
		case noticeQueued:
			snapshot.queuedNotices++
		case noticeReserved:
			snapshot.reservedNotices++
		}
	}
	snapshot.eligible = !snapshot.interrupted && !snapshot.closed && snapshot.internalError == nil &&
		snapshot.runningAgents == 0 && snapshot.runningShells == 0 && snapshot.activePublishers == 0 &&
		snapshot.queuedNotices == 0 && snapshot.reservedNotices == 0 &&
		!snapshot.supervisorQueued && !snapshot.supervisorReviewing &&
		snapshot.rootObservedSequence == snapshot.eventSequence
	return snapshot
}

func (s *backgroundState) runningLocked() bool {
	for _, state := range s.agents {
		if state == backgroundRunning {
			return true
		}
	}
	for _, state := range s.shells {
		if state == backgroundRunning {
			return true
		}
	}
	return false
}

func (s *backgroundState) backgroundChangedLocked() {
	s.scheduleIndex = 0
	s.invalidateTimerLocked()
	if s.goalLifecycle == goalActive && s.rootParked && s.runningLocked() {
		s.reviewQueued = true
	}
}

func (s *backgroundState) invalidateTimerLocked() {
	if s.supervisorGeneration != math.MaxUint64 {
		s.supervisorGeneration++
	} else {
		s.internalErr = errBackgroundSequenceOverflow
	}
	s.stopTimerLocked()
}

func (s *backgroundState) invalidateSupervisorLocked() {
	if s.supervisorGeneration != math.MaxUint64 {
		s.supervisorGeneration++
	} else {
		s.internalErr = errBackgroundSequenceOverflow
	}
	s.stopTimerLocked()
	s.reviewQueued = false
	s.reviewing = false
	s.scheduleIndex = 0
}

func (s *backgroundState) stopTimerLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// armGoalTimer replaces any prior timer. The callback durably queues one review
// only when its generation and goal still match, then sends a best-effort wake
// hint without blocking.
func (s *backgroundState) armGoalTimer(factory goalTimerFactory, delay time.Duration, wake chan<- struct{}) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopTimerLocked()
	if s.closed || s.interrupted || s.internalErr != nil || s.goalLifecycle != goalActive || !s.rootParked || !s.runningLocked() || s.reviewQueued || s.reviewing {
		return s.supervisorGeneration
	}
	if s.supervisorGeneration == math.MaxUint64 {
		s.internalErr = errBackgroundSequenceOverflow
		return s.supervisorGeneration
	}
	s.supervisorGeneration++
	generation := s.supervisorGeneration
	goal := s.goalIdentity
	s.timer = factory.AfterFunc(delay, func() {
		s.goalTimerFired(generation, goal, wake)
	})
	return generation
}

func (s *backgroundState) goalTimerFired(generation, goal uint64, wake chan<- struct{}) {
	s.mu.Lock()
	if s.closed || s.interrupted || s.internalErr != nil || generation != s.supervisorGeneration || goal != s.goalIdentity || s.goalLifecycle != goalActive || !s.rootParked || !s.runningLocked() || s.reviewQueued || s.reviewing {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	s.reviewQueued = true
	s.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}
