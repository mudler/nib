package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// exitArmWindow is how long an armed exit waits for the second Ctrl+C.
const exitArmWindow = 2 * time.Second

// exitDisarmMsg disarms the exit armed with the same seq, when exitArmWindow
// passes without a second Ctrl+C.
type exitDisarmMsg struct{ seq int }

// dialogOpen reports whether a dialog or panel owns the keyboard. Ctrl+C then
// acts as Esc, which each of them handles as "close" or "cancel".
func (m Model) dialogOpen() bool {
	return m.panelOpen() || m.showLogs ||
		m.loginWait.active || m.loginForm.active ||
		m.providerPicker.active || m.modelPicker.active ||
		m.awaitingApproval || m.awaitingAsk || m.awaitingResume
}

// foregroundBusy reports whether there is foreground work for an interrupt to
// stop: a turn in flight or a run that is still alive. Background sub-agents
// and shell jobs do not count, because an interrupt does not stop them.
func (m Model) foregroundBusy() bool {
	return (m.loading || m.parked) && !m.interruptArmed
}

// handleCtrlC does the first of these that applies, and only that:
//
//  1. clear the draft in the composer (↑ restores it),
//  2. interrupt foreground work,
//  3. arm the exit and warn what it stops,
//  4. quit, if the exit is already armed.
//
// Dialogs never get here: Update turns Ctrl+C into Esc while one is open.
//
// One step per press is the point. Ctrl+C used to quit whenever nothing was
// running, so a press meant to clear a draft or close a dialog closed nib.
func (m Model) handleCtrlC() (tea.Model, tea.Cmd) {
	if draft := m.textarea.Value(); strings.TrimSpace(draft) != "" {
		m.pushHistory(draft)
		m.textarea.Reset()
		m.completion.sync("")
		m.hint = theme.HintDraftCleared
		m.reflowLayout()
		return m, nil
	}
	if m.foregroundBusy() {
		return m.interrupt()
	}
	if m.exitArmed {
		return m.quit()
	}
	m.exitArmed = true
	m.exitSeq++
	m.hint = exitWarning(m.backgroundWork())
	seq := m.exitSeq
	m.updateViewport()
	return m, tea.Tick(exitArmWindow, func(time.Time) tea.Msg { return exitDisarmMsg{seq: seq} })
}

// handleEsc closes the completion popup, cancels an ask_user question, or
// interrupts foreground work. It never quits: Esc is pressed to back out of
// things, and it used to close nib when there was nothing left to back out of.
func (m Model) handleEsc() (tea.Model, tea.Cmd) {
	switch {
	case m.completion.active:
		m.completion.active = false
		m.completion.matches = nil
		m.reflowLayout()
		return m, nil
	case m.awaitingAsk:
		return m.resolveAsk("")
	case m.foregroundBusy():
		return m.interrupt()
	}
	return m, nil
}

// interrupt cancels the in-flight turn. The turn's end (responseMsg) reports
// what the interrupt stopped and what it did not.
//
// The queue is not held: what the user queued behind the turn is sent when
// the turn ends, as after any turn. It stops the self-paced loop, whose
// re-arming turn is the one being cancelled.
func (m Model) interrupt() (tea.Model, tea.Cmd) {
	if m.session != nil {
		m.session.Interrupt()
	}
	m.interruptArmed = true
	m.toolEvents.end()
	m.clearRunning()
	m.selfPaced = 0
	m.status = theme.StatusInterrupting
	m.updateViewport()
	return m, nil
}

// disarmExit cancels an armed exit and its warning.
func (m *Model) disarmExit() {
	m.exitArmed = false
	m.hint = ""
}

// backgroundWork lists the work that keeps running without a turn: sub-agents,
// shell jobs and loops. Both the interrupt notice ("still running") and the
// exit warning ("stops") name it, so neither leaves it invisible.
func (m Model) backgroundWork() []string {
	var parts []string
	agents := 0
	for _, j := range m.jobs {
		if j.Status == chat.AgentStatusRunning {
			agents++
		}
	}
	shells := 0
	for _, j := range m.shellJobs.List() {
		if j.Running {
			shells++
		}
	}
	loops := m.selfPaced
	if m.loops != nil {
		loops += len(m.loops.List())
	}
	parts = appendCount(parts, shells, "shell job")
	parts = appendCount(parts, agents, "sub-agent")
	parts = appendCount(parts, loops, "loop")
	return parts
}

func appendCount(parts []string, n int, noun string) []string {
	switch {
	case n == 1:
		return append(parts, "1 "+noun)
	case n > 1:
		return append(parts, fmt.Sprintf("%d %ss", n, noun))
	}
	return parts
}

// exitWarning is the hint an armed exit shows.
func exitWarning(work []string) string {
	if len(work) == 0 {
		return theme.HintExitArmed
	}
	return theme.HintExitArmed + " · stops " + strings.Join(work, ", ")
}

// interruptNotice is the transcript line an interrupted turn leaves: what the
// interrupt paused, what is sent next, and what it did not stop.
func (m Model) interruptNotice() string {
	lines := []string{"interrupted."}
	if m.session != nil && m.session.GoalPaused() {
		lines = append(lines, theme.NoticeGoalPaused)
	}
	if n := len(m.queue) + len(m.redispatch); n > 0 {
		lines = append(lines, fmt.Sprintf(theme.NoticeQueueSending, n))
	}
	if work := m.backgroundWork(); len(work) > 0 {
		lines = append(lines, "still running: "+strings.Join(work, ", ")+theme.NoticeStillRunningHelp)
	}
	return strings.Join(lines, "\n")
}
