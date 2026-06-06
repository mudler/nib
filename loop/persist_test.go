package loop

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	r.Add("*/5 * * * *", "/foo", true, true) // durable
	r.Add("0 9 * * *", "/bar", true, false)  // not durable — must NOT persist

	path := filepath.Join(t.TempDir(), "loops.json")
	if err := r.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	r2 := NewRegistry()
	r2.SetClock(func() time.Time { return now })
	n, err := r2.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if n != 1 {
		t.Fatalf("loaded %d, want 1 (only durable)", n)
	}
	got := r2.List()
	if len(got) != 1 || got[0].Prompt != "/foo" || !got[0].Durable {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	r := NewRegistry()
	n, err := r.Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || n != 0 {
		t.Fatalf("missing file: n=%d err=%v", n, err)
	}
}
