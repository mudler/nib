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
			pendingTool:      &chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`},
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

	// `1` → approve once (numbered alias for y).
	m, ch = newModel()
	nm, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}, ch)
	if nm.awaitingApproval || resp == nil || !resp.Approved || resp.AlwaysAllow || resp.AllowAllTurn {
		t.Fatalf("1 should send a plain approval, got %+v", resp)
	}

	// `2` → always allow, scoped to the derived bash prefix.
	m, ch = newModel()
	nm, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}, ch)
	if nm.awaitingApproval || resp == nil || !resp.Approved || !resp.AlwaysAllow {
		t.Fatalf("2 should send Approved+AlwaysAllow, got %+v", resp)
	}
	if resp.AlwaysPrefix != "git" {
		t.Fatalf("2 on a simple git command should grant prefix \"git\", got %q", resp.AlwaysPrefix)
	}

	// `a` → alias for 2, same scoped grant.
	m, ch = newModel()
	_, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, ch)
	if resp == nil || !resp.AlwaysAllow || resp.AlwaysPrefix != "git" {
		t.Fatalf("a should match 2's scoped grant, got %+v", resp)
	}

	// `3` → allow all this turn (numbered alias for A).
	m, ch = newModel()
	_, _, resp = run(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}, ch)
	if resp == nil || !resp.Approved || !resp.AllowAllTurn {
		t.Fatalf("3 should send Approved+AllowAllTurn, got %+v", resp)
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

// TestApprovalAlwaysWholeTool verifies `2` falls back to a whole-tool grant
// (empty AlwaysPrefix) when no safe bash prefix can be derived.
func TestApprovalAlwaysWholeTool(t *testing.T) {
	ch := make(chan chat.ToolCallResponse, 1)
	m := Model{
		textarea:         textarea.New(),
		viewport:         viewport.New(80, 10),
		awaitingApproval: true,
		pendingTool:      &chat.ToolCallRequest{Name: "bash", Arguments: `{"script":"a && b"}`},
		toolResponseChan: ch,
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		cmd()
	}
	if next.(Model).awaitingApproval {
		t.Fatal("2 should clear awaitingApproval")
	}
	resp := <-ch
	if !resp.Approved || !resp.AlwaysAllow || resp.AlwaysPrefix != "" {
		t.Fatalf("compound command should grant the whole tool, got %+v", resp)
	}
}
