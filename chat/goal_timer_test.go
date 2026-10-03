package chat

import (
	"sync"
	"testing"
	"time"
)

type fakeGoalTimerFactory struct {
	mu      sync.Mutex
	timers  []*fakeGoalTimer
	active  int
	maximum int
}

type fakeGoalTimer struct {
	factory *fakeGoalTimerFactory
	delay   time.Duration
	fn      func()
	stopped bool
	fired   bool
}

func (f *fakeGoalTimerFactory) AfterFunc(delay time.Duration, fn func()) goalTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &fakeGoalTimer{factory: f, delay: delay, fn: fn}
	f.timers = append(f.timers, timer)
	f.active++
	if f.active > f.maximum {
		f.maximum = f.active
	}
	return timer
}

func (t *fakeGoalTimer) Stop() bool {
	t.factory.mu.Lock()
	defer t.factory.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	t.factory.active--
	return true
}

// fire deliberately permits stale callbacks after Stop, matching the race an
// implementation must defend against with its generation token.
func (t *fakeGoalTimer) fire() {
	t.factory.mu.Lock()
	if !t.fired && !t.stopped {
		t.fired = true
		t.factory.active--
	}
	fn := t.fn
	t.factory.mu.Unlock()
	fn()
}

func timerReadyState() *backgroundState {
	s := newBackgroundState()
	s.setGoal(7, goalActive)
	s.startBackground(backgroundAgent, "a")
	s.setRoot(true, false, true)
	return s
}

func TestGoalTimerStaleGenerationAndAtMostOne(t *testing.T) {
	s := timerReadyState()
	factory := &fakeGoalTimerFactory{}
	wake := make(chan struct{}, 1)
	firstGeneration := s.armGoalTimer(factory, time.Minute, wake)
	secondGeneration := s.armGoalTimer(factory, 2*time.Minute, wake)
	if firstGeneration == secondGeneration {
		t.Fatal("replacement did not advance generation")
	}
	factory.mu.Lock()
	first, second, maximum := factory.timers[0], factory.timers[1], factory.maximum
	factory.mu.Unlock()
	if maximum > 1 {
		t.Fatalf("had %d concurrently active timers", maximum)
	}
	first.fire()
	if s.terminalSnapshot().supervisorQueued {
		t.Fatal("stale callback queued a review")
	}
	second.fire()
	if !s.terminalSnapshot().supervisorQueued {
		t.Fatal("current callback did not queue a review")
	}
}

func TestGoalTimerFireIsNonblockingAndQueuesOnce(t *testing.T) {
	s := timerReadyState()
	factory := &fakeGoalTimerFactory{}
	wake := make(chan struct{}, 1)
	wake <- struct{}{} // Saturate the hint channel.
	s.armGoalTimer(factory, time.Second, wake)
	factory.mu.Lock()
	timer := factory.timers[0]
	factory.mu.Unlock()

	done := make(chan struct{})
	go func() {
		timer.fire()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timer callback blocked on wake hint")
	}
	if !s.terminalSnapshot().supervisorQueued {
		t.Fatal("full wake channel lost durable queued state")
	}
	// A callback can race/re-enter, but cannot queue an additional review.
	timer.fire()
	if !s.terminalSnapshot().supervisorQueued {
		t.Fatal("duplicate callback cleared queued review")
	}
}
