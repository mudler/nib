package tui

import (
	"fmt"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/loop"
	"strings"
	"sync"
)

// scheduleOwner is shared by Model value copies and session callbacks. The
// lock serializes epoch validation plus cron mutation/persistence with reset.
// Lock order is owner -> registry; neither UI dispatch nor session Close runs
// under this lock.
type scheduleOwner struct {
	mu    sync.Mutex
	epoch uint64
}

func (m *Model) ensureScheduleOwner() {
	if m.scheduleOwner == nil {
		m.scheduleOwner = &scheduleOwner{epoch: m.scheduleEpoch}
	}
}

func (m *Model) scheduleCallbacks() chat.Callbacks {
	m.ensureScheduleOwner()
	// Capture a value copy: callbacks must never read the Update owner's fields.
	return m.sessionScheduleCallbacks()
}

func (m Model) sessionScheduleCallbacks() chat.Callbacks {
	return chat.Callbacks{
		OnScheduleWakeup: func(req chat.WakeupRequest) string {
			// Non-blocking: hand the request to the UI loop and confirm now. The
			// UI arms a timer; when it fires it injects the note into the live
			// run (see wakeupFireMsg).
			select {
			case m.wakeupChan <- wakeupScheduledMsg{WakeupRequest: req, epoch: m.scheduleEpoch}:
				if req.Reason != "" {
					return fmt.Sprintf("Scheduled a wake-up in %ds (%s). You'll be re-invoked then.", req.DelaySeconds, req.Reason)
				}
				return fmt.Sprintf("Scheduled a wake-up in %ds: %q. You'll be re-invoked then.", req.DelaySeconds, req.Prompt)
			default:
				return "Could not schedule wake-up (too many pending)."
			}
		},
		OnCronCreate: func(req chat.CronRequest) string {
			m.scheduleOwner.mu.Lock()
			defer m.scheduleOwner.mu.Unlock()
			if m.scheduleOwner.epoch != m.scheduleEpoch {
				return "cron rejected: session replaced"
			}
			j, err := m.loops.Add(req.Expr, req.Prompt, req.Recurring, req.Durable, loop.MonitorConfig{Script: req.MonitorScript, URL: req.MonitorURL})
			if err != nil {
				return "cron rejected: " + err.Error()
			}
			if req.Durable {
				_ = m.loops.Save(m.loopsPath)
			}
			return fmt.Sprintf("Scheduled %s (%s) → %q", j.ID, j.Expr, j.Prompt)
		},
		OnCronList: func() string {
			jobs := m.loops.List()
			if len(jobs) == 0 {
				return "No active cron loops."
			}
			var b strings.Builder
			for _, j := range jobs {
				b.WriteString(loopLine(j) + "\n")
			}
			return strings.TrimRight(b.String(), "\n")
		},
		OnCronDelete: func(id string) string {
			m.scheduleOwner.mu.Lock()
			defer m.scheduleOwner.mu.Unlock()
			if m.scheduleOwner.epoch != m.scheduleEpoch {
				return "cron rejected: session replaced"
			}
			if m.loops.Delete(id) {
				_ = m.loops.Save(m.loopsPath)
				return "Cancelled " + id
			}
			return "No such loop: " + id
		},
		OnCronPause: func(id string) string {
			m.scheduleOwner.mu.Lock()
			defer m.scheduleOwner.mu.Unlock()
			if m.scheduleOwner.epoch != m.scheduleEpoch {
				return "cron rejected: session replaced"
			}
			if m.loops.Pause(id) {
				_ = m.loops.Save(m.loopsPath)
				return "Paused " + id
			}
			return "No such loop: " + id
		},
		OnCronResume: func(id string) string {
			m.scheduleOwner.mu.Lock()
			defer m.scheduleOwner.mu.Unlock()
			if m.scheduleOwner.epoch != m.scheduleEpoch {
				return "cron rejected: session replaced"
			}
			if m.loops.Resume(id) {
				_ = m.loops.Save(m.loopsPath)
				return "Resumed " + id
			}
			return "No such loop: " + id
		},
		OnCronTrigger: func(id string) string {
			j, ok := m.loops.Get(id)
			if !ok {
				return "No such loop: " + id
			}
			// Hand the prompt to the UI loop, which queues it behind the
			// current turn like any cron fire (see dispatchLoop).
			select {
			case m.cronFireChan <- cronFireMsg{prompt: j.Prompt, epoch: m.scheduleEpoch}:
				return "Queued " + id + " to run after this turn."
			default:
				return "Could not run " + id + " now (too many pending)."
			}
		},
		OnParked: func(reply string) {
			m.toolEvents.push(toolEvent{park: &parkEvent{parked: true, reply: reply, epoch: m.scheduleEpoch}})
		},
		OnResumed: func() {
			m.toolEvents.push(toolEvent{park: &parkEvent{parked: false, epoch: m.scheduleEpoch}})
		},
	}
}
