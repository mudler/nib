package chat

import (
	"fmt"
	"strings"
	"time"
)

// goalReview is the harness-owned instruction for one supervisor pass. The goal
// identity lets the turn driver reject a review that became stale after it was
// taken but before it was added to a request.
type goalReview struct {
	goalIdentity uint64
	prompt       string
}

const goalCheckInPrompt = `Goal supervision review (this is a harness-generated check-in, not user input):
- Begin the short visible assistant update with exactly "Goal check-in:".
- Briefly report progress and inspect relevant agent and shell-job status; do not assume progress.
- Identify failures, missing evidence, and newly unblocked work.
- Follow up with existing work, or spawn bounded additional work only when justified.
- Continue waiting when that is the correct action.
- Call goal_done only after verifying the whole goal, not merely one subtask.`

// goalSupervisor is a small policy wrapper over backgroundState. All mutable
// lifecycle/timer state remains in backgroundState's single synchronization
// domain; the schedule and timer factory are immutable except while that mutex
// is held too.
type goalSupervisor struct {
	state   *backgroundState
	delays  []time.Duration
	factory goalTimerFactory
	wake    chan<- struct{}
}

func newGoalSupervisor(state *backgroundState, delays []time.Duration, factory goalTimerFactory, wake chan<- struct{}) *goalSupervisor {
	if factory == nil {
		factory = realGoalTimerFactory{}
	}
	return &goalSupervisor{state: state, delays: append([]time.Duration(nil), delays...), factory: factory, wake: wake}
}

func validateGoalCheckInDelays(delays []time.Duration) error {
	if len(delays) == 0 {
		return fmt.Errorf("goal check-in delays must not be empty")
	}
	for i, delay := range delays {
		if delay <= 0 {
			return fmt.Errorf("goal check-in delays[%d]: must be positive (got %s)", i, delay)
		}
	}
	return nil
}

func (g *goalSupervisor) setDelays(delays []time.Duration) error {
	if err := validateGoalCheckInDelays(delays); err != nil {
		return err
	}
	g.state.mu.Lock()
	g.delays = append([]time.Duration(nil), delays...)
	g.state.scheduleIndex = 0
	g.state.invalidateTimerLocked()
	g.armCurrentLocked()
	g.state.publishUnlock()
	return nil
}

func (g *goalSupervisor) goalSet(identity uint64) { g.state.setGoal(identity, goalActive) }

func (g *goalSupervisor) stopGoal(lifecycle goalLifecycle) {
	g.state.mu.Lock()
	g.state.goalLifecycle = lifecycle
	g.state.invalidateSupervisorLocked()
	g.state.publishUnlock()
}

func (g *goalSupervisor) goalPause()     { g.stopGoal(goalPaused) }
func (g *goalSupervisor) goalDone()      { g.stopGoal(goalDone) }
func (g *goalSupervisor) goalClear()     { g.stopGoal(goalInactive) }
func (g *goalSupervisor) goalInterrupt() { g.state.interrupt() }
func (g *goalSupervisor) close()         { g.state.close() }

// parked records the root's parked state and starts the current quiet-period
// delay only while an active goal and background work make supervision useful.
func (g *goalSupervisor) parked() {
	g.state.mu.Lock()
	g.state.rootActive, g.state.rootRequesting, g.state.rootParked = true, false, true
	g.armCurrentLocked()
	g.state.publishUnlock()
}

// backgroundEvent applies supervisor policy after a lifecycle transition has
// been recorded in the ledger. It is harmless to call repeatedly: queued review
// state is a bit, and the wake channel is only a best-effort hint.
func (g *goalSupervisor) backgroundEvent() {
	g.state.mu.Lock()
	g.state.scheduleIndex = 0
	g.state.invalidateTimerLocked()
	queued := false
	if g.eligibleBaseLocked() {
		g.state.reviewQueued = true
		queued = true
	}
	g.state.publishUnlock()
	if queued {
		g.wakeNonblocking()
	}
}

func (g *goalSupervisor) takeReview() (goalReview, bool) {
	g.state.mu.Lock()
	defer g.state.publishUnlock()
	if !g.state.reviewQueued || !g.eligibleBaseLocked() {
		return goalReview{}, false
	}
	g.state.reviewQueued = false
	g.state.reviewing = true
	g.state.stopTimerLocked()
	return goalReview{goalIdentity: g.state.goalIdentity, prompt: goalCheckInPrompt}, true
}

func (g *goalSupervisor) reviewFinishedAndParked() {
	g.state.mu.Lock()
	g.state.reviewing = false
	g.state.rootActive, g.state.rootRequesting, g.state.rootParked = true, false, true
	if !g.state.reviewQueued && g.state.scheduleIndex < len(g.delays)-1 {
		g.state.scheduleIndex++
	}
	g.armCurrentLocked()
	g.state.publishUnlock()
}

func (g *goalSupervisor) eligibleBaseLocked() bool {
	s := g.state
	return !s.closed && !s.interrupted && s.internalErr == nil && s.goalLifecycle == goalActive && s.rootParked && s.runningLocked()
}

func (g *goalSupervisor) eligibleLocked() bool {
	return len(g.delays) > 0 && g.eligibleBaseLocked() && !g.state.reviewQueued && !g.state.reviewing
}

func (g *goalSupervisor) armCurrent() {
	g.state.mu.Lock()
	defer g.state.publishUnlock()
	g.armCurrentLocked()
}

func (g *goalSupervisor) armCurrentLocked() {
	if !g.eligibleLocked() {
		return
	}
	delay := g.delays[g.state.scheduleIndex]
	if g.state.supervisorGeneration == ^uint64(0) {
		g.state.internalErr = errBackgroundSequenceOverflow
		g.state.stopTimerLocked()
		return
	}
	g.state.supervisorGeneration++
	generation := g.state.supervisorGeneration
	goal := g.state.goalIdentity
	g.state.stopTimerLocked()
	g.state.timer = g.factory.AfterFunc(delay, func() {
		g.state.goalTimerFired(generation, goal, g.wake)
	})
}

func (g *goalSupervisor) wakeNonblocking() {
	if g.wake == nil {
		return
	}
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

func normalizeGoalCheckIn(reply string) string {
	reply = strings.TrimSpace(reply)
	for strings.HasPrefix(reply, "Goal check-in:") {
		reply = strings.TrimSpace(strings.TrimPrefix(reply, "Goal check-in:"))
	}
	if reply == "" {
		return "Goal check-in: no new status."
	}
	return "Goal check-in: " + reply
}

func (g *goalSupervisor) reviewCurrent(identity uint64) bool {
	g.state.mu.Lock()
	defer g.state.mu.Unlock()
	return g.state.reviewing && g.state.goalIdentity == identity && g.state.goalLifecycle == goalActive
}

func (g *goalSupervisor) reviewAborted(identity uint64) {
	g.state.mu.Lock()
	defer g.state.publishUnlock()
	if g.state.goalIdentity != identity {
		return
	}
	g.state.reviewing = false
	if g.eligibleBaseLocked() && g.state.runningLocked() {
		g.state.reviewQueued = true
	}
}

// SetGoalCheckInDelays validates and atomically replaces the live schedule.
func (s *Session) SetGoalCheckInDelays(delays []time.Duration) error {
	return s.goalSupervisor.setDelays(delays)
}
