package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// noticeTailLimit caps how much job output is inlined into an auto-notify prompt
// (the model can read the rest with bash_job_output / get_agent_result).
const noticeTailLimit = 600

// tailOf returns the trailing portion of s, trimmed and capped.
func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > noticeTailLimit {
		s = "…" + s[len(s)-noticeTailLimit:]
	}
	return s
}

// collectCompletions queues a one-time notice for each newly-finished piece of
// background work: a backgrounded shell job, or a sub-agent the user detached
// with Ctrl+B. It only collects (marking each id seen); delivery happens in
// autoNotifyCmd when the session is idle.
func (m *Model) collectCompletions() {
	if m.notifiedJobs == nil {
		m.notifiedJobs = map[string]bool{}
	}

	// Backgrounded shell jobs (normal foreground commands are consumed inline).
	for _, j := range m.shellJobs.List() {
		if j.Running || !j.Backgrounded || m.notifiedJobs[j.ID] {
			continue
		}
		m.notifiedJobs[j.ID] = true
		notice := "shell job " + j.ID + " " + j.Status
		if so, se, ok := m.shellJobs.Output(j.ID); ok {
			if tail := tailOf(so + se); tail != "" {
				notice += ":\n" + tail
			}
		}
		m.pendingNotices = append(m.pendingNotices, notice)
	}

	// Sub-agents running unattended: spawned with background=true, or a
	// foreground agent the user backgrounded with Ctrl+B. Foreground agents
	// whose result is consumed inline are excluded.
	if m.session != nil {
		for _, a := range m.session.AgentManager().List() {
			if m.notifiedJobs[a.ID] {
				continue
			}
			if !a.Background && !m.bgAgents[a.ID] {
				continue
			}
			st := string(a.Status)
			if st != "completed" && st != "failed" {
				continue
			}
			m.notifiedJobs[a.ID] = true
			notice := "sub-agent " + shortID(a.ID) + " " + st
			switch {
			case a.Result != "":
				notice += ":\n" + tailOf(a.Result)
			case a.Error != nil:
				notice += ": " + a.Error.Error()
			}
			m.pendingNotices = append(m.pendingNotices, notice)
		}
	}
}

// canAutoNotify reports whether it's a good moment to fire an automatic turn:
// the session is ready and idle, and the user isn't mid-compose or being asked
// something.
func (m Model) canAutoNotify() bool {
	return m.sessionReady && m.session != nil && !m.loading &&
		!m.awaitingApproval && !m.awaitingAsk &&
		strings.TrimSpace(m.textarea.Value()) == ""
}

// autoNotifyCmd collects any new completions and, when the session is idle,
// fires an automatic turn so the assistant reacts to the finished work. It
// mutates the model and returns the turn command (or nil).
func (m *Model) autoNotifyCmd() tea.Cmd {
	m.collectCompletions()
	if !m.canAutoNotify() || len(m.pendingNotices) == 0 {
		return nil
	}

	notices := m.pendingNotices
	m.pendingNotices = nil

	var b strings.Builder
	b.WriteString("You have background updates to act on. Check on them if useful ")
	b.WriteString("(read a shell job's output with bash_job_output, list jobs with bash_jobs, ")
	b.WriteString("inspect a sub-agent with agent_logs / check_agent / get_agent_result), ")
	b.WriteString("then continue or acknowledge briefly:\n")
	for _, n := range notices {
		b.WriteString("- ")
		b.WriteString(n)
		b.WriteString("\n")
	}

	m.messages = append(m.messages, ChatMessage{Role: "agent", Content: "reacting to background updates…"})
	m.loading = true
	m.interruptArmed = false
	m.status = "Reacting to finished background work…"
	m.updateViewport()
	return m.sendMessage(b.String())
}
