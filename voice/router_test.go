package voice

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
