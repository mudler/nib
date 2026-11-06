package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	"github.com/mudler/nib/watch"
)

// monitorModel returns an idle model with a fake source and a clock the test
// moves. out is what the source returns next.
type fakeSource struct {
	out   atomic.Value // string
	err   atomic.Value // error holder
	calls atomic.Int32
}

func (f *fakeSource) run(ctx context.Context, src watch.Source) watch.Result {
	f.calls.Add(1)
	if e, _ := f.err.Load().(errBox); e.err != nil {
		return watch.Result{Err: e.err}
	}
	out, _ := f.out.Load().(string)
	return watch.Result{Output: out, Hash: watch.Hash(out)}
}

type errBox struct{ err error }

func newMonitorModel(t *testing.T, durable bool, mc loop.MonitorConfig) (Model, *fakeSource, *time.Time, loop.Job) {
	t.Helper()
	m := newLoopTestModel()
	m.loopsPath = filepath.Join(t.TempDir(), "loops.json")
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local)
	m.loops.SetClock(func() time.Time { return now })
	f := &fakeSource{}
	f.out.Store("v1\n")
	m.runCheck = f.run
	mc.Script = "echo v"
	j, err := m.loops.Add("*/5 * * * *", "look at the build", true, durable, mc)
	if err != nil {
		t.Fatal(err)
	}
	return m, f, &now, j
}

// tick advances the clock one slot and runs the tick plus the checks it started.
func tick(t *testing.T, m *Model, now *time.Time) {
	t.Helper()
	*now = now.Add(5 * time.Minute)
	next, cmd := m.Update(loopTickMsg{})
	*m = next.(Model)
	for _, msg := range collectMsgs(cmd) {
		if cm, ok := msg.(monitorCheckMsg); ok {
			next, _ := m.Update(cm)
			*m = next.(Model)
		}
	}
}

// collectMsgs runs a command and the batch it may be, and returns the messages.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, collectMsgs(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestTickRunsMonitorOffTheUIGoroutine(t *testing.T) {
	m, f, now, _ := newMonitorModel(t, false, loop.MonitorConfig{})
	*now = now.Add(5 * time.Minute)
	_, cmd := m.Update(loopTickMsg{})
	if f.calls.Load() != 0 {
		t.Fatal("the source ran inside Update, on the UI goroutine")
	}
	if cmd == nil {
		t.Fatal("no command returned")
	}
	collectMsgs(cmd)
	if f.calls.Load() != 1 {
		t.Fatalf("the source ran %d times", f.calls.Load())
	}
}

func TestMonitorTickFirstCheckThenUnchangedDoNotWake(t *testing.T) {
	m, _, now, _ := newMonitorModel(t, false, loop.MonitorConfig{})
	tick(t, &m, now) // baseline
	tick(t, &m, now) // unchanged
	if m.loading || len(m.queue) != 0 || len(m.messages) != 0 {
		t.Fatalf("the agent was woken: loading=%v queue=%v messages=%v", m.loading, m.queue, m.messages)
	}
}

