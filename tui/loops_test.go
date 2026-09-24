package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	"github.com/mudler/nib/tui/render"
)

// newLoopTestModel builds an idle, session-ready model with a fresh registry,
// suitable for exercising the loop dispatch helpers without a live run.
func newLoopTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	return newTestModel(Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		session:      &chat.Session{}, // RunLive()==false → idle branch
		sessionReady: true,
		loops:        loop.NewRegistry(),
	})
}

func TestStopLoopAll(t *testing.T) {
	m := newLoopTestModel()
	if _, err := m.loops.Add("*/5 * * * *", "/a", true, false, loop.MonitorConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.loops.Add("*/5 * * * *", "/b", true, false, loop.MonitorConfig{}); err != nil {
		t.Fatal(err)
	}
	got := m.stopLoop("")
	if got != "Stopped 2 loop(s)." {
		t.Fatalf("stopLoop(\"\") = %q want %q", got, "Stopped 2 loop(s).")
	}
	if len(m.loops.List()) != 0 {
		t.Fatalf("registry should be empty after stop-all, got %d", len(m.loops.List()))
	}
}

func TestStopLoopByID(t *testing.T) {
	m := newLoopTestModel()
	j, err := m.loops.Add("*/5 * * * *", "/a", true, false, loop.MonitorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.stopLoop(j.ID); got != "Stopped "+j.ID {
		t.Fatalf("stopLoop(%q) = %q want %q", j.ID, got, "Stopped "+j.ID)
	}
	if got := m.stopLoop("loop-nope"); got != "No such loop: loop-nope" {
		t.Fatalf("stopLoop unknown = %q", got)
	}
}

func TestListLoops(t *testing.T) {
	m := newLoopTestModel()
	if got := m.listLoops(); got != "No active loops." {
		t.Fatalf("empty listLoops = %q", got)
	}
	j, err := m.loops.Add("*/5 * * * *", "/a", true, false, loop.MonitorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	got := m.listLoops()
	if !strings.Contains(got, j.ID) || !strings.Contains(got, "*/5 * * * *") {
		t.Fatalf("listLoops = %q, want id+expr", got)
	}
}

func TestDispatchLoopIdle(t *testing.T) {
	m := newLoopTestModel()
	cmd := m.dispatchLoop("do the thing")
	if cmd == nil {
		t.Fatal("idle dispatchLoop should return a send command")
	}
	if !m.loading {
		t.Fatal("idle dispatchLoop should set loading")
	}
	if len(m.messages) != 1 || m.messages[0].Role != "user" || m.messages[0].Content != "do the thing" {
		t.Fatalf("expected one user message, got %v", m.messages)
	}
}

func TestDispatchLoopNoConcurrentTurns(t *testing.T) {
	m := newLoopTestModel() // sessionReady=true, session=&chat.Session{}, parked=false, loading=false
	// First due job: idle → starts a turn (returns a non-nil cmd, sets loading).
	cmd1 := m.dispatchLoop("/foo")
	if cmd1 == nil {
		t.Fatal("first dispatch should start a turn (non-nil cmd)")
	}
	if !m.loading {
		t.Fatal("first dispatch should set loading")
	}
	// Second job in the SAME tick: must NOT start another turn; it queues.
	cmd2 := m.dispatchLoop("/bar")
	if cmd2 != nil {
		t.Fatal("second dispatch must NOT start a concurrent turn")
	}
	if len(m.queue) != 1 || m.queue[0] != "/bar" {
		t.Fatalf("second dispatch should queue, got queue=%v", m.queue)
	}
}

func TestDurationToCron(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "*/30 * * * * *"}, // sub-minute → 6-field seconds expr
		{5 * time.Second, "*/5 * * * * *"},
		{1 * time.Second, "*/1 * * * * *"},
		{5 * time.Minute, "*/5 * * * *"},
		{1 * time.Hour, "0 */1 * * *"},
		{90 * time.Minute, "0 * * * *"}, // ≥1h non-aligned → hourly (minute step can't exceed 59)
	}
	for _, c := range cases {
		if got := durationToCron(c.d); got != c.want {
			t.Fatalf("durationToCron(%s) = %q want %q", c.d, got, c.want)
		}
	}
}

func TestDurationToCronParses(t *testing.T) {
	for _, d := range []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second, 5 * time.Minute, 1 * time.Hour, 90 * time.Minute} {
		if _, err := loop.Parse(durationToCron(d)); err != nil {
			t.Fatalf("durationToCron(%s)=%q failed to parse: %v", d, durationToCron(d), err)
		}
	}
}

// TestLoopsFooterRow exercises loopsFooterRow, the live path View() actually
// calls (renderLoopsFooter, the fully-styled string-returning function this
// test used to pin, was deleted once it had zero production call sites left
// — see the Task 6 fix-round-2 report).
func TestLoopsFooterRow(t *testing.T) {
	r := loop.NewRegistry()
	if _, ok := loopsFooterRow(r, 0); ok {
		t.Fatal("empty registry should report nothing to show")
	}
	r.Add("*/5 * * * *", "/foo", true, false, loop.MonitorConfig{})
	row, ok := loopsFooterRow(r, 0)
	if !ok {
		t.Fatal("expected a row with one job")
	}
	if row.Kind != render.FooterLoops {
		t.Fatalf("expected FooterLoops kind, got %v", row.Kind)
	}
}

func TestListLoopsMarksPaused(t *testing.T) {
	m := newLoopTestModel()
	j, _ := m.loops.Add("*/5 * * * *", "/a", true, false, loop.MonitorConfig{})
	m.loops.Pause(j.ID)
	if got := m.listLoops(); !strings.Contains(got, "(paused)") {
		t.Fatalf("listLoops = %q, want the paused job marked", got)
	}
}

// cron_trigger mid-turn must queue the prompt, not start a second turn.
func TestCronFireQueuesDuringTurn(t *testing.T) {
	m := newLoopTestModel()
	m.loading = true
	got, _ := m.Update(cronFireMsg("/report"))
	m = got.(Model)
	if len(m.queue) != 1 || m.queue[0] != "/report" {
		t.Fatalf("queue = %v, want the triggered prompt queued", m.queue)
	}
}

// A durable job that fires is saved at once, so a restart cannot fire the
// same slot again.
func TestLoopTickSavesAfterDurableFire(t *testing.T) {
	m := newLoopTestModel()
	m.loopsPath = filepath.Join(t.TempDir(), "loops.json")
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.Local)
	m.loops.SetClock(func() time.Time { return now })
	if _, err := m.loops.Add("*/5 * * * *", "/a", false, true, loop.MonitorConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := m.loops.Save(m.loopsPath); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	got, _ := m.Update(loopTickMsg{})
	m = got.(Model)

	r := loop.NewRegistry()
	if n, err := r.Load(m.loopsPath); err != nil || n != 0 {
		t.Fatalf("reloaded %d jobs (err %v); the fired one-shot must be gone from disk", n, err)
	}
}
