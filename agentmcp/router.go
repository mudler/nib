package agentmcp

import (
	"errors"
	"sync"
)

// errSuperseded terminates an outstanding converse when a second one starts.
// One outstanding converse at a time is the supported model (see router).
var errSuperseded = errors.New("converse superseded by a new utterance")

// replyEvent is one assistant utterance to be spoken. A non-nil Err is a
// terminal error for the turn (Text is then empty).
type replyEvent struct {
	Text    string
	Pending bool // produced at a park: background work continues
	Err     error
}

// notifyFunc pushes a reply the client should speak proactively (no converse
// is waiting for it). turn is the id of the turn that produced it.
type notifyFunc func(ev replyEvent, turn int)

// router serializes assistant replies between chat.Session callbacks and the
// MCP surface. The first reply after a converse starts is handed to that
// converse synchronously; any later reply is pushed via notify.
//
// Exactly one outstanding converse is supported at a time: a second await()
// supersedes (terminates) the first waiter rather than overlapping with it.
type router struct {
	mu     sync.Mutex
	waiter chan replyEvent
	turn   int
	notify notifyFunc
	// lastToWaiter is the text last handed to a waiter for the current turn. A
	// run that parks then completes with no new model text fires OnParked(reply)
	// and a final OnResponse(reply) carrying the SAME string; once the waiter has
	// taken the park reply, the identical final reply must not also be pushed as
	// a duplicate notification.
	lastToWaiter string
}

func newRouter() *router { return &router{} }

// await registers the calling converse as the waiter for the next reply and
// returns the channel to block on plus this converse's turn id. If a waiter is
// already registered (an overlapping converse), it is released with a terminal
// errSuperseded event so that converse returns promptly instead of hanging.
func (r *router) await() (chan replyEvent, int) {
	ch := make(chan replyEvent, 1)
	r.mu.Lock()
	if r.waiter != nil {
		// Buffered (cap 1) and unconsumed, so this send never blocks.
		r.waiter <- replyEvent{Err: errSuperseded}
	}
	r.turn++
	r.waiter = ch
	r.lastToWaiter = ""
	turn := r.turn
	r.mu.Unlock()
	return ch, turn
}

// emit routes a reply to the waiting converse if one is registered, otherwise
// to the notify sink. A non-error reply with no waiter whose text duplicates
// the one already handed to the waiter this turn is dropped (not notified).
func (r *router) emit(ev replyEvent) {
	r.mu.Lock()
	w := r.waiter
	r.waiter = nil
	turn := r.turn
	notify := r.notify
	if w != nil {
		if ev.Err == nil {
			r.lastToWaiter = ev.Text
		}
		r.mu.Unlock()
		w <- ev
		return
	}
	// No waiter: this would go to notify. Suppress a non-error reply that merely
	// repeats the text the waiter already consumed for this turn.
	if ev.Err == nil && ev.Text != "" && ev.Text == r.lastToWaiter {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if notify != nil {
		notify(ev, turn)
	}
}

// setNotify installs the proactive-speech sink. Safe to call concurrently.
func (r *router) setNotify(fn notifyFunc) {
	r.mu.Lock()
	r.notify = fn
	r.mu.Unlock()
}
