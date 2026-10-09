package tui

import (
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestToolStartDoesNotBlockOnReasoningChannel(t *testing.T) {
	m := runningTestModel()
	m.reasoningChan = make(chan reasoningEvent, 1)
	m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "queued"}
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	start, _ := m.toolCallbacks()
	done := make(chan struct{})
	go func() { start(chat.ToolStart{ID: "a", Name: "probe"}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mailbox producer blocked")
	}
	events := m.toolEvents.drain()
	if len(events) != 1 || events[0].reasoning == nil || events[0].reasoning.kind != reasoningEventStepEnd || events[0].start == nil {
		t.Fatalf("missing ordered marker/start: %+v", events)
	}
}

func TestToolStartBackpressurePreservesBoundaryAndResult(t *testing.T) {
	m := runningTestModel()
	m.reasoningChan = make(chan reasoningEvent, 1)
	m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "before"}
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	start, result := m.toolCallbacks()
	done := make(chan struct{})
	go func() { start(chat.ToolStart{ID: "a", Name: "probe"}); close(done) }()
	if ev := <-m.reasoningChan; ev.text != "before" {
		t.Fatal("reasoning reordered")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("start did not finish after drain")
	}
	if len(m.reasoningChan) != 0 {
		t.Fatal("marker must share the tool mailbox")
	}
	m = update(m, toolEventsReadyMsg{})
	if len(m.running) != 1 {
		t.Fatal("start lost")
	}
	m.toolEvents.end()
	result(chat.ToolResult{ID: "a", Name: "probe", Result: "done"})
	m = update(m, toolEventsReadyMsg{})
	if len(m.running) != 0 {
		t.Fatal("result lost after lifecycle end")
	}
}
