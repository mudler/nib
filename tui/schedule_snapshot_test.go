package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	"testing"
	"time"
)

type scheduleClock struct {
	now       time.Time
	due       []time.Time
	callbacks []func(time.Time) tea.Msg
}

func (c *scheduleClock) install(m *Model) {
	m.scheduleNow = func() time.Time { return c.now }
	m.scheduleTick = func(d time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
		c.due = append(c.due, c.now.Add(d))
		c.callbacks = append(c.callbacks, f)
		return func() tea.Msg { return nil }
	}
}
func (c *scheduleClock) fire(t *testing.T, i int) tea.Msg {
	t.Helper()
	if c.now.Before(c.due[i]) {
		t.Fatal("fake clock fired early")
	}
	return c.callbacks[i](c.now)
}
func updateSchedule(m *Model, msg tea.Msg) { next, _ := m.Update(msg); *m = next.(Model) }
func TestScheduleSnapshotWakeupLifecycle(t *testing.T) {
	m := newWakeupTestModel()
	c := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.install(&m)
	req := wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60, Prompt: "same"}, epoch: m.scheduleEpoch}
	updateSchedule(&m, req)
	updateSchedule(&m, req)
	s := m.collectScheduleSnapshot()
	if !s.Known || len(s.Entries) != 2 || s.Entries[0].ID == s.Entries[1].ID {
		t.Fatalf("actual independent arms missing: %+v", s)
	}
	c.now = c.now.Add(59 * time.Second)
	due, ok := earliestScheduledRun(m.collectScheduleSnapshot())
	if !ok || !due.Equal(c.now.Add(time.Second)) {
		t.Fatal("wrong pre-due time")
	}
	c.now = c.now.Add(time.Second)
	fire := c.fire(t, 0)
	updateSchedule(&m, fire)
	after := m.collectScheduleSnapshot()
	if len(after.Entries) != 1 || after.Revision <= s.Revision || len(m.messages) != 1 {
		t.Fatalf("fire not consumed/dispatched: %+v", after)
	}
	updateSchedule(&m, fire)
	if m.collectScheduleSnapshot().Revision != after.Revision || len(m.messages) != 1 {
		t.Fatal("duplicate fire changed state")
	}
	// Busy wakeups drop rather than queue, but still leave the pending registry.
	updateSchedule(&m, c.fire(t, 1))
	if len(m.pendingWakeups) != 0 || len(m.queue) != 0 || len(m.messages) != 1 {
		t.Fatal("busy wakeup dispatch semantics changed")
	}
	updateSchedule(&m, req)
	old := c.callbacks[2](c.now)
	m.resetSchedules()
	rev := m.scheduleRevision
	updateSchedule(&m, req)
	updateSchedule(&m, old)
	if len(m.pendingWakeups) != 0 || m.scheduleRevision != rev {
		t.Fatal("old arm/fire resurrected after reset")
	}
	req.epoch = m.scheduleEpoch
	updateSchedule(&m, req)
	if len(m.pendingWakeups) != 1 {
		t.Fatal("new epoch cannot arm")
	}
}
func TestScheduleSnapshotInvalidation(t *testing.T) {
	m := newWakeupTestModel()
	m.loops = loop.NewRegistry()
	c := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.install(&m)
	updateSchedule(&m, wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60, Poll: true}})
	updateSchedule(&m, wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60}})
	updateSchedule(&m, parkMsg{parked: false})
	if len(m.pendingWakeups) != 1 {
		t.Fatal("resume must invalidate only poll")
	}
	m.selfPaced = 1
	m.stopLoop("")
	if len(m.pendingWakeups) != 0 {
		t.Fatal("stop did not cancel reminder")
	}
	m.loading = false // The resumed run has settled before the orphan timers fire.
	c.now = c.now.Add(time.Minute)
	updateSchedule(&m, c.fire(t, 0))
	updateSchedule(&m, c.fire(t, 1))
	if m.loading {
		t.Fatal("cancelled callback dispatched")
	}
	m.scheduleTick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	updateSchedule(&m, wakeupScheduledMsg{})
	if len(m.pendingWakeups) != 0 {
		t.Fatal("failed arm recorded")
	}
}
func TestScheduleSnapshotEarliestAndCronDispatch(t *testing.T) {
	m := newWakeupTestModel()
	c := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.install(&m)
	m.loops = loop.NewRegistry()
	m.loops.SetClock(func() time.Time { return c.now })
	j, _ := m.loops.Add("* * * * *", "cron", false, false, loop.MonitorConfig{})
	updateSchedule(&m, wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 30}})
	due, _ := earliestScheduledRun(m.collectScheduleSnapshot())
	if !due.Equal(c.now.Add(30 * time.Second)) {
		t.Fatal("earliest wakeup missing")
	}
	m.invalidateWakeups(false)
	due, _ = earliestScheduledRun(m.collectScheduleSnapshot())
	if !due.Equal(c.now.Add(time.Minute)) {
		t.Fatal("earliest cron missing")
	}
	m.loading = true
	before := m.loops.ScheduleSnapshot()
	updateSchedule(&m, cronFireMsg{prompt: "manual", epoch: m.scheduleEpoch})
	if m.loops.ScheduleSnapshot().Revision != before.Revision {
		t.Fatal("manual trigger advanced schedule")
	}
	c.now = c.now.Add(time.Minute)
	updateSchedule(&m, loopTickMsg{})
	if len(m.queue) != 2 || m.queue[0].text != "manual" || m.queue[1].text != "cron" {
		t.Fatalf("dispatch order changed: %+v", m.queue)
	}
	if len(m.collectScheduleSnapshot().Entries) != 0 {
		t.Fatal("queued one shot still pending")
	}
	if _, ok := m.loops.Get(j.ID); ok {
		t.Fatal("one shot retained")
	}
	old := cronFireMsg{prompt: "stale", epoch: m.scheduleEpoch}
	m.resetSchedules()
	updateSchedule(&m, old)
	if len(m.queue) != 2 {
		t.Fatal("old manual trigger crossed reset")
	}
	s := scheduleSnapshot{Known: true, Generation: 7, Entries: []scheduledRun{{Generation: 6, Active: true, NextDue: c.now}, {Generation: 7, Active: true}, {Generation: 7, NextDue: c.now}}}
	if _, ok := earliestScheduledRun(s); ok {
		t.Fatal("unknown/inactive/stale due selected")
	}
	s.Known = false
	if _, ok := earliestScheduledRun(s); ok {
		t.Fatal("unknown selected")
	}
}

