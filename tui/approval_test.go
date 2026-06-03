package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
)

// TestApprovalChoiceKeysResolve verifies the key-driven approval choice mode:
// single keypresses resolve the pending tool call (sending the right
// ToolCallResponse on the channel), and `e` switches into edit mode without
// resolving anything.
func TestApprovalChoiceKeysResolve(t *testing.T) {
	// newModel builds a model already awaiting approval in choice mode, with a
	// buffered response channel so resolveApproval's returned cmd never blocks.
	newModel := func() (Model, chan chat.ToolCallResponse) {
		ch := make(chan chat.ToolCallResponse, 1)
		m := Model{
			textarea:         textarea.New(),
			viewport:         viewport.New(80, 10),
			awaitingApproval: true,
			approvalEditing:  false,
			pendingTool:      &chat.ToolCallRequest{Name: "shell"},
			toolResponseChan: ch,
		}
		return m, ch
	}

	// run feeds a key, executes the returned cmd, and returns the next model and
	// whatever (if anything) landed on the channel.
	run := func(m Model, key tea.KeyMsg, ch chan chat.ToolCallResponse) (Model, chan chat.ToolCallResponse, *chat.ToolCallResponse) {
		next, cmd := m.Update(key)
		nm := next.(Model)
		if cmd != nil {
			cmd()
		}
		select {
		case resp := <-ch:
			return nm, ch, &resp
		default:
			return nm, ch, nil
		}
	}

	// `y` → approve once.
	m, ch := newModel()
	nm, _, resp := run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}, ch)
	if nm.awaitingApproval {
		t.Fatal("y should clear awaitingApproval")
	}
	if resp == nil || !resp.Approved || resp.AlwaysAllow || resp.AllowAllTurn || resp.Adjustment != "" {
		t.Fatalf("y should send a plain approval, got %+v", resp)
	}

	// `A` (shift+a) → allow all for the turn.
	m, ch = newModel()
	nm, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}, ch)
	if nm.awaitingApproval {
		t.Fatal("A should clear awaitingApproval")
	}
	if resp == nil || !resp.Approved || !resp.AllowAllTurn {
		t.Fatalf("A should send Approved+AllowAllTurn, got %+v", resp)
	}

	// `e` → enter edit mode; nothing resolved, nothing on the channel.
	m, ch = newModel()
	nm, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}, ch)
	if !nm.awaitingApproval {
		t.Fatal("e should keep awaitingApproval true")
	}
	if !nm.approvalEditing {
		t.Fatal("e should switch into edit mode")
	}
	if resp != nil {
		t.Fatalf("e should not write to the channel, got %+v", resp)
	}
}
