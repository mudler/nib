package loop

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/watch"
)

func okRes(out string) watch.Result { return watch.Result{Output: out, Hash: watch.Hash(out)} }

func monitorRegistry(t *testing.T, mc MonitorConfig, recurring bool) (*Registry, Job, *time.Time) {
	t.Helper()
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	j, err := r.Add("*/5 * * * *", "check the build", recurring, false, mc)
	if err != nil {
		t.Fatal(err)
	}
	return r, j, &now
}

// observe runs one check the way a host does.
func observe(t *testing.T, r *Registry, id string, res watch.Result, now time.Time) Decision {
	t.Helper()
	if _, ok := r.BeginCheck(id); !ok {
		t.Fatalf("BeginCheck(%s) refused", id)
	}
	return r.Observe(id, res, now)
}

func TestMonitorFirstCheckStoresWithoutWaking(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	d := observe(t, r, j.ID, okRes("v1\n"), *now)
	if d.Dispatch || d.Err != nil {
		t.Fatalf("first check woke the agent: %+v", d)
	}
	got, _ := r.Get(j.ID)
	if got.LastOutputHash != watch.Hash("v1\n") || got.LastOutput != "v1\n" {
		t.Fatalf("baseline not stored: %+v", got)
	}
}

func TestMonitorNotifyFirst(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x", NotifyFirst: true}, true)
	d := observe(t, r, j.ID, okRes("v1\n"), *now)
	if !d.Dispatch || !strings.Contains(d.Prompt, "check the build") || !strings.Contains(d.Prompt, "v1") {
		t.Fatalf("got %+v", d)
	}
}

func TestMonitorUnchangedDoesNotDispatch(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	observe(t, r, j.ID, okRes("v1\n"), *now)
	d := observe(t, r, j.ID, okRes("v1\n"), now.Add(time.Minute))
	if d.Dispatch || d.Persist || d.Err != nil {
		t.Fatalf("unchanged output acted: %+v", d)
	}
}

func TestMonitorChangeDispatchesWithDiffAndStoresFirst(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	observe(t, r, j.ID, okRes("v1\nsame\n"), *now)
	d := observe(t, r, j.ID, okRes("v2\nsame\n"), now.Add(time.Minute))
	if !d.Dispatch {
		t.Fatalf("change did not dispatch: %+v", d)
	}
	for _, want := range []string{"check the build", "-v1", "+v2", "```diff"} {
		if !strings.Contains(d.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, d.Prompt)
		}
	}
	// The hash is stored by the time Observe returns, i.e. before the host
	// dispatches. A dispatch that fails then cannot re-alert on the same change.
	got, _ := r.Get(j.ID)
	if got.LastOutputHash != watch.Hash("v2\nsame\n") {
		t.Fatalf("hash not stored before dispatch: %+v", got)
	}
	if d := observe(t, r, j.ID, okRes("v2\nsame\n"), now.Add(2*time.Minute)); d.Dispatch {
		t.Fatal("the same change alerted twice")
	}
}

func TestMonitorDiffIsCapped(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	big := func(p string) string {
		var b strings.Builder
		for i := 0; i < 600; i++ {
			b.WriteString(p + " line of output number\n")
		}
		return b.String()
	}
	observe(t, r, j.ID, okRes(big("a")), *now)
	d := observe(t, r, j.ID, okRes(big("b")), now.Add(time.Minute))
	if !d.Dispatch || len(d.Prompt) > MaxDiff+500 || !strings.Contains(d.Prompt, "(truncated)") {
		t.Fatalf("diff not capped: %d bytes", len(d.Prompt))
	}
}

func TestMonitorErrorKeepsState(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	observe(t, r, j.ID, okRes("v1\n"), *now)
	before, _ := r.Get(j.ID)

	d := observe(t, r, j.ID, watch.Result{Err: errors.New("boom")}, now.Add(time.Minute))
	if d.Dispatch || d.Err == nil || d.Persist {
		t.Fatalf("error decision: %+v", d)
	}
	after, _ := r.Get(j.ID)
	if after.LastOutputHash != before.LastOutputHash || after.LastOutput != before.LastOutput || !after.LastChangedAt.Equal(before.LastChangedAt) {
		t.Fatalf("an error changed the stored state: %+v -> %+v", before, after)
	}
	if !strings.Contains(after.Label(), "failed") {
		t.Fatalf("the failure is not visible in the list: %q", after.Label())
	}
	// Same output after the outage is not a change.
	if d := observe(t, r, j.ID, okRes("v1\n"), now.Add(2*time.Minute)); d.Dispatch {
		t.Fatal("recovery with the same output woke the agent")
	}
	if cleared, _ := r.Get(j.ID); cleared.LastError != "" {
		t.Fatal("a good check must clear the error")
	}
}

func TestMonitorErrorReportedFirstThenEveryTenth(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	var reported []int
	for i := 1; i <= 21; i++ {
		if d := observe(t, r, j.ID, watch.Result{Err: errors.New("down")}, *now); d.Err != nil {
			reported = append(reported, i)
		}
	}
	if len(reported) != 3 || reported[0] != 1 || reported[1] != 10 || reported[2] != 20 {
		t.Fatalf("reported at %v", reported)
	}
}

func TestMonitorFirstCheckErrorThenBaseline(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	observe(t, r, j.ID, watch.Result{Err: errors.New("down")}, *now)
	if got, _ := r.Get(j.ID); got.LastOutputHash != "" {
		t.Fatal("an error stored a hash")
	}
	if d := observe(t, r, j.ID, okRes("v1"), *now); d.Dispatch {
		t.Fatal("the first good check after errors is still the baseline")
	}
}

