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

func TestListBackgroundedFlagAndOutput(t *testing.T) {
	jobs := NewShellJobs()

	// A bash_background-style job is Backgrounded.
	bg := jobs.mgr.launch(context.Background(), "echo HELLO", false)
	// A plain foreground job (never detached) is not.
	fg := jobs.mgr.launch(context.Background(), "echo FG", true)
	waitJob(t, bg)
	waitJob(t, fg)

	infos := map[string]ShellJobInfo{}
	for _, i := range jobs.List() {
		infos[i.ID] = i
	}
	if !infos[bg.id].Backgrounded {
		t.Fatal("background job should be Backgrounded")
	}
	if infos[fg.id].Backgrounded {
		t.Fatal("plain foreground job should not be Backgrounded")
	}

	if so, _, ok := jobs.Output(bg.id); !ok || !strings.Contains(so, "HELLO") {
		t.Fatalf("Output stdout=%q ok=%v", so, ok)
	}
	if _, _, ok := jobs.Output("bg-nope"); ok {
		t.Fatal("Output should report ok=false for an unknown job")
	}

	// A foreground job the user backgrounds (Ctrl+B) becomes Backgrounded.
	d := jobs.mgr.launch(context.Background(), "sleep 0.2; echo D", true)
	if id, ok := jobs.DetachForeground(); !ok || id != d.id {
		t.Fatalf("DetachForeground = (%q, %v), want (%q, true)", id, ok, d.id)
	}
	waitJob(t, d)
	for _, i := range jobs.List() {
		if i.ID == d.id && !i.Backgrounded {
			t.Fatal("a detached foreground job should be Backgrounded")
		}
	}
}

func TestShellJobsHasRunning(t *testing.T) {
	jobs := NewShellJobs()
	if jobs.HasRunning() {
		t.Fatal("an empty registry should report no running jobs")
	}
	j := jobs.mgr.launch(context.Background(), "sleep 0.3", false)
	if !jobs.HasRunning() {
		t.Fatal("a registry with a running job should report HasRunning")
	}
	waitJob(t, j)
	if jobs.HasRunning() {
		t.Fatal("once the job finishes, HasRunning should be false")
	}
	// nil receiver is safe.
	var nilJobs *ShellJobs
	if nilJobs.HasRunning() {
		t.Fatal("nil registry should report no running jobs")
	}
}

func TestSetOnJobDoneFires(t *testing.T) {
	jobs := NewShellJobs()
	done := make(chan ShellJobInfo, 4)
	jobs.SetOnJobDone(func(info ShellJobInfo) { done <- info })

	// A bash_background job is backgrounded.
	bg := jobs.mgr.launch(context.Background(), "echo HELLO", false)
	select {
	case info := <-done:
		if info.ID != bg.id {
			t.Fatalf("callback id = %q, want %q", info.ID, bg.id)
		}
		if !info.Backgrounded {
			t.Fatal("a bash_background job should be reported Backgrounded")
		}
		if info.Status != "completed" {
			t.Fatalf("status = %q, want completed", info.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion callback did not fire for a background job")
	}

	// A plain foreground job (never detached) also fires, but is not Backgrounded;
	// the session-level handler is responsible for ignoring it.
	fg := jobs.mgr.launch(context.Background(), "echo FG", true)
	select {
	case info := <-done:
		if info.ID != fg.id {
			t.Fatalf("callback id = %q, want %q", info.ID, fg.id)
		}
		if info.Backgrounded {
			t.Fatal("a plain foreground job should not be Backgrounded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion callback did not fire for a foreground job")
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
