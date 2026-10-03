package mcp

import (
	"context"
	"path/filepath"
	"strconv"
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

func TestShellJobLifecycleStartAndSuccess(t *testing.T) {
	jobs := NewShellJobs()
	events := make(chan ShellJobLifecycleEvent, 4)
	stop := jobs.ObserveLifecycle(func(event ShellJobLifecycleEvent) { events <- event })
	defer stop()
	job := jobs.mgr.launch(context.Background(), "echo lifecycle", false)
	want := []ShellJobLifecycleKind{ShellJobLifecycleStart, ShellJobLifecycleSuccess}
	for i, kind := range want {
		select {
		case event := <-events:
			if event.Kind != kind || event.Job.ID != job.id {
				t.Fatalf("event %d = %#v, want kind %q id %q", i, event, kind, job.id)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %q", kind)
		}
	}
}

func TestShellJobLifecycleFailureAndKill(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(*ShellJobs) *bgJob
		want ShellJobLifecycleKind
	}{
		{name: "failure", run: func(jobs *ShellJobs) *bgJob { return jobs.mgr.launch(context.Background(), "exit 7", false) }, want: ShellJobLifecycleFailure},
		{name: "kill", run: func(jobs *ShellJobs) *bgJob {
			job := jobs.mgr.launch(context.Background(), "sleep 30", false)
			if !jobs.Kill(job.id) {
				t.Fatal("Kill returned false")
			}
			return job
		}, want: ShellJobLifecycleKilled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := NewShellJobs()
			events := make(chan ShellJobLifecycleEvent, 4)
			stop := jobs.ObserveLifecycle(func(event ShellJobLifecycleEvent) { events <- event })
			defer stop()
			job := tt.run(jobs)
			for _, want := range []ShellJobLifecycleKind{ShellJobLifecycleStart, tt.want} {
				select {
				case event := <-events:
					if event.Kind != want || event.Job.ID != job.id {
						t.Fatalf("event = %#v, want %q for %s", event, want, job.id)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for %q", want)
				}
			}
		})
	}
}

func TestShellJobLifecycleDetachAndUnregister(t *testing.T) {
	jobs := NewShellJobs()
	events := make(chan ShellJobLifecycleEvent, 4)
	stop := jobs.ObserveLifecycle(func(event ShellJobLifecycleEvent) { events <- event })
	job := jobs.mgr.launch(context.Background(), "sleep 0.1", true)
	select {
	case event := <-events:
		t.Fatalf("foreground job emitted before detach: %#v", event)
	default:
	}
	if id, ok := jobs.DetachForeground(); !ok || id != job.id {
		t.Fatalf("DetachForeground = %q, %v", id, ok)
	}
	for _, want := range []ShellJobLifecycleKind{ShellJobLifecycleStart, ShellJobLifecycleSuccess} {
		select {
		case event := <-events:
			if event.Kind != want {
				t.Fatalf("event = %#v, want %q", event, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	stop()
	jobs.mgr.launch(context.Background(), "echo after-stop", false)
	select {
	case event := <-events:
		t.Fatalf("event after unregister: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
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

func TestLaunchRunsInConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	sj := NewShellJobsInDir(dir)
	j := sj.mgr.launch(context.Background(), "pwd", false)
	<-j.doneCh
	out := strings.TrimSpace(j.stdout.String())
	// macOS /var symlinks to /private/var; compare resolved paths.
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(out)
	if got != want {
		t.Fatalf("pwd = %q, want %q", got, want)
	}
}

func TestLaunchEmptyDirUsesProcessCwd(t *testing.T) {
	sj := NewShellJobs() // no dir → current behavior
	if sj.mgr.dir != "" {
		t.Fatalf("default manager dir = %q, want empty", sj.mgr.dir)
	}
}

func TestLockedBufferKeepsTail(t *testing.T) {
	var w lockedBuffer
	w.Write([]byte(strings.Repeat("a", bgMaxOutput)))
	w.Write([]byte("TAIL"))
	got := w.String()
	if !strings.HasSuffix(got, "TAIL") {
		t.Fatal("the rolling buffer must keep the newest output")
	}
	if !strings.HasPrefix(got, "…[earlier output truncated]") {
		t.Fatal("dropped output should be marked")
	}
}

func TestLockedBufferPageOffsetsSurviveRolling(t *testing.T) {
	var w lockedBuffer
	w.Write([]byte("0123456789"))
	p, start, total := w.page(3, 4)
	if p != "3456" || start != 3 || total != 10 {
		t.Fatalf("page = %q, %d, %d; want 3456, 3, 10", p, start, total)
	}
	w.Write([]byte(strings.Repeat("x", bgMaxOutput)))
	// Byte 3 is gone now: the page starts at the oldest byte still kept.
	_, start, total = w.page(3, 4)
	if start != 10 || total != 10+bgMaxOutput {
		t.Fatalf("start = %d, total = %d; want 10 and %d", start, total, 10+bgMaxOutput)
	}
	// The last page reaches exactly the end.
	p, start, _ = w.page(total-5, 0)
	if p != "xxxxx" || start != total-5 {
		t.Fatalf("tail page = %q at %d", p, start)
	}
}

func TestLockedBufferPageKeepsRunesWhole(t *testing.T) {
	var w lockedBuffer
	w.Write([]byte("aé€b"))     // 1 + 2 + 3 + 1 bytes
	p, start, _ := w.page(2, 3) // starts inside é
	if p != "€" || start != 3 {
		t.Fatalf("page = %q at %d; want € at 3", p, start)
	}
	p, _, _ = w.page(0, 4) // would end inside €
	if p != "aé" {
		t.Fatalf("page = %q; want aé", p)
	}
}

func TestPruneForgetsOldFinishedJobs(t *testing.T) {
	m := newBgJobManager()
	now := time.Now()
	old := &bgJob{id: "old", done: true, ended: now.Add(-bgJobTTL - time.Minute), doneCh: make(chan struct{})}
	recent := &bgJob{id: "recent", done: true, started: now.Add(-2 * bgJobTTL), ended: now.Add(-time.Minute), doneCh: make(chan struct{})}
	running := &bgJob{id: "running", started: now.Add(-2 * bgJobTTL), doneCh: make(chan struct{})}
	m.jobs = map[string]*bgJob{"old": old, "recent": recent, "running": running}

	m.prune(now)

	if _, ok := m.jobs["old"]; ok {
		t.Fatal("a job that ended past the TTL was kept")
	}
	if _, ok := m.jobs["recent"]; !ok {
		t.Fatal("a long job that just ended was pruned; age must count from its end")
	}
	if _, ok := m.jobs["running"]; !ok {
		t.Fatal("a running job was pruned")
	}
}

func TestPruneCapsFinishedJobs(t *testing.T) {
	m := newBgJobManager()
	now := time.Now()
	m.jobs = map[string]*bgJob{}
	for i := 0; i < bgMaxJobs+5; i++ {
		id := "j" + strconv.Itoa(i)
		m.jobs[id] = &bgJob{id: id, done: true, ended: now.Add(time.Duration(i) * time.Second), doneCh: make(chan struct{})}
	}
	m.prune(now.Add(time.Hour - bgJobTTL))
	if len(m.jobs) != bgMaxJobs {
		t.Fatalf("kept %d jobs, want %d", len(m.jobs), bgMaxJobs)
	}
	if _, ok := m.jobs["j0"]; ok {
		t.Fatal("the oldest finished job should go first")
	}
}

func TestWaitReturnsWhenJobEnds(t *testing.T) {
	m := newBgJobManager()
	j := m.launch(context.Background(), "sleep 0.2; echo done", false)
	start := time.Now()
	got, ok := m.wait(context.Background(), j.id, 10*time.Second)
	if !ok || time.Since(start) > 5*time.Second {
		t.Fatalf("wait ok=%v after %s; want an early return when the job ends", ok, time.Since(start))
	}
	if done, _, _ := got.snapshot(); !done {
		t.Fatal("job not done after wait returned")
	}
}

func TestWaitStopsOnCancel(t *testing.T) {
	m := newBgJobManager()
	j := m.launch(context.Background(), "sleep 30", false)
	defer m.kill(j.id)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, ok := m.wait(ctx, j.id, time.Minute); !ok {
		t.Fatal("job not found")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("wait ignored the cancelled context; Ctrl+C could not stop it")
	}
}
