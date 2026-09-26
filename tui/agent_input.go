package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// agentSentMsg reports how sending the user's message to a sub-agent went.
type agentSentMsg struct {
	id, typ, text string
	err           error
}

// newAgentInput is the input line of a running sub-agent's log, addressed to
// the agent by type.
func newAgentInput(typ string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "message " + typ + "…"
	ti.CharLimit = 4000
	ti.Focus()
	return ti
}

// agentInputOpen reports whether the log viewer shows a running sub-agent's
// log, which takes input. A finished agent's log does not: its loop reads no
// more messages.
func (m Model) agentInputOpen() bool {
	if !m.showLogs || m.logOpenKind != "agent" || m.logOpenID == "" {
		return false
	}
	j, ok := m.jobByID(m.logOpenID)
	return ok && j.Status == chat.AgentStatusRunning
}

// agentTypeOf names a sub-agent by its type, "agent" when it has none.
func (m Model) agentTypeOf(id string) string {
	if j, ok := m.jobByID(id); ok && j.Type != "" {
		return j.Type
	}
	return "agent"
}

// handleAgentInputKey handles a key on a running sub-agent's log. Enter sends
// the input, esc clears it (and, when it is empty, goes on to leave the log),
// and the scroll keys and ctrl+o go on to the viewer. Everything else edits
// the input. It reports false for a key the viewer should handle.
func (m *Model) handleAgentInputKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyEnter:
		text := strings.TrimSpace(m.agentInput.Value())
		if text == "" {
			return nil, true
		}
		id, typ := m.logOpenID, m.agentTypeOf(m.logOpenID)
		send := m.sendToAgent
		if send == nil && m.session != nil {
			send = m.session.SendToAgent
		}
		m.agentInputNote = "sending…"
		return func() tea.Msg {
			if send == nil {
				return agentSentMsg{id: id, typ: typ, text: text, err: errors.New("no session")}
			}
			return agentSentMsg{id: id, typ: typ, text: text, err: send(id, text)}
		}, true
	case tea.KeyEsc:
		if m.agentInput.Value() == "" {
			return nil, false
		}
		m.agentInput.Reset()
		m.agentInputNote = ""
		return nil, true
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyCtrlO:
		return nil, false
	}
	var cmd tea.Cmd
	m.agentInput, cmd = m.agentInput.Update(msg)
	return cmd, true
}

// applyAgentSent records a sent message: in the transcript, in the agent's
// log on screen, and in the note under the input. A failed send keeps the text
// so the user can try again.
func (m *Model) applyAgentSent(msg agentSentMsg) {
	if msg.err != nil {
		m.agentInputNote = "not sent: " + msg.err.Error()
		return
	}
	if strings.TrimSpace(m.agentInput.Value()) == msg.text {
		m.agentInput.Reset()
	}
	m.agentInputNote = "sent " + theme.Sep + " " + msg.typ + " reads it at its next step"
	m.appendMessage(ChatMessage{Role: "agent", AgentID: msg.id, Content: "you " + theme.Arrow + " " + msg.typ + ": " + msg.text})
	if m.showLogs && m.logOpenID == msg.id {
		m.syncLogViewport()
	}
}

// renderAgentInput is the composer while a running sub-agent's log is open:
// the input line, and the note about the last message sent.
func (m Model) renderAgentInput(width int) string {
	in := m.agentInput
	in.Width = max(width-lipgloss.Width(theme.PromptGlyph)-2, 10)
	line := theme.Prompt.Render(theme.PromptGlyph) + " " + in.View()
	if m.agentInputNote != "" {
		line += "\n" + theme.Help.Render(m.agentInputNote)
	}
	return line
}
