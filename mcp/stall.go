package mcp

import (
	"context"
	"sort"
	"time"
)

// A background shell job can hang: a command that waits for input, a build
// that deadlocks. The model then waits on it with no sign that anything is
// wrong. A job is stalled when it has not written to stdout or stderr, and has
// not exited, for longer than a limit the caller picks. A job reports one stall
// at a time: new output re-arms it.

// StalledJob is a running background shell job with no activity.
type StalledJob struct {
	ID     string
	Script string
	// Idle is how long the job has been silent.
	Idle time.Duration
}

func (j *bgJob) touch(now time.Time) {
	j.lastActive.Store(now.UnixNano())
	j.stallNotified.Store(false)
}

// Stalled returns, oldest first, the running background jobs that have been
// silent for longer than after at now and whose stall is not reported yet. It
// does not mark them: call MarkStallReported once the notice is delivered, so a
// notice that could not be delivered is retried.
func (s *ShellJobs) Stalled(now time.Time, after time.Duration) []StalledJob {
	if s == nil {
		return nil
	}
	m := s.mgr
	m.mu.Lock()
	var jobs []*bgJob
	for _, j := range m.jobs {
		if j.detach == nil || j.detached {
			jobs = append(jobs, j)
		}
	}
	m.mu.Unlock()

	var out []StalledJob
	for _, j := range jobs {
		if done, _, _ := j.snapshot(); done || j.stallNotified.Load() {
			continue
		}
		idle := now.Sub(time.Unix(0, j.lastActive.Load()))
		if idle > after {
			out = append(out, StalledJob{ID: j.id, Script: j.script, Idle: idle})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

// MarkStallReported records that the current stall of job id was reported. New
// output from the job re-arms the notice.
func (s *ShellJobs) MarkStallReported(id string) {
	if s == nil {
		return
	}
	if j, ok := s.mgr.get(id); ok {
		j.stallNotified.Store(true)
	}
}

// StartBackground runs script as a background job under ctx and returns its
// id. The tool server uses the same path for bash_background.
func (s *ShellJobs) StartBackground(ctx context.Context, script string) string {
	return s.mgr.launch(ctx, script, false).id
}
