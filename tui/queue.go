package tui

import (
	"fmt"
	"strings"

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
	if len(m.queue) == 0 || m.session == nil || !m.session.RunLive() {
		return false
	}
	front := m.queue[0]
	if !m.session.Inject(front) {
		return false
	}
	m.queue = m.queue[1:]
	if m.queueSel > len(m.queue)-1 {
		m.queueSel = len(m.queue) - 1
	}
	if m.queueSel < 0 {
		m.queueSel = 0
	}
	m.messages = append(m.messages, ChatMessage{Role: "user", Content: front})
	m.parked = false
	m.loading = true
	m.interruptArmed = false
	m.status = "Thinking…"
	return true
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
