package tui

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mudler/nib/chat"
)

func TestContentSnapshotMailboxOrdering(t *testing.T) {
	for _, terminal := range []string{"tool", "final", "park"} {
		t.Run(terminal, func(t *testing.T) {
			m := Model{ctx: context.Background(), textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80, loading: true, presenter: testPresenter(), reasoningChan: make(chan reasoningEvent, 10), toolEvents: newToolEventQueue()}
			m.toolEvents.begin()
			next, _ := m.Update(content("Checking"))
			m = next.(Model)
			m.enqueueStepContent("Checking")
			// A listener may already own the snapshot, but Bubble Tea has not yet
			// delivered its message. A later mailbox wakeup/terminal reply wins.
			var held reasoningEventsMsg
			var wake any
			if len(m.reasoningChan) > 0 {
				held = m.listenReasoningEvents()().(reasoningEventsMsg)
			} else {
				wake = m.listenToolEvents()()
			}
			if terminal == "tool" {
				m.toolEvents.push(toolEvent{result: &chat.ToolResult{Name: "bash", Result: "ok"}})
				next, _ = m.Update(toolEventsReadyMsg{})
			} else if terminal == "final" {
				next, _ = m.Update(responseMsg{content: "Done"})
			} else {
				next, _ = m.Update(parkMsg{parked: true, reply: "Done"})
			}
			m = next.(Model)
			if held != nil {
				next, _ = m.Update(held)
				m = next.(Model)
			}
			if wake != nil {
				next, _ = m.Update(wake)
				m = next.(Model)
			}
			var got []string
			for _, msg := range m.messages {
				if msg.Role == "assistant" {
					got = append(got, msg.Content)
				} else if msg.Role == "tool" {
					got = append(got, "tool")
				}
			}
			want := []string{"Checking", "Done"}
			if terminal == "tool" {
				want = []string{"Checking", "tool"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("transcript=%q want %q", got, want)
			}
		})
	}
}

func TestContentMailboxLifecycle(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m.enqueueStepContent("old")
	m.toolEvents.begin()
	if events := m.toolEvents.drain(); len(events) != 0 {
		t.Fatal("superseded lifecycle retained events")
	}
	m.toolEvents.end()
	m.enqueueStepContent("late")
	if events := m.toolEvents.drain(); len(events) != 0 {
		t.Fatal("ended lifecycle accepted content")
	}
	m.toolEvents.begin()
	m.enqueueReasoning(reasoningEvent{kind: reasoningEventContentSnapshot, text: "stale", gen: m.currentTurnGen() + 1})
	m = update(m, toolEventsReadyMsg{})
	if len(m.messages) != 0 {
		t.Fatalf("stale content: %+v", m.messages)
	}
}

func TestContentMailboxCoalescesOnlyAdjacentDeltas(t *testing.T) {
	q := newToolEventQueue()
	q.begin()
	for i := 0; i < 10000; i++ {
		ev := reasoningEvent{kind: reasoningEventContentDelta, text: "x"}
		q.push(toolEvent{reasoning: &ev})
	}
	snapshot := reasoningEvent{kind: reasoningEventContentSnapshot, text: "snapshot"}
	q.push(toolEvent{reasoning: &snapshot})
	delta := reasoningEvent{kind: reasoningEventContentDelta, text: "next"}
	q.push(toolEvent{reasoning: &delta})
	events := q.drain()
	if len(events) != 3 || len(events[0].reasoning.text) != 10000 || events[1].reasoning.text != "snapshot" || events[2].reasoning.text != "next" {
		t.Fatal("coalescing crossed boundary or lost text")
	}
	if len(q.drain()) != 0 {
		t.Fatal("drain retained events")
	}
}

func TestContentMailboxBeforeApproval(t *testing.T) {
	m := runningTestModel()
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m = update(m, content("Checking"))
	m.enqueueStepContent("Checking")
	m = update(m, toolCallMsg(chat.ToolCallRequest{Name: "bash"}))
	m = update(m, toolEventsReadyMsg{})
	if len(m.messages) != 1 || m.messages[0].Content != "Checking" || m.streamingActive {
		t.Fatalf("snapshot not committed before approval: %+v streaming=%v", m.messages, m.streamingActive)
	}
}

func TestContentMailboxParkResumeBeforeDelivery(t *testing.T) {
	m := runningTestModel()
	m.turnGen = new(atomic.Int32)
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	m.enqueueReasoning(reasoningEvent{kind: reasoningEventContentDelta, text: "First", gen: m.currentTurnGen()})
	m.toolEvents.push(toolEvent{park: &parkEvent{parked: true, reply: "First"}})
	m.toolEvents.push(toolEvent{park: &parkEvent{parked: false}})
	m.enqueueReasoning(reasoningEvent{kind: reasoningEventContentDelta, text: "Second", gen: m.currentTurnGen()})
	m.enqueueStepContent("Second")
	m = update(m, toolEventsReadyMsg{})
	var got []string
	for _, msg := range m.messages {
		if msg.Role == "assistant" {
			got = append(got, msg.Content)
		}
	}
	if !reflect.DeepEqual(got, []string{"First", "Second"}) {
		t.Fatalf("replies=%q", got)
	}
}
