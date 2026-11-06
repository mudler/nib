package tui

import (
	"context"
	"fmt"
	"regexp"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/watch"
)

// monitorCheckMsg carries the result of a monitor or wait check back to the UI
// goroutine. The check itself runs in a tea.Cmd, never in Update: a script can
// take 30 seconds.
type monitorCheckMsg struct {
	id  string
	res watch.Result
	at  time.Time
}

// startCheck runs the source of a due monitor or wait job off the UI
// goroutine. It returns nil when the job is gone or its last check is still
// running, so a slow source skips ticks instead of piling up.
func (m *Model) startCheck(id string) tea.Cmd {
	j, ok := m.loops.BeginCheck(id)
	if !ok {
		return nil
	}
	src, reg := j.Source(), m.loops
	run := m.runCheck
	if run == nil {
		run = watch.Run
	}
	return func() tea.Msg {
		return monitorCheckMsg{id: id, res: run(context.Background(), src), at: reg.Now()}
	}
}

// applyCheck records a check result and, when the job reports something,
// wakes the agent. The registry stores the new state before it says to
// dispatch, and a durable job is saved before the dispatch, so a failed agent
// run cannot make the same change alert again.
func (m *Model) applyCheck(msg monitorCheckMsg) tea.Cmd {
	d := m.loops.Observe(msg.id, msg.res, msg.at)
	if d.Persist {
		_ = m.loops.Save(m.loopsPath)
	}
	if m.quitting {
		return nil
	}
	if d.Err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: d.Err.Error()})
		m.updateViewport()
	}
	if d.Dispatch {
		return m.dispatchLoop(d.Prompt)
	}
	return nil
}

// armWait registers the wait a wait_for call asked for. Like the cron
// callbacks it runs on a tool goroutine, so it only touches the registry.
func (m Model) armWait(req chat.WaitRequest) string {
	m.scheduleOwner.mu.Lock()
	defer m.scheduleOwner.mu.Unlock()
	if m.scheduleOwner.epoch != m.scheduleEpoch {
		return "wait_for rejected: session replaced"
	}
	w := watch.Wait{
		Source:    watch.Source{Script: req.Script, URL: req.URL},
		Pattern:   req.Pattern,
		Timeout:   req.Timeout,
		MaxErrors: req.MaxErrors,
	}
	switch req.Until {
	case "match":
		w.Mode = watch.UntilMatch
		if req.Pattern == "" {
			return "wait_for rejected: until=match needs a pattern"
		}
		if _, err := regexp.Compile(req.Pattern); err != nil {
			return "wait_for rejected: pattern: " + err.Error()
		}
	case "exit_zero":
		w.Mode = watch.UntilExitZero
	default:
		w.Mode = watch.UntilChange
	}
	interval := time.Duration(req.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	j, err := m.loops.AddWait(durationToCron(interval), w, req.Then)
	if err != nil {
		return "wait_for rejected: " + err.Error()
	}
	return fmt.Sprintf("Armed %s: %s. Checking every %s. End your turn now: you are woken when the condition is met, the source keeps failing, or the timeout passes. Cancel with cron_delete.", j.ID, j.Label(), interval)
}
