package chat

import (
	"sync"
	"testing"
	"time"
)

func TestObservationStreamContract(t *testing.T) {
	now := time.Unix(100, 0)
	var got []Observation
	emit := newObservationEmitter(func(o Observation) { got = append(got, o) }, func() time.Time { return now })
	for _, ev := range []StreamEvent{{Kind: "content", Content: "root"}, {Kind: "reasoning", Content: "child", AgentID: "child"}, {Kind: "sub_agent", Content: "untyped", AgentID: "child"}, {Kind: "tool_result", Content: "not model text"}, {Kind: "status", Content: "ambiguous"}, {Kind: "content"}} {
		emit.stream(ev)
	}
	if len(got) != 6 {
		t.Fatal(got)
	}
	for i, o := range got {
		if o.Order != uint64(i+1) || !o.Received.Equal(now) {
			t.Fatal(o)
		}
	}
	if !got[0].HasText || !got[0].OwnerKnown || !got[1].HasText || got[1].Owner != "child" {
		t.Fatal(got)
	}
	if got[2].HasText || got[3].HasText || got[4].OwnerKnown || got[5].HasText {
		t.Fatal(got)
	}
}

// Simulate a lifecycle consumer assigning epochs synchronously at receipt.
// Old snapshots keep their scope; resumed root callbacks remain admissible.
func TestObservationContinuedRunAndNewRun(t *testing.T) {
	now := time.Unix(100, 0)
	var got []Observation
	epoch := uint64(1)
	sink := func(o Observation) {
		if o.Kind == "resumed" {
			epoch++
		}
		o.Scope = ObservationScope{Generation: 7, Epoch: epoch}
		got = append(got, o)
	}
	old := newObservationEmitter(sink, func() time.Time { return now })
	old.stream(StreamEvent{Kind: "content", Content: "before"})
	old.record("", true, "parked", "", false)
	now = now.Add(time.Second)
	old.record("", true, "resumed", "", false)
	old.stream(StreamEvent{Kind: "reasoning", Content: "after"})
	next := newObservationEmitter(func(o Observation) { o.Scope.Generation = 8; got = append(got, o) }, func() time.Time { return now })
	next.stream(StreamEvent{Kind: "content", Content: "new run"})
	old.stream(StreamEvent{Kind: "reasoning", Content: "late child", AgentID: "child"})
	if got[0].RunID == 0 || got[0].RunID != got[3].RunID || got[4].RunID == got[0].RunID || got[5].RunID != got[0].RunID {
		t.Fatal(got)
	}
	if got[0].Scope.Epoch != 1 || got[3].Scope.Epoch != 2 || got[5].Scope.Generation != 7 {
		t.Fatal(got)
	}
	if got[0].Order != 1 || got[3].Order != 4 || got[4].Order != 1 || got[5].Order != 5 {
		t.Fatal(got)
	}
	now = now.Add(time.Hour) // a delayed drain cannot change receipt time
	if !got[0].Received.Equal(time.Unix(100, 0)) || !got[3].Received.Equal(time.Unix(101, 0)) {
		t.Fatal(got)
	}
}

func TestObservationConcurrentReceipts(t *testing.T) {
	var got []Observation
	now := time.Unix(200, 0)
	e := newObservationEmitter(func(o Observation) { got = append(got, o) }, func() time.Time { now = now.Add(time.Nanosecond); return now })
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.stream(StreamEvent{Kind: "content", Content: "text"}) }()
	}
	wg.Wait()
	for i, o := range got {
		if o.Order != uint64(i+1) || !o.Received.Equal(time.Unix(200, int64(i+1))) {
			t.Fatal(o)
		}
	}
	if len(got) != 100 {
		t.Fatal(len(got))
	}
}
