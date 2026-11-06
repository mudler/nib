package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	wizmcp "github.com/mudler/nib/mcp"
)

func TestShellStallNoticeOncePerStallAndRearm(t *testing.T) {
	s := newStaleTestSession(true)
	s.shellJobs = wizmcp.NewShellJobs()
	s.shellStallAfter = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := s.shellJobs.StartBackground(ctx, "printf start; sleep 30")
	defer s.shellJobs.Kill(id)
	waitForOutput(t, s.shellJobs, id, "start")

	s.notifyStalledShells(time.Now().Add(30 * time.Second))
	if got := drainInjected(s); len(got) != 0 {
		t.Fatalf("notified before the limit: %v", got)
	}
	s.notifyStalledShells(time.Now().Add(2 * time.Minute))
	got := drainInjected(s)
	if len(got) != 1 || !strings.Contains(got[0], id) || !strings.Contains(got[0], "bash_job_output") {
		t.Fatalf("notices = %v", got)
	}
	s.notifyStalledShells(time.Now().Add(10 * time.Minute))
	if got := drainInjected(s); len(got) != 0 {
		t.Fatalf("the same stall was reported twice: %v", got)
	}
}

func TestShellStallNoticeRetriedWhenRunNotLive(t *testing.T) {
	s := newStaleTestSession(false)
	s.shellJobs = wizmcp.NewShellJobs()
	s.shellStallAfter = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := s.shellJobs.StartBackground(ctx, "sleep 30")
	defer s.shellJobs.Kill(id)

	at := time.Now().Add(5 * time.Minute)
	s.notifyStalledShells(at)
	if got := drainInjected(s); len(got) != 0 {
		t.Fatalf("injected into a run that is not live: %v", got)
	}
	s.runLive = true
	s.notifyStalledShells(at)
	if got := drainInjected(s); len(got) != 1 {
		t.Fatalf("the notice was not retried: %v", got)
	}
}

func waitForOutput(t *testing.T, jobs *wizmcp.ShellJobs, id, want string) {
	t.Helper()
	for range 200 {
		if so, _, _ := jobs.Output(id); strings.Contains(so, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %s never printed %q", id, want)
}

func TestShellStallDefaultIsConservative(t *testing.T) {
	if defaultShellStallAfter < 5*time.Minute {
		t.Fatalf("default %s would flag ordinary quiet builds", defaultShellStallAfter)
	}
	s := newStaleTestSession(true)
	s.notifyStalledShells(time.Now()) // no registry: no panic
}