func TestMonitorOneShotStaysUntilChange(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, false)
	*now = tm("2026-06-06 10:05")
	if due := r.Due(); len(due) != 1 {
		t.Fatalf("due: %d", len(due))
	}
	observe(t, r, j.ID, okRes("a"), *now)
	if _, ok := r.Get(j.ID); !ok {
		t.Fatal("a one-shot monitor was dropped before it saw a change")
	}
	*now = tm("2026-06-06 10:10")
	r.Due()
	d := observe(t, r, j.ID, okRes("b"), *now)
	if !d.Dispatch {
		t.Fatal("no dispatch")
	}
	if _, ok := r.Get(j.ID); ok {
		t.Fatal("a one-shot monitor must go after it reported")
	}
}

func TestBeginCheckSkipsBusyAndPlainJobs(t *testing.T) {
	r, j, now := monitorRegistry(t, MonitorConfig{Script: "x"}, true)
	plain, _ := r.Add("*/5 * * * *", "plain", true, false, MonitorConfig{})
	if _, ok := r.BeginCheck(plain.ID); ok {
		t.Fatal("a plain job has no check")
	}
	if _, ok := r.BeginCheck(j.ID); !ok {
		t.Fatal("first claim refused")
	}
	if _, ok := r.BeginCheck(j.ID); ok {
		t.Fatal("a second check started while one was in flight")
	}
	r.Observe(j.ID, okRes("a"), *now)
	if _, ok := r.BeginCheck(j.ID); !ok {
		t.Fatal("the claim was not released")
	}
	r.Delete(j.ID)
	if d := r.Observe(j.ID, okRes("b"), *now); d.Dispatch {
		t.Fatal("a deleted job dispatched")
	}
}

func TestMonitorPersistsNotifyFirstAndState(t *testing.T) {
	path := t.TempDir() + "/loops.json"
	r := NewRegistry()
	j, _ := r.Add("*/5 * * * *", "p", true, true, MonitorConfig{URL: "http://x", NotifyFirst: true})
	d := observe(t, r, j.ID, okRes("v1"), tm("2026-06-06 10:00"))
	if !d.Persist {
		t.Fatal("a durable job must ask for a save")
	}
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	r2 := NewRegistry()
	if n, err := r2.Load(path); err != nil || n != 1 {
		t.Fatalf("load: %d %v", n, err)
	}
	got := r2.List()[0]
	if !got.MonitorNotifyFirst || got.LastOutputHash != watch.Hash("v1") {
		t.Fatalf("restored %+v", got)
	}
}

func TestWaitFlow(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	j, err := r.AddWait("*/10 * * * * *", watch.Wait{
		Source: watch.Source{Script: "x"}, Mode: watch.UntilMatch, Pattern: "done", Timeout: time.Minute,
	}, "report the build")
	if err != nil {
		t.Fatal(err)
	}
	if !j.IsCheck() || !strings.Contains(j.Label(), "wait_for match") {
		t.Fatalf("label %q", j.Label())
	}
	if d := observe(t, r, j.ID, okRes("running"), now.Add(10*time.Second)); d.Dispatch {
		t.Fatalf("woke too early: %+v", d)
	}
	if _, ok := r.Get(j.ID); !ok {
		t.Fatal("wait removed early")
	}
	d := observe(t, r, j.ID, okRes("all done"), now.Add(20*time.Second))
	if !d.Dispatch || !strings.Contains(d.Prompt, "condition was met") || !strings.Contains(d.Prompt, "report the build") {
		t.Fatalf("got %+v", d)
	}
	if _, ok := r.Get(j.ID); ok {
		t.Fatal("an ended wait stays armed")
	}
}

func TestWaitFailureAndTimeoutWakeWithReason(t *testing.T) {
	now := tm("2026-06-06 10:00")
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	w := watch.Wait{Source: watch.Source{Script: "x"}, Mode: watch.UntilChange, Timeout: time.Minute, MaxErrors: 2}
	j, _ := r.AddWait("*/10 * * * * *", w, "")

	observe(t, r, j.ID, watch.Result{Err: errors.New("down")}, now.Add(time.Second))
	d := observe(t, r, j.ID, watch.Result{Err: errors.New("down")}, now.Add(2*time.Second))
	if !d.Dispatch || !strings.Contains(d.Prompt, "WITHOUT the condition") || !strings.Contains(d.Prompt, "source-failing") {
		t.Fatalf("got %+v", d)
	}

	j, _ = r.AddWait("*/10 * * * * *", w, "")
	d = observe(t, r, j.ID, okRes("same"), now.Add(2*time.Minute))
	if !d.Dispatch || !strings.Contains(d.Prompt, "timed-out") || !strings.Contains(d.Prompt, "WITHOUT") {
		t.Fatalf("got %+v", d)
	}
}

func TestWaitIsListedButNeverPersisted(t *testing.T) {
	r := NewRegistry()
	j, err := r.AddWait("*/10 * * * * *", watch.Wait{Source: watch.Source{URL: "http://x"}, Timeout: time.Minute}, "")
	if err != nil {
		t.Fatal(err)
	}
	if snap := r.ScheduleSnapshot(); len(snap.Entries) != 1 || snap.Entries[0].ID != j.ID {
		t.Fatalf("the wait is not in the schedule: %+v", snap)
	}
	path := t.TempDir() + "/loops.json"
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	if n, _ := NewRegistry().Load(path); n != 0 {
		t.Fatal("a wait was persisted")
	}
	if _, err := r.AddWait("*/10 * * * * *", watch.Wait{Timeout: time.Minute}, ""); err == nil {
		t.Fatal("an invalid wait was accepted")
	}
}
