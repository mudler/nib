package tui

import (
	"context"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// A parked notification is an authoritative lifecycle boundary. This catches
// the former omission where Update changed parked/loading but left the footer
// carrying the preceding Working phase and its elapsed time.
func TestPhaseSynchronizationAtParkBoundary(t *testing.T) {
	m := frameModel()
	m.loading = true
	m.syncActivityPhase(time.Now().Add(-time.Minute))

	next, _ := m.Update(parkMsg{parked: true, reply: "done"})
	got := next.(Model)
	if got.activityPhase.state != phaseParked {
		t.Fatalf("phase = %+v, want Parked", got.activityPhase)
	}
	if got.phaseStartedAt.IsZero() {
		t.Fatal("Parked phase start was not recorded")
	}
}

// A queued follow-up released by the park handler is a second authoritative
// transition in the same update. This catches synchronizing Parked too early
// and then overwriting the Working phase established by releaseQueueFront.
func TestPhaseSynchronizationAtParkBoundaryWithQueuedFollowUp(t *testing.T) {
	s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	setSessionRunLiveForPhaseTest(s)

	m := frameModel()
	m.session = s
	m.loading = true
	m.queue = []string{"follow up"}
	m.syncActivityPhase(time.Now().Add(-time.Minute))

	next, _ := m.Update(parkMsg{parked: true, reply: "done"})
	got := next.(Model)
	if got.activityPhase.state != phaseWorking || !got.loading || got.parked {
		t.Fatalf("phase/state = %+v loading=%v parked=%v, want Working", got.activityPhase, got.loading, got.parked)
	}
	if len(got.queue) != 0 {
		t.Fatalf("queued follow-up was not released: %v", got.queue)
	}
}

// Session intentionally exposes RunLive as read-only. This test needs a live
// run without starting a provider goroutine, so it changes only that private
// test fixture bit; all queue and Update behavior under test remains real.
func setSessionRunLiveForPhaseTest(s *chat.Session) {
	v := reflect.ValueOf(s).Elem().FieldByName("runLive")
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().SetBool(true)
}
