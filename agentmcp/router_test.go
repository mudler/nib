package agentmcp

import "testing"

func TestRouterFirstReplyGoesToWaiter(t *testing.T) {
	r := newRouter()
	ch, turn := r.await()
	if turn != 1 {
		t.Fatalf("first turn id = %d, want 1", turn)
	}
	r.emit(replyEvent{Text: "hello", Pending: true})
	ev := <-ch
	if ev.Text != "hello" || !ev.Pending {
		t.Fatalf("waiter got %+v, want {hello true}", ev)
	}
}

func TestRouterLaterReplyGoesToNotify(t *testing.T) {
	r := newRouter()
	var got []replyEvent
	var gotTurn int
	r.setNotify(func(ev replyEvent, turn int) { got = append(got, ev); gotTurn = turn })

	ch, _ := r.await()
	r.emit(replyEvent{Text: "first"})
	<-ch // consume the synchronous reply

	r.emit(replyEvent{Text: "second"}) // no waiter now
	if len(got) != 1 || got[0].Text != "second" {
		t.Fatalf("notify got %+v, want one 'second'", got)
	}
	if gotTurn != 1 {
		t.Fatalf("notify turn = %d, want 1", gotTurn)
	}
}

func TestRouterNotifyWithNoWaiter(t *testing.T) {
	r := newRouter()
	var called bool
	r.setNotify(func(ev replyEvent, turn int) { called = true })
	r.emit(replyEvent{Text: "background"})
	if !called {
		t.Fatal("notify not called for reply with no waiter")
	}
}

// A run that parks then completes with the same text fires OnParked(reply) to
// the waiter and a final OnResponse(reply) with no waiter. The second carries
// identical text and must NOT be pushed as a duplicate nib/reply.
func TestRouterSuppressesDuplicateParkReply(t *testing.T) {
	r := newRouter()
	var got []replyEvent
	r.setNotify(func(ev replyEvent, turn int) { got = append(got, ev) })

	ch, _ := r.await()
	r.emit(replyEvent{Text: "X", Pending: true}) // park reply -> waiter
	<-ch                                          // converse consumes it
	r.emit(replyEvent{Text: "X"})                 // final reply, no waiter, duplicate
	if len(got) != 0 {
		t.Fatalf("duplicate park reply was notified: %+v", got)
	}
	r.emit(replyEvent{Text: "Y"}) // different text -> notify must fire
	if len(got) != 1 || got[0].Text != "Y" {
		t.Fatalf("notify got %+v, want one 'Y'", got)
	}
}

// Only one outstanding converse is supported. A second await() must release the
// first waiter with a terminal error instead of orphaning it.
func TestRouterSupersedesOutstandingWaiter(t *testing.T) {
	r := newRouter()
	ch1, _ := r.await()
	ch2, _ := r.await()

	select {
	case ev := <-ch1:
		if ev.Err == nil {
			t.Fatalf("first waiter got %+v, want terminal error", ev)
		}
	default:
		t.Fatal("first waiter orphaned: no terminal event delivered")
	}

	r.emit(replyEvent{Text: "hi"})
	if ev := <-ch2; ev.Text != "hi" {
		t.Fatalf("second waiter got %+v, want hi", ev)
	}
}