func TestMonitorTickChangeWakesAgentWithDiff(t *testing.T) {
	m, f, now, _ := newMonitorModel(t, false, loop.MonitorConfig{})
	tick(t, &m, now) // baseline
	f.out.Store("v2\n")
	tick(t, &m, now)
	if !m.loading || len(m.messages) == 0 {
		t.Fatalf("a change did not wake the agent: %+v", m.messages)
	}
	got := m.messages[len(m.messages)-1].Content
	for _, want := range []string{"look at the build", "-v1", "+v2"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}

func TestMonitorTickErrorShowsAndDoesNotWake(t *testing.T) {
	m, f, now, j := newMonitorModel(t, false, loop.MonitorConfig{})
	tick(t, &m, now)
	f.err.Store(errBox{errors.New("connection refused")})
	tick(t, &m, now)
	if m.loading {
		t.Fatal("an error woke the agent as if it were a change")
	}
	if n := len(m.messages); n != 1 || m.messages[0].Role != "error" || !strings.Contains(m.messages[0].Content, "connection refused") {
		t.Fatalf("messages = %+v", m.messages)
	}
	got, _ := m.loops.Get(j.ID)
	if got.LastOutputHash != watch.Hash("v1\n") {
		t.Fatal("an error touched the stored hash")
	}
	if !strings.Contains(m.listLoops(), "failed") {
		t.Fatalf("the list does not show the failure: %s", m.listLoops())
	}
}

// The new hash is on disk before the agent run is even started, so a run that
// fails cannot make the same change alert again after a restart.
func TestMonitorHashSavedBeforeDispatch(t *testing.T) {
	m, f, now, _ := newMonitorModel(t, true, loop.MonitorConfig{})
	tick(t, &m, now)
	f.out.Store("v2\n")

	*now = now.Add(5 * time.Minute)
	next, cmd := m.Update(loopTickMsg{})
	m = next.(Model)
	var check monitorCheckMsg
	for _, msg := range collectMsgs(cmd) {
		if cm, ok := msg.(monitorCheckMsg); ok {
			check = cm
		}
	}
	dispatch := m.applyCheck(check) // the agent run is the returned command, not yet run
	if dispatch == nil {
		t.Fatal("no dispatch")
	}
	data, err := os.ReadFile(m.loopsPath)
	if err != nil || !strings.Contains(string(data), watch.Hash("v2\n")) {
		t.Fatalf("hash not on disk before the dispatch ran: %v %s", err, data)
	}
}

func TestMonitorSlowCheckSkipsTicks(t *testing.T) {
	m, _, now, j := newMonitorModel(t, false, loop.MonitorConfig{})
	if _, ok := m.loops.BeginCheck(j.ID); !ok { // a check in flight
		t.Fatal("setup")
	}
	*now = now.Add(5 * time.Minute)
	_, cmd := m.Update(loopTickMsg{})
	for _, msg := range collectMsgs(cmd) {
		if _, ok := msg.(monitorCheckMsg); ok {
			t.Fatal("a second check started while the first was running")
		}
	}
}

func TestMonitorNotifyFirstWakesOnBaseline(t *testing.T) {
	m, _, now, _ := newMonitorModel(t, false, loop.MonitorConfig{NotifyFirst: true})
	tick(t, &m, now)
	if !m.loading {
		t.Fatal("monitor_notify_first did not wake on the first check")
	}
}

func TestWaitForArmsListsAndWakes(t *testing.T) {
	m := newLoopTestModel()
	m.ensureScheduleOwner()
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local)
	m.loops.SetClock(func() time.Time { return now })
	f := &fakeSource{}
	f.out.Store("building")
	m.runCheck = f.run

	cb := m.sessionScheduleCallbacks()
	if cb.OnWaitFor == nil {
		t.Fatal("the TUI does not provide wait_for")
	}
	out := cb.OnWaitFor(chat.WaitRequest{Script: "x", Until: "match", Pattern: "done", Then: "tell me", Timeout: time.Minute, IntervalSeconds: 5})
	if !strings.Contains(out, "Armed loop-1") {
		t.Fatalf("got %q", out)
	}
	// The armed list, /loop list and cron_list share one renderer.
	if l := m.listLoops(); !strings.Contains(l, "wait_for match") || !strings.Contains(cb.OnCronList(), "wait_for match") {
		t.Fatalf("not listed: %q", l)
	}
	if snap := m.collectScheduleSnapshot(); len(snap.Entries) != 1 {
		t.Fatalf("not in the footer schedule: %+v", snap)
	}

	now = now.Add(10 * time.Second)
	next, cmd := m.Update(loopTickMsg{})
	m = next.(Model)
	for _, msg := range collectMsgs(cmd) {
		if cm, ok := msg.(monitorCheckMsg); ok {
			next, _ := m.Update(cm)
			m = next.(Model)
		}
	}
	if m.loading || len(m.loops.List()) != 1 {
		t.Fatalf("woke before the condition was met: loading=%v jobs=%d msgs=%+v", m.loading, len(m.loops.List()), m.messages)
	}
	f.out.Store("build done")
	now = now.Add(10 * time.Second)
	next, cmd = m.Update(loopTickMsg{})
	m = next.(Model)
	for _, msg := range collectMsgs(cmd) {
		if cm, ok := msg.(monitorCheckMsg); ok {
			next, _ := m.Update(cm)
			m = next.(Model)
		}
	}
	if !m.loading || len(m.loops.List()) != 0 {
		t.Fatalf("not woken or still armed: loading=%v jobs=%d", m.loading, len(m.loops.List()))
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "condition was met") || !strings.Contains(last, "tell me") {
		t.Fatalf("wake-up text: %s", last)
	}
}

func TestWaitForTimeoutWakesWithReason(t *testing.T) {
	m := newLoopTestModel()
	m.ensureScheduleOwner()
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local)
	m.loops.SetClock(func() time.Time { return now })
	f := &fakeSource{}
	f.out.Store("same")
	m.runCheck = f.run
	m.sessionScheduleCallbacks().OnWaitFor(chat.WaitRequest{URL: "http://x", Timeout: 15 * time.Second, IntervalSeconds: 5})
	for range 4 {
		now = now.Add(5 * time.Second)
		next, cmd := m.Update(loopTickMsg{})
		m = next.(Model)
		for _, msg := range collectMsgs(cmd) {
			if cm, ok := msg.(monitorCheckMsg); ok {
				next, _ := m.Update(cm)
				m = next.(Model)
			}
		}
	}
	if !m.loading {
		t.Fatal("a timeout was silent")
	}
	if last := m.messages[len(m.messages)-1].Content; !strings.Contains(last, "WITHOUT") || !strings.Contains(last, "timed-out") {
		t.Fatalf("wake-up text: %s", last)
	}
}

func TestWaitForRejectsBadPattern(t *testing.T) {
	m := newLoopTestModel()
	m.ensureScheduleOwner()
	out := m.armWait(chat.WaitRequest{Script: "x", Until: "match", Pattern: "(", Timeout: time.Minute, IntervalSeconds: 5})
	if !strings.Contains(out, "rejected") || len(m.loops.List()) != 0 {
		t.Fatalf("got %q", out)
	}
}
