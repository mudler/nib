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
	j := mgr.start(context.Background(), "echo HELLO_BG")
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
	j := mgr.start(context.Background(), "exit 3")
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
	j := mgr.start(context.Background(), "sleep 30")
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
	a := mgr.start(context.Background(), "echo a")
	b := mgr.start(context.Background(), "echo b")
	waitJob(t, a)
	waitJob(t, b)

	list := mgr.list()
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
