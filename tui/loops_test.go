package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
)

// newLoopTestModel builds an idle, session-ready model with a fresh registry,
// suitable for exercising the loop dispatch helpers without a live run.
func newLoopTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	return Model{
		textarea:     ta,
		viewport:     viewport.New(80, 10),
		spinner:      spinner.New(),
		session:      &chat.Session{}, // RunLive()==false → idle branch
		sessionReady: true,
		loops:        loop.NewRegistry(),
	}
}

func TestStopLoopAll(t *testing.T) {
	m := newLoopTestModel()
	if _, err := m.loops.Add("*/5 * * * *", "/a", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.loops.Add("*/5 * * * *", "/b", true, false); err != nil {
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
	j, err := m.loops.Add("*/5 * * * *", "/a", true, false)
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
	j, err := m.loops.Add("*/5 * * * *", "/a", true, false)
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

func TestRenderLoopsFooter(t *testing.T) {
	r := loop.NewRegistry()
	if f := renderLoopsFooter(r, 0, 80); f != "" {
		t.Fatalf("empty registry should render nothing, got %q", f)
	}
	r.Add("*/5 * * * *", "/foo", true, false)
	if f := renderLoopsFooter(r, 0, 80); f == "" {
		t.Fatal("expected non-empty footer with one job")
	}
}
