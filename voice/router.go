package voice

import "sync"

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
type router struct {
	mu     sync.Mutex
	waiter chan replyEvent
	turn   int
	notify notifyFunc
}

func newRouter() *router { return &router{} }

// await registers the calling converse as the waiter for the next reply and
// returns the channel to block on plus this converse's turn id.
func (r *router) await() (chan replyEvent, int) {
	ch := make(chan replyEvent, 1)
	r.mu.Lock()
	r.turn++
	r.waiter = ch
	turn := r.turn
	r.mu.Unlock()
	return ch, turn
}

// emit routes a reply to the waiting converse if one is registered, otherwise
// to the notify sink.
func (r *router) emit(ev replyEvent) {
	r.mu.Lock()
	w := r.waiter
	r.waiter = nil
	turn := r.turn
	notify := r.notify
	r.mu.Unlock()
	if w != nil {
		w <- ev
		return
	}
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
