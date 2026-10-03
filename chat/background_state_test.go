package chat

import (
	"math"
	"testing"
)

func TestBackgroundLedgerDuplicateTransitionsAndMixedOrdering(t *testing.T) {
	s := newBackgroundState()
	if !s.startBackground(backgroundAgent, "a") || s.startBackground(backgroundAgent, "a") {
		t.Fatal("agent start was not idempotent")
	}
	if !s.startBackground(backgroundShell, "s") {
		t.Fatal("shell start failed")
	}
	if !s.completeBackground(backgroundShell, "s", "shell done", true) || s.completeBackground(backgroundShell, "s", "duplicate", true) {
		t.Fatal("shell completion was not idempotent")
	}
	if !s.completeBackground(backgroundAgent, "a", "agent failed", false) {
		t.Fatal("agent completion failed")
	}
	reservation, notices := s.reserveNotices(0)
	if reservation == 0 || len(notices) != 2 {
		t.Fatalf("reservation = %d, notices = %d", reservation, len(notices))
	}
	if notices[0].content != "shell done" || notices[1].content != "agent failed" || notices[0].sequence >= notices[1].sequence {
		t.Fatalf("notices out of event order: %#v", notices)
	}
}

func TestNoticeReservationRollbackToFrontAndBurst(t *testing.T) {
	s := newBackgroundState()
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		s.startBackground(backgroundAgent, id)
		s.completeBackground(backgroundAgent, id, id, true)
	}
	first, selected := s.reserveNotices(3)
	if len(selected) != 3 {
		t.Fatalf("selected %d notices", len(selected))
	}
	// Add later work before rolling the first request back.
	s.startBackground(backgroundShell, "late")
	s.completeBackground(backgroundShell, "late", "late", true)
	if !s.rollbackNotices(first) {
		t.Fatal("rollback did not find reservation")
	}
	allReservation, all := s.reserveNotices(0)
	if len(all) != 21 {
		t.Fatalf("burst truncated: got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].sequence >= all[i].sequence {
			t.Fatalf("sequence order broken at %d", i)
		}
	}
	if !s.consumeNotices(allReservation) {
		t.Fatal("consume failed")
	}
	if got := s.terminalSnapshot(); got.queuedNotices != 0 || got.reservedNotices != 0 {
		t.Fatalf("notices remain after handoff: %+v", got)
	}
}

func TestTerminalSnapshotPublisherObservedAndSupervisorStates(t *testing.T) {
	s := newBackgroundState()
	if !s.terminalSnapshot().eligible {
		t.Fatal("empty ledger should be terminal")
	}
	if !s.beginPublisher("agent:a") {
		t.Fatal("publisher did not enter")
	}
	if s.beginPublisher("agent:a") || s.terminalSnapshot().eligible {
		t.Fatal("publisher entry not keyed or did not block terminal state")
	}
	s.endPublisher("agent:a")

	s.startBackground(backgroundAgent, "a")
	s.completeBackground(backgroundAgent, "a", "done", true)
	reservation, notices := s.reserveNotices(0)
	s.consumeNotices(reservation)
	if s.terminalSnapshot().eligible {
		t.Fatal("stale observed sequence allowed terminal state")
	}
	if !s.markRootObserved(notices[len(notices)-1].sequence) || !s.terminalSnapshot().eligible {
		t.Fatal("observed sequence did not permit terminal state")
	}

	s.mu.Lock()
	s.reviewQueued = true
	s.mu.Unlock()
	if s.terminalSnapshot().eligible {
		t.Fatal("queued review allowed terminal state")
	}
	s.setReviewing(true)
	if s.terminalSnapshot().eligible {
		t.Fatal("reviewing allowed terminal state")
	}
	s.setReviewing(false)
	if !s.terminalSnapshot().eligible {
		t.Fatal("finished review did not permit terminal state")
	}
}

func TestBackgroundLedgerSequenceOverflowFailsClosed(t *testing.T) {
	s := newBackgroundState()
	s.mu.Lock()
	s.eventSequence = math.MaxUint64
	s.rootObservedSequence = math.MaxUint64
	s.mu.Unlock()
	if s.startBackground(backgroundAgent, "overflow") {
		t.Fatal("overflowing transition succeeded")
	}
	snapshot := s.terminalSnapshot()
	if snapshot.eventSequence != math.MaxUint64 || snapshot.internalError == nil || snapshot.eligible {
		t.Fatalf("overflow did not fail closed: %+v", snapshot)
	}
}
