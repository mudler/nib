package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"sort"
	"time"
)

type scheduledRun struct {
	ID         string
	Generation uint64
	Active     bool
	NextDue    time.Time
}
type scheduleSnapshot struct {
	Generation, Revision uint64
	Known                bool
	Entries              []scheduledRun
}
type pendingWakeup struct {
	due  time.Time
	fire wakeupFireMsg
}

// collectScheduleSnapshot runs on the Update owner, never in rendering or a
// worker. Only the cron registry is shared with tool callbacks. Its read takes
// its own mutex; no callback or dispatch runs while that mutex is held.
func (m *Model) collectScheduleSnapshot() scheduleSnapshot {
	s := scheduleSnapshot{Revision: m.scheduleRevision, Known: !m.quitting && m.sessionReady && m.session != nil}
	if m.session != nil {
		s.Generation = m.session.ActivitySnapshot().Generation
	}
	for id, w := range m.pendingWakeups {
		s.Entries = append(s.Entries, scheduledRun{ID: id, Generation: s.Generation, Active: true, NextDue: w.due})
	}
	if m.loops != nil {
		c := m.loops.ScheduleSnapshot()
		s.Revision += c.Revision
		for _, e := range c.Entries {
			s.Entries = append(s.Entries, scheduledRun{ID: e.ID, Generation: s.Generation, Active: e.Active, NextDue: e.NextDue})
		}
	}
	sort.Slice(s.Entries, func(i, j int) bool { return s.Entries[i].ID < s.Entries[j].ID })
	return s
}
func earliestScheduledRun(s scheduleSnapshot) (time.Time, bool) {
	var due time.Time
	if !s.Known {
		return due, false
	}
	for _, e := range s.Entries {
		if e.Generation == s.Generation && e.Active && !e.NextDue.IsZero() && (due.IsZero() || e.NextDue.Before(due)) {
			due = e.NextDue
		}
	}
	return due, !due.IsZero()
}

// The immutable epoch belongs to the session callback closure, not the mailbox
// consumer. A delayed request from a replaced session cannot acquire a new epoch.
func (m *Model) armWakeup(req wakeupScheduledMsg) tea.Cmd {
	if req.epoch != m.scheduleEpoch || m.quitting {
		return nil
	}
	now, tick := m.scheduleNow, m.scheduleTick
	if now == nil {
		now = time.Now
	}
	if tick == nil {
		tick = tea.Tick
	}
	gen := m.wakeupGen
	if req.Poll {
		gen = m.pollGen
	}
	m.wakeupSeq++
	fire := wakeupFireMsg{id: fmt.Sprintf("wakeup-%d", m.wakeupSeq), epoch: m.scheduleEpoch, prompt: req.Prompt, gen: gen, poll: req.Poll}
	d := time.Duration(req.DelaySeconds) * time.Second
	due := now().Add(d)
	cmd := tick(d, func(time.Time) tea.Msg { return fire })
	if cmd == nil {
		return nil
	}
	if m.pendingWakeups == nil {
		m.pendingWakeups = make(map[string]pendingWakeup)
	}
	m.pendingWakeups[fire.id] = pendingWakeup{due: due, fire: fire}
	m.scheduleRevision++
	return cmd
}
func (m *Model) consumeWakeup(msg wakeupFireMsg) bool {
	if m.quitting || msg.epoch != m.scheduleEpoch {
		return false
	}
	w, ok := m.pendingWakeups[msg.id]
	if !ok || w.fire != msg {
		return false
	}
	delete(m.pendingWakeups, msg.id)
	m.scheduleRevision++
	return true
}
func (m *Model) invalidateWakeups(poll bool) {
	for id, w := range m.pendingWakeups {
		if w.fire.poll == poll {
			delete(m.pendingWakeups, id)
		}
	}
	m.scheduleRevision++
}
func (m *Model) resetSchedules() {
	m.statusSnapshot = statusSnapshot{}
	m.statusPending = false
	m.compactPending = false
	m.ensureScheduleOwner()
	m.scheduleOwner.mu.Lock()
	m.scheduleEpoch++
	m.scheduleOwner.epoch = m.scheduleEpoch
	m.scheduleOwner.mu.Unlock()
	m.scheduleRevision++
	m.wakeupGen++
	m.pollGen++
	m.pendingWakeups = nil
}
