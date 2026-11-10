package watch

import (
	"strings"
	"testing"
	"time"
)

func jobWaiter(t *testing.T, mode Mode, idle time.Duration) *Waiter {
	t.Helper()
	return newWaiter(t, Wait{Source: Source{Job: "j1"}, Mode: mode, Idle: idle})
}

func TestJobSourceValidate(t *testing.T) {
	if err := (Source{Job: "j1"}).Validate(); err != nil {
		t.Errorf("job alone: %v", err)
	}
	for _, s := range []Source{{Job: "j", Script: "ls"}, {Job: "j", URL: "http://x"}} {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v: want error", s)
		}
	}
	if got := (Source{Job: "j1"}).String(); got != "job j1" {
		t.Errorf("string %q", got)
	}
	if res := Run(t.Context(), Source{Job: "j1"}); res.Err == nil {
		t.Error("Run took a job source")
	}
}

func TestNewWaiterJobValidation(t *testing.T) {
	bad := map[string]Wait{
		"exit no job":     {Source: Source{Script: "true"}, Mode: UntilExit, Timeout: time.Second},
		"stall no job":    {Source: Source{URL: "http://x"}, Mode: UntilStall, Idle: time.Minute, Timeout: time.Second},
		"stall no idle":   {Source: Source{Job: "j"}, Mode: UntilStall, Timeout: time.Second},
		"exit_zero a job": {Source: Source{Job: "j"}, Mode: UntilExitZero, Timeout: time.Second},
	}
	for name, w := range bad {
		if _, err := NewWaiter(w, t0); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestWaiterExit(t *testing.T) {
	wt := jobWaiter(t, UntilExit, 0)
	if v := wt.Step(Result{Output: "x", Idle: time.Hour}, t0.Add(time.Second)); v.Done {
		t.Fatalf("a running job ended the wait: %+v", v)
	}
	v := wt.Step(Result{Output: "done\nbye", Exited: true, ExitCode: 3}, t0.Add(2*time.Second))
	if !v.Done || v.Reason != ReasonExited || !v.Reason.Satisfied() ||
		!strings.Contains(v.Detail, "status 3") || !strings.Contains(v.Detail, "bye") {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterExitNonZeroAndZeroBothEnd(t *testing.T) {
	for _, code := range []int{0, 1, 137} {
		v := jobWaiter(t, UntilExit, 0).Step(Result{Exited: true, ExitCode: code}, t0)
		if !v.Done || v.Reason != ReasonExited {
			t.Errorf("code %d: %+v", code, v)
		}
	}
}

func TestWaiterStallBoundary(t *testing.T) {
	idle := 10 * time.Minute
	wt := jobWaiter(t, UntilStall, idle)
	for _, d := range []time.Duration{0, time.Second, idle - time.Nanosecond} {
		if v := wt.Step(Result{Idle: d}, t0.Add(time.Second)); v.Done {
			t.Fatalf("idle %s ended the wait: %+v", d, v)
		}
	}
	v := wt.Step(Result{Output: "last line", Idle: idle}, t0.Add(2*time.Second))
	if !v.Done || v.Reason != ReasonStalled || !v.Reason.Satisfied() || !strings.Contains(v.Detail, "10m0s") {
		t.Fatalf("at the threshold: %+v", v)
	}
	if v := jobWaiter(t, UntilStall, idle).Step(Result{Idle: idle + time.Hour}, t0); !v.Done || v.Reason != ReasonStalled {
		t.Fatalf("above the threshold: %+v", v)
	}
}

func TestWaiterStallNeverFiresAfterExit(t *testing.T) {
	v := jobWaiter(t, UntilStall, time.Minute).Step(Result{Exited: true, ExitCode: 0, Idle: time.Hour}, t0)
	if !v.Done || v.Reason == ReasonStalled || v.Reason != ReasonExited {
		t.Fatalf("an exited job: %+v", v)
	}
}

func TestWaiterExitModeIgnoresIdle(t *testing.T) {
	if v := jobWaiter(t, UntilExit, 0).Step(Result{Idle: 100 * time.Hour}, t0); v.Done {
		t.Fatalf("exit mode stalled: %+v", v)
	}
}

func TestWaiterJobMatchAndChangeUseTheOutput(t *testing.T) {
	m := newWaiter(t, Wait{Source: Source{Job: "j"}, Mode: UntilMatch, Pattern: `FAIL`})
	if v := m.Step(okRes("ok"), t0); v.Done {
		t.Fatalf("%+v", v)
	}
	if v := m.Step(okRes("1 FAIL"), t0.Add(time.Second)); !v.Done || v.Reason != ReasonMatched {
		t.Fatalf("%+v", v)
	}
	c := newWaiter(t, Wait{Source: Source{Job: "j"}, Mode: UntilChange})
	c.Step(okRes("a"), t0)
	if v := c.Step(okRes("b"), t0.Add(time.Second)); !v.Done || v.Reason != ReasonChanged {
		t.Fatalf("%+v", v)
	}
}

func TestWaiterJobTimeout(t *testing.T) {
	wt := jobWaiter(t, UntilExit, 0)
	v := wt.Step(Result{Output: "still going"}, t0.Add(2*time.Minute))
	if !v.Done || v.Reason != ReasonTimedOut || v.Reason.Satisfied() {
		t.Fatalf("%+v", v)
	}
}

func TestJobModeStrings(t *testing.T) {
	if UntilExit.String() != "exit" || UntilStall.String() != "stall" {
		t.Error("mode names")
	}
}
