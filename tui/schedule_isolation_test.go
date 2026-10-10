package tui

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
)

func replacementScheduleModel(t *testing.T) Model {
	t.Helper()
	m := newWakeupTestModel()
	m.ctx = context.Background()
	m.cfg.BaseDir = t.TempDir()
	s, err := chat.NewSession(m.ctx, m.cfg, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	m.session = s
	t.Cleanup(func() { s.Close() })
	m.loops = loop.NewRegistry()
	m.loopsPath = t.TempDir() + "/loops.json"
	m.cronFireChan = make(chan cronFireMsg, 8)
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin() // A live run can publish park/resume events.
	return m
}

func TestScheduleOldCronMutationsAcrossReplacement(t *testing.T) {
	for _, kind := range []string{"create", "delete", "pause", "resume"} {
		t.Run(kind, func(t *testing.T) {
			m := replacementScheduleModel(t)
			clock := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			clock.install(&m)
			m.loops.SetClock(func() time.Time { return clock.now })
			old := m.scheduleCallbacks()
			// Hold the originating callback until the actual replacement boundary passes.
			release, done := make(chan struct{}), make(chan struct{})
			var id string
			go func() {
				defer close(done)
				<-release
				switch kind {
				case "create":
					old.OnCronCreate(chat.CronRequest{Expr: "* * * * *", Prompt: "obsolete", Recurring: true, Durable: true})
				case "delete":
					old.OnCronDelete(id)
				case "pause":
					old.OnCronPause(id)
				case "resume":
					old.OnCronResume(id)
				}
			}()
			m.applyResume(chat.SessionRecord{})
			j, err := m.loops.Add("* * * * *", "replacement", true, true, loop.MonitorConfig{})
			if err != nil {
				t.Fatal(err)
			}
			id = j.ID
			if kind == "resume" {
				m.loops.Pause(id)
			}
			before := m.loops.ScheduleSnapshot()
			close(release)
			<-done
			if after := m.loops.ScheduleSnapshot(); !reflect.DeepEqual(before, after) {
				t.Errorf("old %s mutated replacement registry/revision: before=%+v after=%+v", kind, before, after)
			}
			clock.now = clock.now.Add(time.Minute)
			m.loading = true
			updateSchedule(&m, loopTickMsg{})
			want := 1
			if kind == "resume" {
				want = 0
			}
			if len(m.queue) != want || (want == 1 && m.queue[0].text != "replacement") {
				t.Errorf("old %s changed replacement dispatch: %+v", kind, m.queue)
			}
		})
	}
}

func TestScheduleQueuedOldParkResumeAcrossReplacement(t *testing.T) {
	for _, parked := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "park"}[parked], func(t *testing.T) {
			m := replacementScheduleModel(t)
			clock := scheduleClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			clock.install(&m)
			old := m.scheduleCallbacks()
			if parked {
				old.OnParked("obsolete reply")
			} else {
				old.OnResumed()
			}
			m.applyResume(chat.SessionRecord{})
			updateSchedule(&m, wakeupScheduledMsg{WakeupRequest: chat.WakeupRequest{DelaySeconds: 60, Poll: true}, epoch: m.scheduleEpoch})
			before, pollGen, messages := m.scheduleRevision, m.pollGen, len(m.messages)
			m.applyToolEvents(m.toolEvents.drain())
			if len(m.pendingWakeups) != 1 || m.scheduleRevision != before || m.pollGen != pollGen || m.parked || m.loading || len(m.messages) != messages {
				t.Fatalf("old park/resume changed replacement state: polls=%d revision=%d want=%d parked=%v loading=%v", len(m.pendingWakeups), m.scheduleRevision, before, m.parked, m.loading)
			}
			current := m.scheduleCallbacks()
			current.OnResumed()
			m.applyToolEvents(m.toolEvents.drain())
			if len(m.pendingWakeups) != 0 || m.scheduleRevision <= before || !m.loading {
				t.Fatal("current resume did not invalidate rightful poll")
			}
		})
	}
}
