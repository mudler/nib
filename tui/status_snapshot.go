package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/tui/render"
	"time"
)

type statusSnapshot struct {
	Execution       chat.ActivitySnapshot
	Schedule        scheduleSnapshot
	Known, Coherent bool
}

// Collection is bounded and synchronous on Update. The request binds two
// independent namespaces: a session pointer and its chat generation, and the
// TUI schedule epoch. ScheduleSnapshot.Generation is already tagged by chat,
// not by scheduleEpoch. No numeric equality between those namespaces is used.
type statusRequest struct {
	session           *chat.Session
	epoch, generation uint64
}

// An attachment dispatch can return before chat starts a root execution (read,
// extraction, transcription failure, or nothing sendable). Only its own result
// may settle that transition; unrelated revisions and obsolete results cannot.
type statusDispatch struct {
	owner    statusRequest
	sequence uint64
}

func (m *Model) settleAttachmentStatus(dispatch statusDispatch) bool {
	if dispatch != m.statusAttachment || dispatch.owner.session != m.session || dispatch.owner.epoch != m.scheduleEpoch || m.session == nil || dispatch.owner.generation != m.session.ActivitySnapshot().Generation {
		return false
	}
	m.statusAttachment = statusDispatch{}
	m.statusPending = false
	// This acknowledges dispatch completion, not readiness. Update still collects
	// coherent authoritative execution and barrier state before displaying Ready.
	return true
}

type statusRefreshMsg struct{ sequence uint64 }

func (m Model) statusTick() tea.Cmd {
	sequence := m.statusTickSequence
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return statusRefreshMsg{sequence} })
}
func (m *Model) reconcileStatus() {
	if m.quitting || !m.sessionReady || m.session == nil {
		m.statusSnapshot = statusSnapshot{}
		return
	}
	request := statusRequest{m.session, m.scheduleEpoch, m.session.ActivitySnapshot().Generation}
	if m.statusOwner != request {
		m.statusOwner = request
		m.statusSnapshot = statusSnapshot{}
		m.statusExecutionRevision, m.statusScheduleRevision = 0, 0
		// Pending input may precede the first collection for this owner.
	}
	for attempt := 0; attempt < 3; attempt++ {
		before := request.session.ActivitySnapshot()
		schedule := m.collectScheduleSnapshot()
		after := request.session.ActivitySnapshot()
		if before.Generation != after.Generation || before.Revision != after.Revision {
			continue
		}
		if m.acceptStatus(request, statusSnapshot{after, schedule, true, true}) {
			return
		}
	}
	m.statusSnapshot.Known = false
	m.statusSnapshot.Coherent = false
}
func (m *Model) acceptStatus(request statusRequest, s statusSnapshot) bool {
	if request != m.statusOwner || request.session != m.session || request.epoch != m.scheduleEpoch || !m.sessionReady || m.quitting {
		return false
	}
	e := s.Execution
	if !s.Known || !s.Coherent || !e.Known || !e.Coherent || !s.Schedule.Known || e.Generation != request.generation || s.Schedule.Generation != request.generation || e.Revision < m.statusExecutionRevision || s.Schedule.Revision < m.statusScheduleRevision {
		return false
	}
	if m.compactPending {
		return false
	}
	if m.statusPending && !e.RootActive && e.RootExecutionSequence <= m.statusPendingExecution {
		return false
	}
	m.statusPending = false
	m.statusExecutionRevision, m.statusScheduleRevision = e.Revision, s.Schedule.Revision
	m.statusSnapshot = s
	return true
}
func (m *Model) invalidateStatus() {
	m.statusDispatchSequence++
	m.statusAttachment = statusDispatch{}
	m.invalidatePendingStatus()
}

// Input acknowledgement is independent of the enclosing response ownership.
// A follow-up resumes the same run; only a new dispatch invalidates its result.
func (m *Model) invalidatePendingStatus() {
	if !m.statusPending {
		m.statusPendingExecution = m.statusSnapshot.Execution.RootExecutionSequence
		if m.session != nil {
			m.statusPendingExecution = m.session.ActivitySnapshot().RootExecutionSequence
		}
	}
	m.statusPending = true
	m.statusSnapshot.Known = false
	m.statusSnapshot.Coherent = false
}

func summarizeStatus(s statusSnapshot, now time.Time) render.ActivitySummary {
	result := render.ActivitySummary{Primary: "Working", Compact: "Working", Secondary: "status updating", Updating: true, Marker: render.SummaryMarkerWorking}
	e := s.Execution
	if !s.Known || !s.Coherent || !e.Known || !e.Coherent || !s.Schedule.Known || e.Generation != s.Schedule.Generation {
		return result
	}
	result.CountsKnown = true
	agents, shells := map[string]bool{}, map[string]bool{}
	for _, j := range e.Agents {
		if j.Running {
			agents[j.ID] = true
		}
	}
	for _, j := range e.Shells {
		if j.Running && j.Background {
			shells[j.ID] = true
		}
	}
	result.Agents, result.Shells = len(agents), len(shells)
	jobs := result.Agents + result.Shells
	b := e.Barrier
	review := b.Known && (b.Publishers > 0 || b.QueuedNotices > 0 || b.ReservedNotices > 0 || b.EventSequence > b.RootObservedSequence || b.SupervisorQueued || b.SupervisorReviewing)
	switch {
	case e.RootActive:
		result.Updating = false
	case e.RootParked && jobs > 0:
		result.Primary = "Waiting for jobs"
		result.Marker = render.SummaryMarkerParked
		result.Updating = false
	case jobs > 0:
	case review:
		result.Primary = "Reviewing results"
		result.Updating = false
	case b.Known && e.ReadyAllowed:
		result.Primary = "Ready for input"
		result.Marker = render.SummaryMarkerReady
		result.Updating = false
	}
	result.Compact = result.Primary
	if !result.Updating {
		result.Secondary = ""
	}
	if due, ok := earliestScheduledRun(s.Schedule); ok {
		d := due.Sub(now)
		switch {
		case d <= 0:
			result.Schedule = "next run due now"
		case d < time.Minute:
			result.Schedule = "next run in less than 1m"
		default:
			minutes := d / time.Minute
			if d%time.Minute != 0 {
				minutes++
			}
			result.Schedule = fmt.Sprintf("next run in %dm", minutes)
		}
	}
	return result
}

// Capture the execution baseline BEFORE publishing input: a resumed run can
// finish before InjectWithDelivery returns. Failed injection owns no transition.
func (m *Model) injectStatus(text string, delivery chat.InputDelivery) bool {
	pending, sequence, snapshot := m.statusPending, m.statusPendingExecution, m.statusSnapshot
	m.invalidatePendingStatus()
	if m.session.InjectWithDelivery(text, delivery) {
		return true
	}
	m.statusPending, m.statusPendingExecution, m.statusSnapshot = pending, sequence, snapshot
	return false
}
