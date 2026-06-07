package loop

import (
	"testing"
	"time"
)

func TestRegistryDueAndRecurring(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })

	job, err := r.Add("*/5 * * * *", "/foo", true, false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if job.ID == "" {
		t.Fatal("expected non-empty id")
	}
	if len(r.List()) != 1 {
		t.Fatalf("list: want 1, got %d", len(r.List()))
	}

	// Not yet due at 10:02.
	now = tm("2026-06-06 10:02")
	if due := r.Due(); len(due) != 0 {
		t.Fatalf("premature due: %d", len(due))
	}
	// Due at 10:05.
	now = tm("2026-06-06 10:05")
	due := r.Due()
	if len(due) != 1 || due[0].Prompt != "/foo" {
		t.Fatalf("due: %+v", due)
	}
	// Recurring → still present, rescheduled past 10:05.
	if len(r.List()) != 1 {
		t.Fatalf("recurring removed: %d", len(r.List()))
	}
	// Not due again immediately.
	if due := r.Due(); len(due) != 0 {
		t.Fatalf("double-fire: %d", len(due))
	}
}

func TestRegistryOneShotAndDelete(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })

	job, _ := r.Add("30 14 6 6 *", "/once", false, false)
	now = tm("2026-06-06 14:30")
	if due := r.Due(); len(due) != 1 {
		t.Fatalf("one-shot due: %d", len(due))
	}
	// One-shot auto-removed after firing.
	if len(r.List()) != 0 {
		t.Fatalf("one-shot not pruned: %d", len(r.List()))
	}

	j2, _ := r.Add("*/5 * * * *", "/x", true, false)
	if !r.Delete(j2.ID) {
		t.Fatal("delete returned false")
	}
	if r.Delete("nope") {
		t.Fatal("delete of unknown id returned true")
	}
	_ = job
}

func TestRegistryAddRejectsBadExpr(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Add("nonsense", "/x", true, false); err == nil {
		t.Fatal("expected error for bad expr")
	}
}
