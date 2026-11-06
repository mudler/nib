package mcp

import (
	"context"
	"testing"
	"time"
)

func TestStalledReportsSilentJobOnce(t *testing.T) {
	jobs := NewShellJobs()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := jobs.mgr.launch(ctx, "sleep 30", false)
	defer jobs.Kill(j.id)

	start := time.Now()
	if got := jobs.Stalled(start.Add(time.Minute), 5*time.Minute); len(got) != 0 {
		t.Fatalf("a young job is stalled: %+v", got)
	}
	later := start.Add(6 * time.Minute)
	got := jobs.Stalled(later, 5*time.Minute)
	if len(got) != 1 || got[0].ID != j.id || got[0].Idle < 5*time.Minute {
		t.Fatalf("got %+v", got)
	}
	// Until it is marked, the same stall is offered again (a notice that
	// failed to deliver is retried). After it is marked, it is not.
	if len(jobs.Stalled(later, 5*time.Minute)) != 1 {
		t.Fatal("an unreported stall disappeared")
	}
	jobs.MarkStallReported(j.id)
	if got := jobs.Stalled(later.Add(time.Hour), 5*time.Minute); len(got) != 0 {
		t.Fatalf("one stall reported twice: %+v", got)
	}
}

func TestStalledRearmsOnOutput(t *testing.T) {
	jobs := NewShellJobs()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j := jobs.mgr.launch(ctx, "sleep 30", false)
	defer jobs.Kill(j.id)

	later := time.Now().Add(10 * time.Minute)
	jobs.MarkStallReported(j.id)
	if len(jobs.Stalled(later, 5*time.Minute)) != 0 {
		t.Fatal("reported stall offered")
	}
	// Output arrives: the job is alive again, so the next stall reports anew.
	j.stdout.Write([]byte("progress\n"))
	if len(jobs.Stalled(time.Now().Add(time.Minute), 5*time.Minute)) != 0 {
		t.Fatal("a job that just wrote is stalled")
	}
	got := jobs.Stalled(time.Now().Add(10*time.Minute), 5*time.Minute)
	if len(got) != 1 {
		t.Fatalf("new output did not re-arm the notice: %+v", got)
	}
}

func TestStalledIgnoresFinishedAndForegroundJobs(t *testing.T) {
	jobs := NewShellJobs()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := jobs.mgr.launch(ctx, "true", false)
	<-done.doneCh
	fg := jobs.mgr.launch(ctx, "sleep 30", true)
	defer jobs.Kill(fg.id)

	if got := jobs.Stalled(time.Now().Add(time.Hour), time.Minute); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	var nilJobs *ShellJobs
	if nilJobs.Stalled(time.Now(), time.Minute) != nil {
		t.Fatal("nil registry")
	}
}
