package voice

import (
	"errors"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func TestBuildCallbacksRoutesParkAndResponse(t *testing.T) {
	r := newRouter()
	cb := buildCallbacks(r, newPolicy(types.Config{}))

	ch, _ := r.await()
	cb.OnParked("partial") // first reply -> waiter
	ev := <-ch
	if ev.Text != "partial" || !ev.Pending {
		t.Fatalf("park event = %+v, want {partial true}", ev)
	}

	var notified []replyEvent
	r.setNotify(func(e replyEvent, _ int) { notified = append(notified, e) })
	cb.OnResponse("final") // later reply -> notify
	if len(notified) != 1 || notified[0].Text != "final" || notified[0].Pending {
		t.Fatalf("notified = %+v, want one {final false}", notified)
	}
}

func TestBuildCallbacksRoutesError(t *testing.T) {
	r := newRouter()
	cb := buildCallbacks(r, newPolicy(types.Config{}))
	ch, _ := r.await()
	cb.OnError(errors.New("boom"))
	ev := <-ch
	if ev.Err == nil || ev.Err.Error() != "boom" {
		t.Fatalf("error event = %+v, want err 'boom'", ev)
	}
}

func TestBuildCallbacksApproval(t *testing.T) {
	r := newRouter()
	cb := buildCallbacks(r, newPolicy(types.Config{}))
	if !cb.OnToolCall(chat.ToolCallRequest{Name: "x"}).Approved {
		t.Fatal("OnToolCall must use the auto-approve policy")
	}
	if cb.OnAskUser(chat.AskRequest{}) == "" {
		t.Fatal("OnAskUser must return a non-empty default")
	}
}
