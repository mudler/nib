package chat

import (
	"strings"
	"testing"
	"time"
)

func newTestGoalSupervisor(delays []time.Duration) (*goalSupervisor, *backgroundState, *fakeGoalTimerFactory, chan struct{}) {
	state := newBackgroundState()
	factory := &fakeGoalTimerFactory{}
	wake := make(chan struct{}, 1)
	return newGoalSupervisor(state, delays, factory, wake), state, factory, wake
}

func latestGoalTimer(t *testing.T, factory *fakeGoalTimerFactory) *fakeGoalTimer {
	t.Helper()
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if len(factory.timers) == 0 {
		t.Fatal("no goal timer was created")
	}
	return factory.timers[len(factory.timers)-1]
}

func goalTimerDelays(factory *fakeGoalTimerFactory) []time.Duration {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	out := make([]time.Duration, len(factory.timers))
	for i, timer := range factory.timers {
		out[i] = timer.delay
	}
	return out
}

func makeSupervisorEligible(g *goalSupervisor, state *backgroundState) {
	g.goalSet(1)
	state.startBackground(backgroundAgent, "worker")
	g.parked()
}

func TestGoalSupervisorFixedScheduleRepeats(t *testing.T) {
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{3 * time.Second})
	makeSupervisorEligible(g, state)
	for i := 0; i < 3; i++ {
		latestGoalTimer(t, factory).fire()
		if _, ok := g.takeReview(); !ok {
			t.Fatalf("review %d was not queued", i)
		}
		g.reviewFinishedAndParked()
	}
	for i, delay := range goalTimerDelays(factory) {
		if delay != 3*time.Second {
			t.Fatalf("timer %d delay = %s", i, delay)
		}
	}
}

func TestGoalSupervisorAdaptiveScheduleCapsAtLast(t *testing.T) {
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second, 2 * time.Second, 4 * time.Second})
	makeSupervisorEligible(g, state)
	for i := 0; i < 4; i++ {
		latestGoalTimer(t, factory).fire()
		if _, ok := g.takeReview(); !ok {
			t.Fatalf("review %d was not queued", i)
		}
		g.reviewFinishedAndParked()
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}
	got := goalTimerDelays(factory)
	if len(got) != len(want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delays = %v, want %v", got, want)
		}
	}
}

func TestGoalSupervisorBackgroundEventResetsAndDedupes(t *testing.T) {
	g, state, factory, wake := newTestGoalSupervisor([]time.Duration{time.Second, 5 * time.Second})
	makeSupervisorEligible(g, state)
	latestGoalTimer(t, factory).fire()
	if _, ok := g.takeReview(); !ok {
		t.Fatal("timer review missing")
	}
	g.reviewFinishedAndParked()

	g.backgroundEvent()
	g.backgroundEvent()
	if got := len(wake); got != 1 {
		t.Fatalf("wake hints = %d, want 1", got)
	}
	state.mu.Lock()
	queued, index, timer := state.reviewQueued, state.scheduleIndex, state.timer
	state.mu.Unlock()
	if !queued || index != 0 || timer != nil {
		t.Fatalf("queued=%v index=%d timer=%v", queued, index, timer)
	}
	if _, ok := g.takeReview(); !ok {
		t.Fatal("event review missing")
	}
	g.reviewFinishedAndParked()
	if delay := latestGoalTimer(t, factory).delay; delay != 5*time.Second {
		t.Fatalf("post-event review delay = %s, want 5s", delay)
	}
}

func TestGoalSupervisorStaleTimerAndLifecycleStops(t *testing.T) {
	stops := []struct {
		name string
		stop func(*goalSupervisor)
	}{
		{"pause", (*goalSupervisor).goalPause},
		{"done", (*goalSupervisor).goalDone},
		{"clear", (*goalSupervisor).goalClear},
		{"interrupt", (*goalSupervisor).goalInterrupt},
		{"close", (*goalSupervisor).close},
	}
	for _, tt := range stops {
		t.Run(tt.name, func(t *testing.T) {
			g, state, factory, wake := newTestGoalSupervisor([]time.Duration{time.Second})
			makeSupervisorEligible(g, state)
			stale := latestGoalTimer(t, factory)
			tt.stop(g)
			stale.fn() // model the Stop/callback race
			if state.terminalSnapshot().supervisorQueued {
				t.Fatal("stale callback queued review")
			}
			if len(wake) != 0 {
				t.Fatal("stale callback sent wake")
			}
		})
	}
}

func TestGoalSupervisorEligibility(t *testing.T) {
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second})
	g.parked()
	if len(goalTimerDelays(factory)) != 0 {
		t.Fatal("armed without active goal or work")
	}
	g.goalSet(1)
	g.parked()
	if len(goalTimerDelays(factory)) != 0 {
		t.Fatal("armed without work")
	}
	state.startBackground(backgroundAgent, "worker")
	if _, ok := g.takeReview(); !ok {
		t.Fatal("background start did not queue immediate review")
	}
	g.reviewFinishedAndParked()
	if len(goalTimerDelays(factory)) != 1 {
		t.Fatal("did not arm after immediate review")
	}
}

func TestGoalSupervisorScheduleValidationCopyAndLiveReplacement(t *testing.T) {
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second})
	for _, bad := range [][]time.Duration{nil, {}, {0}, {-time.Second}, {time.Second, 0}} {
		if err := g.setDelays(bad); err == nil {
			t.Fatalf("setDelays(%v) succeeded", bad)
		}
	}
	makeSupervisorEligible(g, state)
	old := latestGoalTimer(t, factory)
	replacement := []time.Duration{7 * time.Second, 9 * time.Second}
	if err := g.setDelays(replacement); err != nil {
		t.Fatal(err)
	}
	replacement[0] = time.Hour
	if delay := latestGoalTimer(t, factory).delay; delay != 7*time.Second {
		t.Fatalf("replacement was not copied: %s", delay)
	}
	old.fn()
	if state.terminalSnapshot().supervisorQueued {
		t.Fatal("replaced timer queued stale review")
	}
	factory.mu.Lock()
	maximum := factory.maximum
	factory.mu.Unlock()
	if maximum > 1 {
		t.Fatalf("maximum active timers = %d", maximum)
	}
}

func TestGoalSupervisorReviewPrompt(t *testing.T) {
	g, state, factory, _ := newTestGoalSupervisor([]time.Duration{time.Second})
	makeSupervisorEligible(g, state)
	latestGoalTimer(t, factory).fire()
	review, ok := g.takeReview()
	if !ok || review.goalIdentity != 1 {
		t.Fatalf("review = %+v, ok=%v", review, ok)
	}
	for _, text := range []string{"Goal check-in:", "progress", "agent", "shell-job", "failures", "missing evidence", "Follow up", "spawn bounded", "goal_done", "verifying the whole goal"} {
		if !strings.Contains(review.prompt, text) {
			t.Errorf("prompt missing %q: %s", text, review.prompt)
		}
	}
	if _, ok := g.takeReview(); ok {
		t.Fatal("same queued review was consumed twice")
	}
}
