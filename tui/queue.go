package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/theme"
)

// queueMoveSel moves the queue selection by delta, clamped to the queue bounds.
func (m *Model) queueMoveSel(delta int) {
	if len(m.queue) == 0 {
		m.queueSel = 0
		return
	}
	m.queueSel += delta
	if m.queueSel < 0 {
		m.queueSel = 0
	}
	if m.queueSel > len(m.queue)-1 {
		m.queueSel = len(m.queue) - 1
	}
}

// queueDeleteSel removes the selected entry and returns it (empty if none),
// keeping queueSel within bounds.
func (m *Model) queueDeleteSel() string {
	if m.queueSel < 0 || m.queueSel >= len(m.queue) {
		return ""
	}
	removed := m.queue[m.queueSel]
	m.queue = append(m.queue[:m.queueSel], m.queue[m.queueSel+1:]...)
	if m.queueSel > len(m.queue)-1 {
		m.queueSel = len(m.queue) - 1
	}
	if m.queueSel < 0 {
		m.queueSel = 0
	}
	return removed
}

// releaseQueueFront injects the oldest queued entry into the live run and
// reflects it as a transcript line. It is a no-op (returns false) when the
// queue is empty or no run is live; the entry stays queued and retries at the
// next boundary if the injection channel is momentarily full.
func (m *Model) releaseQueueFront() bool {
	if m.queueHeld || len(m.queue) == 0 || m.session == nil || !m.session.RunLive() {
		return false
	}
	front := m.queue[0]
	// Only plain messages inject into a live run. Slash commands / skills can't
	// run mid-turn, so leave them queued; flushQueueAsTurn resolves them when the
	// run ends.
	action := slash.Resolve(front, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
	if action.Kind != slash.KindSend {
		return false
	}
	// InjectUser only carries text; it can't convey @path attachments. Leaving an
	// attachment-bearing entry queued lets flushQueueAsTurn → dispatchInput handle
	// it at end-of-run, where attachments are honored — so the files aren't dropped.
	if len(action.Files) > 0 {
		return false
	}
	// InjectUser (not Inject) so a follow-up the run never consumes is handed
	// back at run end (TakeUndelivered) and re-dispatched instead of lost.
	if !m.session.InjectUser(action.Text) {
		return false
	}
	m.queue = m.queue[1:]
	if m.queueSel > len(m.queue)-1 {
		m.queueSel = len(m.queue) - 1
	}
	if m.queueSel < 0 {
		m.queueSel = 0
	}
	m.appendMessage(ChatMessage{Role: "user", Content: front})
	m.parked = false
	m.loading = true
	m.interruptArmed = false
	m.startThinking()
	return true
}

// flushQueueAsTurn dispatches queued entries as fresh turns when a run ends.
// Consecutive plain messages (no slash commands, no attachments) are combined
// into a single turn so the assistant sees them together rather than handling
// each as a separate round-trip. Non-send entries (skill loads, resolve errors)
// are handled inline; attachment-bearing sends are dispatched individually.
// The loop returns as soon as a turn starts.
//
// Undelivered follow-ups (released into the ended run but never consumed by
// it) go first: they were typed — and echoed — before anything still queued,
// so they re-dispatch without a second transcript echo.
//
// It sends nothing while the queue is held (see queueHeld).
func (m *Model) flushQueueAsTurn() tea.Cmd {
	if m.queueHeld {
		return nil
	}
	var texts []string

	// dispatchAccumulated sends all collected plain-message texts as a single
	// combined turn (texts joined with a blank line). Returns nil when nothing
	// was accumulated, so callers can use it as a guard before handling a
	// non-send entry.
	dispatchAccumulated := func() tea.Cmd {
		if len(texts) == 0 {
			return nil
		}
		combined := strings.Join(texts, "\n\n")
		texts = nil
		m.loading = true
		m.interruptArmed = false
		m.status = ""
		return m.sendMessage(combined)
	}

	// Undelivered follow-ups: already echoed, so collect without re-echoing.
	for len(m.redispatch) > 0 {
		input := m.redispatch[0]
		action := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
		if action.Kind == slash.KindSend && len(action.Files) == 0 && len(m.pending) == 0 {
			m.redispatch = m.redispatch[1:]
			texts = append(texts, action.Text)
			continue
		}
		// Non-send or attachment-bearing: flush accumulated sends first.
		if cmd := dispatchAccumulated(); cmd != nil {
			return cmd
		}
		m.redispatch = m.redispatch[1:]
		if cmd := m.dispatchResolved(input); cmd != nil {
			return cmd
		}
	}

	// Queued entries: echo each, then collect plain sends to combine.
	for len(m.queue) > 0 {
		input := m.queue[0]
		action := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
		if action.Kind == slash.KindSend && len(action.Files) == 0 && len(m.pending) == 0 {
			if m.boot != nil && !m.boot.collapsed {
				m.boot.collapsed = true
			}
			m.queue = m.queue[1:]
			if m.queueSel > len(m.queue)-1 {
				m.queueSel = len(m.queue) - 1
			}
			if m.queueSel < 0 {
				m.queueSel = 0
			}
			m.appendMessage(ChatMessage{Role: "user", Content: input})
			texts = append(texts, action.Text)
			continue
		}
		// Non-send or attachment-bearing: flush accumulated sends first.
		if cmd := dispatchAccumulated(); cmd != nil {
			return cmd
		}
		m.queue = m.queue[1:]
		if m.queueSel > len(m.queue)-1 {
			m.queueSel = len(m.queue) - 1
		}
		if m.queueSel < 0 {
			m.queueSel = 0
		}
		if cmd := m.dispatchInput(input); cmd != nil {
			return cmd
		}
	}

	return dispatchAccumulated()
}

// renderQueue renders the pending-message queue shown above the composer.
// Returns "" when the queue is empty. The selected entry (sel) is marked for
// edit/delete; selection only matters while the composer is empty.
func renderQueue(queue []string, sel, width int) string {
	if len(queue) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(theme.Meta.Render("queued (edit until sent)"))
	b.WriteString("\n")
	for i, entry := range queue {
		marker := "  "
		if i == sel {
			marker = "> "
		}
		flat := strings.ReplaceAll(strings.TrimSpace(entry), "\n", " ")
		line := fmt.Sprintf("%s%d. %s", marker, i+1, clipLine(flat, width-6))
		b.WriteString(theme.Help.Render(line))
		if i < len(queue)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
