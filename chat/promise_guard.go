package chat

import (
	"sync"
	"time"

	"github.com/mudler/nib/promise"
)

// promiseGuardMessage is the one host message sent to the model when its
// answer promised a later check and nothing is armed to make it happen.
const promiseGuardMessage = "[Host notice] Your last reply says you will check, wait, retry or move something later, but nothing is armed, so nothing will run after this turn. " +
	"Either arm it now: cron, wait_for or schedule_wakeup for a timed or conditional check, bash_background for a long command, spawn_agent with background=true for a task. " +
	"Or tell the person plainly that you cannot watch it and what they need to do. Do not repeat the promise."

// wakeupTracker remembers the wake-ups the model scheduled, so the guard can
// tell a pending wake-up from none. The host owns the timer, and a wake-up
// asked for in this very turn may not be visible there yet.
type wakeupTracker struct {
	mu        sync.Mutex
	deadlines []time.Time
}

func (w *wakeupTracker) add(delay time.Duration, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.pruneLocked(now), now.Add(delay))
}

func (w *wakeupTracker) pending(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pruneLocked(now)) > 0
}

func (w *wakeupTracker) pruneLocked(now time.Time) []time.Time {
	kept := w.deadlines[:0]
	for _, d := range w.deadlines {
		if d.After(now) {
			kept = append(kept, d)
		}
	}
	w.deadlines = kept
	return kept
}

// noteWakeup records a wake-up the model asked for.
func (s *Session) noteWakeup(delaySeconds int) {
	s.wakeups.add(time.Duration(delaySeconds)*time.Second, time.Now())
}

// promiseUnkept reports whether the answer promises a later action while
// nothing is armed to carry it out.
//
// The guard is on only when the host can arm something, which it says by
// setting Callbacks.OnArmedCount. A piped or one-shot run sets nothing, so the
// run ends with the turn and there is no later; it is left alone. Sub-agents do
// not pass through here: their final report goes to the parent, which decides.
func (s *Session) promiseUnkept(response string) bool {
	if s.callbacks.OnArmedCount == nil || !promise.Promises(response) {
		return false
	}
	return !s.hasArmedWork()
}

// hasArmedWork reports whether something will run after this turn: a job the
// host holds, a pending wake-up, a running sub-agent or background shell job,
// or input still queued for the model.
func (s *Session) hasArmedWork() bool {
	if s.callbacks.OnArmedCount != nil && s.callbacks.OnArmedCount() > 0 {
		return true
	}
	if s.wakeups.pending(time.Now()) {
		return true
	}
	if s.background.hasPendingWork() {
		return true
	}
	if s.agentManager != nil && s.agentManager.HasRunning() {
		return true
	}
	if s.shellJobs != nil && s.shellJobs.HasRunning() {
		return true
	}
	return len(s.inject) > 0
}
