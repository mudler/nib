package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	if _, err := r.Add("*/5 * * * *", "/foo", true, true); err != nil { // durable
		t.Fatalf("add foo: %v", err)
	}
	if _, err := r.Add("0 9 * * *", "/bar", true, false); err != nil { // not durable — must NOT persist
		t.Fatalf("add bar: %v", err)
	}

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

func TestLoadPreservesCreated(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	if _, err := r.Add("*/5 * * * *", "/foo", true, true); err != nil {
		t.Fatalf("add foo: %v", err)
	}

	path := filepath.Join(t.TempDir(), "loops.json")
	if err := r.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Reload with a different clock to prove Created is restored, not recomputed.
	r2 := NewRegistry()
	r2.SetClock(func() time.Time { return tm("2026-06-07 12:00") })
	n, err := r2.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if n != 1 {
		t.Fatalf("loaded %d, want 1", n)
	}
	got := r2.List()
	if len(got) != 1 {
		t.Fatalf("expected 1 job, got %d", len(got))
	}
	if !got[0].Created.Equal(now) {
		t.Fatalf("Created = %v, want %v", got[0].Created, now)
	}
	if !strings.HasPrefix(got[0].ID, "loop-") {
		t.Fatalf("ID = %q, want non-empty loop-... value", got[0].ID)
	}
}

func TestLoadCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loops.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	r := NewRegistry()
	n, err := r.Load(path)
	if err == nil {
		t.Fatalf("expected error for corrupt JSON, got nil")
	}
	if n != 0 {
		t.Fatalf("loaded %d, want 0 on corrupt JSON", n)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	r := NewRegistry()
	n, err := r.Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || n != 0 {
		t.Fatalf("missing file: n=%d err=%v", n, err)
	}
}
