package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitJob(t *testing.T, j *bgJob) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, _, _ := j.snapshot(); done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", j.id)
}

func TestBgJobCompletesAndCapturesOutput(t *testing.T) {
	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "echo HELLO_BG", false)
	waitJob(t, j)

	if got := j.stdout.String(); !strings.Contains(got, "HELLO_BG") {
		t.Fatalf("stdout = %q, want it to contain HELLO_BG", got)
	}
	if st := j.status(); st != "completed" {
		t.Fatalf("status = %q, want completed", st)
	}
	if _, code, _ := j.snapshot(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestBgJobFailureStatus(t *testing.T) {
	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "exit 3", false)
	waitJob(t, j)
	if st := j.status(); st != "failed" {
		t.Fatalf("status = %q, want failed", st)
	}
	if _, code, _ := j.snapshot(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestBgJobKill(t *testing.T) {
	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "sleep 30", false)
	if !mgr.kill(j.id) {
		t.Fatal("kill returned false for a known job")
	}
	waitJob(t, j)
	if st := j.status(); st == "completed" {
		t.Fatalf("a killed job should not report completed, got %q", st)
	}
	if mgr.kill("bg-nope") {
		t.Fatal("kill should return false for an unknown job")
	}
}

func TestBgJobListOrderAndGet(t *testing.T) {
	mgr := newBgJobManager()
	a := mgr.launch(context.Background(), "echo a", false)
	b := mgr.launch(context.Background(), "echo b", false)
	waitJob(t, a)
	waitJob(t, b)

	list := mgr.ordered()
	if len(list) != 2 || list[0].id != a.id || list[1].id != b.id {
		t.Fatalf("list order wrong: %+v", list)
	}
	if _, ok := mgr.get(a.id); !ok {
		t.Fatal("get should find a started job")
	}
	if _, ok := mgr.get("bg-nope"); ok {
		t.Fatal("get should not find an unknown job")
	}
}

// TestForegroundDetach simulates the Ctrl+B path: a foreground job is launched,
// detachForeground signals it, and it keeps running (then finishes) instead of
// being cancelled.
func TestForegroundDetach(t *testing.T) {
	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "sleep 0.3; echo DONE_BG", true)

	if !mgr.hasForeground() {
		t.Fatal("a running foreground job should be reported by hasForeground")
	}
	id, ok := mgr.detachForeground()
	if !ok || id != j.id {
		t.Fatalf("detachForeground = (%q, %v), want (%q, true)", id, ok, j.id)
	}
	// The detach channel should have been signalled (the bash handler selects on it).
	select {
	case <-j.detach:
	case <-time.After(time.Second):
		t.Fatal("detach channel was not signalled")
	}
	// Once detached it is no longer an eligible foreground target.
	if mgr.hasForeground() {
		t.Fatal("a detached job should not be reported as foreground")
	}
	if _, ok := mgr.detachForeground(); ok {
		t.Fatal("no foreground job should remain to detach")
	}

	// It keeps running to completion (not cancelled by the detach).
	waitJob(t, j)
	if st := j.status(); st != "completed" {
		t.Fatalf("detached job status = %q, want completed", st)
	}
	if got := j.stdout.String(); !strings.Contains(got, "DONE_BG") {
		t.Fatalf("detached job stdout = %q, want DONE_BG", got)
	}
}

func TestLockedBufferTruncates(t *testing.T) {
	var w lockedBuffer
	big := strings.Repeat("x", bgMaxOutput+100)
	n, _ := w.Write([]byte(big))
	if n != len(big) {
		t.Fatalf("Write reported %d, want %d (must consume all to avoid blocking)", n, len(big))
	}
	if got := w.String(); !strings.Contains(got, "truncated") {
		t.Fatal("oversized output should be marked truncated")
	}
}