func TestScheduleSnapshotReplacementUnknown(t *testing.T) {
	m := newWakeupTestModel()
	m.sessionReady = false // applyResume keeps the old pointer until init completes.
	if m.collectScheduleSnapshot().Known {
		t.Fatal("replacement snapshot claims old session is current")
	}
}

func TestScheduleSnapshotMailboxCrossesReplacement(t *testing.T) {
	m := newWakeupTestModel()
	m.ctx = context.Background()
	m.wakeupChan = make(chan wakeupScheduledMsg, 1)
	m.cronFireChan = make(chan cronFireMsg, 1)
	c := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.install(&m)
	// These values carry the callback owner's epoch before either mailbox drains.
	m.wakeupChan <- wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60}, epoch: m.scheduleEpoch}
	m.cronFireChan <- cronFireMsg{prompt: "obsolete", epoch: m.scheduleEpoch}
	updateSchedule(&m, wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60}, epoch: m.scheduleEpoch})
	oldFire := c.callbacks[0](c.now)
	m.session = nil // No live execution in this fixture; exercise the real replacement boundary.
	m.applyResume(chat.SessionRecord{})
	updateSchedule(&m, m.listenWakeup()())
	updateSchedule(&m, m.listenCronFire()())
	updateSchedule(&m, oldFire)
	if len(m.pendingWakeups) != 0 || len(m.queue) != 0 || m.collectScheduleSnapshot().Known {
		t.Fatal("replacement accepted obsolete mailbox state")
	}
}
