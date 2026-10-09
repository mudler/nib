package tui

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestStepContentSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chunks   []string
		snapshot string
	}{
		{"streamed", []string{"Let me ", "check."}, "Let me check."},
		{"partial stream", []string{"Let me "}, "Let me check."},
		{"nonstreamed", nil, "Let me check."},
		{"repeated chunks", []string{"ha", "ha"}, "haha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{viewport: viewport.New(80, 20), width: 80, loading: true, presenter: testPresenter(), reasoningChan: make(chan reasoningEvent, 1)}
			apply := func(events reasoningEventsMsg) { next, _ := m.Update(events); m = next.(Model) }
			// Two steps may legitimately say exactly the same thing. No tool-result
			// message is needed to separate snapshots (e.g. supervisor check-ins).
			for step := 0; step < 2; step++ {
				for _, chunk := range tc.chunks {
					apply(content(chunk))
				}
				m.enqueueStepContent(tc.snapshot)
				apply(reasoningEventsMsg{<-m.reasoningChan})
				var got []string
				for _, msg := range m.messages {
					if msg.Role == "assistant" {
						got = append(got, msg.Content)
					}
				}
				want := []string{tc.snapshot}
				if step == 1 {
					want = append(want, tc.snapshot)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("step %d: replies = %q, want %q", step, got, want)
				}
			}
			// A nonstreamed goal check-in after streamed commentary must survive,
			// and the next answer must not concatenate onto either completed step.
			m.enqueueStepContent("Goal check-in")
			apply(reasoningEventsMsg{<-m.reasoningChan})
			apply(content("Final answer"))
			next, _ := m.Update(responseMsg{content: "Final answer"})
			m = next.(Model)
			var got []string
			for _, msg := range m.messages {
				if msg.Role == "assistant" {
					got = append(got, msg.Content)
				}
			}
			want := []string{tc.snapshot, tc.snapshot, "Goal check-in", "Final answer"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("replies = %q, want %q", got, want)
			}
		})
	}
}

func TestStepContentSnapshotBoundaries(t *testing.T) {
	m := Model{viewport: viewport.New(80, 20), width: 80, loading: true, presenter: testPresenter(), reasoningChan: make(chan reasoningEvent, 1)}
	apply := func(events reasoningEventsMsg) { next, _ := m.Update(events); m = next.(Model) }
	// Reasoning's boundary precedes the content snapshot. It must not close
	// the content target before the snapshot reconciles it.
	apply(content("Checking"))
	apply(boundary("Think first"))
	m.enqueueStepContent("Checking now")
	apply(reasoningEventsMsg{<-m.reasoningChan})
	apply(reasoningEventsMsg{{kind: reasoningEventStepEnd}})
	m.appendMessage(ChatMessage{Role: "tool", Content: "result"})
	apply(content("Checking now"))
	m.enqueueStepContent("Checking now")
	apply(reasoningEventsMsg{<-m.reasoningChan})
	var replies []string
	for _, msg := range m.messages {
		if msg.Role == "assistant" {
			replies = append(replies, msg.Content)
		}
	}
	if !reflect.DeepEqual(replies, []string{"Checking now", "Checking now"}) {
		t.Fatalf("replies = %q", replies)
	}
	// A tool step without a content snapshot must also close its live target.
	apply(content("Before tool"))
	apply(reasoningEventsMsg{{kind: reasoningEventStepEnd}})
	apply(content("After tool"))
	if got := m.messages[len(m.messages)-1].Content; got != "After tool" {
		t.Fatalf("next step = %q", got)
	}
}

func TestStepContentSnapshotIgnoresStaleAndEmpty(t *testing.T) {
	m := Model{viewport: viewport.New(80, 20), width: 80, loading: true, presenter: testPresenter()}
	for _, ev := range []reasoningEvent{
		{kind: reasoningEventContentSnapshot, text: "stale", gen: 1},
		{kind: reasoningEventContentSnapshot},
	} {
		next, _ := m.Update(reasoningEventsMsg{ev})
		m = next.(Model)
	}
	m.loading = false
	next, _ := m.Update(reasoningEventsMsg{{kind: reasoningEventContentSnapshot, text: "late"}})
	if got := next.(Model).messages; len(got) != 0 {
		t.Fatalf("unexpected messages: %v", got)
	}
}

func TestStepContentSnapshotNonstreamedFinal(t *testing.T) {
	m := Model{viewport: viewport.New(80, 20), width: 80, loading: true, presenter: testPresenter(), reasoningChan: make(chan reasoningEvent, 1)}
	m.enqueueStepContent("Let me check.")
	next, _ := m.Update(reasoningEventsMsg{<-m.reasoningChan})
	m = next.(Model)
	next, _ = m.Update(responseMsg{content: "Done."})
	var replies []string
	for _, msg := range next.(Model).messages {
		if msg.Role == "assistant" {
			replies = append(replies, msg.Content)
		}
	}
	if !reflect.DeepEqual(replies, []string{"Let me check.", "Done."}) {
		t.Fatalf("replies = %q", replies)
	}
}
