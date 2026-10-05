package tui

import (
	"bytes"
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func newBellTestModel() (Model, *bytes.Buffer) {
	var bell bytes.Buffer
	m := newQueueTestModel().WithBell(&bell)
	m.loading = true // a run is in flight
	return m, &bell
}

// Every point where the TUI hands control back to the user rings exactly once:
// that BEL is how a host watching the terminal learns nib is done.
func TestBellRingsWhenControlReturnsToTheUser(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"turn ended", responseMsg{content: "all done"}},
		{"turn interrupted", responseMsg{err: context.Canceled}},
		{"run parked", parkMsg{parked: true, reply: "parked reply"}},
		{"approval needed", toolCallMsg(chat.ToolCallRequest{Name: "bash"})},
		{"answer needed", askMsg(chat.AskRequest{Question: "which?", Options: []string{"a", "b"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, bell := newBellTestModel()
			m.Update(tc.msg)
			if got := bell.String(); got != "\a" {
				t.Fatalf("bell wrote %q, want one BEL", got)
			}
		})
	}
}

// Silence while nib keeps working: a resumed park, a queued follow-up that
// starts the next turn, and a tool call yolo answers without asking.
func TestBellStaysQuietWhileStillWorking(t *testing.T) {
	t.Run("park resumed", func(t *testing.T) {
		m, bell := newBellTestModel()
		m.Update(parkMsg{parked: false})
		if bell.Len() != 0 {
			t.Fatalf("bell wrote %q on a resumed run", bell.String())
		}
	})

	t.Run("queued follow-up starts a turn", func(t *testing.T) {
		s, err := chat.NewSession(context.Background(), types.Config{}, chat.Callbacks{})
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer s.Close()
		m, bell := newBellTestModel()
		m.session = s
		m.queue = queuedTexts("next turn please")
		next, _ := m.Update(responseMsg{content: "done"})
		if !next.(Model).loading {
			t.Fatal("the queued follow-up did not start a turn")
		}
		if bell.Len() != 0 {
			t.Fatalf("bell wrote %q while a queued turn started", bell.String())
		}
	})

	t.Run("tool call under yolo", func(t *testing.T) {
		m, bell := newBellTestModel()
		m.session = &chat.Session{}
		m.session.SetAutoApprove(true)
		m.ctx = context.Background()
		m.Update(toolCallMsg(chat.ToolCallRequest{Name: "bash"}))
		if bell.Len() != 0 {
			t.Fatalf("bell wrote %q for a call yolo approved", bell.String())
		}
	})
}

func TestBellHonorsNoBellAndNoTerminal(t *testing.T) {
	m, bell := newBellTestModel()
	m.cfg.UI.NoBell = true
	m.Update(responseMsg{content: "done"})
	if bell.Len() != 0 {
		t.Fatalf("bell wrote %q with ui.no_bell on", bell.String())
	}

	// No WithBell at all: nothing to ring, and nothing to crash on.
	plain := newQueueTestModel()
	plain.loading = true
	plain.Update(responseMsg{content: "done"})
}
