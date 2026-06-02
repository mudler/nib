package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestCtrlCInterruptThenExit verifies the two-stage Ctrl+C behavior: the first
// Ctrl+C on an in-flight (loading) turn interrupts but keeps the session open,
// and a second Ctrl+C exits.
func TestCtrlCInterruptThenExit(t *testing.T) {
	m := Model{
		loading: true,
		cancel:  func() {}, // quit() calls this; no real context needed here
	}

	// First Ctrl+C: interrupt, stay open.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m1 := next.(Model)
	if !m1.interruptArmed {
		t.Fatal("first Ctrl+C should arm the interrupt")
	}
	if m1.quitting {
		t.Fatal("first Ctrl+C should not quit while working")
	}

	// Second Ctrl+C (still loading): exit.
	next2, _ := m1.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m2 := next2.(Model)
	if !m2.quitting {
		t.Fatal("second Ctrl+C should quit")
	}
}

// TestCtrlCWhenIdleExits verifies Ctrl+C exits immediately when no turn is
// running (nothing to interrupt).
func TestCtrlCWhenIdleExits(t *testing.T) {
	m := Model{cancel: func() {}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !next.(Model).quitting {
		t.Fatal("Ctrl+C while idle should quit immediately")
	}
}
