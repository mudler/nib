package tui

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
)

func TestToolStartBackpressureCancellation(t *testing.T) {
	for _, mode := range []string{"turn-end", "session-cancel"} {
		t.Run(mode, func(t *testing.T) {
			m := runningTestModel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.ctx = ctx
			m.reasoningChan = make(chan reasoningEvent, 1)
			m.reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: "queued"}
			m.toolEvents = newToolEventQueue()
			m.toolEvents.begin()
			start, _ := m.toolCallbacks()
			entered := make(chan struct{})
			done := make(chan struct{})
			go func() { close(entered); start(chat.ToolStart{ID: "blocked", Name: "probe"}); close(done) }()
			<-entered
			// With no receiver and a full channel, the active callback cannot finish.
			select {
			case <-done:
				t.Fatal("active callback bypassed reasoning boundary")
			case <-time.After(20 * time.Millisecond):
			}
			if mode == "turn-end" {
				m.toolEvents.end()
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(200 * time.Millisecond):
				// Always release the blocked producer before failing: no leaked goroutine.
				<-m.reasoningChan
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("cleanup failed")
				}
				t.Fatal("tool-start callback did not unblock on cancellation")
			}
		})
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
	if ev := <-m.reasoningChan; ev.kind != reasoningEventStepEnd {
		t.Fatal("missing step boundary")
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
